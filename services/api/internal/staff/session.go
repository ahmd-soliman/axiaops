package staff

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/shared/cache"
)

// StaffSessionCookieName carries the opaque staff session token. A distinct
// name from the tenant axiaops_session cookie keeps the two planes' cookies
// from ever colliding on a shared parent domain (design §3 plane separation).
const StaffSessionCookieName = "axiaops_staff_session"

// staffSessionCachePrefix namespaces staff session keys in the shared cache.
const staffSessionCachePrefix = "staff:sess:"

// sessionValue is the cache payload behind a staff session token.
type sessionValue struct {
	StaffUserID string `json:"staff_user_id"`
}

// SessionManager mints, validates, and revokes cache-backed staff sessions.
//
// Cache-backed (not a PG table) is a deliberate beta trade (admin-portal
// plan, Slice 3): the admin plane is low-volume + internal, so a cache flush
// logging staff out is acceptable and avoids a new table. The cache is the
// in-memory impl when no Redis is wired, so this works in single-replica dev
// without a backend. A future multi-replica admin plane swaps to a durable
// staff_sessions table behind this same Manager.
type SessionManager struct {
	cache cache.Cache
	ttl   time.Duration
}

// NewSessionManager returns a manager. ttl bounds how long a staff session
// stays valid without re-login.
func NewSessionManager(c cache.Cache, ttl time.Duration) *SessionManager {
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	return &SessionManager{cache: c, ttl: ttl}
}

// Mint creates a session for staffUserID and writes the cookie. Returns the
// token hash (eviction key) for audit/telemetry.
func (m *SessionManager) Mint(ctx context.Context, w http.ResponseWriter, r *http.Request, staffUserID string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("staff: mint token entropy: %w", err)
	}
	plaintext := base64.RawURLEncoding.EncodeToString(buf)
	hash := auth.HashToken(plaintext)

	payload, err := json.Marshal(sessionValue{StaffUserID: staffUserID})
	if err != nil {
		return "", fmt.Errorf("staff: marshal session: %w", err)
	}
	if err := m.cache.Set(ctx, staffSessionCachePrefix+hash, payload, m.ttl); err != nil {
		return "", fmt.Errorf("staff: persist session: %w", err)
	}

	expires := time.Now().Add(m.ttl)
	setStaffCookie(w, r, plaintext, expires)
	return hash, nil
}

// resolve looks up the staffUserID behind a plaintext token. Returns
// auth-failure (false) on any miss/error — never leaks the reason.
func (m *SessionManager) resolve(ctx context.Context, plaintextToken string) (staffUserID, tokenHash string, ok bool) {
	if plaintextToken == "" {
		return "", "", false
	}
	hash := auth.HashToken(plaintextToken)
	raw, err := m.cache.Get(ctx, staffSessionCachePrefix+hash)
	if err != nil {
		return "", "", false // cache.ErrNotFound or transport error → unauthenticated
	}
	var v sessionValue
	if err := json.Unmarshal(raw, &v); err != nil || v.StaffUserID == "" {
		return "", "", false
	}
	return v.StaffUserID, hash, true
}

// Revoke deletes the session behind the request cookie and clears the cookie.
// Tolerant — no error when the cookie is absent or already gone.
func (m *SessionManager) Revoke(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(StaffSessionCookieName); err == nil && c.Value != "" {
		_ = m.cache.Del(ctx, staffSessionCachePrefix+auth.HashToken(c.Value))
	}
	clearStaffCookie(w, r)
}

// ── cookie helpers (mirror auth/cookie.go, distinct name) ───────────────────

func secureFromRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

func setStaffCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     StaffSessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   secureFromRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearStaffCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     StaffSessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secureFromRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// readStaffCookie returns the cookie value or "".
func readStaffCookie(r *http.Request) string {
	c, err := r.Cookie(StaffSessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// ErrUnauthenticated is returned by SessionProvider.Authenticate when the
// request cannot be resolved to a live, active staff principal.
var ErrUnauthenticated = errors.New("staff: unauthenticated")
