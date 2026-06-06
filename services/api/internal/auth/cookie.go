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

// CookieConfig controls the per-deployment cookie attributes that don't
// depend on the request: Path and Domain. The Secure flag is decided
// per-request from the request's TLS state (see secureFromRequest) so
// production-behind-TLS and local-dev-via-HTTPS both work without an
// env-var knob, and a misconfigured deployment can't accidentally set
// Secure=false on real production traffic.
type CookieConfig struct {
	// Domain optionally pins the cookie to a host. Empty leaves it
	// host-only — the browser scopes to the exact host that issued it.
	Domain string
	// Path is the URL prefix the cookie is sent for; "/" covers the whole
	// app surface. Tightening this to "/v1" would shave a byte off static
	// asset requests but break preview routes.
	Path string
}

// NewCookieConfig returns the default config — Path "/", no Domain.
// The Secure attribute is no longer a config knob; it's derived
// per-request from r.TLS / X-Forwarded-Proto.
func NewCookieConfig() CookieConfig {
	return CookieConfig{Path: "/"}
}

// secureFromRequest reports whether the request arrived over HTTPS,
// either directly (r.TLS != nil — uncommon for our deployments since
// we sit behind a TLS terminator) or transitively via a proxy that
// set X-Forwarded-Proto. The header is trusted because in every
// supported deployment shape (ECS Express, Docker-Compose-with-nginx,
// production behind any reverse proxy) the LB / nginx is the only
// hop that talks to the API container — direct exposure of :8080 to
// the public internet is unsupported and documented as such.
//
// In local docker-compose the dashboard nginx terminates TLS at
// :8443 and forwards to api:8080 with X-Forwarded-Proto: https,
// matching the production shape exactly.
func secureFromRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

// SetSession writes the session cookie. token is the PLAINTEXT token
// (never the hash). The caller has already minted it via session.MintSession.
// Secure is derived from the incoming request's TLS state.
//
// SameSite=Lax is the right default for a server-rendered admin tool — it
// blocks CSRF on most cross-site POSTs while still letting top-level GET
// navigations from /accept-invite-style links carry the cookie. Strict
// would break the OOB redemption URL flow (the link is external by design).
func SetSession(w http.ResponseWriter, r *http.Request, cfg CookieConfig, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     cfg.Path,
		Domain:   cfg.Domain,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   secureFromRequest(r),
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
// originally-set cookie; the Secure attribute MUST also match (browsers
// scope cookies by Secure flag), so we derive it from the same request
// state as SetSession would.
func ClearSession(w http.ResponseWriter, r *http.Request, cfg CookieConfig) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     cfg.Path,
		Domain:   cfg.Domain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secureFromRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}
