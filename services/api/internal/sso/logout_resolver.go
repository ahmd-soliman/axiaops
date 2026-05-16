package sso

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// LogoutResolver implements auth.SSOLogoutResolver. It turns an SSO-minted
// session into the URL the dashboard navigates to when the user signs out,
// so the IdP session dies in lockstep with our session.
//
// The flow it builds:
//
//	GET <end_session_endpoint>?id_token_hint=<jwt>
//	                          &client_id=<oidc_client_id>
//	                          &post_logout_redirect_uri=<PUBLIC_HOST>/login
//
// id_token_hint is RECOMMENDED by OIDC RP-Initiated Logout 1.0 and lets the
// IdP skip its "are you sure?" confirm prompt — without it, mature OPs
// either render a confirm screen (Keycloak default) or 400 the request
// outright (Okta with strict admin policy).
//
// The resolver is intentionally tolerant — every failure path collapses to
// ("", nil) so the logout handler falls back to the legacy 204 shape rather
// than 500-ing on the user. Losing silent-logout polish is acceptable;
// blocking sign-out is not.
type LogoutResolver struct {
	store      storage.Store
	discovery  discoveryFetcher
	publicHost string
}

// discoveryFetcher is the minimum surface LogoutResolver needs from the
// OIDC validator. Narrowed to an interface so tests can supply a fake
// without spinning up an HTTP IdP fixture. *Validator satisfies it via
// Validator.Discovery.
type discoveryFetcher interface {
	Discovery(ctx context.Context, conn model.SSOConnection) (DiscoveryDoc, error)
}

// NewLogoutResolver wires a resolver. publicHost is the externally-reachable
// origin (e.g. https://app.example.com) used to build post_logout_redirect_uri.
// Empty publicHost omits the param — most OPs default to their own
// post-logout page in that case, which is acceptable but a worse UX than
// landing back on /login.
func NewLogoutResolver(store storage.Store, discovery discoveryFetcher, publicHost string) *LogoutResolver {
	return &LogoutResolver{
		store:      store,
		discovery:  discovery,
		publicHost: strings.TrimRight(publicHost, "/"),
	}
}

// ResolveLogoutURL fulfils auth.SSOLogoutResolver. Returns ("", nil) for
// non-SSO sessions, missing id_token, missing connection, missing
// end_session_endpoint, or any decrypt/discovery failure — the logout
// handler treats an empty URL as "fall back to 204".
func (r *LogoutResolver) ResolveLogoutURL(ctx context.Context, sess model.Session) (string, error) {
	// Fast paths that aren't errors — collapse to empty so the handler
	// falls through to the legacy 204 shape without logging warnings.
	if sess.AuthMode != model.AuthModeSSO {
		return "", nil
	}
	if sess.IDTokenEncrypted == "" {
		// Either an SSO session minted before migration 027 landed, or
		// callback-side encryption fell through. Either way, no hint to
		// pass — and without a hint many OPs reject the logout call.
		return "", nil
	}

	connID, err := r.store.GetUserSSOConnectionID(ctx, sess.UserID)
	if err != nil {
		// User row gone (right-to-erasure cascaded) is not a logout
		// failure — fall back to 204.
		if errors.Is(err, storage.ErrUserNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("get user sso connection: %w", err)
	}
	if connID == "" {
		// User row exists but has no connection — possible if the
		// connection was deleted after the session was minted, or the
		// user was demoted to native. Nothing to log out from.
		return "", nil
	}

	conn, err := r.store.GetSSOConnectionByID(ctx, connID)
	if err != nil {
		if errors.Is(err, storage.ErrSSOConnectionNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("get connection: %w", err)
	}

	doc, err := r.discovery.Discovery(ctx, conn)
	if err != nil {
		return "", fmt.Errorf("discovery: %w", err)
	}
	if doc.EndSessionEndpoint == "" {
		// Older OIDC OPs and some minimally-conformant federations
		// don't advertise the endpoint. Nothing we can do; fall back.
		return "", nil
	}

	idToken, err := crypto.Decrypt(sess.IDTokenEncrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt id_token: %w", err)
	}

	// Parse the endpoint properly rather than appending with a sep char.
	// String-concat works for the "no query, no fragment" common case but
	// silently breaks if the IdP advertises an endpoint with a fragment
	// (`https://idp/logout#section`) — appending `?...` after a fragment
	// places the query in the fragment portion, which the browser strips
	// from the wire request, and the IdP then sees an unauthenticated
	// logout call (rejected, or shows a confirm prompt as if no
	// id_token_hint). Merging via url.Values avoids both cases.
	endURL, err := url.Parse(doc.EndSessionEndpoint)
	if err != nil {
		return "", fmt.Errorf("parse end_session_endpoint: %w", err)
	}
	q := endURL.Query()
	q.Set("id_token_hint", idToken)
	if conn.OIDCClientID != "" {
		q.Set("client_id", conn.OIDCClientID)
	}
	if r.publicHost != "" {
		q.Set("post_logout_redirect_uri", r.publicHost+"/login")
	}
	endURL.RawQuery = q.Encode()
	return endURL.String(), nil
}
