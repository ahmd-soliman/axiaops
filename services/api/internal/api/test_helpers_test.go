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
	"strings"
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
	// "" for the name slot — DevBypass seeds the display name as empty for
	// most tests (covered by the dev user in cmd/main.go).
	middleware.DevBypass(organizationID, userID, email, "", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
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
	zombies                []model.ZombieResource
	accounts               []model.Account
	snapshots              []model.ZombieSnapshot
	resources              []model.ResourceRecord
	costs                  []model.CostRecord
	dismissals             []model.DismissAction
	nextDismID             int64
	auditEvents            []model.AuditEvent
	nextAuditID            int64
	organizationName       string
	channels               []model.NotificationChannel
	listEnabledChannelsErr error
	dispatches             []model.NotificationDispatch

	// UserMembershipsByUser keys ListUserMemberships responses by user_id
	// so a single test can drive multiple distinct users (e.g. the
	// /v1/auth/select-org and /v1/auth/switch-org handlers landing in
	// later B1.5 slices, which call ListUserMemberships for the caller's
	// uid AND must reject reads keyed on someone else's). Tests that don't
	// care about per-user routing may set UserMemberships instead — that
	// flat slice is returned as a fallback when the map lookup misses.
	UserMembershipsByUser map[string][]model.MembershipWithOrganization
	UserMemberships       []model.MembershipWithOrganization

	// ── Call Tracking (optional, for lifecycle tests) ──
	callsToUpdateStatus []struct {
		accountID string
		status    string
	}
	lastSetAccountError struct {
		accountID string
		message   string
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

	// ── Pending invitations (Phase 2 invitations) ──
	pendingInvitations []model.PendingInvitation

	// ── SSO connections (Phase B2 — used by Tasks.md 2.7.20 enforcement
	// hint resolver). Empty slice (default) → ListSSOConnections returns
	// nil — same posture as an org with no SSO configured.
	ssoConnections []model.SSOConnection

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

func (m *MockStore) Save(_ context.Context, _ []model.CostRecord) (int64, int64, error) {
	return 0, 0, nil
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.organizationName == "" {
		m.organizationName = name
	}
	return model.Organization{ID: externalID, Name: m.organizationName}, nil
}

func (m *MockStore) GetOrganizationByID(_ context.Context, id string) (model.Organization, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return model.Organization{ID: id, Name: m.organizationName}, nil
}

func (m *MockStore) ListUserMemberships(_ context.Context, userID string) ([]model.MembershipWithOrganization, error) {
	if rows, ok := m.UserMembershipsByUser[userID]; ok {
		return rows, nil
	}
	return m.UserMemberships, nil
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
	defer m.mu.Unlock()
	for i := range m.accounts {
		if m.accounts[i].ID == a.ID {
			m.accounts[i] = a
			return nil
		}
	}
	m.accounts = append(m.accounts, a)
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

// AccountByID is a test-only accessor that returns the latest persisted state
// of an account from the in-memory store. Used by role-verify tests that need
// to assert SaveAccount mutations after a handler returns.
func (m *MockStore) AccountByID(id string) *model.Account {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.accounts {
		if m.accounts[i].ID == id {
			a := m.accounts[i]
			return &a
		}
	}
	return nil
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

func (m *MockStore) SetAccountError(_ context.Context, id, message string) error {
	m.mu.Lock()
	m.callsToUpdateStatus = append(m.callsToUpdateStatus, struct {
		accountID string
		status    string
	}{id, "error"})
	m.lastSetAccountError = struct {
		accountID string
		message   string
	}{id, message}
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

// IsAccountScanning is a test-only accessor for the in-memory scan-lock map.
// Used to assert that handlers short-circuited *before* calling
// TryMarkAccountScanning (e.g. the pending_role_setup early return).
func (m *MockStore) IsAccountScanning(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.accountScanning[id]
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

func (m *MockStore) DeleteOldNotificationDispatches(_ context.Context, _ time.Time) (int64, error) {
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

func (m *MockStore) GetUserByID(_ context.Context, id string) (model.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return model.User{}, storage.ErrUserNotFound
}

// GetUserSSOConnectionID is the single-purpose lookup the SSO RP-Initiated
// Logout resolver uses. Tests in this package don't exercise SSO logout, so
// the mock returns "" (no SSO connection) for any matched user — same as a
// native-only user with NULL sso_connection_id in the real schema.
func (m *MockStore) GetUserSSOConnectionID(_ context.Context, userID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == userID {
			return "", nil
		}
	}
	return "", storage.ErrUserNotFound
}

// SetUserSSOConnection is the write counterpart called by the OIDC callback.
// Tests in this package don't drive the SSO callback so the mock is a
// minimal "verify user exists, swallow the write" stub.
func (m *MockStore) SetUserSSOConnection(_ context.Context, userID, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.ID == userID {
			return nil
		}
	}
	return storage.ErrUserNotFound
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

// ── Organization rename + onboarding (Phase 2) ───────────────────────────────

func (m *MockStore) RenameOrganization(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.organizationName = name
	return nil
}

func (m *MockStore) MarkOnboardingComplete(_ context.Context) (time.Time, error) {
	return time.Now().UTC(), nil
}

// WithChannels seeds notification channels for tests.
func (m *MockStore) WithChannels(chs []model.NotificationChannel) *MockStore {
	m.channels = append(m.channels, chs...)
	return m
}

// WithOrgName seeds the organization display name GetOrganizationByID returns.
func (m *MockStore) WithOrgName(name string) *MockStore {
	m.organizationName = name
	return m
}

// WithListEnabledChannelsError makes ListEnabledNotificationChannels fail, for
// testing the invite-email error path.
func (m *MockStore) WithListEnabledChannelsError(err error) *MockStore {
	m.listEnabledChannelsErr = err
	return m
}

// ── Pending invitations (Phase 2) ────────────────────────────────────────────

// WithPendingInvitations seeds the mock with pending invitation rows for tests.
func (m *MockStore) WithPendingInvitations(invs []model.PendingInvitation) *MockStore {
	m.mu.Lock()
	m.pendingInvitations = append([]model.PendingInvitation(nil), invs...)
	m.mu.Unlock()
	return m
}

func (m *MockStore) CreatePendingInvitation(_ context.Context, inv model.PendingInvitation) (model.PendingInvitation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	emailLower := strings.ToLower(inv.Email)

	// Pre-check: existing user / membership in this org.
	for _, u := range m.users {
		if u.OrganizationID != inv.OrganizationID || strings.ToLower(u.Email) != emailLower {
			continue
		}
		for _, mb := range m.memberships {
			if mb.OrganizationID == inv.OrganizationID && mb.UserID == u.ID {
				return model.PendingInvitation{}, false, storage.ErrInvitationAlreadyMember
			}
		}
		return model.PendingInvitation{}, false, storage.ErrUserExistsNoMembership
	}

	now := time.Now().UTC()
	if inv.ExpiresAt.IsZero() {
		inv.ExpiresAt = now.Add(14 * 24 * time.Hour)
	}

	// Upsert against pending row.
	for i, existing := range m.pendingInvitations {
		if existing.OrganizationID != inv.OrganizationID || strings.ToLower(existing.Email) != emailLower {
			continue
		}
		if existing.Status != model.InvitationStatusPending {
			continue
		}
		// Update existing pending row in place.
		m.pendingInvitations[i].Role = inv.Role
		m.pendingInvitations[i].ExpiresAt = inv.ExpiresAt
		m.pendingInvitations[i].InvitedByUserID = inv.InvitedByUserID
		m.pendingInvitations[i].InvitedByEmail = inv.InvitedByEmail
		m.pendingInvitations[i].UpdatedAt = now
		return m.pendingInvitations[i], false, nil
	}

	if inv.ID == "" {
		inv.ID = "inv-" + emailLower + "-" + inv.OrganizationID
	}
	inv.Status = model.InvitationStatusPending
	inv.CreatedAt = now
	inv.UpdatedAt = now
	m.pendingInvitations = append(m.pendingInvitations, inv)
	return inv, true, nil
}

func (m *MockStore) ListPendingInvitations(ctx context.Context, status string) ([]model.PendingInvitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status == "" {
		status = model.InvitationStatusPending
	}
	organizationID := storage.OrganizationIDFromCtx(ctx)
	out := []model.PendingInvitation{}
	for _, inv := range m.pendingInvitations {
		if inv.OrganizationID != organizationID {
			continue
		}
		if inv.Status != status {
			continue
		}
		out = append(out, inv)
	}
	return out, nil
}

func (m *MockStore) GetPendingInvitation(ctx context.Context, id string) (model.PendingInvitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	organizationID := storage.OrganizationIDFromCtx(ctx)
	for _, inv := range m.pendingInvitations {
		if inv.ID == id && inv.OrganizationID == organizationID {
			return inv, nil
		}
	}
	return model.PendingInvitation{}, storage.ErrInvitationNotFound
}

func (m *MockStore) RevokePendingInvitation(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	organizationID := storage.OrganizationIDFromCtx(ctx)
	for i := range m.pendingInvitations {
		if m.pendingInvitations[i].ID != id || m.pendingInvitations[i].OrganizationID != organizationID {
			continue
		}
		if m.pendingInvitations[i].Status != model.InvitationStatusPending {
			return storage.ErrInvitationNotPending
		}
		m.pendingInvitations[i].Status = model.InvitationStatusRevoked
		m.pendingInvitations[i].UpdatedAt = time.Now().UTC()
		return nil
	}
	return storage.ErrInvitationNotFound
}

func (m *MockStore) RedeemPendingInvitation(_ context.Context, organizationID, userID, email string) (bool, error) {
	if organizationID == "" || userID == "" || email == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	emailLower := strings.ToLower(email)
	for i, inv := range m.pendingInvitations {
		if inv.OrganizationID != organizationID || strings.ToLower(inv.Email) != emailLower {
			continue
		}
		if inv.Status != model.InvitationStatusPending {
			continue
		}
		if !inv.ExpiresAt.IsZero() && time.Now().UTC().After(inv.ExpiresAt) {
			continue
		}
		// Insert membership if not already present.
		alreadyMember := false
		for _, mb := range m.memberships {
			if mb.OrganizationID == organizationID && mb.UserID == userID {
				alreadyMember = true
				break
			}
		}
		if !alreadyMember {
			m.memberships = append(m.memberships, model.MembershipWithUser{
				Membership: model.Membership{
					ID:             "mb-" + userID + "-" + organizationID,
					OrganizationID: organizationID,
					UserID:         userID,
					Role:           inv.Role,
					InvitedBy:      inv.InvitedByUserID,
					CreatedAt:      time.Now().UTC(),
					UpdatedAt:      time.Now().UTC(),
				},
			})
		}
		// Delete the pending row.
		m.pendingInvitations = append(m.pendingInvitations[:i], m.pendingInvitations[i+1:]...)
		return !alreadyMember, nil
	}
	return false, nil
}

func (m *MockStore) ExpirePendingInvitations(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var n int64
	for i := range m.pendingInvitations {
		if m.pendingInvitations[i].Status != model.InvitationStatusPending {
			continue
		}
		if m.pendingInvitations[i].ExpiresAt.After(now) {
			continue
		}
		m.pendingInvitations[i].Status = model.InvitationStatusExpired
		m.pendingInvitations[i].UpdatedAt = now
		n++
	}
	return n, nil
}

// ── Phase B1 native-auth method stubs ───────────────────────────────────────
// MockStore implementations for the storage.NativeAuthStore methods. The
// existing api-handler tests never exercise the native-auth paths (those are
// covered by services/api/internal/auth/*_test.go and the
// services/shared/storage/postgres integration tests), so each stub returns
// a not-implemented error or zero value. When a test does need real
// behaviour, override the relevant method via a per-test wrapper struct.

func (m *MockStore) CreateUserWithPassword(context.Context, model.User) (model.User, error) {
	return model.User{}, errors.New("MockStore.CreateUserWithPassword not implemented")
}

func (m *MockStore) UpdateUserPassword(context.Context, string, string) error {
	return errors.New("MockStore.UpdateUserPassword not implemented")
}

func (m *MockStore) UpdateUserName(_ context.Context, userID, newName string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, u := range m.users {
		if u.ID == userID {
			old := u.Name
			m.users[i].Name = newName
			return old, nil
		}
	}
	return "", storage.ErrUserNotFound
}

func (m *MockStore) CountOrganizations(context.Context) (int64, error) {
	return 0, nil
}

func (m *MockStore) LookupMembership(context.Context, string, string) (string, string, string, error) {
	return "", "", "", nil
}

func (m *MockStore) LookupUserByEmail(context.Context, string) (model.User, []model.Membership, error) {
	return model.User{}, nil, storage.ErrUserNotFound
}

func (m *MockStore) CreateSession(context.Context, model.Session) (model.Session, error) {
	return model.Session{}, errors.New("MockStore.CreateSession not implemented")
}

func (m *MockStore) CreateSessionEnforcingCap(context.Context, model.Session, int) (model.Session, []string, error) {
	return model.Session{}, nil, errors.New("MockStore.CreateSessionEnforcingCap not implemented")
}

func (m *MockStore) GetSessionByTokenHash(context.Context, string) (model.Session, error) {
	return model.Session{}, storage.ErrSessionNotFound
}

func (m *MockStore) TouchSessionLastSeen(context.Context, string) error { return nil }

func (m *MockStore) RevokeSession(context.Context, string) error { return nil }

func (m *MockStore) RevokeUserSessions(context.Context, string) ([]string, error) {
	return nil, nil
}

func (m *MockStore) ListUserSessionTokenHashes(context.Context, string) ([]string, error) {
	return nil, nil
}

func (m *MockStore) CountSessionsForUser(context.Context, string) (int, error) {
	return 0, nil
}

func (m *MockStore) SweepExpiredSessions(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (m *MockStore) CreatePasswordReset(context.Context, string, string, string, string, string, time.Time) error {
	return errors.New("MockStore.CreatePasswordReset not implemented")
}

func (m *MockStore) RedeemPasswordReset(context.Context, string, string) (string, string, error) {
	return "", "", storage.ErrPasswordResetNotFound
}

func (m *MockStore) CreateBootstrapState(context.Context, string, string) (bool, error) {
	return false, storage.ErrBootstrapAlreadyDone
}

func (m *MockStore) GetBootstrapState(context.Context) (string, string, error) {
	return "", "", storage.ErrBootstrapAlreadyDone
}

func (m *MockStore) ConsumeBootstrapState(context.Context, storage.BootstrapConsume) (storage.BootstrapResult, error) {
	return storage.BootstrapResult{}, storage.ErrBootstrapAlreadyDone
}

func (m *MockStore) CreateNativeInvitation(ctx context.Context, inv model.PendingInvitation) (model.PendingInvitation, bool, error) {
	// Delegates to CreatePendingInvitation (same upsert + sentinel-check
	// shape); inv.InviteTokenHash flows through to the persisted row.
	return m.CreatePendingInvitation(ctx, inv)
}

func (m *MockStore) RedeemNativeInvitation(context.Context, storage.NativeInviteRedeem) (model.User, model.Membership, error) {
	return model.User{}, model.Membership{}, storage.ErrInvitationNotFound
}

func (m *MockStore) LookupInvitationByToken(context.Context, string) (storage.PeekedInvitation, error) {
	return storage.PeekedInvitation{}, storage.ErrInvitationNotFound
}

func (m *MockStore) GetMembershipByOrgUser(context.Context, string, string) (model.Membership, error) {
	return model.Membership{}, storage.ErrMembershipNotFound
}

// ── Phase B2 SSO — stubs (fail-loud so tests using them must override) ──────

func (m *MockStore) CreateSSOConnection(context.Context, model.SSOConnection) (model.SSOConnection, error) {
	return model.SSOConnection{}, errors.New("MockStore.CreateSSOConnection not implemented")
}
func (m *MockStore) GetSSOConnection(context.Context, string) (model.SSOConnection, error) {
	return model.SSOConnection{}, storage.ErrSSOConnectionNotFound
}
func (m *MockStore) GetSSOConnectionByID(context.Context, string) (model.SSOConnection, error) {
	return model.SSOConnection{}, storage.ErrSSOConnectionNotFound
}
func (m *MockStore) ListSSOConnections(context.Context) ([]model.SSOConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.ssoConnections) == 0 {
		return nil, nil
	}
	out := make([]model.SSOConnection, len(m.ssoConnections))
	copy(out, m.ssoConnections)
	return out, nil
}

// WithSSOConnections plants SSO connections returned by ListSSOConnections.
// Used by the Tasks.md 2.7.20 enforcement-hint resolver tests; pass an
// empty slice (default) for "no SSO configured".
func (m *MockStore) WithSSOConnections(conns []model.SSOConnection) *MockStore {
	m.mu.Lock()
	m.ssoConnections = conns
	m.mu.Unlock()
	return m
}
func (m *MockStore) UpdateSSOConnection(context.Context, model.SSOConnection) error {
	return errors.New("MockStore.UpdateSSOConnection not implemented")
}
func (m *MockStore) DeleteSSOConnection(context.Context, string) error {
	return storage.ErrSSOConnectionNotFound
}
func (m *MockStore) CreateSSODomain(context.Context, model.SSODomain) (model.SSODomain, error) {
	return model.SSODomain{}, errors.New("MockStore.CreateSSODomain not implemented")
}
func (m *MockStore) GetSSODomain(context.Context, string) (model.SSODomain, error) {
	return model.SSODomain{}, storage.ErrSSODomainNotFound
}
func (m *MockStore) GetVerifiedSSODomainByName(context.Context, string) (model.SSODomain, error) {
	return model.SSODomain{}, storage.ErrSSODomainNotFound
}
func (m *MockStore) ListSSODomains(context.Context) ([]model.SSODomain, error) {
	return nil, errors.New("MockStore.ListSSODomains not implemented")
}
func (m *MockStore) UpdateSSODomainStatus(context.Context, string, string, time.Time, time.Time) error {
	return errors.New("MockStore.UpdateSSODomainStatus not implemented")
}
func (m *MockStore) DeleteSSODomain(context.Context, string) error {
	return storage.ErrSSODomainNotFound
}
func (m *MockStore) SweepStaleSSODomains(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (m *MockStore) ListSSOGroupMappings(context.Context, string) ([]model.SSOGroupMapping, error) {
	return nil, errors.New("MockStore.ListSSOGroupMappings not implemented")
}
func (m *MockStore) ReplaceSSOGroupMappings(context.Context, string, []model.SSOGroupMapping) error {
	return errors.New("MockStore.ReplaceSSOGroupMappings not implemented")
}

// ── Notification channels (in-memory, used by channel handler tests) ──

func (m *MockStore) SaveNotificationChannel(_ context.Context, ch model.NotificationChannel) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.channels {
		if m.channels[i].ID == ch.ID {
			ch.Kind = m.channels[i].Kind // kind is immutable after insert, mirroring the DB upsert
			m.channels[i] = ch
			return nil
		}
	}
	m.channels = append(m.channels, ch)
	return nil
}

func (m *MockStore) ListNotificationChannels(_ context.Context) ([]model.NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]model.NotificationChannel(nil), m.channels...), nil
}

func (m *MockStore) ListEnabledNotificationChannels(_ context.Context) ([]model.NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listEnabledChannelsErr != nil {
		return nil, m.listEnabledChannelsErr
	}
	var out []model.NotificationChannel
	for _, ch := range m.channels {
		if ch.Enabled {
			out = append(out, ch)
		}
	}
	return out, nil
}

func (m *MockStore) GetNotificationChannel(_ context.Context, id string) (model.NotificationChannel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.channels {
		if ch.ID == id {
			return ch, nil
		}
	}
	return model.NotificationChannel{}, storage.ErrChannelNotFound
}

func (m *MockStore) DeleteNotificationChannel(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.channels {
		if m.channels[i].ID == id {
			m.channels = append(m.channels[:i], m.channels[i+1:]...)
			return nil
		}
	}
	return storage.ErrChannelNotFound
}

func (m *MockStore) SaveNotificationDispatch(_ context.Context, d model.NotificationDispatch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dispatches = append(m.dispatches, d)
	return nil
}

func (m *MockStore) ListNotificationDispatches(_ context.Context, channelID string, limit int) ([]model.NotificationDispatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	var out []model.NotificationDispatch
	// Newest-first, matching the postgres impl's ORDER BY created_at DESC.
	for i := len(m.dispatches) - 1; i >= 0 && len(out) < limit; i-- {
		if m.dispatches[i].ChannelID == channelID {
			out = append(out, m.dispatches[i])
		}
	}
	return out, nil
}

// MockStore implementations for the storage.StaffStore methods (platform admin
// plane). The tenant api-handler tests never exercise the staff plane (it is a
// separate binary + handlers in internal/staff), so each stub returns a
// not-implemented error or zero value. Real staff behaviour is covered by
// services/api/internal/staff/*_test.go and the postgres integration tests.

func (m *MockStore) CreateStaffUser(context.Context, storage.CreateStaffUserInput) (model.StaffUser, error) {
	return model.StaffUser{}, errors.New("MockStore.CreateStaffUser not implemented")
}

func (m *MockStore) LookupStaffUserByEmail(context.Context, string) (model.StaffUser, []model.StaffRoleGrant, error) {
	return model.StaffUser{}, nil, storage.ErrStaffNotFound
}

func (m *MockStore) GetStaffUserByID(context.Context, string) (model.StaffUser, []model.StaffRoleGrant, error) {
	return model.StaffUser{}, nil, storage.ErrStaffNotFound
}

func (m *MockStore) ListStaffUsers(context.Context) ([]model.StaffUser, [][]model.StaffRoleGrant, error) {
	return nil, nil, nil
}

func (m *MockStore) GrantStaffRole(context.Context, string, model.StaffRole, string) error {
	return errors.New("MockStore.GrantStaffRole not implemented")
}

func (m *MockStore) RevokeStaffRole(context.Context, string, model.StaffRole) error {
	return errors.New("MockStore.RevokeStaffRole not implemented")
}

func (m *MockStore) CountStaffWithRole(context.Context, model.StaffRole) (int, error) {
	return 0, nil
}

func (m *MockStore) ListAllOrganizations(context.Context) ([]model.Organization, error) {
	return nil, nil
}

func (m *MockStore) StaffTenantSummary(context.Context, string) (model.StaffTenantSummary, error) {
	return model.StaffTenantSummary{}, storage.ErrOrganizationNotFound
}

// EntitlementStore stubs (SaaS per-tenant entitlement, dormant Phase 2A
// scaffold) — no api handler consults entitlement yet; these satisfy the
// widened storage.Store interface.
func (m *MockStore) GetEntitlement(context.Context, string) (model.Entitlement, error) {
	return model.Entitlement{}, storage.ErrEntitlementNotFound
}
func (m *MockStore) UpsertEntitlement(context.Context, model.Entitlement) error {
	return nil
}
func (m *MockStore) ListAllEntitlements(context.Context) ([]model.Entitlement, error) {
	return nil, nil
}
