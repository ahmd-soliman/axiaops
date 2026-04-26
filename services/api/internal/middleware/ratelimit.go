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
	rateLimitMax    = 60              // requests per minute
	rateLimitWindow = 2 * time.Minute // TTL covers current + next bucket boundary
)

// RateLimiter enforces 60 requests per minute per tenant using cache.Cache.Incr.
// Key format: ratelimit:{tenant_id}:{minute_bucket}
// When cache is unavailable the request is allowed (fail-open).
type RateLimiter struct {
	cache cache.Cache
}

// NewRateLimiter creates a RateLimiter backed by the provided cache.
func NewRateLimiter(c cache.Cache) *RateLimiter {
	return &RateLimiter{cache: c}
}

// Allow returns true if the tenant is within the rate limit for the current minute.
func (rl *RateLimiter) Allow(ctx context.Context, tenantID string) bool {
	bucket := time.Now().Unix() / 60
	key := fmt.Sprintf("ratelimit:%s:%d", tenantID, bucket)

	n, err := rl.cache.Incr(ctx, key, rateLimitWindow)
	if err != nil {
		slog.Warn("ratelimit: cache error, allowing request", "err", err)
		return true // fail-open
	}
	return n <= rateLimitMax
}

// Wrap returns an http.Handler that enforces the rate limit per tenant.
func (rl *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		tenantID := OrganizationID(r.Context())
		if tenantID == "" {
			next.ServeHTTP(w, r)
			return
		}

		if !rl.Allow(r.Context(), tenantID) {
			slog.Warn("ratelimit: too many requests", "tenant_id", tenantID)
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
