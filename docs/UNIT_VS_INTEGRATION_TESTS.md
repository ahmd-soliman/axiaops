# Unit Tests vs Integration Tests — Detailed Comparison

Side-by-side comparison of unit tests and PostgreSQL integration tests for AxiaOps.

---

## Quick Reference Table

| Aspect | Unit Tests | Integration Tests |
|--------|-----------|-------------------|
| **Purpose** | Test business logic, handlers, middleware | Test database layer, RLS, migrations |
| **Database** | None (mocks) | PostgreSQL (shared instance) |
| **Isolation** | Per-test mock | Per-organization (same DB) |
| **Parallelization** | YES (-parallel 4) | NO (-p=1 sequential) |
| **Cleanup** | None needed | TRUNCATE CASCADE before/after |
| **Speed** | Fast (~1-2 sec) | Moderate (~5 sec) |
| **Environment Setup** | None | PostgreSQL container required |
| **Location** | `*_test.go` everywhere | `services/shared/storage/postgres/*_test.go` |

---

## By Example

### Example 1: Testing a Handler

#### Unit Test (✓ Correct Approach)

```go
// services/api/internal/api/handler_test.go
package api_test

func TestGetZombies_ReturnsJSON(t *testing.T) {
	mockStore := &mockStore{}
	handler := api.NewHandler(mockStore)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/zombies", nil)
	handler.ServeHTTP(resp, req)

	if resp.Code != 200 {
		t.Errorf("expected 200, got %d", resp.Code)
	}
}
```

**Why unit test here:** Tests HTTP layer only. Uses mock Store. Fast, parallel-safe.

---

### Example 2: Testing Storage Layer

#### Integration Test (✓ Correct Approach)

```go
// services/shared/storage/postgres/postgres_test.go
func TestSave_InsertsRecords(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

	records := []model.CostRecord{ ... }
	inserted, err := s.Save(ctx, records)

	if inserted != 2 {
		t.Errorf("expected 2, got %d", inserted)
	}
}
```

**Why integration test here:** Needs real PostgreSQL for RLS, constraints, and migrations.

---

### Example 3: Testing the Analyzer

#### Unit Test (✓ Correct Approach)

```go
// services/shared/analyzer/detector_test.go
func TestDetect_FlagsZeroUsageEC2(t *testing.T) {
	costs := []model.CostRecord{{Service: "AmazonEC2", ResourceID: "i-001", Amount: 100.00}}
	usage := []model.UsageRecord{{ResourceID: "i-001", Metric: "CPUUtilization", Avg: 2.5}}

	zombies := analyzer.Detect(costs, usage)

	if len(zombies) != 1 {
		t.Errorf("expected 1 zombie, got %d", len(zombies))
	}
}
```

**Why unit test here:** Pure business logic, no I/O, deterministic.

---

### Example 4: Testing RLS Isolation

#### Integration Test (✓ Correct Approach)

```go
func TestZombies_OrganizationIsolation(t *testing.T) {
	s := newTestStore(t)
	ctx1, _ := newOrgCtx(t, s)
	ctx2, _ := newOrgCtx(t, s)

	s.SaveZombies(ctx1, []model.ZombieResource{{ResourceID: "res-1"}})

	loaded2, _ := s.LoadZombies(ctx2)
	if len(loaded2) != 0 {
		t.Error("Organization 2 can see Organization 1's data — RLS failed")
	}
}
```

**Why integration test here:** RLS is a PostgreSQL feature — can't be tested without a real database.

---

## Cleanup

### Unit Tests

No database → no cleanup needed. Any temporary state is in-memory or via `t.Cleanup()`.

### Integration Tests (PostgreSQL)

```go
func setup(t *testing.T) {
	db := connectTestDB(t)
	// Truncate before test
	db.Exec("TRUNCATE TABLE snapshots, resources, accounts, users, organizations CASCADE")
	t.Cleanup(func() {
		// Truncate after test
		db.Exec("TRUNCATE TABLE snapshots, resources, accounts, users, organizations CASCADE")
		db.Close()
	})
}
```

---

## Parallelization

### Unit Tests — parallel-safe

Each test uses mocks or in-memory data. No shared state → run 4 at a time.

### Integration Tests — must be sequential

All tests share the same PostgreSQL instance. A `TRUNCATE` in one test would wipe data
mid-flight for another. Use `-p=1`.

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
