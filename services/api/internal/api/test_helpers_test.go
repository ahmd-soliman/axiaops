// Package api_test — unified mock store for handler tests.
//
// This file provides a single, consistent Store mock implementation used by
// all handler tests (both simple unit tests and complex lifecycle tests).
// It combines stubStore's simplicity with trackingStore's instrumentation.
package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"time"

	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// injectIdentity returns a context populated with the same unexported keys
// that Auth.Wrap / DevBypass set in production. The middleware package owns
// those keys, so we round-trip through DevBypass to reach them.
func injectIdentity(parent context.Context, organizationID, userID, email string) context.Context {
	src := httptest.NewRequest(http.MethodGet, "/seed", nil).WithContext(parent)
	var captured context.Context
	middleware.DevBypass(organizationID, userID, email, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = r.Context()
	})).ServeHTTP(httptest.NewRecorder(), src)
	return captured
}

// MockStore is a unified in-memory Store implementation for testing.
// It provides:
//   - Real data storage (zombies, accounts, snapshots, dismissals)
//   - Method call tracking (who called what, in what order)
//   - Per-method error injection (simulate failures)
//   - Context value capture (verify organization propagation)
//   - Async signaling (wait for background goroutines)
//
// Use MockStore for all handler tests — simple tests use basic fields,
// complex tests use tracking/instrumentation.
type MockStore struct {
	mu sync.Mutex

	// ── Data Storage (used by all tests) ──
	zombies     []model.ZombieResource
	accounts    []model.Account
	snapshots   []model.ZombieSnapshot
	resources   []model.ResourceRecord
	costs       []model.CostRecord
	dismissals  []model.DismissAction
	nextDismID  int64
	auditEvents []model.AuditEvent
	nextAuditID int64

	// ── Call Tracking (optional, for lifecycle tests) ──
	callsToUpdateStatus []struct {
		accountID string
		status    string
	}
	capturedOrganizationIDs    []string
	lastListSnapshotsAccountID string
	lastCostFilter             storage.CostFilter

	// ── Error Injection (optional, for failure testing) ──
	errLoadZombies       error
	errListAccounts      error
	errDeleteAccount     error
	errGetAccount        error
	errListSnapshots     error
	errTryMarkScanning   error
	errDismissZombie     error
	errListActiveDismiss error
	errListCostRecords   error
	errAuditWrite        error

	// ── Memberships / users (RBAC) ──
	memberships    []model.MembershipWithUser
	users          []model.User
	fixedRole      string // role returned by RoleOf when roleOverridden is true
	roleOverridden bool   // distinguishes "explicitly empty" from "not set"

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

// WithZombies pre-populates the mock with zombie records.
func (m *MockStore) WithZombies(zombies []model.ZombieResource) *MockStore {
	m.mu.Lock()
	m.zombies = zombies
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
func (m *MockStore) WithSnapshots(snapshots []model.ZombieSnapshot) *MockStore {
	m.mu.Lock()
	m.snapshots = snapshots
	m.mu.Unlock()
	return m
}

// WithCostRecords pre-populates the mock with cost records.
func (m *MockStore) WithCostRecords(costs []model.CostRecord) *MockStore {
	m.mu.Lock()
	m.costs = costs
	m.mu.Unlock()
	return m
}

// ── Error Injection (for failure testing) ──

// WithLoadZombiesError makes LoadZombies return an error.
func (m *MockStore) WithLoadZombiesError(err error) *MockStore {
	m.mu.Lock()
	m.errLoadZombies = err
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

// WithDismissZombieError makes DismissZombie return an error.
func (m *MockStore) WithDismissZombieError(err error) *MockStore {
	m.mu.Lock()
	m.errDismissZombie = err
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

// GetDismissals returns a snapshot of dismissals recorded by the mock.
func (m *MockStore) GetDismissals() []model.DismissAction {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]model.DismissAction(nil), m.dismissals...)
}

// WithAuditEvents pre-populates the mock with audit events.
func (m *MockStore) WithAuditEvents(events []model.AuditEvent) *MockStore {
	m.mu.Lock()
	m.auditEvents = events
	m.mu.Unlock()
	return m
}

// WithAuditWriteError makes AuditLogWrite return err.
func (m *MockStore) WithAuditWriteError(err error) *MockStore {
	m.mu.Lock()
	m.errAuditWrite = err
	m.mu.Unlock()
	return m
}

// GetAuditEvents returns a snapshot of audit events recorded by the mock.
func (m *MockStore) GetAuditEvents() []model.AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]model.AuditEvent(nil), m.auditEvents...)
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

// GetCapturedOrganizationIDs returns all organization IDs captured during store calls.
// Use to verify organization propagation through context.
func (m *MockStore) GetCapturedOrganizationIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.capturedOrganizationIDs...)
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

func (m *MockStore) SaveZombies(_ context.Context, z []model.ZombieResource) error {
	m.mu.Lock()
	m.zombies = z
	m.mu.Unlock()
	return nil
}

func (m *MockStore) LoadZombies(ctx context.Context) ([]model.ZombieResource, error) {
	m.mu.Lock()
	m.capturedOrganizationIDs = append(m.capturedOrganizationIDs, storage.OrganizationIDFromCtx(ctx))
	err := m.errLoadZombies
	zombies := append([]model.ZombieResource(nil), m.zombies...)
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return zombies, nil
}

func (m *MockStore) UpsertOrganization(_ context.Context, externalID, name string) (model.Organization, error) {
	return model.Organization{ID: externalID, Name: name}, nil
}

func (m *MockStore) EnsureOrganization(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *MockStore) UpsertUser(_ context.Context, _, _, _, _ string) (model.User, error) {
	return model.User{}, nil
}

func (m *MockStore) EnsureUser(_ context.Context, _ model.User) error {
	return nil
}

func (m *MockStore) AuditLogWrite(_ context.Context, e model.AuditEvent) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.errAuditWrite != nil {
		return 0, m.errAuditWrite
	}
	m.nextAuditID++
	e.ID = m.nextAuditID
	m.auditEvents = append(m.auditEvents, e)
	return e.ID, nil
}

func (m *MockStore) AuditLogList(_ context.Context, f model.AuditFilter) ([]model.AuditEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Mirror postgres ORDER BY created_at DESC, id DESC + cursor predicate
	// `(created_at, id) < cursor` so the export's pagination loop is actually
	// exercised. Other filter fields (UserID, ResourceType, time range) stay
	// no-ops — postgres integration tests cover those.
	out := append([]model.AuditEvent(nil), m.auditEvents...)
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if !f.Cursor.IsZero() {
		filtered := out[:0]
		for _, e := range out {
			if e.CreatedAt.Before(f.Cursor.CreatedAt) ||
				(e.CreatedAt.Equal(f.Cursor.CreatedAt) && e.ID < f.Cursor.ID) {
				filtered = append(filtered, e)
			}
		}
		out = filtered
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (m *MockStore) AuditLogAnonymiseUser(_ context.Context, userID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for i := range m.auditEvents {
		if m.auditEvents[i].UserID == userID {
			m.auditEvents[i].UserID = ""
			m.auditEvents[i].ActorEmail = "deleted-user"
			n++
		}
	}
	return n, nil
}

func (m *MockStore) SaveAccount(_ context.Context, a model.Account) error {
	m.mu.Lock()
	m.accounts = append(m.accounts, a)
	m.mu.Unlock()
	return nil
}

func (m *MockStore) ListAccounts(ctx context.Context) ([]model.Account, error) {
	m.mu.Lock()
	m.capturedOrganizationIDs = append(m.capturedOrganizationIDs, storage.OrganizationIDFromCtx(ctx))
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
	// Note: Not capturing organization ID for ListAllAccounts, as it intentionally bypasses organization isolation
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

func (m *MockStore) ListCostRecords(_ context.Context, filter storage.CostFilter) ([]model.CostRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastCostFilter = filter
	if m.errListCostRecords != nil {
		return nil, m.errListCostRecords
	}
	return append([]model.CostRecord(nil), m.costs...), nil
}

// WithListCostRecordsError makes ListCostRecords return an error.
func (m *MockStore) WithListCostRecordsError(err error) *MockStore {
	m.mu.Lock()
	m.errListCostRecords = err
	m.mu.Unlock()
	return m
}

// GetLastCostFilter returns the filter from the most recent ListCostRecords call.
func (m *MockStore) GetLastCostFilter() storage.CostFilter {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastCostFilter
}

func (m *MockStore) SaveSnapshot(_ context.Context, s model.ZombieSnapshot) error {
	m.mu.Lock()
	m.snapshots = append(m.snapshots, s)
	m.mu.Unlock()
	return nil
}

func (m *MockStore) ListSnapshots(_ context.Context, accountID string) ([]model.ZombieSnapshot, error) {
	m.mu.Lock()
	m.lastListSnapshotsAccountID = accountID
	err := m.errListSnapshots
	snapshots := append([]model.ZombieSnapshot(nil), m.snapshots...)
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

func (m *MockStore) SaveSnapshotServices(_ context.Context, _ []model.SnapshotService) error {
	return nil
}

func (m *MockStore) ListSnapshotsByService(_ context.Context, _, _, _ string) ([]model.ZombieSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Return all snapshots — mock doesn't filter by service or resource type.
	return append([]model.ZombieSnapshot(nil), m.snapshots...), nil
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

func (m *MockStore) DismissZombie(_ context.Context, d model.DismissAction) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.errDismissZombie != nil {
		return 0, m.errDismissZombie
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

// ── Memberships (RBAC Phase 1) ──────────────────────────────────────────────
//
// Backed by an in-memory slice with the same field shape as the postgres
// implementation. Behaviours used by the handler tests (last-owner guard,
// duplicate-membership rejection, transfer-ownership atomicity) live here so
// the tests don't depend on a real DB.

func (m *MockStore) WithRole(role string) *MockStore {
	m.mu.Lock()
	m.fixedRole = role
	m.roleOverridden = true
	m.mu.Unlock()
	return m
}

func (m *MockStore) WithMemberships(ms []model.MembershipWithUser) *MockStore {
	m.mu.Lock()
	m.memberships = ms
	m.mu.Unlock()
	return m
}

func (m *MockStore) WithUsers(us []model.User) *MockStore {
	m.mu.Lock()
	m.users = us
	m.mu.Unlock()
	return m
}

func (m *MockStore) RoleOf(_ context.Context, _, _ string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.roleOverridden {
		return m.fixedRole, nil
	}
	return "owner", nil // tests default to owner so existing handler tests keep passing
}

func (m *MockStore) ListMemberships(_ context.Context) ([]model.MembershipWithUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]model.MembershipWithUser, len(m.memberships))
	copy(out, m.memberships)
	return out, nil
}

func (m *MockStore) GetMembership(_ context.Context, id string) (model.Membership, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mu := range m.memberships {
		if mu.ID == id {
			return mu.Membership, nil
		}
	}
	return model.Membership{}, storage.ErrMembershipNotFound
}

func (m *MockStore) SaveMembership(_ context.Context, mb model.Membership) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.memberships {
		if existing.OrganizationID == mb.OrganizationID && existing.UserID == mb.UserID {
			return storage.ErrMembershipExists
		}
	}
	m.memberships = append(m.memberships, model.MembershipWithUser{Membership: mb})
	return nil
}

func (m *MockStore) UpdateMembershipRole(_ context.Context, id, newRole string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := -1
	for i, mu := range m.memberships {
		if mu.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return storage.ErrMembershipNotFound
	}
	if m.memberships[idx].Role == "owner" && newRole != "owner" && m.countOwnersLocked(m.memberships[idx].OrganizationID) <= 1 {
		return storage.ErrLastOwner
	}
	m.memberships[idx].Role = newRole
	m.memberships[idx].UpdatedAt = time.Now().UTC()
	return nil
}

func (m *MockStore) DeleteMembership(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := -1
	for i, mu := range m.memberships {
		if mu.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return storage.ErrMembershipNotFound
	}
	if m.memberships[idx].Role == "owner" && m.countOwnersLocked(m.memberships[idx].OrganizationID) <= 1 {
		return storage.ErrLastOwner
	}
	m.memberships = append(m.memberships[:idx], m.memberships[idx+1:]...)
	return nil
}

func (m *MockStore) TransferOwnership(ctx context.Context, toUserID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	organizationID := storage.OrganizationIDFromCtx(ctx)
	targetIdx := -1
	for i, mu := range m.memberships {
		if mu.OrganizationID == organizationID && mu.UserID == toUserID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return storage.ErrMembershipNotFound
	}
	now := time.Now().UTC()
	for i := range m.memberships {
		if m.memberships[i].OrganizationID == organizationID && m.memberships[i].Role == "owner" {
			m.memberships[i].Role = "admin"
			m.memberships[i].UpdatedAt = now
		}
	}
	m.memberships[targetIdx].Role = "owner"
	m.memberships[targetIdx].UpdatedAt = now
	return nil
}

func (m *MockStore) EnsureFirstMembership(_ context.Context, organizationID, userID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mu := range m.memberships {
		if mu.OrganizationID == organizationID {
			return false, nil
		}
	}
	m.memberships = append(m.memberships, model.MembershipWithUser{
		Membership: model.Membership{
			ID:             "first-" + userID,
			OrganizationID: organizationID,
			UserID:         userID,
			Role:           "owner",
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		},
	})
	return true, nil
}

func (m *MockStore) EnsureDevMembership(_ context.Context, organizationID, userID, role string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, mu := range m.memberships {
		if mu.OrganizationID == organizationID && mu.UserID == userID {
			m.memberships[i].Role = role
			m.memberships[i].UpdatedAt = time.Now().UTC()
			return nil
		}
	}
	m.memberships = append(m.memberships, model.MembershipWithUser{
		Membership: model.Membership{
			ID:             "dev-" + userID,
			OrganizationID: organizationID,
			UserID:         userID,
			Role:           role,
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		},
	})
	return nil
}

func (m *MockStore) GetUserByEmail(_ context.Context, email string) (model.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return model.User{}, storage.ErrUserNotFound
}

func (m *MockStore) DeleteUser(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Sole-owner guard mirrors the postgres implementation.
	for _, mb := range m.memberships {
		if mb.UserID != userID || mb.Role != "owner" {
			continue
		}
		others := 0
		for _, mb2 := range m.memberships {
			if mb2.OrganizationID == mb.OrganizationID && mb2.Role == "owner" && mb2.UserID != userID {
				others++
			}
		}
		if others == 0 {
			return storage.ErrLastOwner
		}
	}

	// Anonymise audit footprint across all organizations.
	for i := range m.auditEvents {
		if m.auditEvents[i].UserID == userID {
			m.auditEvents[i].UserID = ""
			m.auditEvents[i].ActorEmail = "deleted-user"
		}
	}

	// Cascade memberships and the user row.
	keptMemberships := m.memberships[:0]
	for _, mb := range m.memberships {
		if mb.UserID != userID {
			keptMemberships = append(keptMemberships, mb)
		}
	}
	m.memberships = keptMemberships

	keptUsers := m.users[:0]
	for _, u := range m.users {
		if u.ID != userID {
			keptUsers = append(keptUsers, u)
		}
	}
	m.users = keptUsers
	return nil
}

func (m *MockStore) DeleteOrganizationCascade(_ context.Context, organizationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Identify users whose primary organization is this one — they're going away.
	purgedUsers := map[string]bool{}
	for _, u := range m.users {
		if u.OrganizationID == organizationID {
			purgedUsers[u.ID] = true
		}
	}

	// Anonymise audit entries in OTHER organizations for users about to be deleted.
	for i := range m.auditEvents {
		if m.auditEvents[i].OrganizationID != organizationID && purgedUsers[m.auditEvents[i].UserID] {
			m.auditEvents[i].UserID = ""
			m.auditEvents[i].ActorEmail = "deleted-user"
		}
	}

	// Drop this organization's audit, accounts, dismissals.
	keptAudit := m.auditEvents[:0]
	for _, e := range m.auditEvents {
		if e.OrganizationID != organizationID {
			keptAudit = append(keptAudit, e)
		}
	}
	m.auditEvents = keptAudit

	keptAccounts := m.accounts[:0]
	for _, a := range m.accounts {
		if a.OrganizationID != organizationID {
			keptAccounts = append(keptAccounts, a)
		}
	}
	m.accounts = keptAccounts

	// Dismissals in the mock aren't tagged with organization_id (the model omits it
	// — RLS supplies isolation in the real DB). Clear the lot; multi-organization
	// dismissal coverage lives in the postgres integration test instead.
	m.dismissals = nil

	// Users whose primary organization is this one + their memberships everywhere.
	keptUsers := m.users[:0]
	for _, u := range m.users {
		if !purgedUsers[u.ID] {
			keptUsers = append(keptUsers, u)
		}
	}
	m.users = keptUsers

	keptMemberships := m.memberships[:0]
	for _, mb := range m.memberships {
		if mb.OrganizationID != organizationID && !purgedUsers[mb.UserID] {
			keptMemberships = append(keptMemberships, mb)
		}
	}
	m.memberships = keptMemberships
	return nil
}

func (m *MockStore) countOwnersLocked(organizationID string) int {
	n := 0
	for _, mu := range m.memberships {
		if mu.OrganizationID == organizationID && mu.Role == "owner" {
			n++
		}
	}
	return n
}
