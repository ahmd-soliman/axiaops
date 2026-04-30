package auth

import (
	"errors"
	"net/http"
)

// Identity is the caller resolved from a request. The auth provider returns
// this to the middleware, which copies the fields onto the request context
// for handlers to read via middleware.OrganizationID / .UserID / .Role.
//
// AuthMode is one of "password" | "sso" | "bootstrap" | "kinde". The first
// three come from sessions.auth_mode; "kinde" is set by the legacy provider
// that wraps the existing JWT path during the strangler window.
type Identity struct {
	UserID           string
	OrganizationID   string
	Role             string
	AuthMode         string
	Email            string
	SessionID        string // for native; empty under Kinde
	SessionTokenHash string // for native; cache-eviction key on logout
}

// ErrUnauthenticated is returned by Provider.Authenticate when the request
// cannot be resolved to a live identity (no cookie, expired session,
// revoked session, malformed JWT, ...). The middleware maps every
// non-nil error from Authenticate to HTTP 401 — callers MUST NOT echo
// the underlying reason to the client (architect §11 logging-discipline).
var ErrUnauthenticated = errors.New("auth: unauthenticated")

// Provider authenticates an incoming request. Self-hosted v1 ships
// nativeProvider (cookie + sessions table). The strangler `both` mode
// chains a compositeProvider; the legacy `kinde` mode plugs in a
// kindeProvider that wraps the existing Auth middleware.
//
// Implementations MUST be safe for concurrent use across requests.
type Provider interface {
	Authenticate(r *http.Request) (Identity, error)
}
