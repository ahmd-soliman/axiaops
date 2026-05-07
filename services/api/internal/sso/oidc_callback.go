package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/api/internal/audit"
	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/storage"
)

// callbackErrorRedirect is the path the callback redirects to on any failure.
// `?error=<code>` lets the login page surface a generic banner; we never
// leak internal error specifics — the failure mode is a discoverable
// surface (an attacker can hit the callback with random codes) and detailed
// reasons would be a side channel.
const callbackErrorRedirect = "/login?error=auth_failed"

// codeExchangeTimeout caps a token-endpoint POST. Generous because some
// IdPs (Entra under load) take a couple seconds; bounded so a deadlocked
// IdP doesn't hold goroutines open forever.
const codeExchangeTimeout = 10 * time.Second

// CallbackStore is the narrow Store surface NewCallbackHandler reads.
// Mirrors the InitiateStore / JITMembershipStore narrowing pattern —
// storage.Store satisfies this interface unchanged.
type CallbackStore interface {
	GetSSOConnectionByID(ctx context.Context, id string) (model.SSOConnection, error)
	GetVerifiedSSODomainByName(ctx context.Context, domain string) (model.SSODomain, error)
	UpsertUser(ctx context.Context, organizationID, externalID, email, name string) (model.User, error)
	RedeemPendingInvitation(ctx context.Context, organizationID, userID, email string) (bool, error)
	ListSSOGroupMappings(ctx context.Context, connID string) ([]model.SSOGroupMapping, error)
	SaveMembership(ctx context.Context, m model.Membership) error
	GetMembershipByOrgUser(ctx context.Context, organizationID, userID string) (model.Membership, error)
	UpdateMembershipRole(ctx context.Context, id, newRole string) error
	AuditLogWrite(ctx context.Context, e model.AuditEvent) (int64, error)
}

// SessionMinter is the auth.Manager surface the callback uses. Narrowed so
// tests don't need a full Manager (which itself drags in a pgxpool, cache,
// session config). Production passes *auth.Manager unchanged.
type SessionMinter interface {
	MintSession(ctx context.Context, in auth.MintRequest) (auth.MintResult, error)
}

// CallbackOptions bundles the callback handler's dependencies. Avoids an
// 8-positional-arg constructor and documents what each field is for.
//
// Note: the AES-256-GCM key for decrypting OIDCClientSecretCiphertext
// comes from the ENCRYPTION_KEY env var read by crypto.Decrypt — same
// idiom as crypto.Encrypt at the connection-CRUD callsite. Tests use
// t.Setenv. No key flows through this struct.
type CallbackOptions struct {
	Store        CallbackStore
	Validator    *Validator
	StateStore   *StateStore
	Sessions     SessionMinter
	CookieConfig auth.CookieConfig
	PublicHost   string       // matches initiate's publicHost — must agree with IdP-registered redirect_uri
	HTTPClient   *http.Client // optional; defaults to http.DefaultClient
}

// NewCallbackHandler serves the OIDC callback. Wired at two routes:
//
//   - GET /v1/sso/oidc/callback         — standard, connection-agnostic shape (Tasks.md 2.7.22).
//   - GET /v1/sso/oidc/{cid}/callback   — legacy form, kept for one release so already-registered
//     IdP redirect URIs continue to work; hits increment
//     axiaops_sso_legacy_callback_total{cid}.
//
// Pre-auth route (browser arrives from IdP redirect with no session). Wired
// in serverbuild.ComposeServer alongside the initiate handler.
//
// Ceremony (design §10.1, plan §5.2):
//  1. Validate code + state present.
//  2. Consume state (single-use). Connection ID is read from state.CID.
//     When the legacy path-cid route fires, the path cid must agree with
//     state.CID — guards against a hypothetical state-store compromise where
//     an attacker rewrites a state record's CID.
//  3. Look up connection (unscoped); reject inactive / non-OIDC.
//  4. Decrypt OIDCClientSecretCiphertext.
//  5. Exchange auth code for tokens at the IdP token_endpoint.
//  6. Validator.ValidateIDToken with state.Nonce.
//  7. Extract sub / email / name / groups (Entra: oid for sub, fallback
//     chain for email).
//  8. Confirm email's domain is verified for conn.OrganizationID
//     (anti-spoofing: the customer admin proved DNS control over this
//     domain; we don't trust the IdP's email_verified claim).
//  9. UpsertUser. RedeemPendingInvitation precedence — if an invite
//     matches the user's email, it wins over JIT.
//  10. Else JIT: ListSSOGroupMappings → JITResolveRole → JITProvisionMembership.
//  11. MintSession with auth_mode='sso'; SetSession cookie.
//  12. Audit AuditActionSSOLoginSucceeded; redirect to state.RedirectAfterLogin
//      (validated at initiate time) or "/" — the SPA's post-login landing
//      route; "/dashboard" was a stale alias that no SPA route handles, so
//      a default redirect there rendered a blank page.
//
// All failure paths after we've identified the connection emit
// AuditActionSSOLoginFailed and redirect to /login?error=auth_failed.
// Failures BEFORE connection lookup (missing state, malformed query) do
// the same redirect but skip audit (no organization context).
func NewCallbackHandler(opts CallbackOptions) http.Handler {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	publicHost := strings.TrimRight(opts.PublicHost, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// pathCID is empty on the standard /v1/sso/oidc/callback route and
		// non-empty on the legacy path-cid form. Connection identity comes
		// from state.CID after Consume — pathCID is used only as a
		// defence-in-depth check when the legacy route fires.
		pathCID := r.PathValue("cid")
		if pathCID != "" {
			observability.Global.SSOLegacyCallbackTotal.WithLabelValues(pathCID).Inc()
		}
		code := r.URL.Query().Get("code")
		stateToken := r.URL.Query().Get("state")
		if code == "" || stateToken == "" {
			slog.Warn("sso: callback: missing parameters", "path_cid", pathCID, "code_present", code != "", "state_present", stateToken != "")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}

		stateData, err := opts.StateStore.Consume(r.Context(), stateToken)
		if err != nil {
			slog.Warn("sso: callback: state invalid", "path_cid", pathCID, "err", err)
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}
		// On the legacy path-cid route, the path cid and state cid must
		// agree. State is the source of truth for the connection (it's the
		// 256-bit CSPRNG nonce minted at initiate time and bound to the
		// connection there); the path-cid check is a second seam against
		// a hypothetical state-store compromise where an attacker rewrites
		// a state record's CID. The standard route bypasses this check
		// because there is no path cid to compare.
		if pathCID != "" && stateData.CID != pathCID {
			slog.Warn("sso: callback: state cid mismatch", "path_cid", pathCID, "state_cid", stateData.CID)
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}
		cid := stateData.CID

		conn, err := opts.Store.GetSSOConnectionByID(r.Context(), cid)
		if err != nil {
			slog.Warn("sso: callback: load connection", "cid", cid, "err", err)
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}
		if conn.Status != model.SSOStatusActive || conn.Protocol != model.SSOProtocolOIDC {
			slog.Warn("sso: callback: connection not active oidc", "cid", cid, "status", conn.Status, "protocol", conn.Protocol)
			recordLoginFailed(r, opts.Store, conn, "connection_inactive")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}

		clientSecret, err := decryptClientSecret(conn)
		if err != nil {
			slog.Error("sso: callback: decrypt client secret", "cid", cid, "err", err)
			recordLoginFailed(r, opts.Store, conn, "client_secret_unavailable")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}

		doc, err := opts.Validator.Discovery(r.Context(), conn)
		if err != nil {
			slog.Error("sso: callback: discovery", "cid", cid, "err", err)
			recordLoginFailed(r, opts.Store, conn, "discovery_unavailable")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}

		idToken, err := exchangeCode(r.Context(), httpClient, doc.TokenEndpoint, codeExchangeParams{
			Code:         code,
			CodeVerifier: stateData.CodeVerifier,
			ClientID:     conn.OIDCClientID,
			ClientSecret: clientSecret,
			// Must match the redirect_uri that initiate sent at authorize time
			// (RFC 6749 §4.1.3). Initiate now always uses the cid-less standard
			// form regardless of which route the IdP redirects back to, so the
			// exchange URI is fixed.
			RedirectURI: publicHost + CallbackPath,
		})
		if err != nil {
			slog.Warn("sso: callback: code exchange", "cid", cid, "err", err)
			recordLoginFailed(r, opts.Store, conn, "code_exchange_failed")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}

		claims, err := opts.Validator.ValidateIDToken(r.Context(), conn, idToken, stateData.Nonce)
		if err != nil {
			slog.Warn("sso: callback: id token invalid", "cid", cid, "err", err)
			recordLoginFailed(r, opts.Store, conn, "id_token_invalid")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}

		sub, email, name, groups, err := extractClaims(claims, conn)
		if err != nil {
			slog.Warn("sso: callback: claim extraction", "cid", cid, "err", err)
			recordLoginFailed(r, opts.Store, conn, "claim_extraction_failed")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}

		// Anti-spoofing (design §11.1): the IdP can claim any email; we
		// trust only emails on a domain the customer admin has proved
		// control over via DNS verification. The verified domain row must
		// also be wired to THIS connection — preventing a misconfigured
		// org with two connections from cross-routing.
		dom := emailDomain(email)
		ssoDomain, err := opts.Store.GetVerifiedSSODomainByName(r.Context(), dom)
		if err != nil {
			slog.Warn("sso: callback: domain not verified", "cid", cid, "domain", dom, "err", err)
			recordLoginFailed(r, opts.Store, conn, "domain_unverified")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}
		if ssoDomain.OrganizationID != conn.OrganizationID || ssoDomain.SSOConnectionID != conn.ID {
			slog.Warn("sso: callback: domain bound to different connection",
				"cid", cid,
				"domain", dom,
				"expected_org", conn.OrganizationID,
				"got_org", ssoDomain.OrganizationID,
				"expected_conn", conn.ID,
				"got_conn", ssoDomain.SSOConnectionID,
			)
			recordLoginFailed(r, opts.Store, conn, "domain_cross_org")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}

		// All RLS-scoped operations from here on need the org on context.
		ctx := storage.WithOrganizationID(r.Context(), conn.OrganizationID)

		user, err := opts.Store.UpsertUser(ctx, conn.OrganizationID, sub, email, name)
		if err != nil {
			slog.Error("sso: callback: upsert user", "cid", cid, "err", err)
			recordLoginFailed(r, opts.Store, conn, "upsert_user_failed")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}

		// Pending-invitation precedence (design §10.4): if the email matches
		// a pending_memberships row for this org, redeem it instead of JIT.
		// The invite carries an explicit role choice from the admin and
		// MUST win — silently degrading to JIT on a redeem failure would
		// assign the user a different role than the admin chose, with no
		// visible signal. Fail the login so the user re-attempts SSO and
		// the redeem retries; the admin's role choice is never silently
		// bypassed even on a transient DB error here.
		//
		// The cross-flow race (user hits both POST
		// /v1/auth/invitations/redeem and this callback for the same
		// org+email simultaneously) is also covered: the loser of the
		// FOR-UPDATE on pending_memberships sees (false, nil), falls
		// through to JIT, but JITProvisionMembership's provenance guard
		// in jit.go skips the role update because the existing membership
		// has provisioned_via='invitation' rather than 'jit'.
		invited, redeemErr := opts.Store.RedeemPendingInvitation(ctx, conn.OrganizationID, user.ID, email)
		if redeemErr != nil {
			slog.Error("sso: callback: redeem pending invitation", "cid", cid, "err", redeemErr)
			recordLoginFailed(r, opts.Store, conn, "invitation_redeem_failed")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}
		if !invited {
			mappings, listErr := opts.Store.ListSSOGroupMappings(ctx, conn.ID)
			if listErr != nil {
				slog.Error("sso: callback: list group mappings", "cid", cid, "err", listErr)
				recordLoginFailed(r, opts.Store, conn, "group_mappings_unavailable")
				http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
				return
			}
			role := JITResolveRole(mappings, groups, conn.DefaultRole)
			outcome, err := JITProvisionMembership(ctx, opts.Store, conn.OrganizationID, user.ID, role)
			if err != nil {
				slog.Error("sso: callback: jit provision", "cid", cid, "err", err)
				recordLoginFailed(r, opts.Store, conn, "jit_failed")
				http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
				return
			}
			// Distinct audit actions for first-time provision vs re-login
			// role change (design §10.3). Noop case (re-login, role
			// unchanged) intentionally writes nothing — auditing every
			// SSO login as a JIT event would drown the trail in noise.
			meta := map[string]any{
				"connection_id": conn.ID,
				"role":          role,
				"groups":        groups,
			}
			switch outcome {
			case JITOutcomeCreated:
				recordEvent(r, opts.Store, conn, user.ID, email, model.AuditActionSSOJITProvisioned, meta)
			case JITOutcomeUpdated:
				recordEvent(r, opts.Store, conn, user.ID, email, model.AuditActionSSOJITRoleUpdated, meta)
			}
		}

		mint, err := opts.Sessions.MintSession(ctx, auth.MintRequest{
			UserID:         user.ID,
			OrganizationID: conn.OrganizationID,
			AuthMode:       model.AuthModeSSO,
			IP:             callbackClientIP(r),
			UserAgent:      r.Header.Get("User-Agent"),
		})
		if err != nil {
			slog.Error("sso: callback: mint session", "cid", cid, "err", err)
			recordLoginFailed(r, opts.Store, conn, "mint_session_failed")
			http.Redirect(w, r, callbackErrorRedirect, http.StatusFound)
			return
		}

		auth.SetSession(w, r, opts.CookieConfig, mint.PlaintextToken, mint.ExpiresAt)
		recordEvent(r, opts.Store, conn, user.ID, email, model.AuditActionSSOLoginSucceeded, map[string]any{
			"connection_id":        conn.ID,
			"redeemed_invitation":  invited,
			"protocol":             "oidc",
		})

		// Defense-in-depth: re-validate the state-stored RedirectAfterLogin
		// at the redirect site (not just at the initiate-time boundary)
		// so that any corruption between persist and consume — storage
		// bug, hostile cache write, post-validation tampering — cannot
		// turn the callback into an open-redirect. Architect N4 §5.5
		// "regardless of state content" acceptance.
		target := ValidatedReturnTo(stateData.RedirectAfterLogin)
		if target == "" {
			target = "/"
		}
		http.Redirect(w, r, target, http.StatusFound)
	})
}

// codeExchangeParams collects the fields the token-endpoint POST needs.
type codeExchangeParams struct {
	Code         string
	CodeVerifier string
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

// tokenResponse captures the fields of an RFC 6749 §5.1 token response we
// read. access_token, token_type, expires_in are present in the wire shape
// but we don't consume them — v1 doesn't use refresh tokens or call any
// IdP API beyond the token-exchange.
type tokenResponse struct {
	IDToken string `json:"id_token"`
	Error   string `json:"error,omitempty"`
}

// exchangeCode POSTs the authorization code to the IdP token_endpoint and
// returns the id_token. Error shapes per RFC 6749 §5.2 (json `error` field)
// are surfaced as Go errors with the IdP-supplied error code so the
// callback can distinguish invalid_grant from server_error.
func exchangeCode(ctx context.Context, client *http.Client, tokenEndpoint string, p codeExchangeParams) (string, error) {
	if tokenEndpoint == "" {
		return "", errors.New("token endpoint missing from discovery doc")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {p.Code},
		"redirect_uri":  {p.RedirectURI},
		"code_verifier": {p.CodeVerifier},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
	}

	postCtx, cancel := context.WithTimeout(ctx, codeExchangeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("post token endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Cap the response body. A real token-endpoint response is a few KB; a
	// malicious or broken IdP streaming MB+ would otherwise exhaust memory
	// per request. 1 MiB leaves ample headroom for any sane payload.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("parse response: %w (status=%d)", err, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || tr.Error != "" {
		errCode := tr.Error
		if errCode == "" {
			errCode = fmt.Sprintf("http_%d", resp.StatusCode)
		}
		return "", fmt.Errorf("token endpoint rejected: %s", errCode)
	}
	if tr.IDToken == "" {
		return "", errors.New("token endpoint returned no id_token")
	}
	return tr.IDToken, nil
}

// decryptClientSecret returns the plaintext OIDC client_secret for the
// connection. The ciphertext was hex-encoded by crypto.Encrypt at the
// connection-CRUD callsite and stored as text bytes; we round-trip
// through string(...) to feed it back to crypto.Decrypt which expects
// the hex form.
func decryptClientSecret(conn model.SSOConnection) (string, error) {
	if len(conn.OIDCClientSecretCiphertext) == 0 {
		return "", errors.New("connection has no client secret ciphertext")
	}
	plaintext, err := crypto.Decrypt(string(conn.OIDCClientSecretCiphertext))
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

// extractClaims pulls subject / email / name / groups from the validated
// ID token. The subject choice is connection-aware: Entra (OIDCTenantID
// non-empty) prefers `oid` because `sub` is per-app and changes if the
// admin re-registers. Non-Entra OIDC uses `sub`.
//
// Email fallback chain (design §9.4 — Entra B2B guests):
//   1. `email` claim if present
//   2. `preferred_username` if it parses as an email
//   3. `upn` if it parses as an email
// Returns an error if no usable email surfaces — every flow needs an email
// for domain verification.
//
// Groups are read as a string array; missing claim is treated as empty
// (not an error — JIT falls through to default_role).
func extractClaims(claims jwt.MapClaims, conn model.SSOConnection) (sub, email, name string, groups []string, err error) {
	if conn.OIDCTenantID != "" {
		sub, _ = claims["oid"].(string)
		if sub == "" {
			sub, _ = claims["sub"].(string) // fallback for Entra v1 tokens
		}
	} else {
		sub, _ = claims["sub"].(string)
	}
	if sub == "" {
		return "", "", "", nil, errors.New("subject claim missing")
	}

	for _, key := range []string{"email", "preferred_username", "upn"} {
		v, _ := claims[key].(string)
		if v == "" {
			continue
		}
		// net/mail.ParseAddress rejects malformed shapes (`@`, `user@`,
		// `@domain`) that a bare strings.Contains "@" would accept. Catches
		// pathological IdP claims at this boundary instead of letting them
		// surface downstream as "domain_unverified".
		if _, err := mail.ParseAddress(v); err == nil {
			email = v
			break
		}
	}
	if email == "" {
		return "", "", "", nil, errors.New("no usable email in claims")
	}

	name, _ = claims["name"].(string)

	switch g := claims["groups"].(type) {
	case []any:
		groups = make([]string, 0, len(g))
		for _, v := range g {
			if s, ok := v.(string); ok {
				groups = append(groups, s)
			}
		}
	case []string:
		groups = g
	}

	return sub, email, name, groups, nil
}

// recordLoginFailed writes an AuditActionSSOLoginFailed row. Failure-path
// helper that takes only the reason bucket — the connection + request
// supply the rest. Reasons are coarse buckets, never raw error text.
func recordLoginFailed(r *http.Request, w audit.Writer, conn model.SSOConnection, reason string) {
	if conn.ID == "" || conn.OrganizationID == "" {
		return // no org context — nothing to write to
	}
	enriched := middleware.WithOrganizationID(r.Context(), conn.OrganizationID)
	r = r.WithContext(enriched)
	audit.Record(r, w, model.AuditEvent{
		Action:       model.AuditActionSSOLoginFailed,
		ResourceType: "sso_connection",
		ResourceID:   conn.ID,
		Metadata: map[string]any{
			"connection_id": conn.ID,
			"protocol":      "oidc",
			"reason":        reason,
		},
	})
}

// recordEvent enriches the ctx with org+user+email so audit.Record's
// from-context fields populate correctly, then writes the event.
func recordEvent(r *http.Request, w audit.Writer, conn model.SSOConnection, userID, email, action string, metadata map[string]any) {
	if conn.OrganizationID == "" || userID == "" {
		return
	}
	ctx := middleware.WithOrganizationID(r.Context(), conn.OrganizationID)
	ctx = middleware.WithUserID(ctx, userID)
	ctx = middleware.WithUserEmail(ctx, email)
	r = r.WithContext(ctx)
	audit.Record(r, w, model.AuditEvent{
		Action:       action,
		ResourceType: "sso_connection",
		ResourceID:   conn.ID,
		Metadata:     metadata,
	})
}

// callbackClientIP returns the request's client IP as net.IP for session
// binding. XFF-aware (production sits behind nginx/App Runner that
// overwrites the header). Returns nil if neither XFF nor RemoteAddr
// yields a parseable IP — auth.MintSession persists IP=nil as NULL.
func callbackClientIP(r *http.Request) net.IP {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if first, _, ok := strings.Cut(fwd, ","); ok {
			if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
				return ip
			}
		}
		if ip := net.ParseIP(strings.TrimSpace(fwd)); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}
