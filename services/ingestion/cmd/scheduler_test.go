package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"axiaops.io/shared/license"
	"axiaops.io/shared/model"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
)

// mockStoreForScheduler is a minimal mock store for testing the scheduler.
type mockStoreForScheduler struct {
	accounts        []model.Account
	listErr         error
	listCalls       int
	getAccountCalls int

	// SaaS entitlement-gate fixtures (resolver != nil path).
	entitlements         []model.Entitlement
	listEntitlementCalls int
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
func (m *mockStoreForScheduler) UpdateAccountStatus(context.Context, string, string) error {
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
	scanScheduledAccounts(context.Background(), store, q, nil, 0)
	if len(q.jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(q.jobs))
	}
}

func TestScanScheduledAccounts_ListError(t *testing.T) {
	store := &mockStoreForScheduler{listErr: errors.New("db error")}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q, nil, 0) // should not panic
}

func TestScanScheduledAccounts_SkipsAlreadyScanning(t *testing.T) {
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 24, Status: "scanning",
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q, nil, 0)
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
	scanScheduledAccounts(context.Background(), store, q, nil, 0)
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
	scanScheduledAccounts(context.Background(), store, q, nil, 0)
	if len(q.jobs) != 1 {
		t.Fatalf("expected 1 job for overdue account, got %d", len(q.jobs))
	}
	if q.jobs[0].AccountID != "acc-1" {
		t.Errorf("expected account acc-1, got %s", q.jobs[0].AccountID)
	}
}

// stubResolver drives the SaaS path of scanScheduledAccounts without a DB.
type stubResolver struct{ ent model.Entitlement }

func (s stubResolver) GetEntitlement(context.Context, string) (model.Entitlement, error) {
	return s.ent, nil
}

// TestScanScheduledAccounts_SaaS_PerOrgEntitlement is the golden test for the
// pass-wide→per-org restructure: in ONE pass, an entitled org is scanned and a
// lapsed org is skipped, and entitlements are batch-loaded exactly once.
func TestScanScheduledAccounts_SaaS_PerOrgEntitlement(t *testing.T) {
	period := time.Now().Add(30 * 24 * time.Hour)
	store := &mockStoreForScheduler{
		accounts: []model.Account{
			{ID: "acc-A", OrganizationID: "org-A", ScanIntervalHours: 24, LastScannedAt: nil, Status: "connected"},
			{ID: "acc-B", OrganizationID: "org-B", ScanIntervalHours: 24, LastScannedAt: nil, Status: "connected"},
		},
		entitlements: []model.Entitlement{
			{OrganizationID: "org-A", Status: model.StatusActive, CurrentPeriodEnd: &period},
			{OrganizationID: "org-B", Status: model.StatusSuspended},
			// org-C has no row at all — fail-closed (not scanned even if it had
			// an account).
		},
	}
	q := &captureQueue{}
	// resolver != nil triggers the SaaS path; the per-org predicate uses the map.
	scanScheduledAccounts(context.Background(), store, q, stubResolver{}, 21*24*time.Hour)

	if len(q.jobs) != 1 {
		t.Fatalf("expected exactly 1 job (entitled org-A), got %d", len(q.jobs))
	}
	if q.jobs[0].OrganizationID != "org-A" {
		t.Errorf("expected org-A scanned, got %s", q.jobs[0].OrganizationID)
	}
	if store.listEntitlementCalls != 1 {
		t.Errorf("expected ListAllEntitlements batched to 1 call, got %d", store.listEntitlementCalls)
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
	scanScheduledAccounts(context.Background(), store, q, nil, 0)
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
	scanScheduledAccounts(context.Background(), store, q, nil, 0)
	if len(q.jobs) != 1 {
		t.Fatalf("expected 1 job for zero-interval account, got %d", len(q.jobs))
	}
}

// TestScanScheduledAccounts_SkippedWhenLicenseExpired — Phase B1.6 slice 5c.
// Past-grace boots block scheduled scans wholesale. Single check at the top
// of the pass, not per-account — license state is binary-wide.
func TestScanScheduledAccounts_SkippedWhenLicenseExpired(t *testing.T) {
	// Clear TestMain's package-default bypass so the gate evaluates the
	// snapshot we set up below. Cleanup restores the default for any
	// subsequent test.
	license.ClearEnforcementBypass()
	license.SetCurrent(&license.License{
		LicenseID:       "lic_test",
		CustomerID:      "test-001",
		ExpiresAt:       time.Now().Add(-60 * 24 * time.Hour),
		GracePeriodDays: 30,
	})
	t.Cleanup(func() {
		license.SetCurrent(nil)
		license.SetEnforcementBypass()
	})

	now := time.Now()
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 0, LastScannedAt: &now, Status: "connected",
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q, nil, 0)
	if len(q.jobs) != 0 {
		t.Fatalf("license_expired should suppress all scheduled scans, got %d jobs", len(q.jobs))
	}
	// Defensive: also confirm ListAllAccounts was NOT called — the gate
	// short-circuits before any DB read so an expired license also stops
	// burning DB load on every tick.
	if store.listCalls > 0 {
		t.Errorf("expected ListAllAccounts to be skipped under expired license, got %d calls", store.listCalls)
	}
}

// TestScanScheduledAccounts_AllowedInGrace — in-grace deliberately keeps
// scans running (plan §4.9 Option-3 scope). The dashboard banner + slog
// warns are the renewal-pressure mechanism for grace; only past-grace
// silences the scheduler.
func TestScanScheduledAccounts_AllowedInGrace(t *testing.T) {
	license.ClearEnforcementBypass()
	license.SetCurrent(&license.License{
		LicenseID:       "lic_test",
		CustomerID:      "test-001",
		ExpiresAt:       time.Now().Add(-2 * 24 * time.Hour),
		GracePeriodDays: 30,
	})
	t.Cleanup(func() {
		license.SetCurrent(nil)
		license.SetEnforcementBypass()
	})

	now := time.Now()
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 0, LastScannedAt: &now, Status: "connected",
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q, nil, 0)
	if len(q.jobs) != 1 {
		t.Fatalf("in-grace must allow scheduled scans, got %d jobs", len(q.jobs))
	}
}

// TestWorker_SkipsJobWhenLicenseExpired closes the gap a holistic review of
// MR !71 caught: the api-side scan-gate catches most jobs at trigger time,
// but a job enqueued before expiry and dequeued AFTER would otherwise sneak
// past. The worker must re-evaluate the gate at execution time. Asserts the
// worker drops the job (logs + continue) without ever calling GetAccount —
// runScan's first store touch is a useful "did we run any scan code at all"
// signal.
func TestWorker_SkipsJobWhenLicenseExpired(t *testing.T) {
	license.ClearEnforcementBypass()
	license.SetCurrent(&license.License{
		LicenseID:       "lic_test",
		CustomerID:      "test-001",
		ExpiresAt:       time.Now().Add(-60 * 24 * time.Hour),
		GracePeriodDays: 30,
	})
	t.Cleanup(func() {
		license.SetCurrent(nil)
		license.SetEnforcementBypass()
	})

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
	startWorker(ctx, q, store, nil, 5*time.Minute, false, nil, 0)

	// Worker dequeues the seeded job, hits the gate, continues. With no
	// further pending jobs the next Dequeue blocks on ctx — short sleep is
	// enough wall-clock for the loop to spin once. 50ms is generous given
	// the gate path is purely in-memory; tighten only if this becomes flaky.
	time.Sleep(50 * time.Millisecond)
	cancel()
	// Brief drain window for the worker goroutine to observe ctx.Done.
	time.Sleep(20 * time.Millisecond)

	if store.getAccountCalls != 0 {
		t.Errorf("worker called GetAccount %d times under expired license; gate should short-circuit before runScan", store.getAccountCalls)
	}
	if len(q.pending) != 0 {
		t.Errorf("worker did not consume the seeded job: %d pending", len(q.pending))
	}
}

// TestWorker_ProcessesJobWhenLicenseValid is the control case. Without it,
// a regression that flipped the gate sense (e.g. `if IsScanAllowed()` →
// `if !IsScanAllowed()` inverted) would only be caught by the expired test
// trivially passing — the gate-fired path looks identical to the
// gate-never-checked path from the outside.
func TestWorker_ProcessesJobWhenLicenseValid(t *testing.T) {
	license.ClearEnforcementBypass()
	license.SetCurrent(&license.License{
		LicenseID:       "lic_test",
		CustomerID:      "test-001",
		ExpiresAt:       time.Now().Add(365 * 24 * time.Hour),
		GracePeriodDays: 30,
	})
	t.Cleanup(func() {
		license.SetCurrent(nil)
		license.SetEnforcementBypass()
	})

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
	startWorker(ctx, q, store, nil, 5*time.Minute, false, nil, 0)

	// runScan path is best-effort here — it'll fail somewhere downstream
	// (mock store returns empty accounts for various lookups) but that's
	// orthogonal to the gate. The gate-passed signal is GetAccount > 0.
	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	if store.getAccountCalls == 0 {
		t.Errorf("worker did not reach runScan under valid license; gate may be inverted")
	}
}

// StaffStore stubs (platform admin plane) — the ingestion scheduler never
// touches the staff plane; these satisfy the widened storage.Store interface.
func (m *mockStoreForScheduler) CreateStaffUser(context.Context, storage.CreateStaffUserInput) (model.StaffUser, error) {
	return model.StaffUser{}, errors.New("not implemented")
}
func (m *mockStoreForScheduler) LookupStaffUserByEmail(context.Context, string) (model.StaffUser, []model.StaffRoleGrant, error) {
	return model.StaffUser{}, nil, storage.ErrStaffNotFound
}
func (m *mockStoreForScheduler) GetStaffUserByID(context.Context, string) (model.StaffUser, []model.StaffRoleGrant, error) {
	return model.StaffUser{}, nil, storage.ErrStaffNotFound
}
func (m *mockStoreForScheduler) ListStaffUsers(context.Context) ([]model.StaffUser, [][]model.StaffRoleGrant, error) {
	return nil, nil, nil
}
func (m *mockStoreForScheduler) GrantStaffRole(context.Context, string, model.StaffRole, string) error {
	return errors.New("not implemented")
}
func (m *mockStoreForScheduler) RevokeStaffRole(context.Context, string, model.StaffRole) error {
	return errors.New("not implemented")
}
func (m *mockStoreForScheduler) CountStaffWithRole(context.Context, model.StaffRole) (int, error) {
	return 0, nil
}
func (m *mockStoreForScheduler) ListAllOrganizations(context.Context) ([]model.Organization, error) {
	return nil, nil
}
func (m *mockStoreForScheduler) StaffTenantSummary(context.Context, string) (model.StaffTenantSummary, error) {
	return model.StaffTenantSummary{}, storage.ErrOrganizationNotFound
}

// EntitlementStore stubs (SaaS per-tenant entitlement, dormant Phase 2A
// scaffold) — the ingestion scheduler does not consult entitlement yet; these
// satisfy the widened storage.Store interface.
func (m *mockStoreForScheduler) GetEntitlement(context.Context, string) (model.Entitlement, error) {
	return model.Entitlement{}, storage.ErrEntitlementNotFound
}
func (m *mockStoreForScheduler) UpsertEntitlement(context.Context, model.Entitlement) error {
	return nil
}
func (m *mockStoreForScheduler) ListAllEntitlements(context.Context) ([]model.Entitlement, error) {
	m.listEntitlementCalls++
	return append([]model.Entitlement(nil), m.entitlements...), nil
}
