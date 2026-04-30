package auth

import (
	"net/http"
	"time"
)

// SessionCookieName is the cookie carrying the opaque session token.
//
// The cookie value is the plaintext token only — the server stores
// SHA-256(token) in sessions.session_token_hash. A leak of the cookie value
// IS a leak of the session; the cookie attributes (HttpOnly, Secure,
// SameSite=Lax) are the front-line mitigations and the per-user cap +
// admin-revocation paths are the back-line.
const SessionCookieName = "axiaops_session"

// CookieConfig controls cookie attributes. The defaults via NewCookieConfig()
// match the production posture; tests / DEV_MODE flip Secure off so the
// browser doesn't drop the cookie on plaintext localhost.
type CookieConfig struct {
	// Secure adds the Secure attribute — the cookie is only sent over HTTPS.
	// Set to false in DEV_MODE so http://localhost:5173 can authenticate.
	Secure bool
	// Domain optionally pins the cookie to a host. Empty leaves it
	// host-only — the browser scopes to the exact host that issued it.
	Domain string
	// Path is the URL prefix the cookie is sent for; "/" covers the whole
	// app surface. Tightening this to "/v1" would shave a byte off static
	// asset requests but break preview routes.
	Path string
}

// NewCookieConfig returns the default config for a given DEV_MODE flag.
// devMode=true means we are running on plaintext localhost; everywhere else,
// Secure is set.
func NewCookieConfig(devMode bool) CookieConfig {
	return CookieConfig{
		Secure: !devMode,
		Path:   "/",
	}
}

// SetSession writes the session cookie. token is the PLAINTEXT token
// (never the hash). The caller has already minted it via session.MintSession.
//
// SameSite=Lax is the right default for a server-rendered admin tool — it
// blocks CSRF on most cross-site POSTs while still letting top-level GET
// navigations from /accept-invite-style links carry the cookie. Strict
// would break the OOB redemption URL flow (the link is external by design).
func SetSession(w http.ResponseWriter, cfg CookieConfig, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     cfg.Path,
		Domain:   cfg.Domain,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ReadSession returns the cookie value or "" if absent. Callers that need to
// distinguish "no cookie" from "empty cookie" must inspect r.Cookie directly,
// but the auth middleware treats both as unauthenticated.
func ReadSession(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// ClearSession overwrites the cookie with an immediately-expired one — the
// browser drops it on the next render. The Path/Domain MUST match the
// originally-set cookie; passing the same CookieConfig used by SetSession
// keeps that aligned without the caller having to remember.
func ClearSession(w http.ResponseWriter, cfg CookieConfig) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     cfg.Path,
		Domain:   cfg.Domain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}
