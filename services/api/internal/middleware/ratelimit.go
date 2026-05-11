package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"axiaops.io/shared/cache"
)

const (
	rateLimitMax    = 300             // requests per minute, per (organization, user)
	rateLimitWindow = 2 * time.Minute // TTL covers current + next bucket boundary
)

// RateLimiter enforces a per-minute request cap keyed on the bucket subject
// (organization + user when authenticated, organization-only as a fallback).
// Per-user keying matters when multiple users share one organization — under
// the previous org-only key, one user hammering the dashboard locked out
// every teammate sharing the org, and a single refresh-storm cost the entire
// org its budget. Cache-backed (Redis in prod, in-memory in tests) so the
// counter survives process restarts. Fails open if the cache errors.
//
// Key format: ratelimit:{subject}:{minute_bucket}
//   subject = "{org_id}:{user_id}" when both are present in context
//           = "{org_id}"          when the request is unauthenticated /
//                                  pre-auth but somehow already carries org
type RateLimiter struct {
	cache cache.Cache
}

// NewRateLimiter creates a RateLimiter backed by the provided cache.
func NewRateLimiter(c cache.Cache) *RateLimiter {
	return &RateLimiter{cache: c}
}

// Allow returns true if the given subject is within the rate limit for the
// current minute. `subject` is an opaque cache-key segment — callers compose
// it however they like (Wrap builds it from organization + user IDs).
func (rl *RateLimiter) Allow(ctx context.Context, subject string) bool {
	bucket := time.Now().Unix() / 60
	key := fmt.Sprintf("ratelimit:%s:%d", subject, bucket)

	n, err := rl.cache.Incr(ctx, key, rateLimitWindow)
	if err != nil {
		slog.Warn("ratelimit: cache error, allowing request", "err", err)
		return true // fail-open
	}
	return n <= rateLimitMax
}

// Wrap returns an http.Handler that enforces the rate limit per (org, user).
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

		if !rl.Allow(r.Context(), subject) {
			slog.Warn("ratelimit: too many requests", "subject", subject)
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
