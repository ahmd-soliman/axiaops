package postgres_test

import (
	"context"
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
//	TEST_DATABASE_URL  — owner/admin URL used for migrations (axiaops_owner).
//	                     postgres://axiaops_owner:axiaops_owner@localhost:5432/axiaops?sslmode=disable
//
//	TEST_STORE_URL     — app user URL used for the Store (axiaops).
//	                     postgres://axiaops:axiaops@localhost:5432/axiaops?sslmode=disable
//	                     The axiaops user is a non-superuser, so RLS is enforced.
//	                     If omitted, TEST_DATABASE_URL is used (RLS isolation tests will be skipped).
//
// Run with:
//
//	TEST_DATABASE_URL=... TEST_STORE_URL=... go test ./storage/postgres/...
func storeURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("TEST_STORE_URL"); url != "" {
		return url
	}
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping postgres integration tests")
	}
	return url
}

// connectTestDB opens a pgx connection to TEST_DATABASE_URL (owner/admin user).
// Used only for setup/teardown truncation — not for Store operations.
func connectTestDB(t *testing.T) *pgx.Conn {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping postgres integration tests")
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
		axiaops.ghost_snapshots,
		axiaops.resource_records,
		axiaops.ghost_records,
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
		conn.Close(context.Background())
	})
	return conn
}

// rlsEnforced reports whether the store connects as a non-superuser (RLS is active).
// Tests that rely on tenant isolation skip when connecting as a superuser.
func rlsEnforced() bool {
	return os.Getenv("TEST_STORE_URL") != ""
}

// TestMain runs migrations once before all tests in this package.
func TestMain(m *testing.M) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url != "" {
		if err := postgres.Migrate(url); err != nil {
			panic("postgres: migration failed: " + err.Error())
		}
	}
	os.Exit(m.Run())
}

// newTestStore opens a postgres store and cleans database before test.
// Each test gets a fresh clean database state via truncation.
func newTestStore(t *testing.T) *postgres.Store {
	t.Helper()

	// Clean database before test starts
	setup(t)

	s, err := postgres.New(context.Background(), storeURL(t))
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
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

// ── SaveGhosts / LoadGhosts ───────────────────────────────────────────────────

func ghostResource(service string, cost float64) model.GhostResource {
	return model.GhostResource{
		Provider:    "aws",
		AccountID:   "000000000000",
		Service:     service,
		Region:      "eu-central-1",
		ResourceID:  "res-ghost-001",
		Tags:        map[string]string{"team": "platform"},
		MonthlyCost: cost,
		Currency:    "USD",
		PeriodStart: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		UsageMetric: "CPUUtilization",
		UsageAvg:    2.5,
		UsageUnit:   "Percent",
		Reason:      "Instance CPU below 5% — likely idle",
		Owner:       "platform",
	}
}

func TestSaveGhosts_LoadGhosts_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	ghosts := []model.GhostResource{
		ghostResource("AmazonEC2", 100.00),
		ghostResource("AmazonRDS", 200.00),
	}

	if err := s.SaveGhosts(ctx, ghosts); err != nil {
		t.Fatalf("SaveGhosts: %v", err)
	}

	loaded, err := s.LoadGhosts(ctx)
	if err != nil {
		t.Fatalf("LoadGhosts: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("expected 2 ghosts, got %d", len(loaded))
	}
}

func TestSaveGhosts_ReplacesOnSecondRun(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	if err := s.SaveGhosts(ctx, []model.GhostResource{
		ghostResource("AmazonEC2", 100.00),
		ghostResource("AmazonRDS", 200.00),
	}); err != nil {
		t.Fatalf("first SaveGhosts: %v", err)
	}

	// Second run with only one ghost — should replace, not append.
	if err := s.SaveGhosts(ctx, []model.GhostResource{
		ghostResource("AWSLambda", 50.00),
	}); err != nil {
		t.Fatalf("second SaveGhosts: %v", err)
	}

	loaded, err := s.LoadGhosts(ctx)
	if err != nil {
		t.Fatalf("LoadGhosts: %v", err)
	}
	if len(loaded) != 1 {
		t.Errorf("expected 1 ghost after replacement, got %d", len(loaded))
	}
	if loaded[0].Service != "AWSLambda" {
		t.Errorf("expected AWSLambda ghost, got %s", loaded[0].Service)
	}
}

func TestLoadGhosts_EmptyWhenNoneSaved(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires TEST_STORE_URL (non-superuser) for RLS to filter out other tenants' data")
	}
	s := newTestStore(t)
	ctx, _ := newTenantCtx(t, s)

	ghosts, err := s.LoadGhosts(ctx)
	if err != nil {
		t.Fatalf("LoadGhosts: %v", err)
	}
	if len(ghosts) != 0 {
		t.Errorf("expected 0 ghosts for new tenant, got %d", len(ghosts))
	}
}

// ── Tenant isolation (RLS) ────────────────────────────────────────────────────

func TestGhosts_TenantIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires TEST_STORE_URL (non-superuser) for RLS enforcement")
	}
	s := newTestStore(t)

	ctxA, _ := newTenantCtx(t, s)
	ctxB, _ := newTenantCtx(t, s)

	// Tenant A saves ghosts.
	if err := s.SaveGhosts(ctxA, []model.GhostResource{ghostResource("AmazonEC2", 100)}); err != nil {
		t.Fatalf("SaveGhosts tenant A: %v", err)
	}

	// Tenant B should see none.
	ghostsB, err := s.LoadGhosts(ctxB)
	if err != nil {
		t.Fatalf("LoadGhosts tenant B: %v", err)
	}
	if len(ghostsB) != 0 {
		t.Errorf("tenant B should see 0 ghosts, got %d", len(ghostsB))
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

// ── Account CRUD ──────────────────────────────────────────────────────────────

func testAccount(tenantID string) model.Account {
	return model.Account{
		ID:              uuid.New().String(),
		TenantID:        tenantID,
		Provider:        "aws",
		Label:           "dev account",
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretEncrypted: "encrypted-secret",
		Region:          "eu-central-1",
		Status:          "connected",
		CreatedAt:       time.Now().UTC(),
	}
}

func TestAccount_SaveAndList(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires TEST_STORE_URL (non-superuser) for RLS to scope ListAccounts to this tenant")
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
		t.Skip("skipping: requires TEST_STORE_URL (non-superuser) for RLS to scope ListAccounts to this tenant")
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
		t.Skip("skipping: requires TEST_STORE_URL (non-superuser) for RLS enforcement")
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

// ── SaveResources / LoadResources ─────────────────────────────────────────────

func resourceRecord(service string, isGhost bool) model.ResourceRecord {
	return model.ResourceRecord{
		Provider:    "aws",
		AccountID:   "000000000000",
		Service:     service,
		Region:      "eu-central-1",
		ResourceID:  "res-" + uuid.New().String(),
		Tags:        map[string]string{"team": "platform"},
		MonthlyCost: 100.00,
		Currency:    "USD",
		PeriodStart: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		UsageMetric: "CPUUtilization",
		UsageAvg:    2.5,
		UsageUnit:   "Percent",
		IsGhost:     isGhost,
		Reason:      "idle",
		Owner:       "platform",
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

func ghostSnapshot(tenantID, accountID string, cost float64, ghostCount int) model.GhostSnapshot {
	return model.GhostSnapshot{
		ID:               uuid.New().String(),
		TenantID:         tenantID,
		AccountID:        accountID,
		SnapshotAt:       time.Now().UTC(),
		GhostCount:       ghostCount,
		TotalMonthlyCost: cost,
		Currency:         "USD",
	}
}

func TestSaveSnapshot_ListSnapshots_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	snap := ghostSnapshot(tenant.ID, "acc-001", 150.00, 3)
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
	if got.GhostCount != 3 {
		t.Errorf("expected ghost_count 3, got %d", got.GhostCount)
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
	snapsToSave := []model.GhostSnapshot{
		{ID: uuid.New().String(), TenantID: tenant.ID, AccountID: "acc-1", SnapshotAt: base.Add(2 * time.Hour), GhostCount: 5, TotalMonthlyCost: 500, Currency: "USD"},
		{ID: uuid.New().String(), TenantID: tenant.ID, AccountID: "acc-1", SnapshotAt: base, GhostCount: 1, TotalMonthlyCost: 100, Currency: "USD"},
		{ID: uuid.New().String(), TenantID: tenant.ID, AccountID: "acc-1", SnapshotAt: base.Add(time.Hour), GhostCount: 3, TotalMonthlyCost: 300, Currency: "USD"},
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
	// Oldest-first: ghost_count should go 1 → 3 → 5.
	if loaded[0].GhostCount != 1 {
		t.Errorf("expected first (oldest) ghost_count 1, got %d", loaded[0].GhostCount)
	}
	if loaded[2].GhostCount != 5 {
		t.Errorf("expected last (newest) ghost_count 5, got %d", loaded[2].GhostCount)
	}
}

func TestListSnapshots_FilterByAccountID(t *testing.T) {
	s := newTestStore(t)
	ctx, tenant := newTenantCtx(t, s)

	// Two snapshots for acc-A, one for acc-B.
	if err := s.SaveSnapshot(ctx, ghostSnapshot(tenant.ID, "acc-A", 100, 2)); err != nil {
		t.Fatalf("SaveSnapshot acc-A first: %v", err)
	}
	if err := s.SaveSnapshot(ctx, ghostSnapshot(tenant.ID, "acc-A", 200, 4)); err != nil {
		t.Fatalf("SaveSnapshot acc-A second: %v", err)
	}
	if err := s.SaveSnapshot(ctx, ghostSnapshot(tenant.ID, "acc-B", 50, 1)); err != nil {
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
		t.Skip("skipping: requires TEST_STORE_URL (non-superuser) for RLS to filter out other tenants' snapshots")
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

	snap := model.GhostSnapshot{
		ID:               uuid.New().String(),
		AccountID:        "acc-1",
		SnapshotAt:       time.Now().UTC(),
		GhostCount:       1,
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
		t.Skip("skipping: requires TEST_STORE_URL (non-superuser) for RLS enforcement")
	}
	s := newTestStore(t)

	ctxA, tenantA := newTenantCtx(t, s)
	ctxB, _ := newTenantCtx(t, s)

	// Tenant A saves a snapshot.
	if err := s.SaveSnapshot(ctxA, ghostSnapshot(tenantA.ID, "acc-1", 100, 2)); err != nil {
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

	// Simulate three consecutive scans — unlike ghost_records, snapshots must not be replaced.
	for i := 1; i <= 3; i++ {
		snap := ghostSnapshot(tenant.ID, "acc-1", float64(i)*100, i)
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
