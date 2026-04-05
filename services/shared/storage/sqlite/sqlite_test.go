package sqlite_test

import (
	"context"
	"os"
	"testing"
	"time"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage/sqlite"
)

func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	f, err := os.CreateTemp("", "axiaops-test-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	store, err := sqlite.New(f.Name())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func record(service, region string, amount float64) model.CostRecord {
	return model.CostRecord{
		Provider:    "aws",
		AccountID:   "000000000000",
		Service:     service,
		Region:      region,
		ResourceID:  "res-001",
		Amount:      amount,
		Currency:    "USD",
		PeriodStart: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		Tags:        map[string]string{"team": "platform"},
		FetchedAt:   time.Now().UTC(),
	}
}

func TestSave_InsertsRecords(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	records := []model.CostRecord{
		record("AmazonEC2", "eu-central-1", 100.00),
		record("AmazonRDS", "eu-central-1", 200.00),
	}

	inserted, err := store.Save(ctx, records)
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if inserted != 2 {
		t.Errorf("expected 2 inserted, got %d", inserted)
	}
}

func TestSave_DeduplicatesOnRerun(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	records := []model.CostRecord{
		record("AmazonEC2", "eu-central-1", 100.00),
	}

	// First run — inserts
	inserted, err := store.Save(ctx, records)
	if err != nil {
		t.Fatalf("first save failed: %v", err)
	}
	if inserted != 1 {
		t.Errorf("expected 1 inserted on first run, got %d", inserted)
	}

	// Second run — same record, should be skipped
	inserted, err = store.Save(ctx, records)
	if err != nil {
		t.Fatalf("second save failed: %v", err)
	}
	if inserted != 0 {
		t.Errorf("expected 0 inserted on second run (duplicate), got %d", inserted)
	}
}

func TestSave_EmptyBatch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	inserted, err := store.Save(ctx, nil)
	if err != nil {
		t.Fatalf("save with nil records failed: %v", err)
	}
	if inserted != 0 {
		t.Errorf("expected 0 inserted for empty batch, got %d", inserted)
	}
}

func TestSave_DifferentRegionIsNotDuplicate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	records := []model.CostRecord{
		record("AmazonEC2", "eu-central-1", 100.00),
		record("AmazonEC2", "eu-west-1", 100.00), // same service, different region
	}

	inserted, err := store.Save(ctx, records)
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if inserted != 2 {
		t.Errorf("expected 2 inserted (different regions), got %d", inserted)
	}
}

func TestSave_TagsStoredAsJSON(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	r := record("AmazonEC2", "eu-central-1", 100.00)
	r.Tags = map[string]string{"team": "backend", "env": "production"}

	inserted, err := store.Save(ctx, []model.CostRecord{r})
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if inserted != 1 {
		t.Errorf("expected 1 inserted, got %d", inserted)
	}
}

// ── Tenant tests ──────────────────────────────────────────────────────────────

func TestUpsertTenant_CreatesOnFirstCall(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tenant, err := store.UpsertTenant(ctx, "org_abc", "Acme Corp")
	if err != nil {
		t.Fatalf("upsert tenant failed: %v", err)
	}
	if tenant.ID == "" {
		t.Error("expected non-empty tenant ID")
	}
	if tenant.OrgCode != "org_abc" {
		t.Errorf("expected org_code org_abc, got %s", tenant.OrgCode)
	}
	if tenant.Name != "Acme Corp" {
		t.Errorf("expected name Acme Corp, got %s", tenant.Name)
	}
}

func TestUpsertTenant_ReturnsSameIDOnSecondCall(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	first, err := store.UpsertTenant(ctx, "org_abc", "Acme Corp")
	if err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}
	second, err := store.UpsertTenant(ctx, "org_abc", "Acme Corp")
	if err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected same tenant ID on second call, got %s and %s", first.ID, second.ID)
	}
}

func TestUpsertTenant_UpdatesName(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.UpsertTenant(ctx, "org_abc", "Old Name")
	if err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}
	updated, err := store.UpsertTenant(ctx, "org_abc", "New Name")
	if err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}
	if updated.Name != "New Name" {
		t.Errorf("expected name to be updated to New Name, got %s", updated.Name)
	}
}

// ── User tests ────────────────────────────────────────────────────────────────

func TestUpsertUser_CreatesOnFirstLogin(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tenant, _ := store.UpsertTenant(ctx, "org_abc", "Acme Corp")

	user, err := store.UpsertUser(ctx, tenant.ID, "kp_user_123", "alice@acme.com", "Alice")
	if err != nil {
		t.Fatalf("upsert user failed: %v", err)
	}
	if user.ID == "" {
		t.Error("expected non-empty user ID")
	}
	if user.TenantID != tenant.ID {
		t.Errorf("expected tenant_id %s, got %s", tenant.ID, user.TenantID)
	}
	if user.Email != "alice@acme.com" {
		t.Errorf("expected email alice@acme.com, got %s", user.Email)
	}
}

func TestUpsertUser_ReturnsSameIDOnSecondLogin(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tenant, _ := store.UpsertTenant(ctx, "org_abc", "Acme Corp")

	first, err := store.UpsertUser(ctx, tenant.ID, "kp_user_123", "alice@acme.com", "Alice")
	if err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}
	second, err := store.UpsertUser(ctx, tenant.ID, "kp_user_123", "alice@acme.com", "Alice")
	if err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected same user ID on second login, got %s and %s", first.ID, second.ID)
	}
}
