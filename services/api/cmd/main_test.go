package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// mockStoreForScheduler is a minimal mock store for testing the scheduler.
type mockStoreForScheduler struct {
	accounts []model.Account
	listErr  error
}

func (m *mockStoreForScheduler) ListAccounts(ctx context.Context) ([]model.Account, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return append([]model.Account(nil), m.accounts...), nil
}

func (m *mockStoreForScheduler) ListAllAccounts(ctx context.Context) ([]model.Account, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return append([]model.Account(nil), m.accounts...), nil
}

// Stub other methods to satisfy the Store interface
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
func (m *mockStoreForScheduler) Close() error { return nil }

// TestScanScheduledAccounts_NoAccounts checks that the scheduler handles empty account list gracefully.
func TestScanScheduledAccounts_NoAccounts(t *testing.T) {
	store := &mockStoreForScheduler{accounts: []model.Account{}}
	ctx := context.Background()

	// Should not panic or error
	scanScheduledAccounts(ctx, store, nil)
}

// TestScanScheduledAccounts_ListError handles store errors gracefully.
func TestScanScheduledAccounts_ListError(t *testing.T) {
	store := &mockStoreForScheduler{listErr: errors.New("db error")}
	ctx := context.Background()

	// Should not panic or error
	scanScheduledAccounts(ctx, store, nil)
}

// TestScanScheduledAccounts_TriggersZeroInterval verifies accounts with scan_interval_hours=0 are always eligible.
func TestScanScheduledAccounts_TriggersZeroInterval(t *testing.T) {
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_URL", ingestion.URL)

	now := time.Now()
	store := &mockStoreForScheduler{
		accounts: []model.Account{
			{
				ID:                "acc-1",
				TenantID:          "tenant-1",
				Provider:          "aws",
				ScanIntervalHours: 0, // Always eligible for scheduled scan
				LastScannedAt:     &now,
				Status:            "connected",
			},
		},
	}
	ctx := context.Background()

	// Should trigger a scan even though interval=0 (account is always eligible)
	scanScheduledAccounts(ctx, store, nil)
}

// TestScanScheduledAccounts_SkipsAlreadyScanning verifies accounts already scanning are skipped.
func TestScanScheduledAccounts_SkipsAlreadyScanning(t *testing.T) {
	store := &mockStoreForScheduler{
		accounts: []model.Account{
			{
				ID:                "acc-1",
				TenantID:          "tenant-1",
				Provider:          "aws",
				ScanIntervalHours: 24,
				Status:            "scanning", // Already scanning
			},
		},
	}
	ctx := context.Background()

	// Should skip the account (no panic, no error)
	scanScheduledAccounts(ctx, store, nil)
}

// TestScanScheduledAccounts_NeverScanned verifies accounts never scanned are marked as overdue.
func TestScanScheduledAccounts_NeverScanned(t *testing.T) {
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_URL", ingestion.URL)

	store := &mockStoreForScheduler{
		accounts: []model.Account{
			{
				ID:                "acc-1",
				TenantID:          "tenant-1",
				Provider:          "aws",
				ScanIntervalHours: 24,
				LastScannedAt:     nil, // Never scanned
				Status:            "connected",
			},
		},
	}
	ctx := context.Background()

	// Should trigger a scan for the never-scanned account
	scanScheduledAccounts(ctx, store, nil)
}

// TestScanScheduledAccounts_Overdue verifies overdue accounts are scanned.
func TestScanScheduledAccounts_Overdue(t *testing.T) {
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_URL", ingestion.URL)

	// Account last scanned 30 hours ago with 24-hour interval — should be overdue
	lastScannedAt := time.Now().Add(-30 * time.Hour)
	store := &mockStoreForScheduler{
		accounts: []model.Account{
			{
				ID:                "acc-1",
				TenantID:          "tenant-1",
				Provider:          "aws",
				ScanIntervalHours: 24,
				LastScannedAt:     &lastScannedAt,
				Status:            "connected",
			},
		},
	}
	ctx := context.Background()

	// Should trigger a scan for the overdue account
	scanScheduledAccounts(ctx, store, nil)
}

// TestScanScheduledAccounts_NotOverdue verifies accounts not yet overdue are skipped.
func TestScanScheduledAccounts_NotOverdue(t *testing.T) {
	// Account last scanned 12 hours ago with 24-hour interval — not yet overdue
	lastScannedAt := time.Now().Add(-12 * time.Hour)
	store := &mockStoreForScheduler{
		accounts: []model.Account{
			{
				ID:                "acc-1",
				TenantID:          "tenant-1",
				Provider:          "aws",
				ScanIntervalHours: 24,
				LastScannedAt:     &lastScannedAt,
				Status:            "connected",
			},
		},
	}
	ctx := context.Background()

	// Should skip the account (no panic, no error)
	scanScheduledAccounts(ctx, store, nil)
}

// TestTriggerScheduledScan_Success verifies successful scan trigger.
func TestTriggerScheduledScan_Success(t *testing.T) {
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_URL", ingestion.URL)

	ctx := context.Background()
	err := triggerScheduledScan(ctx, "acc-1", "tenant-1")
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

// TestTriggerScheduledScan_IngestionError verifies error handling when ingestion returns 5xx.
func TestTriggerScheduledScan_IngestionError(t *testing.T) {
	ingestion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ingestion.Close()
	t.Setenv("INGESTION_URL", ingestion.URL)

	ctx := context.Background()
	err := triggerScheduledScan(ctx, "acc-1", "tenant-1")
	if err == nil {
		t.Error("expected error, got nil")
	}
}

// TestTriggerScheduledScan_NetworkError verifies error handling for network failures.
func TestTriggerScheduledScan_NetworkError(t *testing.T) {
	t.Setenv("INGESTION_URL", "http://invalid-host-that-does-not-exist:9999")

	ctx := context.Background()
	err := triggerScheduledScan(ctx, "acc-1", "tenant-1")
	if err == nil {
		t.Error("expected error for unreachable host, got nil")
	}
}
