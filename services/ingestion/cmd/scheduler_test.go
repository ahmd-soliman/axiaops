package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/shared/model"
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
func (m *mockStoreForScheduler) DeleteOldCostRecords(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) Close() error { return nil }

func TestScanScheduledAccounts_NoAccounts(t *testing.T) {
	store := &mockStoreForScheduler{accounts: []model.Account{}}
	scanScheduledAccounts(context.Background(), store)
}

func TestScanScheduledAccounts_ListError(t *testing.T) {
	store := &mockStoreForScheduler{listErr: errors.New("db error")}
	scanScheduledAccounts(context.Background(), store)
}

func TestScanScheduledAccounts_TriggersZeroInterval(t *testing.T) {
	triggered := make(chan struct{}, 1)
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		triggered <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_PORT", ingestion.URL[len("http://localhost:"):])

	now := time.Now()
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", TenantID: "tenant-1", Provider: "aws",
			ScanIntervalHours: 0, LastScannedAt: &now, Status: "connected",
		}},
	}
	scanScheduledAccounts(context.Background(), store)
	select {
	case <-triggered:
	case <-time.After(5 * time.Second):
		t.Fatal("expected ingestion POST within 5s")
	}
}

func TestScanScheduledAccounts_SkipsAlreadyScanning(t *testing.T) {
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", TenantID: "tenant-1", ScanIntervalHours: 24, Status: "scanning",
		}},
	}
	scanScheduledAccounts(context.Background(), store)
}

func TestScanScheduledAccounts_NeverScanned(t *testing.T) {
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_PORT", ingestion.URL[len("http://localhost:"):])

	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", TenantID: "tenant-1", ScanIntervalHours: 24, LastScannedAt: nil, Status: "connected",
		}},
	}
	scanScheduledAccounts(context.Background(), store)
}

func TestScanScheduledAccounts_Overdue(t *testing.T) {
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_PORT", ingestion.URL[len("http://localhost:"):])

	last := time.Now().Add(-30 * time.Hour)
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", TenantID: "tenant-1", ScanIntervalHours: 24, LastScannedAt: &last, Status: "connected",
		}},
	}
	scanScheduledAccounts(context.Background(), store)
}

func TestScanScheduledAccounts_NotOverdue(t *testing.T) {
	last := time.Now().Add(-12 * time.Hour)
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", TenantID: "tenant-1", ScanIntervalHours: 24, LastScannedAt: &last, Status: "connected",
		}},
	}
	scanScheduledAccounts(context.Background(), store)
}

func TestTriggerScan_Success(t *testing.T) {
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_PORT", ingestion.URL[len("http://localhost:"):])

	if err := triggerScan(context.Background(), "acc-1", "tenant-1"); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestTriggerScan_IngestionError(t *testing.T) {
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_PORT", ingestion.URL[len("http://localhost:"):])

	if err := triggerScan(context.Background(), "acc-1", "tenant-1"); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestTriggerScan_NetworkError(t *testing.T) {
	t.Setenv("INGESTION_PORT", "19999") // nothing listening there
	if err := triggerScan(context.Background(), "acc-1", "tenant-1"); err == nil {
		t.Error("expected error for unreachable host, got nil")
	}
}
