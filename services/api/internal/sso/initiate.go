package sso

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// InitiateStore is the minimal Store surface NewInitiateHandler reads.
// Narrowing the parameter type keeps the handler testable without standing
// up a full mock-of-everything. storage.Store satisfies this interface —
// production callers pass it unchanged. Same pattern as JITMembershipStore.
type InitiateStore interface {
	GetSSOConnectionByID(ctx context.Context, id string) (model.SSOConnection, error)
}

// initiateScopes are the OIDC scopes the RP requests at authorize time.
// Group membership comes through the IdP-side token configuration (Entra
// "groups claim"); we don't request a separate `groups` scope because some
// IdPs reject unknown scopes outright. `offline_access` is intentionally
// absent — v1 doesn't use refresh tokens.
const initiateScopes = "openid email profile"

// NewInitiateHandler serves GET /v1/sso/oidc/{cid}/initiate. Pre-auth route
// (browser arrives without a session). Wired in cmd/main.go alongside
// /v1/sso/discover; NOT through Handler.Register which gates everything
// behind middleware.Require.
//
// Behaviour:
//   1. Resolve connection by {cid} via the unscoped store lookup
//      (RLS bypass — the cid is opaque from the URL and not enumerable).
//   2. Reject when status != 'active' or protocol != 'oidc' (draft and
//      non-OIDC connections shouldn't appear in any redirect_url the
//      discovery flow built).
//   3. Generate PKCE verifier + state + nonce; persist StateData under
//      sso:state:{state} with 10min TTL (single-use).
//   4. Fetch discovery doc (cached) for authorization_endpoint.
//   5. Build authorize URL with response_type=code, client_id, redirect_uri,
//      scope, state, nonce, code_challenge, code_challenge_method=S256, and
//      login_hint when ?email=... was supplied.
//   6. 302 to the authorize URL.
//
// Error responses are intentionally generic (404/400/503 + a one-line body).
// Detailed reasons go to slog only — the IdP redirect path is a discoverable
// surface and a chatty error is a side channel.
//
// Audit: initiate emits no audit row. The callback owns success/failure
// audit per design §10.1.
//
// publicHost is the externally-reachable origin the IdP redirects back to
// (matches the value configured in the IdP app registration). Required —
// callers must not pass "".
func NewInitiateHandler(store InitiateStore, validator *Validator, stateStore *StateStore, publicHost string) http.Handler {
	publicHost = strings.TrimRight(publicHost, "/")
	// publicHost is load-bearing: it goes into redirect_uri, which the IdP
	// has registered. Empty produces a relative URL the IdP will reject.
	// Non-https in production sends the post-callback session cookie over
	// plain HTTP. Surface both at startup so deployment-time misconfig is
	// visible in logs before the first login attempt.
	switch {
	case publicHost == "":
		slog.Error("sso: initiate: PUBLIC_HOST is empty — redirect_uri will be relative and IdP will reject it")
	case !strings.HasPrefix(publicHost, "https://") && !strings.HasPrefix(publicHost, "http://"):
		slog.Warn("sso: initiate: PUBLIC_HOST has no scheme — IdP will reject", "public_host", publicHost)
	case strings.HasPrefix(publicHost, "http://"):
		slog.Warn("sso: initiate: PUBLIC_HOST is non-https — only acceptable for local development", "public_host", publicHost)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cid := r.PathValue("cid")
		if cid == "" {
			http.Error(w, "missing connection id", http.StatusBadRequest)
			return
		}

		conn, err := store.GetSSOConnectionByID(r.Context(), cid)
		if errors.Is(err, storage.ErrSSOConnectionNotFound) {
			http.Error(w, "connection not found", http.StatusNotFound)
			return
		}
		if err != nil {
			slog.Error("sso: initiate: load connection", "cid", cid, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if conn.Status != model.SSOStatusActive {
			slog.Warn("sso: initiate: connection not active", "cid", cid, "status", conn.Status)
			http.Error(w, "connection not active", http.StatusBadRequest)
			return
		}
		if conn.Protocol != model.SSOProtocolOIDC {
			slog.Warn("sso: initiate: non-OIDC protocol", "cid", cid, "protocol", conn.Protocol)
			http.Error(w, "connection is not oidc", http.StatusBadRequest)
			return
		}
		if conn.OIDCClientID == "" || conn.OIDCDiscoveryURL == "" {
			slog.Error("sso: initiate: connection missing oidc fields", "cid", cid)
			http.Error(w, "connection misconfigured", http.StatusInternalServerError)
			return
		}

		doc, err := validator.Discovery(r.Context(), conn)
		if err != nil {
			slog.Error("sso: initiate: fetch discovery", "cid", cid, "err", err)
			http.Error(w, "discovery unavailable", http.StatusServiceUnavailable)
			return
		}
		if doc.AuthorizationEndpoint == "" {
			slog.Error("sso: initiate: discovery missing authorization_endpoint", "cid", cid)
			http.Error(w, "discovery unavailable", http.StatusServiceUnavailable)
			return
		}

		state, data, err := GenerateState(cid, ValidatedReturnTo(r.URL.Query().Get("return_to")))
		if err != nil {
			slog.Error("sso: initiate: generate state", "cid", cid, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := stateStore.Persist(r.Context(), state, data); err != nil {
			slog.Error("sso: initiate: persist state", "cid", cid, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		authorizeURL, err := buildAuthorizeURL(doc.AuthorizationEndpoint, authorizeParams{
			ClientID:      conn.OIDCClientID,
			RedirectURI:   publicHost + "/v1/sso/oidc/" + cid + "/callback",
			Scope:         initiateScopes,
			State:         state,
			Nonce:         data.Nonce,
			CodeChallenge: CodeChallenge(data.CodeVerifier),
			LoginHint:     r.URL.Query().Get("email"),
		})
		if err != nil {
			slog.Error("sso: initiate: build authorize url", "cid", cid, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, authorizeURL, http.StatusFound)
	})
}

// authorizeParams collects the fields buildAuthorizeURL needs. Keeping them
// in a struct prevents argument-order mistakes and documents what the
// authorize URL must carry.
type authorizeParams struct {
	ClientID      string
	RedirectURI   string
	Scope         string
	State         string
	Nonce         string
	CodeChallenge string
	LoginHint     string // optional; empty omitted from URL
}

// ValidatedReturnTo accepts a `?return_to=` query value and returns it iff
// it looks like a same-origin relative path. Open-redirect defense: any
// absolute URL ("https://evil.com"), protocol-relative URL ("//evil.com"),
// non-http scheme ("javascript:alert(1)"), or non-leading-slash value is
// dropped to "" so it never reaches StateData.RedirectAfterLogin and the
// callback's redirect target falls through to the default.
//
// Defense-in-depth: this is called at TWO sites — the entry boundary in
// initiate.go (where the user-supplied ?return_to= is sanitised before
// landing in state) AND the redirect site in oidc_callback.go (where
// stateData.RedirectAfterLogin is re-validated immediately before use).
// The second call closes architect N4's "regardless of state content"
// acceptance: even if the state record were corrupted between persist and
// consume (storage bug, hostile cache write, post-validation tampering),
// the callback still cannot be coerced into redirecting off-origin.
//
// Idempotent on a previously-validated value: a known-good "/dashboard/x"
// passes back through unchanged, so calling it twice has no effect on the
// happy path.
func ValidatedReturnTo(raw string) string {
	if raw == "" || len(raw) > 1024 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	// Absolute URL or protocol-relative — both have non-empty Scheme or Host.
	if u.Scheme != "" || u.Host != "" {
		return ""
	}
	if !strings.HasPrefix(raw, "/") {
		return ""
	}
	// Backslash + control chars trigger browser quirks under some renderers.
	if strings.ContainsAny(raw, "\\\x00\n\r") {
		return ""
	}
	return raw
}

// buildAuthorizeURL appends ceremony query params to the IdP's
// authorization_endpoint. Returns the absolute URL the browser is redirected
// to. Honours an existing query string on the endpoint (Entra v2 endpoints
// don't have one; some test fixtures do).
func buildAuthorizeURL(authorizeEndpoint string, p authorizeParams) (string, error) {
	u, err := url.Parse(authorizeEndpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", p.Scope)
	q.Set("state", p.State)
	q.Set("nonce", p.Nonce)
	q.Set("code_challenge", p.CodeChallenge)
	q.Set("code_challenge_method", "S256")
	if p.LoginHint != "" {
		q.Set("login_hint", p.LoginHint)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
