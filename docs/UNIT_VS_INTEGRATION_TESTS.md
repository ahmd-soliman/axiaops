# Unit Tests vs Integration Tests — Detailed Comparison

Side-by-side comparison of unit tests and PostgreSQL integration tests for AxiaOps.

---

## Quick Reference Table

| Aspect | Unit Tests (test:unit) | Integration Tests (test:db) |
|--------|----------------------|----------------------------|
| **Purpose** | Test business logic, handlers, middleware | Test database layer, RLS, migrations |
| **Database** | SQLite (temp file per test) | PostgreSQL (shared instance) |
| **Isolation** | Complete (own file) | Per-tenant (same DB) |
| **Parallelization** | YES (-parallel 4) | NO (-p=1 sequential) |
| **Cleanup** | Auto-deleted | TRUNCATE CASCADE before/after |
| **Speed** | Fast (~1-2 sec) | Moderate (~5 sec) |
| **Environment Setup** | None | PostgreSQL container required |
| **Location** | `*_test.go` everywhere | `services/shared/storage/postgres/*_test.go` |
| **Runs On** | Every commit | Every commit |
| **CI Parallelization** | test:shared, test:api, test:ingestion (3 jobs in parallel) | test:db (1 job, runs sequentially internally) |

---

## By Example

### Example 1: Testing a Handler

#### Unit Test (✓ Correct Approach)

```go
// services/api/internal/api/handler_test.go
package api_test

import "testing"

func TestGetGhosts_ReturnsJSON(t *testing.T) {
	// No database setup needed
	mockStore := &mockStore{} // Mock interface
	handler := api.NewHandler(mockStore)
	
	// Test HTTP response
	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ghosts", nil)
	handler.ServeHTTP(resp, req)
	
	if resp.Code != 200 {
		t.Errorf("expected 200, got %d", resp.Code)
	}
}

// Cleanup: Automatic (no database created)
// Run: make test:unit (parallel-safe)
// Speed: <100ms
```

**Why unit test here:**
- Tests HTTP layer, not database
- Uses mock Store
- No shared state
- Can run in parallel
- Fast feedback

#### NOT as Integration Test (✗ Overkill)

```go
// ❌ DON'T: Testing HTTP layer with real PostgreSQL

func TestGetGhosts_WithRealDB(t *testing.T) {
	// Waste time spinning up PostgreSQL
	s := newTestStore(t)  // Real DB
	ctx, _ := newTenantCtx(t, s)
	
	// Insert test data
	ghosts := []model.GhostResource{ ... }
	s.SaveGhosts(ctx, ghosts)
	
	// Now test HTTP
	handler := api.NewHandler(s)
	// ... test ...
}

// Problems:
// • Unnecessary database overhead
// • Slow (requires PostgreSQL)
// • Tests multiple layers (handler + storage)
// • Can't parallelize
// • Hard to isolate failure
```

---

### Example 2: Testing Storage Layer

#### Integration Test (✓ Correct Approach)

```go
// services/shared/storage/postgres/postgres_test.go
package postgres_test

func TestSave_InsertsRecords(t *testing.T) {
	// newTestStore() calls setup() automatically
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)
	
	// Test with real PostgreSQL
	records := []model.CostRecord{ ... }
	inserted, err := s.Save(ctx, records)
	
	if inserted != 2 {
		t.Errorf("expected 2, got %d", inserted)
	}
	
	// Cleanup: Automatic via t.Cleanup()
}

// Run: make test:db (sequential)
// Speed: ~500ms
```

**Why integration test here:**
- Tests database layer specifically
- Needs real PostgreSQL for RLS/constraints
- Tests migrations, schema, isolation
- Per-tenant data isolation is important

#### NOT as Unit Test (✗ Won't Work)

```go
// ❌ DON'T: Testing storage layer with SQLite

func TestSave_WithSQLite(t *testing.T) {
	// Open SQLite temp file
	db, _ := sql.Open("sqlite", ":memory:")
	defer db.Close()
	
	// Try to test PostgreSQL-specific features
	s := postgres.New(db)  // ← Won't work!
	
	// Problems:
	// • SQLite doesn't support PostgreSQL features
	// • RLS policies don't exist in SQLite
	// • Schema migration won't work
	// • Tests the wrong database
}
```

---

### Example 3: Testing the Analyzer

#### Unit Test (✓ Correct Approach)

```go
// services/shared/analyzer/detector_test.go
package analyzer_test

func TestDetect_FlagsZeroUsageEC2(t *testing.T) {
	// No database needed for business logic
	costs := []model.CostRecord{
		{
			Service:    "AmazonEC2",
			ResourceID: "i-001",
			Amount:     100.00,
		},
	}
	
	usage := []model.UsageRecord{
		{
			ResourceID: "i-001",
			Metric:     "CPUUtilization",
			Avg:        2.5,  // Below 5% threshold
		},
	}
	
	// Run detector (pure function)
	ghosts := analyzer.Detect(costs, usage)
	
	if len(ghosts) != 1 {
		t.Errorf("expected 1 ghost, got %d", len(ghosts))
	}
	if ghosts[0].Reason != "Instance CPU below 5% — likely idle" {
		t.Errorf("expected idle reason, got %q", ghosts[0].Reason)
	}
}

// Cleanup: None needed
// Run: make test:unit (parallel-safe)
// Speed: <1ms
```

**Why unit test here:**
- Pure business logic
- No I/O or databases
- Deterministic output
- Tests threshold logic
- Very fast

---

### Example 4: Testing RLS Isolation

#### Integration Test (✓ Correct Approach)

```go
// services/shared/storage/postgres/postgres_test.go

func TestGhosts_TenantIsolation(t *testing.T) {
	s := newTestStore(t)
	
	// Create two separate tenants
	ctx1, tenant1 := newTenantCtx(t, s)
	ctx2, tenant2 := newTenantCtx(t, s)
	
	// Tenant 1 saves ghosts
	ghosts1 := []model.GhostResource{ {ResourceID: "res-1"} }
	s.SaveGhosts(ctx1, ghosts1)
	
	// Tenant 2 saves ghosts
	ghosts2 := []model.GhostResource{ {ResourceID: "res-2"} }
	s.SaveGhosts(ctx2, ghosts2)
	
	// Tenant 1 loads ghosts — should only see their own
	loaded1, _ := s.LoadGhosts(ctx1)
	if len(loaded1) != 1 || loaded1[0].ResourceID != "res-1" {
		t.Error("Tenant 1 can see Tenant 2's data!")  // RLS failed
	}
	
	// Tenant 2 loads ghosts — should only see their own
	loaded2, _ := s.LoadGhosts(ctx2)
	if len(loaded2) != 1 || loaded2[0].ResourceID != "res-2" {
		t.Error("Tenant 2 can see Tenant 1's data!")  // RLS failed
	}
}

// Cleanup: Automatic via t.Cleanup()
// Run: make test:db (sequential, -p=1)
// Speed: ~200ms
```

**Why integration test here:**
- Tests PostgreSQL RLS specifically
- Needs real database constraints
- Tests tenant isolation
- Unit test can't verify RLS policies

---

## Test Structure

### Unit Test Structure

```go
package mypackage_test

import "testing"

func TestMyFunction(t *testing.T) {
	// Setup: minimal, no DB
	input := []int{1, 2, 3}
	
	// Exercise
	result := MyFunction(input)
	
	// Verify
	if result != 6 {
		t.Errorf("expected 6, got %d", result)
	}
	
	// Cleanup: automatic or via t.Cleanup()
}
```

**Pattern:**
1. **Setup:** Create inputs, mocks, temporary files
2. **Exercise:** Call the function
3. **Verify:** Check the result
4. **Cleanup:** Done (auto-deleted or via t.Cleanup)

**Duration:** <100ms
**Database:** None

### Integration Test Structure

```go
package postgres_test

import "testing"

func TestStoreWithDatabase(t *testing.T) {
	// Setup: Create fresh database state
	s := newTestStore(t)  // Calls setup() → TRUNCATE CASCADE
	ctx, tenant := newTenantCtx(t, s)
	
	// Exercise: Use real database
	records := []model.CostRecord{ ... }
	inserted, err := s.Save(ctx, records)
	
	// Verify: Check database state
	if inserted != len(records) {
		t.Errorf("expected %d inserted, got %d", len(records), inserted)
	}
	
	// Cleanup: Automatic via t.Cleanup()
	// (TRUNCATE CASCADE called after test)
}
```

**Pattern:**
1. **Setup:** `newTestStore()` + `newTenantCtx()`
   - Calls `setup()` to truncate tables
   - Creates fresh tenant
2. **Exercise:** Use real database
3. **Verify:** Check database state
4. **Cleanup:** `t.Cleanup()` handler fires (truncate again)

**Duration:** 500ms–1s
**Database:** PostgreSQL container (shared)

---

## Cleanup Differences

### Unit Tests (SQLite)

```go
func TestWithSQLite(t *testing.T) {
	// Create temp file
	db, _ := sql.Open("sqlite", tempFile)
	
	// ... test ...
	
	// Cleanup: Automatic
	// - tempFile is deleted by os.CreateTemp cleanup
	// - defer in test helper closes db
	// No action needed!
}
```

**Cleanup mechanism:** Automatic temp file deletion

### Integration Tests (PostgreSQL)

```go
func setup(t *testing.T) {
	db := connectTestDB(t)
	
	// Layer 1: TRUNCATE before test
	db.Exec("TRUNCATE TABLE snapshots, resources, accounts, users, tenants CASCADE")
	
	// Layer 2: Register post-test cleanup
	t.Cleanup(func() {
		// TRUNCATE after test
		db.Exec("TRUNCATE TABLE snapshots, resources, accounts, users, tenants CASCADE")
		db.Close()
	})
	
	return db
}
```

**Cleanup mechanism:** Explicit TRUNCATE CASCADE (before and after)

---

## Parallelization Explanation

### Why Unit Tests Can Parallelize

```
Unit Test 1        Unit Test 2        Unit Test 3        Unit Test 4
   ↓                   ↓                   ↓                   ↓
SQLite temp 1      SQLite temp 2      SQLite temp 3      SQLite temp 4
   ↓                   ↓                   ↓                   ↓
Independent        Independent        Independent        Independent
databases          databases          databases          databases
   ↓                   ↓                   ↓                   ↓
✓ No conflicts
✓ Can run simultaneously
```

Each test has its own isolated SQLite file → no interference

### Why Integration Tests Cannot Parallelize

```
Integration Test 1        Integration Test 2        Integration Test 3
   ↓                          ↓                          ↓
PostgreSQL (shared)      PostgreSQL (shared)      PostgreSQL (shared)
   ↓                          ↓                          ↓
Test 1: TRUNCATE         Test 2: TRUNCATE         Test 3: SELECT
Test 1: INSERT           Test 2: INSERT           Test 3: Oops! No data!
Test 1: SELECT           Test 2: TRUNCATE ← Deletes Test 3's data
                              ↓
                         ✗ Conflict!
                         ✗ Data pollution
                         ✗ Must run sequentially
```

All tests share same PostgreSQL instance → need sequential execution

---

## Command Reference

### Run Unit Tests Only
```bash
make test:unit        # Fast: ~5 seconds
```

Inside: 
- `go test ./... -parallel 4` (4 tests simultaneously)
- SQLite temp files (auto-cleaned)
- No database required

### Run Integration Tests Only
```bash
make test:db          # Moderate: ~10 seconds
```

Inside:
- `go test ./storage/postgres/... -p=1` (sequential)
- PostgreSQL container (required)
- Cleanup before/after each test

### Run All Tests
```bash
make test-all         # Total: ~20 seconds
```

Runs:
1. Unit tests (~5s)
2. Integration tests (~10s)
3. Combined time (overlap if parallelizable)

### Manually Clean Database
```bash
make clean-db         # If tests fail with "table exists"
```

Executes:
```sql
TRUNCATE TABLE snapshots, resources, accounts, users, tenants CASCADE;
```

---

## Summary

| Need | Use | Speed | Database |
|------|-----|-------|----------|
| Test handler logic | Unit test | <100ms | None |
| Test business logic | Unit test | <100ms | None |
| Test analyzer | Unit test | <100ms | None |
| Test storage layer | Integration test | 500ms | PostgreSQL |
| Test RLS isolation | Integration test | 500ms | PostgreSQL |
| Test migrations | Integration test | 500ms | PostgreSQL |

**Rule of thumb:** If it doesn't need a real database, use a unit test.

