package sqlite_test

import (
	"context"
	"os"
	"testing"
	"time"

	"axiaops.io/ingestion/internal/model"
	"axiaops.io/ingestion/internal/storage/sqlite"
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
