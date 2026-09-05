package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"axiaops.io/shared/model"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
)

// mockStoreForScheduler is a minimal mock store for testing the scheduler.
type mockStoreForScheduler struct {
	accounts          []model.Account
	listErr           error
	listCalls         int
	getAccountCalls   int
	updateStatusCalls []struct{ id, status string }
}

func (m *mockStoreForScheduler) ListAccounts(ctx context.Context) ([]model.Account, error) {
	return m.ListAllAccounts(ctx)
}
func (m *mockStoreForScheduler) ListAllAccounts(ctx context.Context) ([]model.Account, error) {
	m.listCalls++
	if m.listErr != nil {
		return nil, m.listErr
	}
	return append([]model.Account(nil), m.accounts...), nil
}
func (m *mockStoreForScheduler) Save(context.Context, []model.CostRecord) (int64, int64, error) {
	return 0, 0, nil
}
func (m *mockStoreForScheduler) SaveZombies(context.Context, []model.ZombieResource) error {
	return nil
}
func (m *mockStoreForScheduler) LoadZombies(context.Context) ([]model.ZombieResource, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) UpsertOrganization(context.Context, string, string) (model.Organization, error) {
	return model.Organization{}, nil
}
func (m *mockStoreForScheduler) GetOrganizationByID(context.Context, string) (model.Organization, error) {
	return model.Organization{}, nil
}
func (m *mockStoreForScheduler) ListUserMemberships(context.Context, string) ([]model.MembershipWithOrganization, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) EnsureOrganization(context.Context, string, string, string) error {
	return nil
}
func (m *mockStoreForScheduler) UpsertUser(context.Context, string, string, string, string) (model.User, error) {
	return model.User{}, nil
}
func (m *mockStoreForScheduler) EnsureUser(context.Context, model.User) error {
	return nil
}
func (m *mockStoreForScheduler) AuditLogWrite(context.Context, model.AuditEvent) (int64, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) AuditLogList(context.Context, model.AuditFilter) ([]model.AuditEvent, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) AuditLogAnonymiseUser(context.Context, string) (int64, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) SaveAccount(context.Context, model.Account) error { return nil }
func (m *mockStoreForScheduler) GetAccount(_ context.Context, id string) (model.Account, error) {
	m.getAccountCalls++
	for _, a := range m.accounts {
		if a.ID == id {
			return a, nil
		}
	}
	return model.Account{}, nil
}
func (m *mockStoreForScheduler) DeleteAccount(context.Context, string) error { return nil }
func (m *mockStoreForScheduler) UpdateAccountStatus(_ context.Context, id, status string) error {
	m.updateStatusCalls = append(m.updateStatusCalls, struct{ id, status string }{id, status})
	return nil
}
func (m *mockStoreForScheduler) SetAccountError(context.Context, string, string) error {
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
func (m *mockStoreForScheduler) ListCostRecords(context.Context, storage.CostFilter) ([]model.CostRecord, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) SaveSnapshot(context.Context, model.ZombieSnapshot) error { return nil }
func (m *mockStoreForScheduler) ListSnapshots(context.Context, string) ([]model.ZombieSnapshot, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) SaveSnapshotServices(context.Context, []model.SnapshotService) error {
	return nil
}
func (m *mockStoreForScheduler) ListSnapshotsByService(context.Context, string, string, string) ([]model.ZombieSnapshot, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) ListTrendServices(context.Context) ([]string, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) ListTrendResourceTypes(context.Context, string) ([]string, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) DismissZombie(context.Context, model.DismissAction) (int64, error) {
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
func (m *mockStoreForScheduler) DeleteOldNotificationDispatches(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) Close() error { return nil }
func (m *mockStoreForScheduler) RoleOf(context.Context, string, string) (string, error) {
	return "", nil
}
func (m *mockStoreForScheduler) ListMemberships(context.Context) ([]model.MembershipWithUser, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) GetMembership(context.Context, string) (model.Membership, error) {
	return model.Membership{}, nil
}
func (m *mockStoreForScheduler) SaveMembership(context.Context, model.Membership) error { return nil }
func (m *mockStoreForScheduler) UpdateMembershipRole(context.Context, string, string) error {
	return nil
}
func (m *mockStoreForScheduler) DeleteMembership(context.Context, string) error  { return nil }
func (m *mockStoreForScheduler) TransferOwnership(context.Context, string) error { return nil }
func (m *mockStoreForScheduler) EnsureFirstMembership(context.Context, string, string) (bool, error) {
	return false, nil
}
func (m *mockStoreForScheduler) EnsureDevMembership(context.Context, string, string, string) error {
	return nil
}
func (m *mockStoreForScheduler) GetUserByEmail(context.Context, string) (model.User, error) {
	return model.User{}, nil
}
func (m *mockStoreForScheduler) GetUserByID(context.Context, string) (model.User, error) {
	return model.User{}, nil
}

// GetUserSSOConnectionID is on the Store interface for the API service's
// SSO RP-Initiated Logout resolver. No ingestion code path uses it today;
// if a future ingestion-side feature ever wires this method, the panic
// surfaces the new dependency loudly rather than letting tests silently
// pass on a stub that always reports "no SSO connection".
func (m *mockStoreForScheduler) GetUserSSOConnectionID(context.Context, string) (string, error) {
	panic("mockStoreForScheduler: GetUserSSOConnectionID called — ingestion doesn't exercise SSO logout; add real fake if a new path depends on this")
}
func (m *mockStoreForScheduler) SetUserSSOConnection(context.Context, string, string) error {
	panic("mockStoreForScheduler: SetUserSSOConnection called — ingestion doesn't mint SSO sessions; add real fake if a new path depends on this")
}
func (m *mockStoreForScheduler) DeleteUser(context.Context, string) error                { return nil }
func (m *mockStoreForScheduler) DeleteOrganizationCascade(context.Context, string) error { return nil }
func (m *mockStoreForScheduler) CreatePendingInvitation(context.Context, model.PendingInvitation) (model.PendingInvitation, bool, error) {
	return model.PendingInvitation{}, false, nil
}
func (m *mockStoreForScheduler) ListPendingInvitations(context.Context, string) ([]model.PendingInvitation, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) GetPendingInvitation(context.Context, string) (model.PendingInvitation, error) {
	return model.PendingInvitation{}, nil
}
func (m *mockStoreForScheduler) RevokePendingInvitation(context.Context, string) error { return nil }
func (m *mockStoreForScheduler) RedeemPendingInvitation(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (m *mockStoreForScheduler) ExpirePendingInvitations(context.Context) (int64, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) RenameOrganization(context.Context, string) error { return nil }
func (m *mockStoreForScheduler) MarkOnboardingComplete(context.Context) (time.Time, error) {
	return time.Time{}, nil
}

// ── Phase B1 native-auth method stubs ───────────────────────────────────────
// The scheduler never calls into these — it only deals with accounts and
// scans. Stubs return zero values / sentinel errors so compilation succeeds.

func (m *mockStoreForScheduler) CreateUserWithPassword(context.Context, model.User) (model.User, error) {
	return model.User{}, errors.New("CreateUserWithPassword not implemented")
}
func (m *mockStoreForScheduler) UpdateUserPassword(context.Context, string, string) error {
	return errors.New("UpdateUserPassword not implemented")
}
func (m *mockStoreForScheduler) UpdateUserName(context.Context, string, string) (string, error) {
	return "", errors.New("UpdateUserName not implemented")
}
func (m *mockStoreForScheduler) CountOrganizations(context.Context) (int64, error) { return 0, nil }
func (m *mockStoreForScheduler) LookupMembership(context.Context, string, string) (string, string, string, error) {
	return "", "", "", nil
}
func (m *mockStoreForScheduler) LookupUserByEmail(context.Context, string) (model.User, []model.Membership, error) {
	return model.User{}, nil, storage.ErrUserNotFound
}
func (m *mockStoreForScheduler) CreateSession(context.Context, model.Session) (model.Session, error) {
	return model.Session{}, errors.New("CreateSession not implemented")
}
func (m *mockStoreForScheduler) CreateSessionEnforcingCap(context.Context, model.Session, int) (model.Session, []string, error) {
	return model.Session{}, nil, errors.New("CreateSessionEnforcingCap not implemented")
}
func (m *mockStoreForScheduler) GetSessionByTokenHash(context.Context, string) (model.Session, error) {
	return model.Session{}, storage.ErrSessionNotFound
}
func (m *mockStoreForScheduler) TouchSessionLastSeen(context.Context, string) error { return nil }
func (m *mockStoreForScheduler) RevokeSession(context.Context, string) error        { return nil }
func (m *mockStoreForScheduler) RevokeUserSessions(context.Context, string) ([]string, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) ListUserSessionTokenHashes(context.Context, string) ([]string, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) CountSessionsForUser(context.Context, string) (int, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) SweepExpiredSessions(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) CreatePasswordReset(context.Context, string, string, string, string, string, time.Time) error {
	return errors.New("CreatePasswordReset not implemented")
}
func (m *mockStoreForScheduler) RedeemPasswordReset(context.Context, string, string) (string, string, error) {
	return "", "", storage.ErrPasswordResetNotFound
}
func (m *mockStoreForScheduler) CreateBootstrapState(context.Context, string, string) (bool, error) {
	return false, storage.ErrBootstrapAlreadyDone
}
func (m *mockStoreForScheduler) GetBootstrapState(context.Context) (string, string, error) {
	return "", "", storage.ErrBootstrapAlreadyDone
}
func (m *mockStoreForScheduler) ConsumeBootstrapState(context.Context, storage.BootstrapConsume) (storage.BootstrapResult, error) {
	return storage.BootstrapResult{}, storage.ErrBootstrapAlreadyDone
}
func (m *mockStoreForScheduler) CreateNativeInvitation(context.Context, model.PendingInvitation) (model.PendingInvitation, bool, error) {
	return model.PendingInvitation{}, false, errors.New("CreateNativeInvitation not implemented")
}
func (m *mockStoreForScheduler) RedeemNativeInvitation(context.Context, storage.NativeInviteRedeem) (model.User, model.Membership, error) {
	return model.User{}, model.Membership{}, storage.ErrInvitationNotFound
}
func (m *mockStoreForScheduler) LookupInvitationByToken(context.Context, string) (storage.PeekedInvitation, error) {
	return storage.PeekedInvitation{}, storage.ErrInvitationNotFound
}

func (m *mockStoreForScheduler) GetMembershipByOrgUser(context.Context, string, string) (model.Membership, error) {
	return model.Membership{}, storage.ErrMembershipNotFound
}

// ── Phase B2 SSO — fail-loud stubs (scheduler doesn't touch SSO) ────────────

func (m *mockStoreForScheduler) CreateSSOConnection(context.Context, model.SSOConnection) (model.SSOConnection, error) {
	return model.SSOConnection{}, errors.New("mockStoreForScheduler.CreateSSOConnection not implemented")
}
func (m *mockStoreForScheduler) GetSSOConnection(context.Context, string) (model.SSOConnection, error) {
	return model.SSOConnection{}, storage.ErrSSOConnectionNotFound
}
func (m *mockStoreForScheduler) GetSSOConnectionByID(context.Context, string) (model.SSOConnection, error) {
	return model.SSOConnection{}, storage.ErrSSOConnectionNotFound
}
func (m *mockStoreForScheduler) ListSSOConnections(context.Context) ([]model.SSOConnection, error) {
	return nil, errors.New("mockStoreForScheduler.ListSSOConnections not implemented")
}
func (m *mockStoreForScheduler) UpdateSSOConnection(context.Context, model.SSOConnection) error {
	return errors.New("mockStoreForScheduler.UpdateSSOConnection not implemented")
}
func (m *mockStoreForScheduler) DeleteSSOConnection(context.Context, string) error {
	return storage.ErrSSOConnectionNotFound
}
func (m *mockStoreForScheduler) CreateSSODomain(context.Context, model.SSODomain) (model.SSODomain, error) {
	return model.SSODomain{}, errors.New("mockStoreForScheduler.CreateSSODomain not implemented")
}
func (m *mockStoreForScheduler) GetSSODomain(context.Context, string) (model.SSODomain, error) {
	return model.SSODomain{}, storage.ErrSSODomainNotFound
}
func (m *mockStoreForScheduler) GetVerifiedSSODomainByName(context.Context, string) (model.SSODomain, error) {
	return model.SSODomain{}, storage.ErrSSODomainNotFound
}
func (m *mockStoreForScheduler) ListSSODomains(context.Context) ([]model.SSODomain, error) {
	return nil, errors.New("mockStoreForScheduler.ListSSODomains not implemented")
}
func (m *mockStoreForScheduler) UpdateSSODomainStatus(context.Context, string, string, time.Time, time.Time) error {
	return errors.New("mockStoreForScheduler.UpdateSSODomainStatus not implemented")
}
func (m *mockStoreForScheduler) DeleteSSODomain(context.Context, string) error {
	return storage.ErrSSODomainNotFound
}
func (m *mockStoreForScheduler) SweepStaleSSODomains(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) ListSSOGroupMappings(context.Context, string) ([]model.SSOGroupMapping, error) {
	return nil, errors.New("mockStoreForScheduler.ListSSOGroupMappings not implemented")
}
func (m *mockStoreForScheduler) ReplaceSSOGroupMappings(context.Context, string, []model.SSOGroupMapping) error {
	return errors.New("mockStoreForScheduler.ReplaceSSOGroupMappings not implemented")
}
func (m *mockStoreForScheduler) SaveNotificationChannel(context.Context, model.NotificationChannel) error {
	return nil
}
func (m *mockStoreForScheduler) ListNotificationChannels(context.Context) ([]model.NotificationChannel, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) ListEnabledNotificationChannels(context.Context) ([]model.NotificationChannel, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) GetNotificationChannel(context.Context, string) (model.NotificationChannel, error) {
	return model.NotificationChannel{}, nil
}
func (m *mockStoreForScheduler) DeleteNotificationChannel(context.Context, string) error {
	return nil
}
func (m *mockStoreForScheduler) SaveNotificationDispatch(context.Context, model.NotificationDispatch) error {
	return nil
}
func (m *mockStoreForScheduler) ListNotificationDispatches(context.Context, string, int) ([]model.NotificationDispatch, error) {
	return nil, nil
}

// captureQueue records enqueued jobs and optionally returns pre-seeded jobs
// from Dequeue (used by the worker tests). When `pending` is empty, Dequeue
// blocks on ctx — same shape as the production Redis queue under quiet load.
type captureQueue struct {
	jobs    []queue.ScanJob
	pending []queue.ScanJob
}

func (q *captureQueue) Enqueue(_ context.Context, job queue.ScanJob) error {
	q.jobs = append(q.jobs, job)
	return nil
}
func (q *captureQueue) Dequeue(ctx context.Context) (queue.ScanJob, error) {
	if len(q.pending) > 0 {
		j := q.pending[0]
		q.pending = q.pending[1:]
		return j, nil
	}
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
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 24, Status: "scanning",
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
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 24, LastScannedAt: nil, Status: "connected",
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
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 24, LastScannedAt: &last, Status: "connected",
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
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 24, LastScannedAt: &last, Status: "connected",
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
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 0, LastScannedAt: &now, Status: "connected",
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q)
	if len(q.jobs) != 1 {
		t.Fatalf("expected 1 job for zero-interval account, got %d", len(q.jobs))
	}
}

// ── isAccountOverdue: pending-CUR-delivery delay ────────────────────────────
//
// A CUR account still awaiting its first billing export delivery shouldn't
// have its first scan fire on literally the next scheduler tick after
// connecting — the export can take up to ~24h, so that scan is guaranteed
// to find nothing. These pin the CreatedAt-anchored delay directly on the
// extracted pure function, no queue/store involved.

func TestIsAccountOverdue_PendingCURDelivery_BeforeAnchor(t *testing.T) {
	acc := model.Account{
		ID:                "acc-1",
		Status:            model.AccountStatusPendingCURDelivery,
		ScanIntervalHours: 24,
		CreatedAt:         time.Now().Add(-1 * time.Hour), // connected 1h ago
		LastScannedAt:     nil,
	}
	if isAccountOverdue(acc, time.Now()) {
		t.Error("expected not overdue — export delivery window (24h) hasn't elapsed since CreatedAt")
	}
}

func TestIsAccountOverdue_PendingCURDelivery_AfterAnchor(t *testing.T) {
	acc := model.Account{
		ID:                "acc-1",
		Status:            model.AccountStatusPendingCURDelivery,
		ScanIntervalHours: 24,
		CreatedAt:         time.Now().Add(-25 * time.Hour), // connected 25h ago
		LastScannedAt:     nil,
	}
	if !isAccountOverdue(acc, time.Now()) {
		t.Error("expected overdue — 25h since CreatedAt exceeds the 24h interval")
	}
}

func TestIsAccountOverdue_NeverScanned_NotPending_ScansImmediately(t *testing.T) {
	// A never-scanned account NOT in PendingCURDelivery (e.g. Cost Explorer,
	// which has data immediately) keeps the original "scan on first tick"
	// behavior — CreatedAt is irrelevant here even if it's recent.
	acc := model.Account{
		ID:                "acc-1",
		Status:            model.AccountStatusConnected,
		ScanIntervalHours: 24,
		CreatedAt:         time.Now(),
		LastScannedAt:     nil,
	}
	if !isAccountOverdue(acc, time.Now()) {
		t.Error("expected overdue — never-scanned non-CUR-pending accounts scan immediately")
	}
}

func TestIsAccountOverdue_AlreadyScanned_UsesLastScannedAt_NotCreatedAt(t *testing.T) {
	// Once LastScannedAt is set (even from an empty pending-delivery scan),
	// the normal interval-since-last-scan rule takes over — CreatedAt is
	// only consulted pre-first-scan.
	last := time.Now().Add(-1 * time.Hour)
	acc := model.Account{
		ID:                "acc-1",
		Status:            model.AccountStatusPendingCURDelivery,
		ScanIntervalHours: 24,
		CreatedAt:         time.Now().Add(-100 * time.Hour), // long past its own delay window
		LastScannedAt:     &last,
	}
	if isAccountOverdue(acc, time.Now()) {
		t.Error("expected not overdue — last scan was 1h ago against a 24h interval, regardless of CreatedAt")
	}
}

// TestScanScheduledAccounts_PendingCURDelivery_NotYetDue / _Due exercise the
// same delay through the full scheduler loop (not just the pure function),
// pinning that scanScheduledAccounts actually wires isAccountOverdue in.

func TestScanScheduledAccounts_PendingCURDelivery_NotYetDue(t *testing.T) {
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 24,
			Status: model.AccountStatusPendingCURDelivery, CreatedAt: time.Now().Add(-1 * time.Hour),
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q)
	if len(q.jobs) != 0 {
		t.Fatalf("expected 0 jobs — pending-CUR-delivery account connected only 1h ago, got %d", len(q.jobs))
	}
}

func TestScanScheduledAccounts_PendingCURDelivery_Due(t *testing.T) {
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 24,
			Status: model.AccountStatusPendingCURDelivery, CreatedAt: time.Now().Add(-25 * time.Hour),
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q)
	if len(q.jobs) != 1 {
		t.Fatalf("expected 1 job — pending-CUR-delivery account connected 25h ago, got %d", len(q.jobs))
	}
}

// ── finalizeAccountStatus ────────────────────────────────────────────────

func TestFinalizeAccountStatus_PendingCURDelivery_NoData_StaysPending(t *testing.T) {
	store := &mockStoreForScheduler{}
	acc := model.Account{ID: "acc-1", Status: model.AccountStatusPendingCURDelivery}
	finalizeAccountStatus(context.Background(), store, acc, false)

	if len(store.updateStatusCalls) != 1 {
		t.Fatalf("expected 1 UpdateAccountStatus call, got %d", len(store.updateStatusCalls))
	}
	if got := store.updateStatusCalls[0].status; got != model.AccountStatusPendingCURDelivery {
		t.Errorf("expected status to stay %q (so LastScannedAt still advances for the next backoff check), got %q",
			model.AccountStatusPendingCURDelivery, got)
	}
}

func TestFinalizeAccountStatus_PendingCURDelivery_GotData_FlipsToConnected(t *testing.T) {
	store := &mockStoreForScheduler{}
	acc := model.Account{ID: "acc-1", Status: model.AccountStatusPendingCURDelivery}
	finalizeAccountStatus(context.Background(), store, acc, true)

	if len(store.updateStatusCalls) != 1 {
		t.Fatalf("expected 1 UpdateAccountStatus call, got %d", len(store.updateStatusCalls))
	}
	if got := store.updateStatusCalls[0].status; got != model.AccountStatusConnected {
		t.Errorf("expected status Connected once real cost data was found, got %q", got)
	}
}

func TestFinalizeAccountStatus_NotPending_AlwaysConnected(t *testing.T) {
	// A CE account, or a CUR account that already delivered once — the
	// pending/empty distinction only applies pre-first-data.
	store := &mockStoreForScheduler{}
	acc := model.Account{ID: "acc-1", Status: model.AccountStatusConnected}
	finalizeAccountStatus(context.Background(), store, acc, false)

	if len(store.updateStatusCalls) != 1 || store.updateStatusCalls[0].status != model.AccountStatusConnected {
		t.Errorf("expected status Connected regardless of gotCostData, got %+v", store.updateStatusCalls)
	}
}

// TestWorker_ProcessesJob is the worker's basic control case: dequeue a
// pending job and reach runScan (signalled by GetAccount being called) --
// pins that the worker drives a job straight through from dequeue to scan.
func TestWorker_ProcessesJob(t *testing.T) {
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", OrganizationID: "organization-1", Status: "connected",
		}},
	}
	q := &captureQueue{
		pending: []queue.ScanJob{{
			AccountID:      "acc-1",
			OrganizationID: "organization-1",
			EnqueuedAt:     time.Now().Add(-1 * time.Hour),
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(ctx, q, store, nil, 5*time.Minute, false)

	// Worker dequeues the seeded job and calls into runScan. With no further
	// pending jobs the next Dequeue blocks on ctx — short sleep is enough
	// wall-clock for the loop to spin once.
	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	if store.getAccountCalls == 0 {
		t.Errorf("worker did not reach runScan")
	}
}
