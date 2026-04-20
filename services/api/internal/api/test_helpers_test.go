// Package api_test — unified mock store for handler tests.
//
// This file provides a single, consistent Store mock implementation used by
// all handler tests (both simple unit tests and complex lifecycle tests).
// It combines stubStore's simplicity with trackingStore's instrumentation.
package api_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)


// MockStore is a unified in-memory Store implementation for testing.
// It provides:
//   - Real data storage (ghosts, accounts, snapshots, dismissals)
//   - Method call tracking (who called what, in what order)
//   - Per-method error injection (simulate failures)
//   - Context value capture (verify tenant propagation)
//   - Async signaling (wait for background goroutines)
//
// Use MockStore for all handler tests — simple tests use basic fields,
// complex tests use tracking/instrumentation.
type MockStore struct {
	mu sync.Mutex

	// ── Data Storage (used by all tests) ──
	ghosts     []model.GhostResource
	accounts   []model.Account
	snapshots  []model.GhostSnapshot
	resources  []model.ResourceRecord
	dismissals []model.DismissAction
	nextDismID int64

	// ── Call Tracking (optional, for lifecycle tests) ──
	callsToUpdateStatus []struct {
		accountID string
		status    string
	}
	capturedTenantIDs          []string
	lastListSnapshotsAccountID string

	// ── Error Injection (optional, for failure testing) ──
	errLoadGhosts         error
	errListAccounts       error
	errDeleteAccount      error
	errGetAccount         error
	errListSnapshots      error
	errTryMarkScanning    error
	errDismissGhost       error
	errListActiveDismiss  error

	// ── Account Status (for concurrency testing) ──
	accountScanning map[string]bool // account ID → is scanning

	// ── Signaling (for async testing) ──
	statusSignal chan struct{} // non-blocking send after each UpdateAccountStatus
}

// NewMockStore creates a new unified mock store.
// Simple tests can use it with default settings.
// Complex tests can configure tracking, error injection, and signals.
func NewMockStore() *MockStore {
	return &MockStore{
		accountScanning: make(map[string]bool),
	}
}

// ── Data Mutators (for test setup) ──

// WithGhosts pre-populates the mock with ghost records.
func (m *MockStore) WithGhosts(ghosts []model.GhostResource) *MockStore {
	m.mu.Lock()
	m.ghosts = ghosts
	m.mu.Unlock()
	return m
}

// WithAccounts pre-populates the mock with accounts.
func (m *MockStore) WithAccounts(accounts []model.Account) *MockStore {
	m.mu.Lock()
	m.accounts = accounts
	m.mu.Unlock()
	return m
}

// WithSnapshots pre-populates the mock with snapshots.
func (m *MockStore) WithSnapshots(snapshots []model.GhostSnapshot) *MockStore {
	m.mu.Lock()
	m.snapshots = snapshots
	m.mu.Unlock()
	return m
}

// ── Error Injection (for failure testing) ──

// WithLoadGhostsError makes LoadGhosts return an error.
func (m *MockStore) WithLoadGhostsError(err error) *MockStore {
	m.mu.Lock()
	m.errLoadGhosts = err
	m.mu.Unlock()
	return m
}

// WithListAccountsError makes ListAccounts return an error.
func (m *MockStore) WithListAccountsError(err error) *MockStore {
	m.mu.Lock()
	m.errListAccounts = err
	m.mu.Unlock()
	return m
}

// WithDeleteAccountError makes DeleteAccount return an error.
func (m *MockStore) WithDeleteAccountError(err error) *MockStore {
	m.mu.Lock()
	m.errDeleteAccount = err
	m.mu.Unlock()
	return m
}

// WithGetAccountError makes GetAccount return an error.
func (m *MockStore) WithGetAccountError(err error) *MockStore {
	m.mu.Lock()
	m.errGetAccount = err
	m.mu.Unlock()
	return m
}

// WithListSnapshotsError makes ListSnapshots return an error.
func (m *MockStore) WithListSnapshotsError(err error) *MockStore {
	m.mu.Lock()
	m.errListSnapshots = err
	m.mu.Unlock()
	return m
}

// WithTryMarkScanningError makes TryMarkAccountScanning return an error.
func (m *MockStore) WithTryMarkScanningError(err error) *MockStore {
	m.mu.Lock()
	m.errTryMarkScanning = err
	m.mu.Unlock()
	return m
}

// WithDismissGhostError makes DismissGhost return an error.
func (m *MockStore) WithDismissGhostError(err error) *MockStore {
	m.mu.Lock()
	m.errDismissGhost = err
	m.mu.Unlock()
	return m
}

// WithListActiveDismissalsError makes ListActiveDismissals return an error.
func (m *MockStore) WithListActiveDismissalsError(err error) *MockStore {
	m.mu.Lock()
	m.errListActiveDismiss = err
	m.mu.Unlock()
	return m
}

// WithDismissals pre-populates the mock with active dismissals.
func (m *MockStore) WithDismissals(dismissals []model.DismissAction) *MockStore {
	m.mu.Lock()
	m.dismissals = dismissals
	m.mu.Unlock()
	return m
}

// WithAccountAlreadyScanning pre-marks an account as scanning,
// causing TryMarkAccountScanning to return (false, nil) for that account.
func (m *MockStore) WithAccountAlreadyScanning(accountID string) *MockStore {
	m.mu.Lock()
	m.accountScanning[accountID] = true
	m.mu.Unlock()
	return m
}

// WithStatusSignal enables signal emission after UpdateAccountStatus calls.
// Use for waiting on async goroutines in lifecycle tests.
func (m *MockStore) WithStatusSignal(ch chan struct{}) *MockStore {
	m.mu.Lock()
	m.statusSignal = ch
	m.mu.Unlock()
	return m
}

// ── Call Tracking (for assertion testing) ──

// GetLastListSnapshotsAccountID returns the account_id from the most recent ListSnapshots call.
// Use to verify that query params are forwarded correctly.
func (m *MockStore) GetLastListSnapshotsAccountID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastListSnapshotsAccountID
}

// GetCapturedTenantIDs returns all tenant IDs captured during store calls.
// Use to verify tenant propagation through context.
func (m *MockStore) GetCapturedTenantIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.capturedTenantIDs...)
}

// GetStatusUpdateCalls returns all UpdateAccountStatus calls in order.
// Use to verify async status transitions.
func (m *MockStore) GetStatusUpdateCalls() []struct {
	accountID string
	status    string
} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]struct {
		accountID string
		status    string
	}(nil), m.callsToUpdateStatus...)
}

// ── Store Interface Implementation ──

func (m *MockStore) Save(_ context.Context, _ []model.CostRecord) (int64, error) {
	return 0, nil
}

func (m *MockStore) SaveGhosts(_ context.Context, g []model.GhostResource) error {
	m.mu.Lock()
	m.ghosts = g
	m.mu.Unlock()
	return nil
}

func (m *MockStore) LoadGhosts(ctx context.Context) ([]model.GhostResource, error) {
	m.mu.Lock()
	m.capturedTenantIDs = append(m.capturedTenantIDs, storage.TenantIDFromCtx(ctx))
	err := m.errLoadGhosts
	ghosts := append([]model.GhostResource(nil), m.ghosts...)
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return ghosts, nil
}

func (m *MockStore) UpsertTenant(_ context.Context, _, _ string) (model.Tenant, error) {
	return model.Tenant{}, nil
}

func (m *MockStore) UpsertUser(_ context.Context, _, _, _, _ string) (model.User, error) {
	return model.User{}, nil
}

func (m *MockStore) SaveAccount(_ context.Context, a model.Account) error {
	m.mu.Lock()
	m.accounts = append(m.accounts, a)
	m.mu.Unlock()
	return nil
}

func (m *MockStore) ListAccounts(ctx context.Context) ([]model.Account, error) {
	m.mu.Lock()
	m.capturedTenantIDs = append(m.capturedTenantIDs, storage.TenantIDFromCtx(ctx))
	err := m.errListAccounts
	accounts := append([]model.Account(nil), m.accounts...)
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return accounts, nil
}

func (m *MockStore) ListAllAccounts(_ context.Context) ([]model.Account, error) {
	m.mu.Lock()
	// Note: Not capturing tenant ID for ListAllAccounts, as it intentionally bypasses tenant isolation
	err := m.errListAccounts
	accounts := append([]model.Account(nil), m.accounts...)
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return accounts, nil
}

func (m *MockStore) GetAccount(_ context.Context, id string) (model.Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.errGetAccount != nil {
		return model.Account{}, m.errGetAccount
	}

	for _, a := range m.accounts {
		if a.ID == id {
			return a, nil
		}
	}
	return model.Account{}, errors.New("not found")
}

func (m *MockStore) DeleteAccount(_ context.Context, _ string) error {
	m.mu.Lock()
	err := m.errDeleteAccount
	m.mu.Unlock()
	return err
}

func (m *MockStore) UpdateAccountStatus(_ context.Context, id, status string) error {
	m.mu.Lock()
	m.callsToUpdateStatus = append(m.callsToUpdateStatus, struct {
		accountID string
		status    string
	}{id, status})
	ch := m.statusSignal
	m.mu.Unlock()

	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return nil
}

func (m *MockStore) TryMarkAccountScanning(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.errTryMarkScanning != nil {
		return false, m.errTryMarkScanning
	}

	if m.accountScanning[id] {
		return false, nil // Already scanning
	}
	m.accountScanning[id] = true
	return true, nil
}

func (m *MockStore) SaveResources(_ context.Context, r []model.ResourceRecord) error {
	m.mu.Lock()
	m.resources = r
	m.mu.Unlock()
	return nil
}

func (m *MockStore) LoadResources(_ context.Context) ([]model.ResourceRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]model.ResourceRecord(nil), m.resources...), nil
}

func (m *MockStore) SaveSnapshot(_ context.Context, s model.GhostSnapshot) error {
	m.mu.Lock()
	m.snapshots = append(m.snapshots, s)
	m.mu.Unlock()
	return nil
}

func (m *MockStore) ListSnapshots(_ context.Context, accountID string) ([]model.GhostSnapshot, error) {
	m.mu.Lock()
	m.lastListSnapshotsAccountID = accountID
	err := m.errListSnapshots
	snapshots := append([]model.GhostSnapshot(nil), m.snapshots...)
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

func (m *MockStore) SaveSnapshotServices(_ context.Context, _ []model.SnapshotService) error {
	return nil
}

func (m *MockStore) ListSnapshotsByService(_ context.Context, _, _, _ string) ([]model.GhostSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Return all snapshots — mock doesn't filter by service or resource type.
	return append([]model.GhostSnapshot(nil), m.snapshots...), nil
}

func (m *MockStore) ListTrendServices(_ context.Context) ([]string, error) {
	return []string{}, nil
}

func (m *MockStore) ListTrendResourceTypes(_ context.Context, _ string) ([]string, error) {
	return []string{}, nil
}

func (m *MockStore) Close() error {
	return nil
}

func (m *MockStore) DeleteOldCostRecords(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

func (m *MockStore) DismissGhost(_ context.Context, d model.DismissAction) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.errDismissGhost != nil {
		return 0, m.errDismissGhost
	}
	m.nextDismID++
	d.ID = m.nextDismID
	m.dismissals = append(m.dismissals, d)
	return d.ID, nil
}

func (m *MockStore) RevokeDismissal(_ context.Context, id int64, revokedBy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, d := range m.dismissals {
		if d.ID == id && d.RevokedAt == nil {
			now := time.Now()
			m.dismissals[i].RevokedAt = &now
			m.dismissals[i].RevokedBy = revokedBy
			return nil
		}
	}
	return errors.New("dismissal not found or already revoked")
}

func (m *MockStore) ListActiveDismissals(_ context.Context, accountID string) ([]model.DismissAction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.errListActiveDismiss != nil {
		return nil, m.errListActiveDismiss
	}
	var out []model.DismissAction
	for _, d := range m.dismissals {
		if d.RevokedAt != nil {
			continue
		}
		if accountID != "" && d.AccountID != accountID {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (m *MockStore) ExpireSnoozes(_ context.Context) (int64, error) {
	return 0, nil
}
