package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/google/uuid"

	"axiaops.io/shared/model"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/storage"
)

// SessionMinter / SessionValidator are the surface this package exports for
// handlers and middleware. Bundled into one struct because they share the
// Store and the cache; splitting would just multiply constructor params.

// Default knobs. The plan §4.5 documents these as env-overridable; the
// composition root in cmd/main.go reads the env and passes a Config in.
const (
	// DefaultSessionTTL — see SESSION_TTL_HOURS (default 24h).
	DefaultSessionTTL = 24 * time.Hour
	// DefaultSessionsPerUserCap — architect C2 (SESSIONS_PER_USER_CAP).
	// 0 disables the cap entirely.
	DefaultSessionsPerUserCap = 10
	// sessionTokenBytes is the entropy of the plaintext token before
	// base64url-encoding. 32 bytes → 256 bits, far above the 128-bit
	// floor for capability tokens.
	sessionTokenBytes = 32
)

// RevokeReason is the metric label fed into AuthSessionRevocationsTotal.
type RevokeReason string

const (
	RevokeReasonLogout            RevokeReason = "logout"
	RevokeReasonPasswordReset     RevokeReason = "password_reset"
	RevokeReasonAdminRevoke       RevokeReason = "admin_revoke"
	RevokeReasonCapExceeded       RevokeReason = "cap_exceeded"
	RevokeReasonEnforcementChange RevokeReason = "enforcement_change"
	RevokeReasonOrgSwitch         RevokeReason = "org_switch"
)

// Config tunes the session manager. The composition root in cmd/main.go
// reads env vars (SESSION_TTL_HOURS, SESSIONS_PER_USER_CAP) and populates
// this struct; tests construct it directly.
//
// Field semantics:
//   - TTL: session lifetime. Zero falls back to DefaultSessionTTL (24h).
//   - SessionsPerUser: max concurrent live sessions per user.
//     Zero = unlimited (matches plan §4.5 — `SESSIONS_PER_USER_CAP=0` disables the cap).
//     There is no auto-default to 10 — the composition root MUST set this
//     explicitly (defaulting to DefaultSessionsPerUserCap when the env var
//     is absent). Don't confuse "zero means default" with "zero means
//     unlimited"; they're different here, so the composition root carries
//     the policy choice.
type Config struct {
	TTL             time.Duration
	SessionsPerUser int
	NowFunc         func() time.Time
}

// Manager is the session orchestrator: minting, validation (cache-aside),
// revocation. Constructed once at startup; safe for concurrent use.
type Manager struct {
	store storage.NativeAuthStore
	cache *SessionCache
	cfg   Config
}

// NewManager wires the dependencies. The cache may be nil — Manager will
// then degrade to PG-only reads (architect C4: cache is optional).
//
// Only TTL has a default here (matches DefaultSessionTTL). SessionsPerUser
// is passed through verbatim — see Config doc for why the policy choice
// belongs in the composition root.
func NewManager(store storage.NativeAuthStore, cache *SessionCache, cfg Config) *Manager {
	if cfg.TTL == 0 {
		cfg.TTL = DefaultSessionTTL
	}
	return &Manager{store: store, cache: cache, cfg: cfg}
}

func (m *Manager) now() time.Time {
	if m.cfg.NowFunc != nil {
		return m.cfg.NowFunc()
	}
	return time.Now().UTC()
}

// MintRequest carries the per-mint inputs that aren't part of Manager state.
type MintRequest struct {
	UserID         string
	OrganizationID string
	AuthMode       model.AuthMode
	IP             net.IP
	UserAgent      string // raw header; Manager hashes it
}

// MintResult is what MintSession returns to the handler. The handler drops
// PlaintextToken into a Set-Cookie header via auth.SetSession; the rest
// goes into the JSON body / audit row.
type MintResult struct {
	Session        model.Session
	PlaintextToken string
	ExpiresAt      time.Time
}

// MintSession creates a new session row and returns the plaintext token to
// the handler. Steps:
//
//  1. Generate 32 bytes of CSPRNG entropy → base64url plaintext.
//  2. Hash → SHA-256 hex (matches sessions.session_token_hash).
//  3. INSERT sessions row.
//  4. Cap-enforce per-user: if SessionsPerUser > 0 and the user is now over
//     the cap, revoke the OLDEST live session to bring the count back in
//     bounds (architect C2). Cap is checked after insert so the new session
//     wins the seat; if the user already had cap+1 we evict cap's worth.
//  5. Cache the new session.
func (m *Manager) MintSession(ctx context.Context, in MintRequest) (MintResult, error) {
	if in.UserID == "" || in.OrganizationID == "" {
		return MintResult{}, errors.New("auth: mint session: user_id and organization_id required")
	}
	if in.AuthMode == "" {
		return MintResult{}, errors.New("auth: mint session: auth_mode required")
	}

	plaintext, err := mintTokenPlaintext()
	if err != nil {
		return MintResult{}, fmt.Errorf("auth: mint session: %w", err)
	}
	hash := HashToken(plaintext)
	now := m.now()
	expires := now.Add(m.cfg.TTL)

	s := model.Session{
		ID:               uuid.New().String(),
		UserID:           in.UserID,
		OrganizationID:   in.OrganizationID,
		AuthMode:         in.AuthMode,
		SessionTokenHash: hash,
		ExpiresAt:        expires,
		IP:               in.IP,
		UserAgentHash:    hashUserAgent(in.UserAgent),
	}
	saved, err := m.store.CreateSession(ctx, s)
	if err != nil {
		return MintResult{}, fmt.Errorf("auth: mint session: %w", err)
	}
	// LastSeenAt is only present after the DB stamps it; copy back for cache.
	s = saved

	if m.cache != nil {
		m.cache.Put(ctx, s, now)
	}

	if m.cfg.SessionsPerUser > 0 {
		if err := m.enforceCap(ctx, in.UserID); err != nil {
			// Cap enforcement is best-effort. The new session is already
			// minted and usable; an over-cap leftover will be cleaned up by
			// the next login or the sweep ticker. Log and move on.
			slog.Warn("auth: per-user session cap enforcement failed",
				"err", err, "user_id", in.UserID)
		}
	}

	return MintResult{
		Session:        s,
		PlaintextToken: plaintext,
		ExpiresAt:      expires,
	}, nil
}

// ValidateSession is the hot-path read invoked by the auth middleware. It
// is cache-aside: cache hit → re-check Live() and return; miss → SELECT PG,
// re-check Live(), populate cache, also TouchSessionLastSeen. last_seen_at
// is updated only on cache miss (architect N3 — write amplification check).
//
// Returns the Session on success. On any failure (not found, expired,
// revoked), returns storage.ErrSessionNotFound. Callers MUST treat all
// failures identically — never echo the internal reason to the client.
func (m *Manager) ValidateSession(ctx context.Context, plaintextToken string) (model.Session, error) {
	if plaintextToken == "" {
		return model.Session{}, storage.ErrSessionNotFound
	}
	hash := HashToken(plaintextToken)
	now := m.now()

	if m.cache != nil {
		if cached, err := m.cache.Get(ctx, hash); err == nil {
			if !cached.Live(now) {
				// The cached row is stale (revoked or expired between Put
				// and Get). Best-effort: drop the cache entry so the next
				// request takes the cold path. Don't try to re-fetch —
				// reading a known-dead session is the same as a miss.
				m.cache.Delete(ctx, hash)
				return model.Session{}, storage.ErrSessionNotFound
			}
			return cached, nil
		}
	}

	s, err := m.store.GetSessionByTokenHash(ctx, hash)
	if err != nil {
		return model.Session{}, err
	}
	if !s.Live(now) {
		return model.Session{}, storage.ErrSessionNotFound
	}

	// Cache-miss-only last_seen_at write (architect N3). The grain is
	// "every cache TTL" rather than "every request" — when REDIS_URL is
	// set this is a few minutes; with the in-memory cache it's per-pod.
	// Errors are logged but never fail the request.
	if err := m.store.TouchSessionLastSeen(ctx, s.ID); err != nil {
		slog.Warn("auth: touch last_seen_at failed", "err", err, "session_id", s.ID)
	}
	if m.cache != nil {
		// Update the in-memory copy so the cache write reflects the new
		// LastSeenAt (avoids a flicker where cache shows old timestamp).
		s.LastSeenAt = now
		m.cache.Put(ctx, s, now)
	}
	return s, nil
}

// RevokeSession revokes the session identified by sessionID and evicts any
// matching cache entry. Write-through invalidation: PG first, then cache.
// The PG write is the source of truth; if the cache delete fails, the
// next ValidateSession call falls through to PG and Live() rejects the
// already-revoked row.
func (m *Manager) RevokeSession(ctx context.Context, sessionID, tokenHash string, reason RevokeReason) error {
	if sessionID == "" {
		return errors.New("auth: revoke session: session_id required")
	}
	if err := m.store.RevokeSession(ctx, sessionID); err != nil {
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	if m.cache != nil && tokenHash != "" {
		m.cache.Delete(ctx, tokenHash)
	}
	observability.Global.AuthSessionRevocationsTotal.WithLabelValues(string(reason)).Inc()
	return nil
}

// RevokeUserSessions revokes EVERY live session for userID and clears the
// cache for each. Used by password-reset, admin "log out everywhere",
// SESSIONS_PER_USER_CAP enforcement, and SSO enforcement-change flips.
//
// The cache eviction enumerates token hashes returned by
// store.RevokeUserSessions (architect C4: no scan/wildcard — the cache
// abstraction has no such operation, and inventing one would couple us to
// Redis-specific semantics).
func (m *Manager) RevokeUserSessions(ctx context.Context, userID string, reason RevokeReason) (int, error) {
	if userID == "" {
		return 0, errors.New("auth: revoke user sessions: user_id required")
	}
	hashes, err := m.store.RevokeUserSessions(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("auth: revoke user sessions: %w", err)
	}
	if m.cache != nil && len(hashes) > 0 {
		m.cache.DeleteMany(ctx, hashes)
	}
	if len(hashes) > 0 {
		observability.Global.AuthSessionRevocationsTotal.
			WithLabelValues(string(reason)).
			Add(float64(len(hashes)))
	}
	return len(hashes), nil
}

// enforceCap is invoked after each successful MintSession. Only runs when
// cfg.SessionsPerUser > 0. Strategy: count live sessions; if over cap,
// fetch the token hashes (oldest first) and revoke until back at the cap.
//
// Implemented as count + list-and-revoke rather than a single SQL DELETE so
// the cache layer gets the same explicit eviction the rest of the system
// uses (no scan).
func (m *Manager) enforceCap(ctx context.Context, userID string) error {
	count, err := m.store.CountSessionsForUser(ctx, userID)
	if err != nil {
		return err
	}
	if count <= m.cfg.SessionsPerUser {
		return nil
	}
	// Over cap. ListUserSessionTokenHashes returns hashes oldest-first
	// (contractual — see storage_native_auth.go). We revoke the first
	// `excess` of those, which is precisely the oldest excess sessions —
	// matching the plan §4.6 acceptance criterion ("the 11th login revokes
	// the OLDEST active session"). The just-minted session is the newest
	// and stays at the tail.
	//
	// In practice the count is small (cap is 10 by default) so listing in
	// memory is fine. If this ever shows up in flame graphs we can replace
	// it with a single `DELETE ... WHERE session_token_hash = ANY($1)`.
	hashes, err := m.store.ListUserSessionTokenHashes(ctx, userID)
	if err != nil {
		return err
	}
	excess := count - m.cfg.SessionsPerUser
	if excess > len(hashes) {
		excess = len(hashes)
	}
	if excess <= 0 {
		return nil
	}
	for i := 0; i < excess; i++ {
		s, err := m.store.GetSessionByTokenHash(ctx, hashes[i])
		if err != nil {
			// Row may have been swept between list and lookup; tolerate.
			continue
		}
		if err := m.RevokeSession(ctx, s.ID, s.SessionTokenHash, RevokeReasonCapExceeded); err != nil {
			slog.Warn("auth: cap enforcement revoke failed",
				"err", err, "user_id", userID, "session_id", s.ID)
		}
	}
	return nil
}

// mintTokenPlaintext generates a CSPRNG token. base64url is chosen so the
// value is cookie-safe without percent-encoding.
func mintTokenPlaintext() (string, error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashUserAgent SHA-256s the User-Agent header into a hex string. Empty
// header → empty hash (sessions.user_agent_hash is nullable in spirit; we
// store empty string for absence). The hash is for forensics — operators
// can correlate "is this same client" without storing the raw UA.
func hashUserAgent(ua string) string {
	if ua == "" {
		return ""
	}
	return HashToken(ua)
}
