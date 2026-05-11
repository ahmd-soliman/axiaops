package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/cache"
)

func newTestRateLimiter() *middleware.RateLimiter {
	return middleware.NewRateLimiter(cache.New("")) // memory backend
}

func TestRateLimiter_AllowsUpToLimit(t *testing.T) {
	rl := newTestRateLimiter()
	ctx := context.Background()

	for i := 0; i < middleware.RateLimitMax; i++ {
		if !rl.Allow(ctx, "organization-A") {
			t.Fatalf("expected allowed on request %d", i+1)
		}
	}
	if rl.Allow(ctx, "organization-A") {
		t.Fatal("expected blocked after limit exceeded")
	}
}

func TestRateLimiter_Isolation(t *testing.T) {
	rl := newTestRateLimiter()
	ctx := context.Background()

	for i := 0; i < middleware.RateLimitMax; i++ {
		rl.Allow(ctx, "organization-1") //nolint:errcheck
	}
	// organization-2 should still have full capacity
	if !rl.Allow(ctx, "organization-2") {
		t.Fatal("organization-2 should not be affected by organization-1's limit")
	}
}

func TestRateLimiter_Wrap_PerUserIsolation(t *testing.T) {
	// Two users in the same organization must not share a bucket. Regression
	// test for the org-only keying that previously logged co-workers out
	// whenever one teammate refresh-stormed the dashboard.
	rl := newTestRateLimiter()
	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	makeReq := func(orgID, userID string) int {
		r := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
		ctx := middleware.ContextWithOrganizationID(r.Context(), orgID)
		ctx = middleware.WithUserID(ctx, userID)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r.WithContext(ctx))
		return w.Code
	}

	// Drain user-A's bucket.
	for i := 0; i < middleware.RateLimitMax; i++ {
		if code := makeReq("org-shared", "user-A"); code != http.StatusOK {
			t.Fatalf("user-A request %d: expected 200, got %d", i+1, code)
		}
	}
	if code := makeReq("org-shared", "user-A"); code != http.StatusTooManyRequests {
		t.Fatalf("user-A over limit: expected 429, got %d", code)
	}
	// user-B in the same org must still have full capacity.
	if code := makeReq("org-shared", "user-B"); code != http.StatusOK {
		t.Fatalf("user-B (same org): expected 200, got %d", code)
	}
}

func TestRateLimiter_Wrap_Returns429(t *testing.T) {
	rl := newTestRateLimiter()
	ctx := context.Background()

	// Drain the bucket
	for i := 0; i < middleware.RateLimitMax; i++ {
		rl.Allow(ctx, "alpha") //nolint:errcheck
	}

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	r = r.WithContext(middleware.ContextWithOrganizationID(r.Context(), "alpha"))
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
	for i := 0; i < middleware.RateLimitMax; i++ {
		rl.Allow(ctx, "organization-x") //nolint:errcheck
	}

	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, tc := range []struct{ method, path string }{
		{http.MethodOptions, "/v1/zombies"},
		{http.MethodGet, "/health"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r = r.WithContext(middleware.ContextWithOrganizationID(r.Context(), "organization-x"))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("%s %s: expected 200, got %d", tc.method, tc.path, w.Code)
		}
	}
}

func TestRateLimiter_Wrap_NoOrganizationAllowed(t *testing.T) {
	rl := newTestRateLimiter()
	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil) // no organization in ctx
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for missing organization, got %d", w.Code)
	}
}

func TestRateLimiter_CacheError_FailsOpen(t *testing.T) {
	errCache := &errorCache{}
	rl := middleware.NewRateLimiter(errCache)
	// Should allow even when cache errors
	if !rl.Allow(context.Background(), "organization-err") {
		t.Fatal("expected fail-open on cache error")
	}
}

func TestRateLimiter_SurvivesRestart(t *testing.T) {
	// Simulate restart: two RateLimiter instances sharing the same cache.
	c := cache.New("")
	rl1 := middleware.NewRateLimiter(c)
	ctx := context.Background()

	for i := 0; i < middleware.RateLimitMax; i++ {
		rl1.Allow(ctx, "organization-restart") //nolint:errcheck
	}

	// New instance, same cache — counter should be preserved.
	rl2 := middleware.NewRateLimiter(c)
	if rl2.Allow(ctx, "organization-restart") {
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
func (e *errorCache) Ping(_ context.Context) error { return errors.New("cache down") }
func (e *errorCache) Close() error                 { return nil }
