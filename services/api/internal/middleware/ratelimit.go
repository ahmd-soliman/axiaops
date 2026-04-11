package middleware

import (
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// tokenBucket represents a single tenant's bucket holding request tokens.
type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

// RateLimiter manages token buckets per tenant to prevent API abuse.
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	rate     float64 // tokens added per second
	capacity float64 // maximum tokens the bucket can hold
}

// NewRateLimiter creates an in-memory RateLimiter.
// For example: 60 requests a minute = rate: 1.0, capacity: 60.0.
func NewRateLimiter(rate, capacity float64) *RateLimiter {
	return &RateLimiter{
		buckets:  make(map[string]*tokenBucket),
		rate:     rate,
		capacity: capacity,
	}
}

// Allow determines whether a request for tenantID is allowed to proceed.
func (rl *RateLimiter) Allow(tenantID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[tenantID]
	if !ok {
		b = &tokenBucket{
			tokens:     rl.capacity, // start full
			lastRefill: now,
		}
		rl.buckets[tenantID] = b
	}

	// Refill tokens based on time elapsed since last check.
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > rl.capacity {
		b.tokens = rl.capacity
	}
	b.lastRefill = now

	// Attempt to consume a token
	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true
	}
	return false
}

// Wrap returns an http.Handler that enforces the rate limit.
func (rl *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignore preflight and health checks
		if r.Method == http.MethodOptions || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		tenantID := TenantID(r.Context())
		if tenantID == "" {
			// If no tenant is injected (e.g. unauthenticated request hitting public surface), we allow it.
			// Auth wrapper should have already intercepted unauthenticated requests, but this makes the 
			// limiter robust if the route doesn't require auth.
			next.ServeHTTP(w, r)
			return
		}

		if !rl.Allow(tenantID) {
			slog.Warn("ratelimit: too many requests", "tenant_id", tenantID)
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
