package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"axiaops.io/shared/cache"
)

// Plan §4.2: POST /v1/auth/login is rate-limited at 10/min/IP and
// 5/min per email. The IP cap is general anti-abuse; the per-email cap
// is anti-credential-stuffing — caps how many guesses an attacker can
// make against a single account from a botnet (where each request
// may come from a different IP and would slip past the IP cap).
//
// Both caps share one minute-bucket window. Failing-open on cache
// errors mirrors the existing middleware/ratelimit posture: we'd
// rather let a legitimate request through during a Redis blip than
// 429 honest users.
//
// Email is hashed before becoming part of the cache key so the key
// space doesn't carry plaintext addresses (a Redis dump shouldn't
// reveal who's been logging in).
const (
	defaultLoginPerIPPerMinute    = 10
	defaultLoginPerEmailPerMinute = 5
	loginCounterTTL               = 2 * time.Minute // TTL covers current + next bucket
)

// RateLimitOutcome describes which cap (if any) tripped. Surfaced to
// the handler so the slog warning + telemetry label can attribute the
// hit correctly without the handler re-deriving the limit type.
type RateLimitOutcome struct {
	Allowed     bool
	Reason      string        // "" when allowed; "ip" or "email" otherwise
	RetryAfter  time.Duration // 0 when allowed
}

// LoginRateLimiter caps POST /v1/auth/login attempts. Constructed once
// at startup; safe for concurrent use.
//
// When cache is nil (REDIS_URL unset and no in-memory fallback wired —
// which shouldn't happen with cache.New, but defensive) Allow returns
// allowed=true on every call. The cache fallback in cache.New is
// in-memory, so even single-instance dev deployments get rate
// limiting. Multi-replica deployments without Redis fragment the
// counter per replica — flagged in the doc but acceptable for a
// posture that's already fail-open.
type LoginRateLimiter struct {
	cache         cache.Cache
	perIPPerMin   int
	perEmailPerMin int
}

// NewLoginRateLimiter builds a limiter with the plan defaults.
// Pass cache.New(REDIS_URL) so multi-replica deployments share the
// counter via Redis. nil cache disables rate limiting (Allow always
// returns allowed=true).
func NewLoginRateLimiter(c cache.Cache) *LoginRateLimiter {
	return &LoginRateLimiter{
		cache:          c,
		perIPPerMin:    defaultLoginPerIPPerMinute,
		perEmailPerMin: defaultLoginPerEmailPerMinute,
	}
}

// WithLimits overrides the default caps. Used by tests to drive the
// 11th-request-rejected case without spamming. Production wiring uses
// the constructor defaults.
func (l *LoginRateLimiter) WithLimits(perIP, perEmail int) *LoginRateLimiter {
	l.perIPPerMin = perIP
	l.perEmailPerMin = perEmail
	return l
}

// Allow returns whether the candidate login attempt is within both the
// per-IP and per-email caps. ip is the client IP (zero / nil → caller
// passed nothing identifiable; treated as a single bucket "unknown").
// email is normalised to lower-case before hashing.
//
// On cache errors the limit fails open — same posture as the existing
// middleware rate limiter. The slog warning gives operators visibility
// into a degraded cache without surfacing as a user-visible 429.
func (l *LoginRateLimiter) Allow(ctx context.Context, ip net.IP, email string) RateLimitOutcome {
	if l == nil || l.cache == nil {
		return RateLimitOutcome{Allowed: true}
	}

	bucket := time.Now().Unix() / 60
	ipKey := loginIPKey(ip, bucket)
	emailKey := loginEmailKey(email, bucket)

	// Order: IP first, then email. Two consequences worth pinning
	// in code so a future reader doesn't try to "fix" them:
	//
	//   - Email-counter only advances when IP path was clear. Saves
	//     a Redis round-trip on the IP-blocked path.
	//   - IP counter advances even on email-blocked attempts. INTENT:
	//     a single-IP attacker iterating over many victim emails IS
	//     drilling against the system from one source, and the IP
	//     cap is the right place to cap that. If the user's intent
	//     is to "count only successful login latency", that's not
	//     this limiter's job — it's an anti-abuse cap, not a billing
	//     meter. Don't decrement on email-block.
	if n, err := l.cache.Incr(ctx, ipKey, loginCounterTTL); err != nil {
		slog.Warn("auth: rate limiter cache error (IP key); failing open", "err", err)
	} else if int(n) > l.perIPPerMin {
		return RateLimitOutcome{
			Allowed:    false,
			Reason:     "ip",
			RetryAfter: retryAfterForBucket(bucket),
		}
	}

	if n, err := l.cache.Incr(ctx, emailKey, loginCounterTTL); err != nil {
		slog.Warn("auth: rate limiter cache error (email key); failing open", "err", err)
	} else if int(n) > l.perEmailPerMin {
		return RateLimitOutcome{
			Allowed:    false,
			Reason:     "email",
			RetryAfter: retryAfterForBucket(bucket),
		}
	}

	return RateLimitOutcome{Allowed: true}
}

func loginIPKey(ip net.IP, bucket int64) string {
	id := "unknown"
	if ip != nil {
		id = ip.String()
	}
	return fmt.Sprintf("auth:login:ip:%s:%d", id, bucket)
}

func loginEmailKey(email string, bucket int64) string {
	// Hash the email so the cache key namespace doesn't carry
	// plaintext addresses — a Redis dump must not reveal who's been
	// trying to log in.
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return fmt.Sprintf("auth:login:email:%s:%d", hex.EncodeToString(sum[:8]), bucket)
}

// retryAfterForBucket returns the seconds until the next minute
// boundary, which is when the counter rolls over. Caps at 60 even
// though the cache TTL is 2 min — Retry-After tells the client when
// to come back, not when the row expires.
func retryAfterForBucket(bucket int64) time.Duration {
	now := time.Now().Unix()
	nextBucket := (bucket + 1) * 60
	if nextBucket <= now {
		return time.Second
	}
	return time.Duration(nextBucket-now) * time.Second
}
