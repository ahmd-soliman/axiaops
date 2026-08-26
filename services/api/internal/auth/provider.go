package auth

import (
	"errors"
	"net/http"
)

// Identity is the caller resolved from a request. The auth provider returns
// this to the middleware, which copies the fields onto the request context
// for handlers to read via middleware.OrganizationID / .UserID / .Role.
//
// AuthMode is one of "password" | "sso" | "bootstrap" — all sourced from
// the sessions.auth_mode column.
type Identity struct {
	UserID           string
	OrganizationID   string
	Role             string
	AuthMode         string
	Email            string
	// Name is the user's display name at session-resolution time. Used to
	// stamp audit_log.actor_name on writes, so anonymisation/rename later
	// can't rewrite history. Empty string when the user has no display name
	// set — audit rendering falls back to Email.
	Name             string
	SessionID        string
	SessionTokenHash string // cache-eviction key on logout
}

// ErrUnauthenticated is returned by Provider.Authenticate when the request
// cannot be resolved to a live identity (no cookie, expired session,
// revoked session, ...). The middleware maps every non-nil error from
// Authenticate to HTTP 401 — callers MUST NOT echo the underlying reason
// to the client (architect §11 logging-discipline).
var ErrUnauthenticated = errors.New("auth: unauthenticated")

// Provider authenticates an incoming request. Self-hosted v1 ships
// NativeProvider (cookie + sessions table); the interface stays as a
// single-method seam so a different impl (e.g. a remote IdP) can swap in
// without touching the middleware chain.
//
// Implementations MUST be safe for concurrent use across requests.
type Provider interface {
	Authenticate(r *http.Request) (Identity, error)
}
