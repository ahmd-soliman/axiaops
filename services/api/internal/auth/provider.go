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
// single-method seam so a SaaS reactivation can swap in a remote-IdP
// impl without touching the middleware chain.
//
// Implementations MUST be safe for concurrent use across requests.
type Provider interface {
	Authenticate(r *http.Request) (Identity, error)
}
