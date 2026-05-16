package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"time"

	"axiaops.io/shared/cache"
	"axiaops.io/shared/model"
	"axiaops.io/shared/observability"
)

// sessionCachePrefix is the key namespace for cached sessions. The full key
// is prefix + hex(sha256(plaintext token)) — the same hash stored in
// sessions.session_token_hash, so the cache key is computable from either
// the cookie value or the DB row.
const sessionCachePrefix = "axiaops:session:"

// HashToken returns hex(sha256(plaintext)). Exported because both the
// session-mint path (which writes the hash to sessions.session_token_hash)
// and the cache layer (which keys on the same hash) need to agree on the
// representation.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// sessionCacheValue is the serialised cache payload. It carries every field
// needed for the read path to enforce model.Session.Live() WITHOUT having to
// round-trip to PG (architect C4) — `revoked_at` and `expires_at` are
// load-bearing.
type sessionCacheValue struct {
	ID               string     `json:"id"`
	UserID           string     `json:"user_id"`
	OrganizationID   string     `json:"organization_id"`
	AuthMode         string     `json:"auth_mode"`
	SessionTokenHash string     `json:"session_token_hash"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
	LastSeenAt       time.Time  `json:"last_seen_at"`
	IP               string     `json:"ip,omitempty"`
	UserAgentHash    string     `json:"user_agent_hash,omitempty"`
}

func toCacheValue(s model.Session) sessionCacheValue {
	v := sessionCacheValue{
		ID:               s.ID,
		UserID:           s.UserID,
		OrganizationID:   s.OrganizationID,
		AuthMode:         string(s.AuthMode),
		SessionTokenHash: s.SessionTokenHash,
		CreatedAt:        s.CreatedAt,
		ExpiresAt:        s.ExpiresAt,
		RevokedAt:        s.RevokedAt,
		LastSeenAt:       s.LastSeenAt,
		UserAgentHash:    s.UserAgentHash,
	}
	if s.IP != nil {
		v.IP = s.IP.String()
	}
	return v
}

func (v sessionCacheValue) toSession() model.Session {
	s := model.Session{
		ID:               v.ID,
		UserID:           v.UserID,
		OrganizationID:   v.OrganizationID,
		AuthMode:         model.AuthMode(v.AuthMode),
		SessionTokenHash: v.SessionTokenHash,
		CreatedAt:        v.CreatedAt,
		ExpiresAt:        v.ExpiresAt,
		RevokedAt:        v.RevokedAt,
		LastSeenAt:       v.LastSeenAt,
		UserAgentHash:    v.UserAgentHash,
	}
	if v.IP != "" {
		s.IP = net.ParseIP(v.IP)
	}
	return s
}

// SessionCache is the cache-aside view onto sessions. It is intentionally a
// thin wrapper over cache.Cache so the session-validation logic can be
// unit-tested against an in-memory cache without dragging the rest of the
// auth package along.
//
// Errors from the underlying cache are NEVER fatal — the read path returns
// (zero session, ErrSessionCacheMiss) and the caller falls through to PG.
// The metric counter (`SessionCacheErrorsTotal`) is the operator's signal
// that the cache backend is degraded.
type SessionCache struct {
	c cache.Cache
}

// NewSessionCache wraps the supplied cache. Pass cache.New(redisURL) — or
// for tests, an in-memory implementation.
func NewSessionCache(c cache.Cache) *SessionCache { return &SessionCache{c: c} }

// ErrSessionCacheMiss is returned by Get on cold reads. The caller should
// treat this identically to a cache backend error — fall through to PG.
var ErrSessionCacheMiss = errors.New("auth: session cache miss")

// Get returns the cached session for the given plaintext-token hash. The
// caller MUST re-check Live() after read — cache presence is not by itself
// proof of liveness (architect C4).
//
// On any cache backend failure (Redis down, deserialisation error), Get
// returns ErrSessionCacheMiss after logging a slog.Warn and bumping
// SessionCacheErrorsTotal. The caller then performs a PG SELECT.
func (sc *SessionCache) Get(ctx context.Context, tokenHash string) (model.Session, error) {
	if tokenHash == "" {
		return model.Session{}, ErrSessionCacheMiss
	}
	key := sessionCachePrefix + tokenHash
	raw, err := sc.c.Get(ctx, key)
	switch {
	case errors.Is(err, cache.ErrNotFound):
		observability.Global.SessionCacheTotal.WithLabelValues("miss").Inc()
		return model.Session{}, ErrSessionCacheMiss
	case err != nil:
		observability.Global.SessionCacheTotal.WithLabelValues("error").Inc()
		observability.Global.SessionCacheErrorsTotal.Inc()
		slog.Warn("auth: session cache get failed; falling through to PG", "err", err)
		return model.Session{}, ErrSessionCacheMiss
	}
	var v sessionCacheValue
	if err := json.Unmarshal(raw, &v); err != nil {
		observability.Global.SessionCacheTotal.WithLabelValues("error").Inc()
		observability.Global.SessionCacheErrorsTotal.Inc()
		slog.Warn("auth: session cache deserialise failed; falling through to PG", "err", err)
		return model.Session{}, ErrSessionCacheMiss
	}
	observability.Global.SessionCacheTotal.WithLabelValues("hit").Inc()
	return v.toSession(), nil
}

// Put writes the session to the cache with TTL = remaining session lifetime
// (capped to 24h to bound how long a *cached* revocation lag can persist
// under a Redis outage that prevents Del from running). Cache failure is
// logged but never returned to the caller — Put is best-effort.
//
// `now` is the caller's clock — same value the Manager used to compute
// expiry. Using `time.Until(s.ExpiresAt)` here would ignore an injected
// fake clock in tests and could disagree with the value Manager just
// stamped into ExpiresAt.
//
// minTTL avoids the degenerate case where time-until-expiry is sub-second
// (the cache rejects 0 TTLs on most backends and even valid micro-TTLs
// thrash). 5s is short enough that the next request misses naturally.
func (sc *SessionCache) Put(ctx context.Context, s model.Session, now time.Time) {
	if s.SessionTokenHash == "" {
		return
	}
	const minTTL = 5 * time.Second
	const maxTTL = 24 * time.Hour
	ttl := s.ExpiresAt.Sub(now)
	if ttl <= minTTL {
		return
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}
	raw, err := json.Marshal(toCacheValue(s))
	if err != nil {
		// Should not happen — sessionCacheValue is a fixed-shape struct of
		// JSON-friendly fields. Log and move on.
		slog.Warn("auth: session cache marshal failed; skipping cache write",
			"err", err, "session_id", s.ID)
		return
	}
	key := sessionCachePrefix + s.SessionTokenHash
	if err := sc.c.Set(ctx, key, raw, ttl); err != nil {
		observability.Global.SessionCacheErrorsTotal.Inc()
		slog.Warn("auth: session cache set failed", "err", err)
	}
}

// Delete removes the cache entry for the given token hash. Always paired
// with the corresponding PG write (RevokeSession or session expiry). On
// cache failure, the next Get falls through to PG and the stale revoked
// session is rejected by the Live() re-check (architect C4) — but the
// metric still increments so the operator is alerted.
func (sc *SessionCache) Delete(ctx context.Context, tokenHash string) {
	if tokenHash == "" {
		return
	}
	key := sessionCachePrefix + tokenHash
	if err := sc.c.Del(ctx, key); err != nil {
		observability.Global.SessionCacheErrorsTotal.Inc()
		slog.Warn("auth: session cache delete failed", "err", err)
	}
}

// DeleteMany evicts a batch of token hashes — used by RevokeUserSessions
// (architect C4: explicit enumeration, no scan/wildcard delete because the
// cache abstraction has no such operation). Returns no error: failures are
// logged + counted.
func (sc *SessionCache) DeleteMany(ctx context.Context, tokenHashes []string) {
	for _, h := range tokenHashes {
		sc.Delete(ctx, h)
	}
}
