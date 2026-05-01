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
	accounts  []model.Account
	listErr   error
	listCalls int
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
func (m *mockStoreForScheduler) Save(context.Context, []model.CostRecord) (int64, error) {
	return 0, nil
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
func (m *mockStoreForScheduler) GetAccount(context.Context, string) (model.Account, error) {
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
func (m *mockStoreForScheduler) DeleteUser(context.Context, string) error                { return nil }
func (m *mockStoreForScheduler) DeleteOrganizationCascade(context.Context, string) error { return nil }
func (m *mockStoreForScheduler) CreatePendingInvitation(context.Context, model.PendingInvitation) (model.PendingInvitation, bool, error) {
	return model.PendingInvitation{}, false, nil
}
func (m *mockStoreForScheduler) UpdateInvitationKindeIDs(context.Context, string, string, string) error {
	return nil
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
func (m *mockStoreForScheduler) CountOrganizations(context.Context) (int64, error) { return 0, nil }
func (m *mockStoreForScheduler) LookupMembership(context.Context, string, string) (string, string, error) {
	return "", "", nil
}
func (m *mockStoreForScheduler) LookupUserByEmail(context.Context, string) (model.User, []model.Membership, error) {
	return model.User{}, nil, storage.ErrUserNotFound
}
func (m *mockStoreForScheduler) CreateSession(context.Context, model.Session) (model.Session, error) {
	return model.Session{}, errors.New("CreateSession not implemented")
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

// TestScanScheduledAccounts_SkippedWhenLicenseExpired — Phase B1.6 slice 5c.
// Past-grace boots block scheduled scans wholesale. Single check at the top
// of the pass, not per-account — license state is binary-wide.
func TestScanScheduledAccounts_SkippedWhenLicenseExpired(t *testing.T) {
	license.SetCurrent(&license.License{
		LicenseID:       "lic_test",
		CustomerID:      "test-001",
		ExpiresAt:       time.Now().Add(-60 * 24 * time.Hour),
		GracePeriodDays: 30,
	})
	t.Cleanup(func() { license.SetCurrent(nil) })

	now := time.Now()
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 0, LastScannedAt: &now, Status: "connected",
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q)
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
	license.SetCurrent(&license.License{
		LicenseID:       "lic_test",
		CustomerID:      "test-001",
		ExpiresAt:       time.Now().Add(-2 * 24 * time.Hour),
		GracePeriodDays: 30,
	})
	t.Cleanup(func() { license.SetCurrent(nil) })

	now := time.Now()
	store := &mockStoreForScheduler{
		accounts: []model.Account{{
			ID: "acc-1", OrganizationID: "organization-1", ScanIntervalHours: 0, LastScannedAt: &now, Status: "connected",
		}},
	}
	q := &captureQueue{}
	scanScheduledAccounts(context.Background(), store, q)
	if len(q.jobs) != 1 {
		t.Fatalf("in-grace must allow scheduled scans, got %d jobs", len(q.jobs))
	}
}
