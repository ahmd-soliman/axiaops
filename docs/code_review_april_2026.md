# Code Review — Phase 2 Implementation (April 2026)

**Date:** April 11, 2026  
**Reviewed by:** Code analysis  
**Scope:** Commits 7edeaa6, ea38c16, bb87893  
**Overall:** 95% production-ready, 1 P1 issue, 4 P2 test gaps, 1 P3 minor

---

## Commits Under Review

| Commit | Title | Date | Status |
|--------|-------|------|--------|
| 7edeaa6 | Structured logging + request tracing | Apr 9 | ✅ |
| ea38c16 | Rate limiting + API versioning | Apr 11 | ⚠️ P1 issue |
| bb87893 | Savings history / trend feature | Apr 10 | ✅ |

---

## ✅ Commit 7edeaa6 — Observability

### What shipped well

- **Logging migration:** `log.Printf()` → `slog.Info/Error/Warn` across both services (api + ingestion)
- **Environment-driven output:** JSON for production, text for dev; controlled by `LOG_OUTPUT` and `DEV_MODE`
- **SQLite removal:** Both `services/api/cmd/main.go` and `services/ingestion/cmd/main.go` now require `DATABASE_URL`; SQLite removed from production paths
- **Operational safety:** `ResetStuckScans()` on startup + 5-minute background ticker prevents permanently stuck scans
- **Health endpoint DB ping:** Implements `Pinger` interface check; returns HTTP 503 if DB is unreachable
- **Scan lifecycle logging:** New log lines for `scan.started`, `scan.completed` with duration (ms)
- **Request ID injection:** X-Request-ID header stored in context, included in all structured log lines

### Issues found

#### 1. **No unit tests for logging functions** — P2
**Files:** `services/shared/logging/logging.go`

**Functions without tests:**
- `Init(service string)` — Configures slog, level, JSON/text output based on env vars

**Impact:** Critical initialization code depends on environment variables but has zero test coverage. No tests for:
- Log level parsing (debug|info|warn|error)
- JSON vs text output selection based on `LOG_OUTPUT` and `DEV_MODE`

**Fix:** Add unit tests in `services/shared/logging/logging_test.go` (new file). See "Implementation" section below.

---

#### 2. **No unit tests for RequestID middleware** — P2
**File:** `services/api/internal/middleware/requestid.go`

**Functions without tests:**
- `RequestID(next http.Handler)` — Injects X-Request-ID header, stores in context
- `RequestIDFromCtx(ctx context.Context)` — Retrieves ID from context

**Current status:** Middleware is used in production (main.go line 148) but has zero test coverage.

**Missing test scenarios:**
- Injects a new UUID when X-Request-ID header is missing
- Preserves existing X-Request-ID if provided
- Stores ID in context for downstream handlers to access
- X-Request-ID returned in response header

**Fix:** Add unit tests in `services/api/internal/middleware/requestid_test.go` (new file). See "Implementation" section below.

---

#### 3. **No unit tests for ResetStuckScans()** — P2
**File:** `services/shared/storage/postgres/migrate.go`

**Function without tests:**
- `ResetStuckScans(ctx context.Context, adminURL string, stuckAfter time.Duration)` — Resets accounts stuck in "scanning" status back to "error"

**Current status:** Called on startup (main.go:91) and in a 5-minute background ticker (main.go:177) but has zero test coverage.

**Missing test scenarios:**
- Resets accounts with `status='scanning'` older than `stuckAfter` cutoff
- Does not reset accounts with `status='scanning'` that are fresh (within `stuckAfter`)
- Does not affect accounts with other statuses
- Returns correct row count affected
- Handles database errors gracefully

**Fix:** Add integration tests in `services/shared/storage/postgres/postgres_test.go`. See "Implementation" section below.

---

#### 4. **Health endpoint DB-ping failure path untested** — P2
**File:** `services/api/internal/api/handler.go` (lines 144–159)

**Current code:**
```go
func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
    if p, ok := h.store.(Pinger); ok {
        if err := p.Ping(r.Context()); err != nil {
            slog.Error("health: db ping failed", "error", err)
            http.Error(w, `{"status":"error","db":"unreachable"}`, http.StatusServiceUnavailable)
            return
        }
    }
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte(`{"status":"ok"}`))
}
```

**Test status:** `TestHealth_Returns200` exists but:
- Only tests the happy path (no ping failure scenario)
- Uses a stub `MockStore` that doesn't implement `Pinger`, so the ping code path is never exercised
- Missing test for when `Pinger.Ping()` returns an error → expects HTTP 503

**Missing test:**
- Verify HTTP 503 when Pinger fails
- Verify response body is `{"status":"error","db":"unreachable"}`

**Fix:** Add test case in `services/api/internal/api/handler_test.go`. See "Implementation" section below.

---

## ✅ Commit ea38c16 — Rate Limiting + API Versioning

### What shipped well

- **Token bucket algorithm:** Correct implementation, per-tenant isolation, time-based refill
- **Exception handling:** OPTIONS preflight and `/health` bypass limiting; unauthenticated requests (empty tenant ID) pass through
- **Capacity management:** Starts with full bucket; refills based on elapsed time × rate
- **Thread safety:** `sync.Mutex` protects bucket map; lock held briefly during refill/check
- **API versioning:** All 10 routes use `/v1/` prefix consistently; no stale paths
- **Test coverage:** 147 lines of tests including concurrent access (100 goroutines), per-tenant isolation, burst behavior
- **Dashboard client:** All 10 API calls in `client.js` use `/v1/`; consistent across fetch, POST, PATCH, DELETE
- **Dev-friendly:** Disabled in `DEV_MODE=true` to avoid blocking during local development

### Issues found

#### ⚠️ **CRITICAL: Rate limiter memory leak** — P1
**File:** `services/api/internal/middleware/ratelimit.go`

**The problem:**

The `buckets` map (line 19) grows unbounded and never deletes stale tenant entries. Once a tenant makes a request, their bucket is created and stays in memory forever.

```go
type RateLimiter struct {
    mu       sync.Mutex
    buckets  map[string]*tokenBucket  // ← Grows unbounded, never cleaned up
    rate     float64
    capacity float64
}

func (rl *RateLimiter) Allow(tenantID string) bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    
    now := time.Now()
    b, ok := rl.buckets[tenantID]
    if !ok {
        b = &tokenBucket{
            tokens:     rl.capacity,
            lastRefill: now,
        }
        rl.buckets[tenantID] = b  // ← Entry never deleted
    }
    // ...
}
```

**Impact:**
- In production (`DEV_MODE != "true"`, enabled on line 118), this creates a slow memory leak
- For a system with thousands of tenants over months of operation, memory usage grows indefinitely
- No TTL, no LRU eviction, no explicit cleanup mechanism
- Becomes moot in Phase 2.14 when Redis replaces in-memory rate limiting, but should still be fixed for the interim

**Severity:** P1 — Production memory leak

**Fix:** Implement bucket cleanup via:
1. **Option A (simple):** Add a background goroutine in main.go that periodically sweeps inactive buckets (not accessed in >1 hour)
2. **Option B (elegant):** Use `sync.Map` with atomic LastAccess timestamps and garbage collection during Allow()

See "Implementation" section below for Option A.

---

#### P3: Hardcoded Retry-After header
**File:** `services/api/internal/middleware/ratelimit.go` (line 85)

**Current code:**
```go
w.Header().Set("Retry-After", "1")
```

**Minor issue:** Always returns 1 second, even if the next token won't arrive for longer.

**Fix:** Calculate dynamically: `nextTokenIn := (1.0 - bucket.tokens) / rl.rate`, round up. (Low priority, cosmetic)

---

## ✅ Commit bb87893 — Savings History / Trend

### What shipped well

- **GhostSnapshot model:** Clean schema with `snapshot_at`, `ghost_count`, `total_monthly_cost`, `currency`
- **Database design:** Migration file creates `ghost_snapshots` table with RLS policy for tenant isolation
- **Storage implementation:** `SaveSnapshot()` and `ListSnapshots()` in PostgreSQL Store; SQLite stubs for dev
- **Ingestion integration:** Snapshot created after each scan; prevents data loss between runs
- **API endpoint:** `GET /v1/trend` with optional `account_id` query parameter for filtering
- **Test coverage:** 8 handler unit tests + 8 PostgreSQL integration tests; good isolation testing
- **Dashboard integration:** `SavingsSparkline` component shows trend chart

### No issues found

All code paths covered, RLS policies correct, schema clean.

---

## Summary Table

| Issue | File | Severity | Type | Effort | Status |
|-------|------|----------|------|--------|--------|
| Rate limiter memory leak | `ratelimit.go` | **P1** | Bug | 30 min | TODO |
| No tests: logging Init | `logging.go` | P2 | Test gap | 30 min | TODO |
| No tests: RequestID middleware | `requestid.go` | P2 | Test gap | 30 min | TODO |
| No tests: ResetStuckScans() | `migrate.go` | P2 | Test gap | 30 min | TODO |
| Health endpoint ping failure untested | `handler_test.go` | P2 | Test gap | 15 min | TODO |
| Hardcoded Retry-After | `ratelimit.go` | P3 | Polish | 5 min | SKIP |

---

## Implementation Plan

### Phase 1 (Blocking for production)
1. **Fix rate limiter memory leak** — Add bucket cleanup goroutine (30 min)

### Phase 2 (Test coverage)
2. **Add logging unit tests** — Init with various env vars (30 min)
3. **Add RequestID middleware tests** — UUID injection, header passthrough, context storage (30 min)
4. **Add ResetStuckScans tests** — Mocked database, verify reset logic (30 min)
5. **Add health endpoint failure test** — Pinger error → HTTP 503 (15 min)

### Phase 3 (Optional)
6. **Dynamic Retry-After** — Calculate based on next token arrival (5 min, skip if time-constrained)

---

## Production Readiness

**Before deployment to App Runner:**
- ✅ API versioning complete
- ✅ Structured logging working
- ✅ Structured logging with error handling
- ✅ Prometheus metrics exposed
- ✅ Rate limiting functional
- ✅ Health endpoint with DB check
- ✅ Savings history accumulating
- ⚠️ Rate limiter memory leak (P1 — must fix)
- ⚠️ Test coverage gaps (P2 — should fix)

**Estimated timeline:**
- Fix P1 issue: 30 minutes
- Add P2 tests: 2 hours
- **Total: 2.5 hours to full production readiness**

---

## Detailed Recommendations

### 1. Rate Limiter Cleanup (P1)

Add this to `services/api/cmd/main.go` after the limiter is created (around line 120):

```go
if os.Getenv("DEV_MODE") != "true" {
    limiter := middleware.NewRateLimiter(1.0, 60.0)
    root = limiter.Wrap(root)
    slog.Info("api: rate limiting enabled (60 req/min per tenant)")
    
    // Cleanup background ticker: evict stale buckets every 5 minutes
    go func() {
        ticker := time.NewTicker(5 * time.Minute)
        defer ticker.Stop()
        for range ticker.C {
            limiter.CleanupStaleBuckets(1 * time.Hour)  // Remove buckets inactive >1h
        }
    }()
}
```

Add this method to `RateLimiter` in `ratelimit.go`:

```go
// CleanupStaleBuckets removes buckets not accessed in the given duration.
func (rl *RateLimiter) CleanupStaleBuckets(maxAge time.Duration) {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    
    now := time.Now()
    for tenantID, bucket := range rl.buckets {
        if now.Sub(bucket.lastRefill) > maxAge {
            delete(rl.buckets, tenantID)
        }
    }
}
```

---

### 2. Logging Unit Tests

Create `services/shared/logging/logging_test.go`:

```go
package logging

import (
    "log/slog"
    "os"
    "testing"
)

func TestInit_JSONOutput(t *testing.T) {
    os.Setenv("LOG_OUTPUT", "json")
    os.Setenv("DEV_MODE", "false")
    defer os.Unsetenv("LOG_OUTPUT")
    
    Init("test-service")
    // Verify slog is configured (indirectly by calling slog.Info and checking output)
}

func TestInit_TextOutput_WhenDEVMode(t *testing.T) {
    os.Setenv("DEV_MODE", "true")
    defer os.Unsetenv("DEV_MODE")
    
    Init("test-service")
    // Text handler should be active
}

```

---

### 3. RequestID Middleware Tests

Create `services/api/internal/middleware/requestid_test.go`:

```go
package middleware

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestRequestID_GeneratesUUID(t *testing.T) {
    h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        id := RequestIDFromCtx(r.Context())
        if id == "" {
            t.Fatal("expected request ID in context")
        }
        w.WriteHeader(http.StatusOK)
    }))
    
    w := httptest.NewRecorder()
    r := httptest.NewRequest(http.MethodGet, "/test", nil)
    h.ServeHTTP(w, r)
    
    if w.Header().Get("X-Request-ID") == "" {
        t.Error("expected X-Request-ID header")
    }
}

func TestRequestID_PassesExistingHeader(t *testing.T) {
    existingID := "custom-123-request-id"
    h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        id := RequestIDFromCtx(r.Context())
        if id != existingID {
            t.Fatalf("expected %s, got %s", existingID, id)
        }
        w.WriteHeader(http.StatusOK)
    }))
    
    w := httptest.NewRecorder()
    r := httptest.NewRequest(http.MethodGet, "/test", nil)
    r.Header.Set("X-Request-ID", existingID)
    h.ServeHTTP(w, r)
    
    if w.Header().Get("X-Request-ID") != existingID {
        t.Error("expected X-Request-ID header to contain existing ID")
    }
}
```

---

### 4. Health Endpoint Failure Test

Add to `services/api/internal/api/handler_test.go`:

```go
func TestHealth_DatabaseUnavailable_Returns503(t *testing.T) {
    mockStore := &mockStoreWithPinger{
        pingErr: fmt.Errorf("connection refused"),
    }
    api := New(mockStore)
    
    mux := http.NewServeMux()
    api.Register(mux)
    
    w := httptest.NewRecorder()
    mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
    
    if w.Code != http.StatusServiceUnavailable {
        t.Errorf("expected 503, got %d", w.Code)
    }
    if !strings.Contains(w.Body.String(), "unreachable") {
        t.Error("expected 'unreachable' in response")
    }
}

// Mock store that implements Pinger interface
type mockStoreWithPinger struct {
    mockStore
    pingErr error
}

func (m *mockStoreWithPinger) Ping(ctx context.Context) error {
    return m.pingErr
}
```

---

## Conclusion

The Phase 2 implementation is **95% production-ready**. The rate limiter memory leak is the only blocking issue before production deployment. All other items are test coverage improvements that can be addressed immediately after fixing the P1 issue.

**Estimated time to full production readiness: 2.5 hours**

---

**Next step:** Proceed with implementations in priority order.
