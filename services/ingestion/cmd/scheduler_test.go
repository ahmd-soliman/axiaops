package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"axiaops.io/shared/model"
	"axiaops.io/shared/queue"
)

// mockStoreForScheduler is a minimal mock store for testing the scheduler.
type mockStoreForScheduler struct {
	accounts []model.Account
	listErr  error
}

func (m *mockStoreForScheduler) ListAccounts(ctx context.Context) ([]model.Account, error) {
	return m.ListAllAccounts(ctx)
}
func (m *mockStoreForScheduler) ListAllAccounts(ctx context.Context) ([]model.Account, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return append([]model.Account(nil), m.accounts...), nil
}
func (m *mockStoreForScheduler) Save(context.Context, []model.CostRecord) (int64, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) SaveGhosts(context.Context, []model.GhostResource) error { return nil }
func (m *mockStoreForScheduler) LoadGhosts(context.Context) ([]model.GhostResource, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) UpsertTenant(context.Context, string, string) (model.Tenant, error) {
	return model.Tenant{}, nil
}
func (m *mockStoreForScheduler) UpsertUser(context.Context, string, string, string, string) (model.User, error) {
	return model.User{}, nil
}
func (m *mockStoreForScheduler) SaveAccount(context.Context, model.Account) error { return nil }
func (m *mockStoreForScheduler) GetAccount(context.Context, string) (model.Account, error) {
	return model.Account{}, nil
}
func (m *mockStoreForScheduler) DeleteAccount(context.Context, string) error { return nil }
func (m *mockStoreForScheduler) UpdateAccountStatus(context.Context, string, string) error {
	return nil
}
func (m *mockStoreForScheduler) TryMarkAccountScanning(context.Context, string) (bool, error) {
	return false, nil
}
func (m *mockStoreForScheduler) SaveResources(context.Context, []model.ResourceRecord) error {
	return nil
}
func (m *mockStoreForScheduler) LoadResources(context.Context) ([]model.ResourceRecord, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) SaveSnapshot(context.Context, model.GhostSnapshot) error { return nil }
func (m *mockStoreForScheduler) ListSnapshots(context.Context, string) ([]model.GhostSnapshot, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) DismissGhost(context.Context, model.DismissAction) (int64, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) RevokeDismissal(context.Context, int64, string) error { return nil }
func (m *mockStoreForScheduler) ListActiveDismissals(context.Context, string) ([]model.DismissAction, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) ExpireSnoozes(context.Context) (int64, error) { return 0, nil }
func (m *mockStoreForScheduler) DeleteOldCostRecords(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) Close() error { return nil }

// captureQueue records enqueued jobs.
type captureQueue struct{ jobs []queue.ScanJob }

func (q *captureQueue) Enqueue(_ context.Context, job queue.ScanJob) error {
	q.jobs = append(q.jobs, job)
	return nil
}
func (q *captureQueue) Dequeue(ctx context.Context) (queue.ScanJob, error) {
	<-ctx.Done()
	return queue.ScanJob{}, ctx.Err()
}
func (q *captureQueue) Close() error { return nil }

func TestScanScheduledAccounts_NoAccounts(t *testing.T) {
	store := &mockStoreForScheduler{accounts: []model.Account{}}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q)
	if len(q.jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(q.jobs))
	}
}

func TestScanScheduledAccounts_ListError(t *testing.T) {
	store := &mockStoreForScheduler{listErr: errors.New("db error")}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q) // should not panic
}

func TestScanScheduledAccounts_SkipsAlreadyScanning(t *testing.T) {
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", TenantID: "tenant-1", ScanIntervalHours: 24, Status: "scanning",
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q)
	if len(q.jobs) != 0 {
		t.Fatalf("expected 0 jobs for scanning account, got %d", len(q.jobs))
	}
}

func TestScanScheduledAccounts_NeverScanned(t *testing.T) {
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", TenantID: "tenant-1", ScanIntervalHours: 24, LastScannedAt: nil, Status: "connected",
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q)
	if len(q.jobs) != 1 {
		t.Fatalf("expected 1 job for never-scanned account, got %d", len(q.jobs))
	}
}

func TestScanScheduledAccounts_Overdue(t *testing.T) {
	last := time.Now().Add(-30 * time.Hour)
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", TenantID: "tenant-1", ScanIntervalHours: 24, LastScannedAt: &last, Status: "connected",
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q)
	if len(q.jobs) != 1 {
		t.Fatalf("expected 1 job for overdue account, got %d", len(q.jobs))
	}
	if q.jobs[0].AccountID != "acc-1" {
		t.Errorf("expected account acc-1, got %s", q.jobs[0].AccountID)
	}
}

func TestScanScheduledAccounts_NotOverdue(t *testing.T) {
	last := time.Now().Add(-12 * time.Hour)
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", TenantID: "tenant-1", ScanIntervalHours: 24, LastScannedAt: &last, Status: "connected",
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q)
	if len(q.jobs) != 0 {
		t.Fatalf("expected 0 jobs for non-overdue account, got %d", len(q.jobs))
	}
}

func TestScanScheduledAccounts_ZeroInterval_AlwaysOverdue(t *testing.T) {
	now := time.Now()
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", TenantID: "tenant-1", ScanIntervalHours: 0, LastScannedAt: &now, Status: "connected",
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q)
	if len(q.jobs) != 1 {
		t.Fatalf("expected 1 job for zero-interval account, got %d", len(q.jobs))
	}
}
