# Test Strategy — AxiaOps

Comprehensive guide to the testing architecture, test isolation, database cleanup, and parallelization strategy.

---

## Overview

AxiaOps uses a **two-tier testing strategy**:

1. **Unit Tests** — Fast, parallel, isolated (mocks, no database)
2. **Integration Tests** — Slower, sequential, database-backed (PostgreSQL)

Each serves a different purpose and has different constraints.

---

## Test Types

### Unit Tests

**What they test:**
- Business logic (analyzer, crypto, models)
- HTTP handlers and middleware
- Provider interfaces (with mocks)
- Serialization/deserialization

**Database:** None — mocks only.

**Parallelization:**
- ✅ **Safe to run in parallel**
- Default: `go test -parallel 4`

**Speed:**
- ~1–2 seconds per service
- No network I/O, no container startup

**Services tested:**
- `services/api` — HTTP handlers, middleware
- `services/ingestion` — provider interface, detectors
- `services/shared` — analyzer, crypto, models (non-storage)

### Integration Tests

**What they test:**
- PostgreSQL storage layer
- Row-Level Security (RLS) organization isolation
- Database migrations
- Concurrent access patterns

**Database:**
- **PostgreSQL 16** — shared test database
- Cleaned before each test (`TRUNCATE CASCADE`)
- Each test creates own organization for isolation

**Parallelization:**
- ❌ **NOT safe to parallelize** — tests share same database
- Uses `-p=1` flag (sequential execution)

**Speed:**
- ~2–5 seconds for all integration tests
- Includes PostgreSQL startup (3–5 seconds first run)

**Services tested:**
- `services/shared/storage/postgres` — database operations only

---

## Key Differences

| Aspect | Unit Tests | Integration Tests |
|--------|-----------|-------------------|
| **Database** | None (mocks) | PostgreSQL (persistent) |
| **Isolation** | Per-test mock | Per-test organization |
| **Parallelization** | Yes (4 parallel) | No (sequential only) |
| **Speed** | Fast (~1s) | Moderate (~5s) |
| **Environment** | No setup required | Requires PostgreSQL running |
| **Location** | `*_test.go` in each package | `services/shared/storage/postgres/*_test.go` |

---

## Database Cleanup Strategy

### Integration Tests (PostgreSQL)
```go
// Two-layer cleanup:

// Layer 1: Before test starts
func newTestStore(t *testing.T) *postgres.Store {
	setup(t)  // ← TRUNCATE all tables here
	s, _ := postgres.New(ctx, TEST_DATABASE_URL)
	return s
}

// Layer 2: After test completes
func setup(t *testing.T) {
	db.Exec("TRUNCATE TABLE snapshots, resources, accounts, users, organizations CASCADE")
	t.Cleanup(func() {
		db.Exec("TRUNCATE TABLE snapshots, resources, accounts, users, organizations CASCADE")
		db.Close()
	})
}
```

**Cleanup happens:**
1. **Before test** — removes leftover data from previous tests
2. **After test** — removes test data created by this test

---

## Running Tests Locally

### Unit Tests Only (Fast)
```bash
make test

# Or manually
cd services/shared && go test ./... -count=1 -parallel 4
```

**Time:** ~5 seconds (no database required)

### Integration Tests Only
```bash
make test-storage

# Or manually (with postgres running)
docker compose up -d postgres
cd services/shared && TEST_DATABASE_URL=... go test ./storage/postgres/... -p=1
```

**Time:** ~10 seconds (first run includes PostgreSQL startup)

### All Tests (Unit + Integration)
```bash
make test-all
```

---

## Parallelization Details

### Why `-p=1` for PostgreSQL Tests?

Tests share the same PostgreSQL database instance. Running in parallel risks one test's
`TRUNCATE CASCADE` wiping data mid-flight for another test.

**Solution:** Run PostgreSQL tests sequentially (`-p=1`)

### Why `-parallel 4` for Unit Tests?

Unit tests don't share state — each uses mocks or in-memory data. All run simultaneously
with no conflicts.

---

## Best Practices

### ✅ Do

1. **Use `t.Cleanup()`** for post-test cleanup
2. **Create fresh organizations per test** via `newOrgCtx(t, store)`
3. **Run integration tests sequentially**: `go test ./storage/postgres/... -p=1`
4. **Use `-count=1`** to disable test caching

### ❌ Don't

1. Don't run PostgreSQL tests in parallel — use `-p=1`
2. Don't assume test order — tests can run in any order
3. Don't rely on global state — each test should be independent

---

## Adding Tests

### New Unit Test

```go
func TestNewHandler(t *testing.T) {
	h := NewHandler(mockStore)
	// ... test ...
}
```

No database required. Will run in parallel.

### New Integration Test

```go
// services/shared/storage/postgres/postgres_test.go
func TestSaveWithComplexQuery(t *testing.T) {
	s := newTestStore(t)  // setup + cleanup wired in
	ctx, _ := newOrgCtx(t, s)
	// ... test with real PostgreSQL ...
}
```

---

## Summary

| Scenario | Command | Time | Parallel? |
|----------|---------|------|-----------|
| **Unit tests only** | `make test` | ~5s | Yes |
| **Integration tests only** | `make test-storage` | ~10s | No (sequential) |
| **All tests** | `make test-all` | ~20s | Mixed |
