package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRateLimiter_Allow(t *testing.T) {
	rl := NewRateLimiter(5.0, 5.0) // 5 tokens per second, max 5

	// Consume all 5 tokens instantly
	for i := 0; i < 5; i++ {
		if !rl.Allow("tenant-A") {
			t.Fatalf("expected allowed on request %d", i+1)
		}
	}

	// 6th request should be blocked
	if rl.Allow("tenant-A") {
		t.Fatal("expected 6th request to be blocked")
	}

	// Refill 1 token by waiting 0.25 sec
	// 1 token = 1.0/5.0 sec = 0.2 sec
	time.Sleep(250 * time.Millisecond)

	if !rl.Allow("tenant-A") {
		t.Fatal("expected allowed after waiting for token refill")
	}

	// Should be blocked again
	if rl.Allow("tenant-A") {
		t.Fatal("expected blocked after consuming the single refilled token")
	}
}

func TestRateLimiter_Isolation(t *testing.T) {
	rl := NewRateLimiter(2.0, 2.0)

	if !rl.Allow("tenant-1") {
		t.Fatal("tenant-1 req 1 blocked")
	}
	if !rl.Allow("tenant-1") {
		t.Fatal("tenant-1 req 2 blocked")
	}
	if rl.Allow("tenant-1") {
		t.Fatal("tenant-1 req 3 should be blocked")
	}

	// tenant-2 should still have full capacity
	if !rl.Allow("tenant-2") {
		t.Fatal("tenant-2 req 1 blocked unexpectedly")
	}
}

func TestRateLimiter_Wrap(t *testing.T) {
	rl := NewRateLimiter(2.0, 2.0)
	
	count := 0
	handler := rl.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusOK)
	}))

	req := func(tenantID string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil)
		if tenantID != "" {
			ctx := context.WithValue(r.Context(), tenantIDKey, tenantID)
			r = r.WithContext(ctx)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	// 1. Success
	w1 := req("alpha")
	if w1.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w1.Code)
	}

	// 2. Success
	w2 := req("alpha")
	if w2.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w2.Code)
	}

	// 3. Blocked (Too Many Requests)
	w3 := req("alpha")
	if w3.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w3.Code)
	}
	if retry := w3.Header().Get("Retry-After"); retry != "1" {
		t.Errorf("expected Retry-After: 1, got %q", retry)
	}

	// Unauthenticated request should bypass limiter (tenantID = "")
	// For example, if it's hitting a public API route.
	w4 := req("")
	if w4.Code != http.StatusOK {
		t.Errorf("expected 200 for missing tenant, got %d", w4.Code)
	}

	// OPTIONS preflight check bypassing
	rOpt := httptest.NewRequest(http.MethodOptions, "/v1/api", nil)
	wOpt := httptest.NewRecorder()
	handler.ServeHTTP(wOpt, rOpt)
	if wOpt.Code != http.StatusOK {
		t.Errorf("expected 200 for OPTIONS, got %d", wOpt.Code)
	}

	// Health check bypassing
	rHlt := httptest.NewRequest(http.MethodGet, "/health", nil)
	wHlt := httptest.NewRecorder()
	handler.ServeHTTP(wHlt, rHlt)
	if wHlt.Code != http.StatusOK {
		t.Errorf("expected 200 for health, got %d", wHlt.Code)
	}
	
	if count != 5 {
		t.Errorf("expected handler execution 5 times, got %d", count)
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(100.0, 100.0)
	var wg sync.WaitGroup

	// Make 100 concurrent requests, exactly the capacity.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rl.Allow("tenant-busy")
		}()
	}
	wg.Wait()

	// 101st request should be blocked
	if rl.Allow("tenant-busy") {
		t.Fatal("expected blocked after concurrent drain")
	}
}
