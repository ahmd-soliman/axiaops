package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"axiaops.io/shared/cache"
)

// DefaultRateLimitMax is the fallback when callers pass max<=0 or the env
// var is unset. 1000/min/(org,user) ≈ 17 req/sec sustained — comfortable for
// interactive dashboard use (cold loads, navigation bursts, dev refresh
// storms) without giving up the runaway-client safety net entirely. Tighten
// in production via the RATE_LIMIT_MAX env var if abuse is observed.
const DefaultRateLimitMax = 1000

const rateLimitWindow = 2 * time.Minute // TTL covers current + next bucket boundary

// RateLimiter enforces a per-minute request cap keyed on the bucket subject
// (organization + user when authenticated, organization-only as a fallback).
// Per-user keying matters when multiple users share one organization — under
// an org-only key, one user hammering the dashboard would lock out every
// teammate sharing the org. Cache-backed (Redis in prod, in-memory in tests)
// so the counter survives process restarts. Fails open if the cache errors.
//
// Cap is configurable at construction time. The HTTP handler advertises the
// current state on every authenticated response via the de-facto standard
// X-RateLimit-* trio + Retry-After (RFC 6585), so well-behaved clients can
// self-throttle before they earn a 429.
//
// Key format: ratelimit:{subject}:{minute_bucket}
//   subject = "{org_id}:{user_id}" when both are present in context
//           = "{org_id}"          fallback for pre-auth paths that already
//                                  carry org but no user
type RateLimiter struct {
	cache cache.Cache
	max   int
}

// NewRateLimiter creates a RateLimiter backed by the provided cache. max≤0
// falls back to DefaultRateLimitMax so callers can pass a freshly-parsed env
// var without an extra branch.
func NewRateLimiter(c cache.Cache, max int) *RateLimiter {
	if max <= 0 {
		max = DefaultRateLimitMax
	}
	return &RateLimiter{cache: c, max: max}
}

// Max returns the configured cap. Exported so tests and the Wrap headers can
// agree on the active limit without re-parsing env vars.
func (rl *RateLimiter) Max() int { return rl.max }

// Decision captures the limiter's verdict for one request. Allowed reflects
// whether the request should pass; Remaining is how many requests the
// subject has left in the current bucket (floor 0); ResetAt is the wall
// clock the bucket rolls over (i.e. when Remaining returns to Max).
type Decision struct {
	Allowed   bool
	Remaining int
	ResetAt   time.Time
}

// Check increments the subject's bucket and returns the decision. Fails open
// (Allowed=true, Remaining=Max) when the cache errors, matching the
// availability-over-strictness posture documented above.
func (rl *RateLimiter) Check(ctx context.Context, subject string) Decision {
	now := time.Now()
	bucket := now.Unix() / 60
	resetAt := time.Unix((bucket+1)*60, 0)
	key := fmt.Sprintf("ratelimit:%s:%d", subject, bucket)

	n, err := rl.cache.Incr(ctx, key, rateLimitWindow)
	if err != nil {
		slog.Warn("ratelimit: cache error, allowing request", "err", err)
		return Decision{Allowed: true, Remaining: rl.max, ResetAt: resetAt}
	}
	remaining := rl.max - int(n)
	if remaining < 0 {
		remaining = 0
	}
	return Decision{Allowed: int(n) <= rl.max, Remaining: remaining, ResetAt: resetAt}
}

// Allow is a thin wrapper around Check that returns just the boolean —
// retained for callers (and tests) that only care about pass/block.
func (rl *RateLimiter) Allow(ctx context.Context, subject string) bool {
	return rl.Check(ctx, subject).Allowed
}

// Wrap returns an http.Handler that enforces the rate limit per (org, user)
// and stamps X-RateLimit-Limit / -Remaining / -Reset on every authenticated
// response so clients can self-pace.
func (rl *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		organizationID := OrganizationID(r.Context())
		if organizationID == "" {
			next.ServeHTTP(w, r)
			return
		}

		subject := organizationID
		if userID := UserID(r.Context()); userID != "" {
			subject = organizationID + ":" + userID
		}

		d := rl.Check(r.Context(), subject)
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rl.max))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(d.Remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(d.ResetAt.Unix(), 10))

		if !d.Allowed {
			slog.Warn("ratelimit: too many requests", "subject", subject, "limit", rl.max)
			// Retry-After in seconds-until-bucket-rollover, not a hardcoded 60
			// — a request denied at second 59 only needs to wait ~1s, not a
			// full minute. Floor at 1 to guard against clock skew producing
			// a non-positive value (RFC 7231 §7.1.3 forbids 0 for delta-seconds).
			retry := int(time.Until(d.ResetAt).Seconds())
			if retry < 1 {
				retry = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
