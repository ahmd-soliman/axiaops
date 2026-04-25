package postgres_test

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
)

// Two env vars are required to run these tests:
//
//	MIGRATION_DATABASE_URL  — owner/admin URL used for migrations (axiaops_owner).
//	                          postgres://axiaops_owner:axiaops_owner@localhost:5432/axiaops?sslmode=disable
//
//	DATABASE_URL            — app user URL used for the Store (axiaops).
//	                          postgres://axiaops:axiaops@localhost:5432/axiaops?sslmode=disable
//	                          The axiaops user is a non-superuser, so RLS is enforced.
//	                          If omitted, MIGRATION_DATABASE_URL is used (RLS isolation tests will be skipped).
//
// Run with:
//
//	MIGRATION_DATABASE_URL=... DATABASE_URL=... go test ./storage/postgres/...
func storeURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("DATABASE_URL"); url != "" {
		return url
	}
	url := os.Getenv("MIGRATION_DATABASE_URL")
	if url == "" {
		t.Skip("MIGRATION_DATABASE_URL not set — skipping postgres integration tests")
	}
	return url
}

// connectTestDB opens a pgx connection to MIGRATION_DATABASE_URL (owner/admin user).
// Used only for setup/teardown truncation — not for Store operations.
func connectTestDB(t *testing.T) *pgx.Conn {
	t.Helper()
	url := os.Getenv("MIGRATION_DATABASE_URL")
	if url == "" {
		t.Skip("MIGRATION_DATABASE_URL not set — skipping postgres integration tests")
	}
	conn, err := pgx.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connectTestDB: connect: %v", err)
	}
	return conn
}

func setup(t *testing.T) *pgx.Conn {
	t.Helper()
	conn := connectTestDB(t)
	const truncate = `TRUNCATE TABLE
		axiaops.audit_log,
		axiaops.memberships,
		axiaops.zombie_snapshots,
		axiaops.resource_records,
		axiaops.zombie_records,
		axiaops.cost_records,
		axiaops.accounts,
		axiaops.users,
		axiaops.tenants
	CASCADE`
	// Wipe everything to ensure a "known good state"
	if _, err := conn.Exec(context.Background(), truncate); err != nil {
		t.Fatalf("cleanup truncate: %v", err)
	}
	// Register cleanup for after test completes
	t.Cleanup(func() {
		if _, err := conn.Exec(context.Background(), truncate); err != nil {
			t.Logf("post-test cleanup truncate: %v", err)
		}
		_ = conn.Close(context.Background())
	})
	return conn
}

// rlsEnforced reports whether the store connects as a non-superuser (RLS is active).
// Tests that rely on tenant isolation skip when connecting as a superuser.
func rlsEnforced() bool {
	return os.Getenv("DATABASE_URL") != ""
}

// TestMain bootstraps the database and runs migrations once before all tests.
// When DATABASE_URL is set (non-superuser), Bootstrap() creates/updates the
// app user so the test environment is fully self-contained.
func TestMain(m *testing.M) {
	migrationURL := os.Getenv("MIGRATION_DATABASE_URL")
	if migrationURL == "" {
		os.Exit(m.Run())
	}
	appURL := os.Getenv("DATABASE_URL")
	if appURL != "" {
		if err := postgres.Bootstrap(migrationURL, appURL); err != nil {
			panic("postgres: bootstrap failed: " + err.Error())
		}
	}
	if err := postgres.Migrate(migrationURL); err != nil {
		panic("postgres: migration failed: " + err.Error())
	}
	os.Exit(m.Run())
}

// newTestStore opens a postgres store and cleans database before test.
// Each test gets a fresh clean database state via truncation.
func newTestStore(t *testing.T) *postgres.Store {
	t.Helper()

	// Clean database before test starts
	setup(t)

	migrationURL := os.Getenv("MIGRATION_DATABASE_URL")
	s, err := postgres.NewWithOwner(context.Background(), storeURL(t), migrationURL)
	if err != nil {
		t.Fatalf("postgres.NewWithOwner: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// newTenantCtx creates a fresh tenant and returns a context carrying its ID.
// Each test gets its own tenant so RLS isolates test data naturally.
func newTenantCtx(t *testing.T, s *postgres.Store) (context.Context, model.Tenant) {
	t.Helper()
	ctx := context.Background()
	orgCode := "test-org-" + uuid.New().String()
	tenant, err := s.UpsertTenant(ctx, orgCode, "Test Org")
	if err != nil {
		t.Fatalf("UpsertTenant: %v", err)
	}
	return storage.WithTenantID(ctx, tenant.ID), tenant
}

func costRecord(service, region string, amount float64) model.CostRecord {
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

// ── Save ──────────────────────────────────────────────────────────────────────

func TestSave_InsertsRecords(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	records := []model.CostRecord{
		costRecord("AmazonEC2", "eu-central-1", 100.00),
		costRecord("AmazonRDS", "eu-central-1", 200.00),
	}

	inserted, err := s.Save(ctx, records)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if inserted != 2 {
		t.Errorf("expected 2 inserted, got %d", inserted)
	}
}

func TestSave_DeduplicatesOnRerun(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	records := []model.CostRecord{costRecord("AmazonEC2", "eu-central-1", 100.00)}

	inserted, err := s.Save(ctx, records)
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if inserted != 1 {
		t.Errorf("expected 1 inserted on first run, got %d", inserted)
	}

	inserted, err = s.Save(ctx, records)
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if inserted != 0 {
		t.Errorf("expected 0 inserted on second run (duplicate), got %d", inserted)
	}
}

func TestSave_EmptyBatch(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	inserted, err := s.Save(ctx, nil)
	if err != nil {
		t.Fatalf("Save with nil: %v", err)
	}
	if inserted != 0 {
		t.Errorf("expected 0 inserted for empty batch, got %d", inserted)
	}
}

func TestSave_DifferentRegionIsNotDuplicate(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	records := []model.CostRecord{
		costRecord("AmazonEC2", "eu-central-1", 100.00),
		costRecord("AmazonEC2", "eu-west-1", 100.00),
	}

	inserted, err := s.Save(ctx, records)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if inserted != 2 {
		t.Errorf("expected 2 inserted (different regions), got %d", inserted)
	}
}

func TestSave_MissingTenantID_Errors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background() // no tenant in context

	_, err := s.Save(ctx, []model.CostRecord{costRecord("AmazonEC2", "eu-central-1", 10)})
	if err == nil {
		t.Error("expected error when tenant_id missing from context, got nil")
	}
}

// ── SaveZombies / LoadZombies ─────────────────────────────────────────────────

func zombieResource(service string, cost float64) model.ZombieResource {
	return model.ZombieResource{
		Provider:          "aws",
		AccountID:         "000000000000",
		InternalAccountID: "test-account-id",
		Service:           service,
		Region:            "eu-central-1",
		ResourceID:        "res-zombie-001",
		Tags:              map[string]string{"team": "platform"},
		MonthlyCost:       cost,
		Currency:          "USD",
		PeriodStart:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:         time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		UsageMetric:       "CPUUtilization",
		UsageAvg:          2.5,
		UsageUnit:         "Percent",
		Reason:            "Instance CPU below 5% — likely idle",
		Owner:             "platform",
	}
}

func TestSaveZombies_LoadZombies_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	zombies := []model.ZombieResource{
		zombieResource("AmazonEC2", 100.00),
		zombieResource("AmazonRDS", 200.00),
	}

	if err := s.SaveZombies(ctx, zombies); err != nil {
		t.Fatalf("SaveZombies: %v", err)
	}

	loaded, err := s.LoadZombies(ctx)
	if err != nil {
		t.Fatalf("LoadZombies: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("expected 2 zombies, got %d", len(loaded))
	}
}

func TestSaveZombies_ReplacesOnSecondRun(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	if err := s.SaveZombies(ctx, []model.ZombieResource{
		zombieResource("AmazonEC2", 100.00),
		zombieResource("AmazonRDS", 200.00),
	}); err != nil {
		t.Fatalf("first SaveZombies: %v", err)
	}

	// Second run with only one zombie — should replace, not append.
	if err := s.SaveZombies(ctx, []model.ZombieResource{
		zombieResource("AWSLambda", 50.00),
	}); err != nil {
		t.Fatalf("second SaveZombies: %v", err)
	}

	loaded, err := s.LoadZombies(ctx)
	if err != nil {
		t.Fatalf("LoadZombies: %v", err)
	}
	if len(loaded) != 1 {
		t.Errorf("expected 1 zombie after replacement, got %d", len(loaded))
	}
	if loaded[0].Service != "AWSLambda" {
		t.Errorf("expected AWSLambda zombie, got %s", loaded[0].Service)
	}
}

func TestLoadZombies_EmptyWhenNoneSaved(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS to filter out other tenants' data")
	}
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	zombies, err := s.LoadZombies(ctx)
	if err != nil {
		t.Fatalf("LoadZombies: %v", err)
	}
	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for new tenant, got %d", len(zombies))
	}
}

// ── Tenant isolation (RLS) ────────────────────────────────────────────────────

func TestZombies_TenantIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS enforcement")
	}
	s := newTestStore(t)

	ctxA, _ := newTenantCtx(t, s)
	ctxB, _ := newTenantCtx(t, s)

	// Tenant A saves zombies.
	if err := s.SaveZombies(ctxA, []model.ZombieResource{zombieResource("AmazonEC2", 100)}); err != nil {
		t.Fatalf("SaveZombies tenant A: %v", err)
	}

	// Tenant B should see none.
	zombiesB, err := s.LoadZombies(ctxB)
	if err != nil {
		t.Fatalf("LoadZombies tenant B: %v", err)
	}
	if len(zombiesB) != 0 {
		t.Errorf("tenant B should see 0 zombies, got %d", len(zombiesB))
	}
}

// ── UpsertTenant ─────────────────────────────────────────────────────────────

func TestUpsertTenant_CreatesOnFirstCall(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	tenant, err := s.UpsertTenant(ctx, "org_"+uuid.New().String(), "Acme Corp")
	if err != nil {
		t.Fatalf("UpsertTenant: %v", err)
	}
	if tenant.ID == "" {
		t.Error("expected non-empty tenant ID")
	}
	if tenant.Name != "Acme Corp" {
		t.Errorf("expected name Acme Corp, got %s", tenant.Name)
	}
}

func TestUpsertTenant_ReturnsSameIDOnSecondCall(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	orgCode := "org_" + uuid.New().String()

	first, err := s.UpsertTenant(ctx, orgCode, "Acme Corp")
	if err != nil {
		t.Fatalf("first UpsertTenant: %v", err)
	}
	second, err := s.UpsertTenant(ctx, orgCode, "Acme Corp")
	if err != nil {
		t.Fatalf("second UpsertTenant: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected same tenant ID, got %s and %s", first.ID, second.ID)
	}
}

func TestUpsertTenant_UpdatesName(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	orgCode := "org_" + uuid.New().String()

	_, err := s.UpsertTenant(ctx, orgCode, "Old Name")
	if err != nil {
		t.Fatalf("first UpsertTenant: %v", err)
	}
	updated, err := s.UpsertTenant(ctx, orgCode, "New Name")
	if err != nil {
		t.Fatalf("second UpsertTenant: %v", err)
	}
	if updated.Name != "New Name" {
		t.Errorf("expected name New Name, got %s", updated.Name)
	}
}

// ── UpsertUser ────────────────────────────────────────────────────────────────

func TestUpsertUser_CreatesOnFirstLogin(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	user, err := s.UpsertUser(ctx, tenant.ID, "kp_"+uuid.New().String(), "alice@acme.com", "Alice")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if user.ID == "" {
		t.Error("expected non-empty user ID")
	}
	if user.TenantID != tenant.ID {
		t.Errorf("expected tenant_id %s, got %s", tenant.ID, user.TenantID)
	}
	if user.Email != "alice@acme.com" {
		t.Errorf("expected email alice@acme.com, got %s", user.Email)
	}
}

func TestUpsertUser_ReturnsSameIDOnSecondLogin(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)
	kindeSub := "kp_" + uuid.New().String()

	first, err := s.UpsertUser(ctx, tenant.ID, kindeSub, "alice@acme.com", "Alice")
	if err != nil {
		t.Fatalf("first UpsertUser: %v", err)
	}
	second, err := s.UpsertUser(ctx, tenant.ID, kindeSub, "alice@acme.com", "Alice")
	if err != nil {
		t.Fatalf("second UpsertUser: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected same user ID, got %s and %s", first.ID, second.ID)
	}
}

// ── EnsureUser ────────────────────────────────────────────────────────────────

func TestEnsureUser_CreatesRow(t *testing.T) {
	s := newTestStore(t)
	_, tenant := newTenantCtx(t, s)

	u := model.User{ID: "dev-user-" + uuid.New().String(), TenantID: tenant.ID, Email: "dev@axiaops.local", Name: "Dev User"}
	if err := s.EnsureUser(context.Background(), u); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()
	var kindeSub, email string
	err := conn.QueryRow(context.Background(),
		`SELECT kinde_sub, email FROM axiaops.users WHERE id = $1`, u.ID,
	).Scan(&kindeSub, &email)
	if err != nil {
		t.Fatalf("fetch inserted row: %v", err)
	}
	if want := "dev:" + u.ID; kindeSub != want {
		t.Errorf("kinde_sub: got %q, want %q", kindeSub, want)
	}
	if email != u.Email {
		t.Errorf("email: got %q, want %q", email, u.Email)
	}
}

func TestEnsureUser_UpdatesOnConflict(t *testing.T) {
	s := newTestStore(t)
	_, tenant1 := newTenantCtx(t, s)
	_, tenant2 := newTenantCtx(t, s)

	id := "dev-user-" + uuid.New().String()
	if err := s.EnsureUser(context.Background(), model.User{ID: id, TenantID: tenant1.ID, Email: "old@axiaops.local", Name: "Old"}); err != nil {
		t.Fatalf("first EnsureUser: %v", err)
	}
	if err := s.EnsureUser(context.Background(), model.User{ID: id, TenantID: tenant2.ID, Email: "new@axiaops.local", Name: "New"}); err != nil {
		t.Fatalf("second EnsureUser: %v", err)
	}

	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()
	var tenantID, email, name string
	err := conn.QueryRow(context.Background(),
		`SELECT tenant_id, email, name FROM axiaops.users WHERE id = $1`, id,
	).Scan(&tenantID, &email, &name)
	if err != nil {
		t.Fatalf("fetch row: %v", err)
	}
	if tenantID != tenant2.ID {
		t.Errorf("tenant_id: got %q, want %q (self-correcting update)", tenantID, tenant2.ID)
	}
	if email != "new@axiaops.local" {
		t.Errorf("email: got %q, want %q", email, "new@axiaops.local")
	}
}

// TestUsersDevKindeSubCheckConstraint verifies that migration 013's CHECK
// constraint rejects rows where kinde_sub starts with "dev:" but does not
// match "dev:" + id — preventing future code paths from producing colliding
// synthetic subs.
func TestUsersDevKindeSubCheckConstraint(t *testing.T) {
	_ = newTestStore(t) // ensures migrations (including 013) have applied
	_, tenant := newTenantCtx(t, newTestStore(t))

	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()

	now := time.Now().UTC()
	_, err := conn.Exec(context.Background(),
		`INSERT INTO axiaops.users (id, tenant_id, kinde_sub, email, name, created_at, last_seen)
		 VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		"user-A", tenant.ID, "dev:user-B", "mismatch@axiaops.local", "Mismatch", now,
	)
	if err == nil {
		t.Fatal("expected CHECK constraint violation for dev:user-B with id user-A, got nil")
	}
}

// ── Account CRUD ──────────────────────────────────────────────────────────────

func testAccount(tenantID string) model.Account {
	return model.Account{
		ID:                uuid.New().String(),
		TenantID:          tenantID,
		Provider:          "aws",
		Label:             "dev account",
		AccessKeyID:       "AKIAIOSFODNN7EXAMPLE",
		SecretEncrypted:   "encrypted-secret",
		Region:            "eu-central-1",
		Status:            "connected",
		ScanIntervalHours: 24,
		CreatedAt:         time.Now().UTC(),
	}
}

func TestAccount_SaveAndList(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS to scope ListAccounts to this tenant")
	}
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	a := testAccount(tenant.ID)
	if err := s.SaveAccount(ctx, a); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}

	accounts, err := s.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}
	if accounts[0].ID != a.ID {
		t.Errorf("expected account ID %s, got %s", a.ID, accounts[0].ID)
	}
	if accounts[0].Label != "dev account" {
		t.Errorf("expected label 'dev account', got %s", accounts[0].Label)
	}
}

func TestAccount_GetByID(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	a := testAccount(tenant.ID)
	if err := s.SaveAccount(ctx, a); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}

	got, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.ID != a.ID {
		t.Errorf("expected ID %s, got %s", a.ID, got.ID)
	}
	if got.SecretEncrypted != a.SecretEncrypted {
		t.Error("SecretEncrypted should be preserved in DB")
	}
}

func TestAccount_Delete(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS to scope ListAccounts to this tenant")
	}
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	a := testAccount(tenant.ID)
	if err := s.SaveAccount(ctx, a); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	if err := s.DeleteAccount(ctx, a.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}

	accounts, err := s.ListAccounts(ctx)
	if err != nil {
		t.Fatalf("ListAccounts after delete: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("expected 0 accounts after delete, got %d", len(accounts))
	}
}

func TestAccount_UpdateStatus(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	a := testAccount(tenant.ID)
	if err := s.SaveAccount(ctx, a); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	if err := s.UpdateAccountStatus(ctx, a.ID, "scanning"); err != nil {
		t.Fatalf("UpdateAccountStatus: %v", err)
	}

	got, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.Status != "scanning" {
		t.Errorf("expected status 'scanning', got %s", got.Status)
	}
	if got.LastScannedAt == nil {
		t.Error("expected last_scanned_at to be set after UpdateAccountStatus")
	}
}

func TestAccount_TryMarkAccountScanning(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	a := testAccount(tenant.ID)
	if err := s.SaveAccount(ctx, a); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}

	ok, err := s.TryMarkAccountScanning(ctx, a.ID)
	if err != nil {
		t.Fatalf("TryMarkAccountScanning first: %v", err)
	}
	if !ok {
		t.Fatal("expected first TryMarkAccountScanning to succeed")
	}
	ok2, err := s.TryMarkAccountScanning(ctx, a.ID)
	if err != nil {
		t.Fatalf("TryMarkAccountScanning second: %v", err)
	}
	if ok2 {
		t.Fatal("expected second TryMarkAccountScanning to fail while already scanning")
	}
}

func TestAccount_TenantIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS enforcement")
	}
	s := newTestStore(t)
	ctxA, tenantA := newTenantCtx(t, s)
	ctxB, _ := newTenantCtx(t, s)

	a := testAccount(tenantA.ID)
	if err := s.SaveAccount(ctxA, a); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}

	accountsB, err := s.ListAccounts(ctxB)
	if err != nil {
		t.Fatalf("ListAccounts tenant B: %v", err)
	}
	if len(accountsB) != 0 {
		t.Errorf("tenant B should see 0 accounts, got %d", len(accountsB))
	}
}

func TestAccount_ScanIntervalHours(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	a := testAccount(tenant.ID)
	a.ScanIntervalHours = 12
	if err := s.SaveAccount(ctx, a); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}

	got, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.ScanIntervalHours != 12 {
		t.Errorf("expected ScanIntervalHours 12, got %d", got.ScanIntervalHours)
	}

	// Test updating scan_interval_hours via SaveAccount (upsert)
	a.ScanIntervalHours = 6
	if err := s.SaveAccount(ctx, a); err != nil {
		t.Fatalf("SaveAccount (update): %v", err)
	}

	got2, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount after update: %v", err)
	}
	if got2.ScanIntervalHours != 6 {
		t.Errorf("expected ScanIntervalHours 6 after update, got %d", got2.ScanIntervalHours)
	}
}

// ── SaveResources / LoadResources ─────────────────────────────────────────────

func resourceRecord(service string, isZombie bool) model.ResourceRecord {
	return model.ResourceRecord{
		Provider:          "aws",
		AccountID:         "000000000000",
		InternalAccountID: "test-account-id",
		Service:           service,
		Region:            "eu-central-1",
		ResourceID:        "res-" + uuid.New().String(),
		Tags:              map[string]string{"team": "platform"},
		MonthlyCost:       100.00,
		Currency:          "USD",
		PeriodStart:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:         time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		UsageMetric:       "CPUUtilization",
		UsageAvg:          2.5,
		UsageUnit:         "Percent",
		IsZombie:          isZombie,
		Reason:            "idle",
		Owner:             "platform",
	}
}

func TestSaveResources_LoadResources_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	resources := []model.ResourceRecord{
		resourceRecord("AmazonEC2", true),
		resourceRecord("AmazonRDS", false),
	}

	if err := s.SaveResources(ctx, resources); err != nil {
		t.Fatalf("SaveResources: %v", err)
	}

	loaded, err := s.LoadResources(ctx)
	if err != nil {
		t.Fatalf("LoadResources: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("expected 2 resources, got %d", len(loaded))
	}
}

func TestSaveResources_ReplacesOnSecondRun(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	if err := s.SaveResources(ctx, []model.ResourceRecord{
		resourceRecord("AmazonEC2", true),
		resourceRecord("AmazonRDS", false),
	}); err != nil {
		t.Fatalf("first SaveResources: %v", err)
	}

	if err := s.SaveResources(ctx, []model.ResourceRecord{
		resourceRecord("AWSLambda", false),
	}); err != nil {
		t.Fatalf("second SaveResources: %v", err)
	}

	loaded, err := s.LoadResources(ctx)
	if err != nil {
		t.Fatalf("LoadResources: %v", err)
	}
	if len(loaded) != 1 {
		t.Errorf("expected 1 resource after replacement, got %d", len(loaded))
	}
}

// ── SaveSnapshot / ListSnapshots ──────────────────────────────────────────────

func zombieSnapshot(tenantID, accountID string, cost float64, zombieCount int) model.ZombieSnapshot {
	return model.ZombieSnapshot{
		ID:               uuid.New().String(),
		TenantID:         tenantID,
		AccountID:        accountID,
		SnapshotAt:       time.Now().UTC(),
		ZombieCount:      zombieCount,
		TotalMonthlyCost: cost,
		Currency:         "USD",
	}
}

func TestSaveSnapshot_ListSnapshots_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	snap := zombieSnapshot(tenant.ID, "acc-001", 150.00, 3)
	if err := s.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	snaps, err := s.ListSnapshots(ctx, "")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	got := snaps[0]
	if got.ID != snap.ID {
		t.Errorf("expected ID %s, got %s", snap.ID, got.ID)
	}
	if got.AccountID != "acc-001" {
		t.Errorf("expected account_id acc-001, got %s", got.AccountID)
	}
	if got.ZombieCount != 3 {
		t.Errorf("expected zombie_count 3, got %d", got.ZombieCount)
	}
	if got.TotalMonthlyCost != 150.00 {
		t.Errorf("expected total_monthly_cost 150.00, got %f", got.TotalMonthlyCost)
	}
	if got.Currency != "USD" {
		t.Errorf("expected currency USD, got %s", got.Currency)
	}
}

func TestListSnapshots_OrderedOldestFirst(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	// Insert three snapshots with explicit timestamps spread one hour apart.
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	snapsToSave := []model.ZombieSnapshot{
		{ID: uuid.New().String(), TenantID: tenant.ID, AccountID: "acc-1", SnapshotAt: base.Add(2 * time.Hour), ZombieCount: 5, TotalMonthlyCost: 500, Currency: "USD"},
		{ID: uuid.New().String(), TenantID: tenant.ID, AccountID: "acc-1", SnapshotAt: base, ZombieCount: 1, TotalMonthlyCost: 100, Currency: "USD"},
		{ID: uuid.New().String(), TenantID: tenant.ID, AccountID: "acc-1", SnapshotAt: base.Add(time.Hour), ZombieCount: 3, TotalMonthlyCost: 300, Currency: "USD"},
	}
	for _, snap := range snapsToSave {
		if err := s.SaveSnapshot(ctx, snap); err != nil {
			t.Fatalf("SaveSnapshot: %v", err)
		}
	}

	loaded, err := s.ListSnapshots(ctx, "")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(loaded))
	}

	// Verify ascending order by snapshot_at.
	for i := 1; i < len(loaded); i++ {
		if !loaded[i].SnapshotAt.After(loaded[i-1].SnapshotAt) {
			t.Errorf("snapshots not in ascending order: index %d (%v) not after index %d (%v)",
				i, loaded[i].SnapshotAt, i-1, loaded[i-1].SnapshotAt)
		}
	}
	// Oldest-first: zombie_count should go 1 → 3 → 5.
	if loaded[0].ZombieCount != 1 {
		t.Errorf("expected first (oldest) zombie_count 1, got %d", loaded[0].ZombieCount)
	}
	if loaded[2].ZombieCount != 5 {
		t.Errorf("expected last (newest) zombie_count 5, got %d", loaded[2].ZombieCount)
	}
}

func TestListSnapshots_FilterByAccountID(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	// Two snapshots for acc-A, one for acc-B.
	if err := s.SaveSnapshot(ctx, zombieSnapshot(tenant.ID, "acc-A", 100, 2)); err != nil {
		t.Fatalf("SaveSnapshot acc-A first: %v", err)
	}
	if err := s.SaveSnapshot(ctx, zombieSnapshot(tenant.ID, "acc-A", 200, 4)); err != nil {
		t.Fatalf("SaveSnapshot acc-A second: %v", err)
	}
	if err := s.SaveSnapshot(ctx, zombieSnapshot(tenant.ID, "acc-B", 50, 1)); err != nil {
		t.Fatalf("SaveSnapshot acc-B: %v", err)
	}

	// Filter to acc-A only.
	snaps, err := s.ListSnapshots(ctx, "acc-A")
	if err != nil {
		t.Fatalf("ListSnapshots acc-A: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots for acc-A, got %d", len(snaps))
	}
	for _, snap := range snaps {
		if snap.AccountID != "acc-A" {
			t.Errorf("expected only acc-A snapshots, got account_id %s", snap.AccountID)
		}
	}

	// Filter to acc-B only.
	snapsB, err := s.ListSnapshots(ctx, "acc-B")
	if err != nil {
		t.Fatalf("ListSnapshots acc-B: %v", err)
	}
	if len(snapsB) != 1 {
		t.Fatalf("expected 1 snapshot for acc-B, got %d", len(snapsB))
	}

	// No filter — all three.
	all, err := s.ListSnapshots(ctx, "")
	if err != nil {
		t.Fatalf("ListSnapshots all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 snapshots total, got %d", len(all))
	}
}

func TestListSnapshots_EmptyWhenNoneSaved(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS to filter out other tenants' snapshots")
	}
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	snaps, err := s.ListSnapshots(ctx, "")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("expected 0 snapshots for new tenant, got %d", len(snaps))
	}
}

func TestSaveSnapshot_MissingTenantID_Errors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background() // no tenant in context

	snap := model.ZombieSnapshot{
		ID:               uuid.New().String(),
		AccountID:        "acc-1",
		SnapshotAt:       time.Now().UTC(),
		ZombieCount:      1,
		TotalMonthlyCost: 50.00,
		Currency:         "USD",
	}
	if err := s.SaveSnapshot(ctx, snap); err == nil {
		t.Error("expected error when tenant_id missing from context, got nil")
	}
}

func TestListSnapshots_MissingTenantID_Errors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background() // no tenant in context

	if _, err := s.ListSnapshots(ctx, ""); err == nil {
		t.Error("expected error when tenant_id missing from context, got nil")
	}
}

func TestSnapshot_TenantIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS enforcement")
	}
	s := newTestStore(t)

	ctxA, tenantA := newTenantCtx(t, s)
	ctxB, _ := newTenantCtx(t, s)

	// Tenant A saves a snapshot.
	if err := s.SaveSnapshot(ctxA, zombieSnapshot(tenantA.ID, "acc-1", 100, 2)); err != nil {
		t.Fatalf("SaveSnapshot tenant A: %v", err)
	}

	// Tenant B should see none of Tenant A's snapshots.
	snapsB, err := s.ListSnapshots(ctxB, "")
	if err != nil {
		t.Fatalf("ListSnapshots tenant B: %v", err)
	}
	if len(snapsB) != 0 {
		t.Errorf("tenant B should see 0 snapshots, got %d", len(snapsB))
	}
}

func TestSaveSnapshot_AccumulatesAcrossScans(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	// Simulate three consecutive scans — unlike zombie_records, snapshots must not be replaced.
	for i := 1; i <= 3; i++ {
		snap := zombieSnapshot(tenant.ID, "acc-1", float64(i)*100, i)
		if err := s.SaveSnapshot(ctx, snap); err != nil {
			t.Fatalf("SaveSnapshot scan %d: %v", i, err)
		}
	}

	snaps, err := s.ListSnapshots(ctx, "")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 3 {
		t.Errorf("expected 3 accumulated snapshots (one per scan), got %d", len(snaps))
	}
}

// ── DeleteOldCostRecords ──────────────────────────────────────────────────────

func TestDeleteOldCostRecords_DeletesExpiredRows(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	old := costRecord("AmazonEC2", "eu-central-1", 10.00)
	old.PeriodEnd = time.Now().UTC().AddDate(0, 0, -100)
	old.PeriodStart = old.PeriodEnd.AddDate(0, 0, -30)

	recent := costRecord("AmazonRDS", "eu-central-1", 20.00)
	// recent uses default PeriodEnd (2026-03-31) which is within 90 days

	if _, err := s.Save(ctx, []model.CostRecord{old, recent}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -90)
	deleted, err := s.DeleteOldCostRecords(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("DeleteOldCostRecords: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 row deleted, got %d", deleted)
	}
}

func TestDeleteOldCostRecords_KeepsRecentRows(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	recent := costRecord("AmazonEC2", "eu-central-1", 50.00)
	if _, err := s.Save(ctx, []model.CostRecord{recent}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -90)
	deleted, err := s.DeleteOldCostRecords(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("DeleteOldCostRecords: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 rows deleted, got %d", deleted)
	}
}

func TestDeleteOldCostRecords_ReturnsZeroWhenEmpty(t *testing.T) {
	s := newTestStore(t)
	// no records inserted

	deleted, err := s.DeleteOldCostRecords(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("DeleteOldCostRecords: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 rows deleted on empty table, got %d", deleted)
	}
}

// ── Audit log ────────────────────────────────────────────────────────────────

func writeAudit(t *testing.T, s *postgres.Store, ctx context.Context, e model.AuditEvent) int64 {
	t.Helper()
	id, err := s.AuditLogWrite(ctx, e)
	if err != nil {
		t.Fatalf("AuditLogWrite: %v", err)
	}
	return id
}

func TestAuditLog_WriteAndList(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	// At least one event has IPAddress set so the happy-path test exercises
	// the INET → text codec path (see TestAuditLog_IPAddressRoundTrips for
	// the dedicated regression test). Defence-in-depth so the cast can't be
	// silently reverted in a future merge without CI catching it.
	writeAudit(t, s, ctx, model.AuditEvent{
		Action:       model.AuditActionDismissZombie,
		UserID:       "user-1",
		ActorEmail:   "alice@acme.com",
		ResourceType: "dismissal",
		ResourceID:   "42",
		Reason:       "intentional",
		Metadata:     map[string]any{"service": "AmazonEC2"},
		IPAddress:    net.ParseIP("203.0.113.7"),
	})
	writeAudit(t, s, ctx, model.AuditEvent{
		Action:     model.AuditActionAccountConnected,
		UserID:     "user-1",
		ActorEmail: "alice@acme.com",
		ResourceID: "acc-1",
	})

	events, err := s.AuditLogList(ctx, model.AuditFilter{})
	if err != nil {
		t.Fatalf("AuditLogList: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	// Newest first.
	if events[0].Action != model.AuditActionAccountConnected {
		t.Errorf("expected account_connected first (newest), got %s", events[0].Action)
	}
	if events[1].Action != model.AuditActionDismissZombie {
		t.Errorf("expected dismiss_zombie second, got %s", events[1].Action)
	}
	if events[1].Metadata["service"] != "AmazonEC2" {
		t.Errorf("expected metadata round-trip, got %v", events[1].Metadata)
	}
	// IP round-trip — without this assertion the codec regression test (see
	// TestAuditLog_IPAddressRoundTrips) is the only thing keeping the cast
	// honest. Defence-in-depth: even if someone deletes that test, this one
	// catches a reverted host(ip_address) cast.
	if events[1].IPAddress == nil {
		t.Errorf("ip_address: got nil, want 203.0.113.7")
	} else if got := events[1].IPAddress.String(); got != "203.0.113.7" {
		t.Errorf("ip_address: got %q, want 203.0.113.7", got)
	}
	// tenant_id column should equal the tenant we wrote under.
	if events[0].TenantID != tenant.ID {
		t.Errorf("tenant_id: got %q, want %q", events[0].TenantID, tenant.ID)
	}
}

// Regression test: INET columns must round-trip through AuditLogList. The
// original implementation cast ip_address via the default binary codec, which
// pgx can't decode into the **string used for the nullable IP field. Fixture
// tests never populated an IP so the bug slipped past CI and surfaced only
// when the dashboard hit the real endpoint with a real client address.
func TestAuditLog_IPAddressRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	writeAudit(t, s, ctx, model.AuditEvent{
		Action:    model.AuditActionScanTriggered,
		IPAddress: net.ParseIP("203.0.113.42"),
	})

	events, err := s.AuditLogList(ctx, model.AuditFilter{})
	if err != nil {
		t.Fatalf("AuditLogList with populated ip_address: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	// nil-guard before .String() — if the codec regresses and ParseIP gets
	// an unparseable string, IPAddress is left nil rather than panicking on
	// the test row. A clear t.Fatalf is more useful than a runtime panic.
	if events[0].IPAddress == nil {
		t.Fatalf("ip_address: got nil, want 203.0.113.42 — codec regression?")
	}
	if got := events[0].IPAddress.String(); got != "203.0.113.42" {
		t.Errorf("ip_address round-trip: got %q, want 203.0.113.42", got)
	}
}

func TestAuditLog_TenantIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL for RLS to scope queries")
	}
	s := newTestStore(t)
	ctxA, _ := newTenantCtx(t, s)
	ctxB, _ := newTenantCtx(t, s)

	writeAudit(t, s, ctxA, model.AuditEvent{Action: model.AuditActionDismissZombie, UserID: "u-a"})
	writeAudit(t, s, ctxB, model.AuditEvent{Action: model.AuditActionSnoozeZombie, UserID: "u-b"})

	aEvents, err := s.AuditLogList(ctxA, model.AuditFilter{})
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(aEvents) != 1 || aEvents[0].UserID != "u-a" {
		t.Errorf("tenant A must see only its own rows, got %+v", aEvents)
	}
	bEvents, err := s.AuditLogList(ctxB, model.AuditFilter{})
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(bEvents) != 1 || bEvents[0].UserID != "u-b" {
		t.Errorf("tenant B must see only its own rows, got %+v", bEvents)
	}
}

func TestAuditLog_Filters(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	writeAudit(t, s, ctx, model.AuditEvent{Action: model.AuditActionDismissZombie, UserID: "u1", ResourceType: "dismissal", ResourceID: "1"})
	writeAudit(t, s, ctx, model.AuditEvent{Action: model.AuditActionSnoozeZombie, UserID: "u2", ResourceType: "dismissal", ResourceID: "2"})
	writeAudit(t, s, ctx, model.AuditEvent{Action: model.AuditActionAccountConnected, UserID: "u1", ResourceType: "account", ResourceID: "acc-1"})

	// Filter by action.
	events, _ := s.AuditLogList(ctx, model.AuditFilter{Action: model.AuditActionDismissZombie})
	if len(events) != 1 || events[0].UserID != "u1" {
		t.Errorf("action filter: got %+v", events)
	}

	// Filter by user.
	events, _ = s.AuditLogList(ctx, model.AuditFilter{UserID: "u1"})
	if len(events) != 2 {
		t.Errorf("user filter: expected 2 events for u1, got %d", len(events))
	}

	// Filter by resource_type + resource_id.
	events, _ = s.AuditLogList(ctx, model.AuditFilter{ResourceType: "account", ResourceID: "acc-1"})
	if len(events) != 1 || events[0].Action != model.AuditActionAccountConnected {
		t.Errorf("resource filter: got %+v", events)
	}
}

func TestAuditLog_Pagination(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	const total = 25
	for i := 0; i < total; i++ {
		writeAudit(t, s, ctx, model.AuditEvent{
			Action: model.AuditActionDismissZombie,
			UserID: "u1",
		})
	}

	first, err := s.AuditLogList(ctx, model.AuditFilter{Limit: 10})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(first) != 10 {
		t.Fatalf("page 1 size: got %d, want 10", len(first))
	}
	cursor := model.AuditCursor{CreatedAt: first[len(first)-1].CreatedAt, ID: first[len(first)-1].ID}

	second, err := s.AuditLogList(ctx, model.AuditFilter{Limit: 10, Cursor: cursor})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(second) != 10 {
		t.Fatalf("page 2 size: got %d, want 10", len(second))
	}
	// Must not overlap with page 1.
	seen := make(map[int64]bool)
	for _, e := range first {
		seen[e.ID] = true
	}
	for _, e := range second {
		if seen[e.ID] {
			t.Errorf("page 2 contains id %d from page 1 — cursor pagination is broken", e.ID)
		}
	}
	cursor = model.AuditCursor{CreatedAt: second[len(second)-1].CreatedAt, ID: second[len(second)-1].ID}

	third, err := s.AuditLogList(ctx, model.AuditFilter{Limit: 10, Cursor: cursor})
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if len(third) != 5 {
		t.Errorf("page 3 size: got %d, want 5 (remaining)", len(third))
	}
}

func TestAuditLog_AnonymiseUser(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	writeAudit(t, s, ctx, model.AuditEvent{Action: model.AuditActionDismissZombie, UserID: "target", ActorEmail: "target@acme.com"})
	writeAudit(t, s, ctx, model.AuditEvent{Action: model.AuditActionSnoozeZombie, UserID: "target", ActorEmail: "target@acme.com"})
	writeAudit(t, s, ctx, model.AuditEvent{Action: model.AuditActionAccountConnected, UserID: "bystander", ActorEmail: "other@acme.com"})

	n, err := s.AuditLogAnonymiseUser(ctx, "target")
	if err != nil {
		t.Fatalf("AnonymiseUser: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 rows anonymised, got %d", n)
	}

	events, _ := s.AuditLogList(ctx, model.AuditFilter{})
	for _, e := range events {
		switch e.Action {
		case model.AuditActionAccountConnected:
			if e.UserID != "bystander" || e.ActorEmail != "other@acme.com" {
				t.Errorf("bystander row was modified: %+v", e)
			}
		default:
			if e.UserID != "" {
				t.Errorf("target row user_id should be empty, got %q", e.UserID)
			}
			if e.ActorEmail != "deleted-user" {
				t.Errorf("target row actor_email: got %q, want 'deleted-user'", e.ActorEmail)
			}
		}
	}
}

func TestAuditLog_MissingTenant_Errors(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.AuditLogWrite(context.Background(), model.AuditEvent{Action: model.AuditActionDismissZombie}); err == nil {
		t.Error("expected error when tenant_id missing from ctx, got nil")
	}
	if _, err := s.AuditLogList(context.Background(), model.AuditFilter{}); err == nil {
		t.Error("expected list error when tenant_id missing from ctx, got nil")
	}
}
