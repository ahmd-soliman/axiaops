package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/shared/cache"
)

func newTestRateLimiter() *RateLimiter {
	return NewRateLimiter(cache.New("")) // memory backend
}

func TestRateLimiter_AllowsUpToLimit(t *testing.T) {
	rl := newTestRateLimiter()
	ctx := context.Background()

	for i := 0; i < rateLimitMax; i++ {
		if !rl.Allow(ctx, "tenant-A") {
			t.Fatalf("expected allowed on request %d", i+1)
		}
	}
	if rl.Allow(ctx, "tenant-A") {
		t.Fatal("expected blocked after limit exceeded")
	}
}

func TestRateLimiter_Isolation(t *testing.T) {
	rl := newTestRateLimiter()
	ctx := context.Background()

	for i := 0; i < rateLimitMax; i++ {
		rl.Allow(ctx, "tenant-1") //nolint:errcheck
	}
	// tenant-2 should still have full capacity
	if !rl.Allow(ctx, "tenant-2") {
		t.Fatal("tenant-2 should not be affected by tenant-1's limit")
	}
}

func TestRateLimiter_Wrap_Returns429(t *testing.T) {
	rl := newTestRateLimiter()
	ctx := context.Background()

	// Drain the bucket
	for i := 0; i < rateLimitMax; i++ {
		rl.Allow(ctx, "alpha") //nolint:errcheck
	}

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil)
	r = r.WithContext(context.WithValue(r.Context(), tenantIDKey, "alpha"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra != "60" {
		t.Errorf("expected Retry-After: 60, got %q", ra)
	}
}

func TestRateLimiter_Wrap_BypassesHealthAndOptions(t *testing.T) {
	rl := newTestRateLimiter()
	ctx := context.Background()
	for i := 0; i < rateLimitMax; i++ {
		rl.Allow(ctx, "tenant-x") //nolint:errcheck
	}

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, tc := range []struct{ method, path string }{
		{http.MethodOptions, "/v1/ghosts"},
		{http.MethodGet, "/health"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r = r.WithContext(context.WithValue(r.Context(), tenantIDKey, "tenant-x"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("%s %s: expected 200, got %d", tc.method, tc.path, w.Code)
		}
	}
}

func TestRateLimiter_Wrap_NoTenantAllowed(t *testing.T) {
	rl := newTestRateLimiter()
	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil) // no tenant in ctx
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for missing tenant, got %d", w.Code)
	}
}

func TestRateLimiter_CacheError_FailsOpen(t *testing.T) {
	errCache := &errorCache{}
	rl := NewRateLimiter(errCache)
	// Should allow even when cache errors
	if !rl.Allow(context.Background(), "tenant-err") {
		t.Fatal("expected fail-open on cache error")
	}
}

func TestRateLimiter_SurvivesRestart(t *testing.T) {
	// Simulate restart: two RateLimiter instances sharing the same cache.
	c := cache.New("")
	rl1 := NewRateLimiter(c)
	ctx := context.Background()

	for i := 0; i < rateLimitMax; i++ {
		rl1.Allow(ctx, "tenant-restart") //nolint:errcheck
	}

	// New instance, same cache — counter should be preserved.
	rl2 := NewRateLimiter(c)
	if rl2.Allow(ctx, "tenant-restart") {
		t.Fatal("expected new instance to see existing counter and block")
	}
}

// errorCache always returns an error from Incr.
type errorCache struct{}

func (e *errorCache) Get(_ context.Context, _ string) ([]byte, error) {
	return nil, errors.New("cache down")
}
func (e *errorCache) Set(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return errors.New("cache down")
}
func (e *errorCache) Del(_ context.Context, _ string) error { return errors.New("cache down") }
func (e *errorCache) Incr(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 0, errors.New("cache down")
}
func (e *errorCache) Close() error { return nil }
