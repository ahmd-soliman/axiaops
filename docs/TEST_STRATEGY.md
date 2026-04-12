# Test Strategy — AxiaOps

Comprehensive guide to the testing architecture, test isolation, database cleanup, and parallelization strategy.

---

## Overview

AxiaOps uses a **two-tier testing strategy**:

1. **Unit Tests** — Fast, parallel, isolated (SQLite/mocks)
2. **Integration Tests** — Slower, sequential, database-backed (PostgreSQL)

Each serves a different purpose and has different constraints.

---

## Test Types

### Unit Tests (test:unit)

**What they test:**
- Business logic (analyzer, crypto, models)
- HTTP handlers and middleware
- Provider interfaces (with mocks)
- Serialization/deserialization

**Database:**
- **SQLite only** — temporary per-test database via `os.CreateTemp()`
- Destroyed after test completes
- No cleanup needed

**Parallelization:**
- ✅ **Safe to run in parallel** — each test gets isolated SQLite file
- Default: `go test -parallel 4`

**Speed:**
- ~1–2 seconds per service
- No network I/O, no container startup

**Services tested:**
- `services/api` — HTTP handlers, middleware
- `services/ingestion` — provider interface, detectors
- `services/shared` — analyzer, crypto, models (non-storage)

### Integration Tests (test:db)

**What they test:**
- PostgreSQL storage layer
- Row-Level Security (RLS) tenant isolation
- Database migrations
- Concurrent access patterns

**Database:**
- **PostgreSQL 16** — shared test database
- Cleaned before each test (`TRUNCATE CASCADE`)
- Each test creates own tenant for isolation

**Parallelization:**
- ❌ **NOT safe to parallelize** — tests share same database
- Uses `-p=1` flag (sequential execution)
- Tests can interfere if run simultaneously

**Speed:**
- ~2–5 seconds for all integration tests
- Includes PostgreSQL startup (3–5 seconds first run)
- Reuses container for subsequent runs

**Services tested:**
- `services/shared/storage/postgres` — database operations only

---

## Key Differences

| Aspect | Unit Tests | Integration Tests |
|--------|-----------|-------------------|
| **Database** | SQLite (temporary) | PostgreSQL (persistent) |
| **Isolation** | Per-test file | Per-test tenant |
| **Parallelization** | Yes (4 parallel) | No (sequential only) |
| **Speed** | Fast (~1s) | Moderate (~5s) |
| **Environment** | No setup required | Requires PostgreSQL running |
| **Failure scope** | Single test fails | May affect subsequent tests |
| **Location** | `*_test.go` in each package | `services/shared/storage/postgres/*_test.go` |

---

## Database Cleanup Strategy

### Unit Tests (SQLite)
```go
// Automatic: SQLite temp file deleted when test ends
func TestSomething(t *testing.T) {
	db, _ := sql.Open("sqlite", tempFile)  // temp file
	// ... test ...
	// SQLite file automatically deleted when test ends
}
```

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
	// Clean before test
	db.Exec("TRUNCATE TABLE snapshots, resources, accounts, users, tenants CASCADE")
	
	// Register post-test cleanup
	t.Cleanup(func() {
		// Clean after test
		db.Exec("TRUNCATE TABLE snapshots, resources, accounts, users, tenants CASCADE")
		db.Close()
	})
}
```

**Cleanup happens:**
1. **Before test** — removes leftover data from previous tests
2. **After test** — removes test data created by this test

This prevents test pollution and data leaks.

---

## Running Tests Locally

### Unit Tests Only (Fast)
```bash
make test:unit

# Or manually
cd services/shared && go test ./... -count=1 -parallel 4
```

**Time:** ~5 seconds (no database required)

### Integration Tests Only
```bash
make test:db

# Or manually (with postgres running)
docker compose up -d postgres
cd services/shared && TEST_DATABASE_URL=... go test ./storage/postgres/... -p=1
```

**Time:** ~10 seconds (first run includes PostgreSQL startup)

### All Tests (Unit + Integration)
```bash
make test-all

# Or
make test:unit && make test:db
```

**Time:** ~20 seconds total

---

## Running Tests in GitLab CI

### test:shared
- **Type:** Unit tests
- **Runs:** All branches
- **Database:** No
- **Parallel:** Yes (4 parallel)
- **Duration:** ~2 minutes

```yaml
test:shared:
  stage: test
  script:
    - cd services/shared && go test ./... -count=1 -v
```

### test:db
- **Type:** Integration tests  
- **Runs:** All branches
- **Database:** PostgreSQL container
- **Parallel:** No (`-p=1` sequential)
- **Duration:** ~3 minutes

```yaml
test:db:
  stage: test
  services:
    - postgres:16-alpine  # Fresh container per run
  before_script:
    - migrate to latest schema  # Runs TestMain
  script:
    - go test ./storage/postgres/... -count=1 -v -p=1  # Sequential
```

### test:api, test:ingestion
- **Type:** Unit tests
- **Runs:** All branches
- **Database:** No
- **Parallel:** Yes
- **Duration:** ~2 minutes each

---

## Parallelization Details

### Why `-p=1` for PostgreSQL Tests?

Tests share the same PostgreSQL database instance. If tests run in parallel:

```
Test A: INSERT tenant-a
           ↓ (parallel)
Test B: INSERT tenant-b
Test A: TRUNCATE TABLE tenants CASCADE  ← Deletes tenant-b's data!
Test B: SELECT FROM ghosts WHERE tenant_id = ...  ← Data missing!
```

**Solution:** Run PostgreSQL tests sequentially (`-p=1`)

```bash
go test ./storage/postgres/... -p=1  # One test at a time
```

### Why `-parallel 4` for Unit Tests?

Unit tests don't share state — each gets isolated SQLite file:

```
Test A: CREATE temp DB A
           ↓ (parallel)
Test B: CREATE temp DB B
           ↓ (parallel)
Test C: CREATE temp DB C
           ↓ (parallel)
Test D: CREATE temp DB D

All run simultaneously, no conflicts!
```

```bash
go test ./... -parallel 4  # 4 tests at once
```

---

## Troubleshooting

### PostgreSQL Tests Fail with "Table already exists"

**Cause:** Previous test didn't clean up completely.

**Fix:**
```bash
make clean-db     # Manually truncate tables
make test:db      # Retry
```

### "TRUNCATE CASCADE failed: permission denied"

**Cause:** Using wrong database user or RLS issues.

**Fix:**
```bash
# Ensure using owner user for cleanup
docker compose exec -T postgres \
  psql -U axiaops_owner -d axiaops \
  -c "TRUNCATE TABLE snapshots, resources, accounts, users, tenants CASCADE"
```

### Test Hangs or Timeout

**Cause:** PostgreSQL container didn't start in time.

**Fix:**
```bash
# Ensure postgres is running
docker compose up -d postgres
sleep 3  # Wait for startup
pg_isready -h localhost  # Verify ready
```

### "test:db: go test: no test files"

**Cause:** Running from wrong directory.

**Fix:**
```bash
cd services/shared  # Must be here
go test ./storage/postgres/...
```

---

## Best Practices

### ✅ Do

1. **Use `t.Cleanup()`** for post-test cleanup:
   ```go
   func TestSomething(t *testing.T) {
       resource := setupResource(t)
       t.Cleanup(func() { resource.Close() })
       // ... test ...
   }
   ```

2. **Create fresh tenants per test**:
   ```go
   ctx, tenant := newTenantCtx(t, store)  // Unique tenant per test
   // ... test using this tenant ...
   ```

3. **Run integration tests sequentially**:
   ```bash
   go test ./storage/postgres/... -p=1
   ```

4. **Use `-count=1`** to disable test caching:
   ```bash
   go test ./... -count=1  # Don't cache results
   ```

### ❌ Don't

1. **Don't share databases between tests** — use per-test isolation
2. **Don't run PostgreSQL tests in parallel** — use `-p=1`
3. **Don't assume test order** — tests can run in any order
4. **Don't rely on global state** — each test should be independent

---

## CI/CD Pipeline

### Test Stage Execution

```
Feature branch pushed
  │
  ├─ test:shared    [2 min] ─────┐
  ├─ test:db        [3 min] ─────┼─→ Parallel execution
  ├─ test:api       [2 min] ─────┤   (5 jobs simultaneously)
  ├─ test:ingestion [2 min] ─────┤
  ├─ test:lint      [1 min] ─────┤
  └─ test:vet       [1 min] ─────┘
  
Total: ~5 minutes (longest job determines duration)
```

**Why parallel?**
- Unit tests don't interfere with each other
- PostgreSQL tests run sequentially internally (`-p=1`)
- Each job is independent (different database/context)

---

## Making Test Changes

### Adding a New Unit Test

```go
// services/api/internal/api/handler_test.go
func TestNewHandler(t *testing.T) {
	// No database setup needed
	h := NewHandler(mockStore)
	// ... test ...
}
```

- No database required
- Will run in parallel with other unit tests
- No cleanup needed (SQLite auto-deleted)

### Adding a New Integration Test

```go
// services/shared/storage/postgres/postgres_test.go
func TestSaveWithComplexQuery(t *testing.T) {
	s := newTestStore(t)  // ← Calls setup() + cleanup
	ctx, tenant := newTenantCtx(t, s)
	
	// Test with real PostgreSQL
	// Cleanup happens automatically via t.Cleanup()
}
```

- Add to `postgres_test.go`
- Use `newTestStore()` for automatic setup/cleanup
- Use `newTenantCtx()` for tenant isolation
- Will run sequentially with other integration tests

---

## Summary

| Scenario | Command | Time | Parallel? |
|----------|---------|------|-----------|
| **Unit tests only** | `make test:unit` | ~5s | Yes |
| **Integration tests only** | `make test:db` | ~10s | No (sequential) |
| **All tests** | `make test-all` | ~20s | Mixed |
| **In GitLab CI** | automatic | ~5 min | Yes (but test:db uses -p=1) |

**Key insight:** Unit tests parallelize well because each uses isolated SQLite. Integration tests must run sequentially because they share PostgreSQL.

