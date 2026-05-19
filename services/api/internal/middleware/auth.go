// Package middleware provides HTTP middleware for the AxiaOps API service:
// native cookie-session auth (via the auth.Provider seam in auth_native.go),
// DEV_MODE bypass, authorisation, rate limiting, and request-ID tagging.
package middleware

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const (
	organizationIDKey contextKey = "organization_id"
	userIDKey         contextKey = "user_id"
	userEmailKey      contextKey = "user_email"
	userNameKey       contextKey = "user_name"
	roleKey           contextKey = "role"
	authModeKey       contextKey = "auth_mode"
)

// publicPath reports whether the path bypasses authentication. Caller is
// net/http.ServeMux which path-cleans the URL before invoking the handler
// chain, so traversal-style inputs (`..`, `//`) are not reachable here.
// Four families bypass:
//
//  1. Infra: /metrics, /health, /livez, /readyz — must remain reachable
//     from container orchestration and Prometheus without a session.
//  2. Auth ceremony: /v1/auth/bootstrap, /v1/auth/login,
//     /v1/auth/invitations/redeem, /v1/auth/password-reset/redeem —
//     the endpoints used to *acquire* authentication. /v1/auth/logout
//     is also bypassed (the handler tolerates a missing/invalid cookie
//     and clears whatever's there).
//  3. SSO discovery: /v1/sso/discover — the email-blur lookup that decides
//     whether to redirect to an IdP or reveal the password field. Pre-auth
//     by definition. The handler returns a constant response shape and
//     pads response time to mask whether the domain is verified, so the
//     bypass doesn't introduce an enumeration channel.
//  4. OIDC ceremony: /v1/sso/oidc/{cid}/initiate, /v1/sso/oidc/callback
//     (cid-less standard form per Tasks.md 2.7.22), and the legacy
//     /v1/sso/oidc/{cid}/callback (deprecation window) — all pre-session
//     (browser pre-redirect / browser post-IdP). Suffix-match explicitly so
//     a future authenticated SSO-management route (e.g.
//     /v1/sso/oidc/{cid}/settings) does NOT silently bypass auth.
func publicPath(p string) bool {
	switch p {
	case "/health", "/livez", "/readyz", "/metrics", "/v1/sso/discover":
		return true
	// Cid-less OIDC callback (Tasks.md 2.7.22). Exact match — kept distinct
	// from the prefix+suffix branch below so a future authenticated route
	// like /v1/sso/oidc/manage/callback can't slip through that pattern.
	case "/v1/sso/oidc/callback":
		return true
	}
	if strings.HasPrefix(p, "/v1/sso/oidc/") &&
		(strings.HasSuffix(p, "/initiate") || strings.HasSuffix(p, "/callback")) {
		return true
	}
	return strings.HasPrefix(p, "/v1/auth/")
}

// OrganizationID returns the internal organization UUID from the request context.
func OrganizationID(ctx context.Context) string {
	id, _ := ctx.Value(organizationIDKey).(string)
	return id
}

// WithOrganizationID returns a child context with the organization ID set.
// Used by handlers that establish an organization context outside the auth
// middleware path — specifically the SSO callback, which derives the org
// from the connection (looked up by cid in the URL) before any session
// exists. The audit helper reads from the same key, so callbacks can write
// audit events with the right organization scoping.
func WithOrganizationID(ctx context.Context, organizationID string) context.Context {
	return context.WithValue(ctx, organizationIDKey, organizationID)
}

// WithUserID returns a child context with the user ID set. Companion to
// WithOrganizationID for the same use case.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// WithUserEmail returns a child context with the user email set. Companion
// to WithOrganizationID for the same use case.
func WithUserEmail(ctx context.Context, email string) context.Context {
	return context.WithValue(ctx, userEmailKey, email)
}

// WithUserName returns a child context with the user's display name set.
// Stamped into audit_log.actor_name at write time so audit history survives
// later renames or GDPR anonymisation.
func WithUserName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, userNameKey, name)
}

// UserID returns the stable user identifier from the request context.
// In production this is the UUID from the users table (set after the
// auth provider resolves the session); under DevBypass it is DEV_USER_ID.
// Returns "" if unset.
func UserID(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

// UserEmail returns the authenticated user's email from the request context.
// Returns "" if unset.
func UserEmail(ctx context.Context) string {
	email, _ := ctx.Value(userEmailKey).(string)
	return email
}

// UserName returns the authenticated user's display name from the request
// context. Returns "" if unset — callers fall back to UserEmail.
func UserName(ctx context.Context) string {
	name, _ := ctx.Value(userNameKey).(string)
	return name
}

// Role returns the membership role ("owner"|"admin"|"member"|"viewer")
// resolved by the auth middleware for the bound (organization, user)
// pair. Empty under DevBypass (handlers must fall back to store.RoleOf).
func Role(ctx context.Context) string {
	role, _ := ctx.Value(roleKey).(string)
	return role
}

// AuthMode returns the auth_mode of the active session: "password",
// "sso", or "bootstrap". Empty when no auth has run on the request
// (DevBypass / pre-auth path).
//
// Used by handlers that need to enforce SSO requirement (B2 §5.2 will
// 403 native-password sessions for orgs whose enforcement is "required").
func AuthMode(ctx context.Context) string {
	mode, _ := ctx.Value(authModeKey).(string)
	return mode
}

// DevBypass injects a fixed organization + user identity into every request context.
// Only active when DEV_MODE=true — local development without auth.
// The organization and user rows are ensured once at service startup (see cmd/main.go),
// so this middleware does no DB work per request.
func DevBypass(organizationID, userID, userEmail, userName string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		ctx = context.WithValue(ctx, organizationIDKey, organizationID)
		ctx = context.WithValue(ctx, userIDKey, userID)
		ctx = context.WithValue(ctx, userEmailKey, userEmail)
		ctx = context.WithValue(ctx, userNameKey, userName)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
