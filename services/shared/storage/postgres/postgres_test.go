package postgres_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
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
		axiaops.sessions,
		axiaops.password_resets,
		axiaops.bootstrap_state,
		axiaops.users,
		axiaops.organizations
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
// Tests that rely on organization isolation skip when connecting as a superuser.
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
		runtimeAdminURL := os.Getenv("RUNTIME_ADMIN_DATABASE_URL")
		if err := postgres.Bootstrap(migrationURL, appURL, runtimeAdminURL); err != nil {
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

// newOrgCtx creates a fresh organization and returns a context carrying its ID.
// Each test gets its own organization so RLS isolates test data naturally.
func newOrgCtx(t *testing.T, s *postgres.Store) (context.Context, model.Organization) {
	t.Helper()
	ctx := context.Background()
	orgCode := "test-org-" + uuid.New().String()
	org, err := s.UpsertOrganization(ctx, orgCode, "Test Org")
	if err != nil {
		t.Fatalf("UpsertOrganization: %v", err)
	}
	return storage.WithOrganizationID(ctx, org.ID), org
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
	ctx, _ := newOrgCtx(t, s)

	records := []model.CostRecord{
		costRecord("AmazonEC2", "eu-central-1", 100.00),
		costRecord("AmazonRDS", "eu-central-1", 200.00),
	}

	inserted, updated, err := s.Save(ctx, records)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if inserted != 2 || updated != 0 {
		t.Errorf("expected inserted=2 updated=0, got inserted=%d updated=%d", inserted, updated)
	}
}

func TestSave_SecondCallUpdatesExisting(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

	records := []model.CostRecord{costRecord("AmazonEC2", "eu-central-1", 100.00)}

	inserted, updated, err := s.Save(ctx, records)
	if err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if inserted != 1 || updated != 0 {
		t.Errorf("first call: expected inserted=1 updated=0, got inserted=%d updated=%d", inserted, updated)
	}

	inserted, updated, err = s.Save(ctx, records)
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if inserted != 0 || updated != 1 {
		t.Errorf("second call: expected inserted=0 updated=1, got inserted=%d updated=%d", inserted, updated)
	}
}

func TestListCostRecords_AbsoluteWindow(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

	// Five daily records; distinct period_start (and resource_id) so none
	// collide on the upsert conflict key.
	days := []int{1, 5, 10, 15, 20}
	var records []model.CostRecord
	for i, d := range days {
		r := costRecord("AmazonEC2", "eu-central-1", 10.0)
		r.ResourceID = fmt.Sprintf("res-%02d", d)
		r.PeriodStart = time.Date(2026, 3, d, 0, 0, 0, 0, time.UTC)
		r.PeriodEnd = time.Date(2026, 3, d+1, 0, 0, 0, 0, time.UTC)
		records = append(records, r)
		_ = i
	}
	if _, _, err := s.Save(ctx, records); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Absolute window 03-05 .. 03-15 (both inclusive) → exactly 03-05/10/15.
	got, err := s.ListCostRecords(ctx, storage.CostFilter{
		Since: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
		// Days is intentionally left unset to prove Since/Until take precedence —
		// the fixture dates are years in the past, so any trailing window would
		// return nothing.
	})
	if err != nil {
		t.Fatalf("ListCostRecords: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 records in [03-05, 03-15], got %d", len(got))
	}
	for _, r := range got {
		day := r.PeriodStart.Day()
		if day < 5 || day > 15 {
			t.Errorf("record outside window leaked: period_start day %d", day)
		}
	}
}

func TestSave_EmptyBatch(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

	inserted, updated, err := s.Save(ctx, nil)
	if err != nil {
		t.Fatalf("Save with nil: %v", err)
	}
	if inserted != 0 || updated != 0 {
		t.Errorf("expected 0/0 for empty batch, got inserted=%d updated=%d", inserted, updated)
	}
}

func TestSave_DifferentRegionIsNotDuplicate(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

	records := []model.CostRecord{
		costRecord("AmazonEC2", "eu-central-1", 100.00),
		costRecord("AmazonEC2", "eu-west-1", 100.00),
	}

	inserted, updated, err := s.Save(ctx, records)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if inserted != 2 || updated != 0 {
		t.Errorf("expected inserted=2 updated=0 (different regions), got inserted=%d updated=%d", inserted, updated)
	}
}

func TestSave_MissingOrganizationID_Errors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background() // no organization in context

	_, _, err := s.Save(ctx, []model.CostRecord{costRecord("AmazonEC2", "eu-central-1", 10)})
	if err == nil {
		t.Error("expected error when organization_id missing from context, got nil")
	}
}

// readBackCostRecord queries the row identified by the conflict key via the
// owner-role connection so the test sees the row independently of RLS. Returns
// id, amount, and internal_account_id. Used by the upsert tests below.
func readBackCostRecord(t *testing.T, orgID, service, region, resourceID string) (id string, amount float64, internalAccountID *string) {
	t.Helper()
	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()
	err := conn.QueryRow(context.Background(), `
		SELECT id::text, amount, internal_account_id
		FROM axiaops.cost_records
		WHERE organization_id = $1 AND provider = 'aws' AND account_id = '000000000000'
		  AND service = $2 AND region = $3 AND resource_id = $4
		  AND period_start = '2026-03-01'::date AND period_end = '2026-03-31'::date`,
		orgID, service, region, resourceID,
	).Scan(&id, &amount, &internalAccountID)
	if err != nil {
		t.Fatalf("readBackCostRecord: %v", err)
	}
	return
}

func TestSave_UpsertWinsLatest(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	first := costRecord("AmazonEC2", "eu-central-1", 0.33)
	if _, _, err := s.Save(ctx, []model.CostRecord{first}); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	second := costRecord("AmazonEC2", "eu-central-1", 4.03) // same conflict key, late-settled amount
	inserted, updated, err := s.Save(ctx, []model.CostRecord{second})
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if inserted != 0 || updated != 1 {
		t.Errorf("expected inserted=0 updated=1, got inserted=%d updated=%d", inserted, updated)
	}

	_, amount, _ := readBackCostRecord(t, org.ID, "AmazonEC2", "eu-central-1", "res-001")
	if amount != 4.03 {
		t.Errorf("expected amount=4.03 after upsert, got %v", amount)
	}
}

func TestSave_UpsertPreservesID(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	first := costRecord("AmazonRDS", "eu-central-1", 100.00)
	if _, _, err := s.Save(ctx, []model.CostRecord{first}); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	firstID, _, _ := readBackCostRecord(t, org.ID, "AmazonRDS", "eu-central-1", "res-001")

	second := costRecord("AmazonRDS", "eu-central-1", 200.00)
	if _, _, err := s.Save(ctx, []model.CostRecord{second}); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	secondID, _, _ := readBackCostRecord(t, org.ID, "AmazonRDS", "eu-central-1", "res-001")

	if firstID != secondID {
		t.Errorf("upsert changed row id: first=%s second=%s — DO UPDATE must preserve the original primary key", firstID, secondID)
	}
}

func TestSave_UpsertPreservesInternalAccountID(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	internal := "internal-acct-abc"
	first := costRecord("AmazonElastiCache", "eu-central-1", 50.00)
	first.InternalAccountID = &internal
	if _, _, err := s.Save(ctx, []model.CostRecord{first}); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	// Second write has the field nil — COALESCE in the upsert clause must preserve the stored value.
	second := costRecord("AmazonElastiCache", "eu-central-1", 75.00)
	second.InternalAccountID = nil
	if _, _, err := s.Save(ctx, []model.CostRecord{second}); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	_, amount, gotInternal := readBackCostRecord(t, org.ID, "AmazonElastiCache", "eu-central-1", "res-001")
	if amount != 75.00 {
		t.Errorf("expected amount refreshed to 75.00, got %v", amount)
	}
	if gotInternal == nil || *gotInternal != internal {
		t.Errorf("expected internal_account_id preserved as %q, got %v", internal, gotInternal)
	}
}

func TestSave_UpsertRLSIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("DATABASE_URL not set — RLS isolation only meaningful against the app role")
	}
	s := newTestStore(t)
	ctxA, orgA := newOrgCtx(t, s)
	ctxB, orgB := newOrgCtx(t, s)

	// Both orgs upsert the same conflict-key shape but with different amounts.
	a := costRecord("AmazonELB", "eu-central-1", 11.00)
	b := costRecord("AmazonELB", "eu-central-1", 22.00)

	if _, _, err := s.Save(ctxA, []model.CostRecord{a}); err != nil {
		t.Fatalf("orgA Save: %v", err)
	}
	if _, _, err := s.Save(ctxB, []model.CostRecord{b}); err != nil {
		t.Fatalf("orgB Save: %v", err)
	}

	// Each org's row must be untouched by the other's write.
	_, amountA, _ := readBackCostRecord(t, orgA.ID, "AmazonELB", "eu-central-1", "res-001")
	_, amountB, _ := readBackCostRecord(t, orgB.ID, "AmazonELB", "eu-central-1", "res-001")
	if amountA != 11.00 {
		t.Errorf("orgA amount: expected 11.00, got %v (RLS leaked org B's write into org A)", amountA)
	}
	if amountB != 22.00 {
		t.Errorf("orgB amount: expected 22.00, got %v (RLS leaked org A's write into org B)", amountB)
	}
}

func TestSave_ConcurrentUpsertLastWriterWins(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	// Seed the row so both goroutines hit the UPDATE path (cleanest race shape).
	seed := costRecord("AmazonVPC", "eu-central-1", 0.10)
	if _, _, err := s.Save(ctx, []model.CostRecord{seed}); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	a := costRecord("AmazonVPC", "eu-central-1", 100.00)
	b := costRecord("AmazonVPC", "eu-central-1", 200.00)

	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		_, _, err := s.Save(ctx, []model.CostRecord{a})
		done <- err
	}()
	go func() {
		<-start
		_, _, err := s.Save(ctx, []model.CostRecord{b})
		done <- err
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent Save: %v", err)
		}
	}

	// Both transactions committed. There must be exactly one row, and its
	// amount must match one of the two writers — never a torn read of a
	// mixed state.
	_, amount, _ := readBackCostRecord(t, org.ID, "AmazonVPC", "eu-central-1", "res-001")
	if amount != 100.00 && amount != 200.00 {
		t.Errorf("expected amount in {100.00, 200.00}, got %v", amount)
	}

	// Assert there is exactly one row for the conflict key — DO UPDATE must
	// never create a duplicate even under contention.
	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()
	var count int
	if err := conn.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM axiaops.cost_records
		WHERE organization_id = $1 AND service = 'AmazonVPC' AND region = 'eu-central-1' AND resource_id = 'res-001'
		  AND period_start = '2026-03-01'::date AND period_end = '2026-03-31'::date`,
		org.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 row for the conflict key, got %d", count)
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
	ctx, _ := newOrgCtx(t, s)

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
	ctx, _ := newOrgCtx(t, s)

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
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS to filter out other organizations' data")
	}
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

	zombies, err := s.LoadZombies(ctx)
	if err != nil {
		t.Fatalf("LoadZombies: %v", err)
	}
	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for new organization, got %d", len(zombies))
	}
}

// ── Organization isolation (RLS) ────────────────────────────────────────────────────

func TestZombies_OrganizationIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS enforcement")
	}
	s := newTestStore(t)

	ctxA, _ := newOrgCtx(t, s)
	ctxB, _ := newOrgCtx(t, s)

	// Organization A saves zombies.
	if err := s.SaveZombies(ctxA, []model.ZombieResource{zombieResource("AmazonEC2", 100)}); err != nil {
		t.Fatalf("SaveZombies organization A: %v", err)
	}

	// Organization B should see none.
	zombiesB, err := s.LoadZombies(ctxB)
	if err != nil {
		t.Fatalf("LoadZombies organization B: %v", err)
	}
	if len(zombiesB) != 0 {
		t.Errorf("organization B should see 0 zombies, got %d", len(zombiesB))
	}
}

// ── UpsertOrganization ─────────────────────────────────────────────────────────────

func TestUpsertOrganization_CreatesOnFirstCall(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	org, err := s.UpsertOrganization(ctx, "org_"+uuid.New().String(), "Acme Corp")
	if err != nil {
		t.Fatalf("UpsertOrganization: %v", err)
	}
	if org.ID == "" {
		t.Error("expected non-empty organization ID")
	}
	if org.Name != "Acme Corp" {
		t.Errorf("expected name Acme Corp, got %s", org.Name)
	}
}

func TestUpsertOrganization_ReturnsSameIDOnSecondCall(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	orgCode := "org_" + uuid.New().String()

	first, err := s.UpsertOrganization(ctx, orgCode, "Acme Corp")
	if err != nil {
		t.Fatalf("first UpsertOrganization: %v", err)
	}
	second, err := s.UpsertOrganization(ctx, orgCode, "Acme Corp")
	if err != nil {
		t.Fatalf("second UpsertOrganization: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected same organization ID, got %s and %s", first.ID, second.ID)
	}
}

// TestUpsertOrganization_PreservesLocalName is the regression guard for a
// name-clobber bug: once an organization
// row exists, AxiaOps owns the `name` field — subsequent UpsertOrganization
// calls (which run on every authenticated request via the auth middleware)
// must not overwrite a local rename with whatever org_name claim Kinde sent.
// Renames go through PATCH /v1/organizations/me + RenameOrganization.
func TestUpsertOrganization_PreservesLocalName(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	orgCode := "org_" + uuid.New().String()

	first, err := s.UpsertOrganization(ctx, orgCode, "Default")
	if err != nil {
		t.Fatalf("first UpsertOrganization: %v", err)
	}
	if first.Name != "Default" {
		t.Fatalf("first insert: expected Default, got %s", first.Name)
	}

	// Simulate a local rename (the path PATCH /v1/organizations/me would take).
	rctx := storage.WithOrganizationID(ctx, first.ID)
	if err := s.RenameOrganization(rctx, "Acme Corp"); err != nil {
		t.Fatalf("RenameOrganization: %v", err)
	}

	// A subsequent UpsertOrganization (the auth middleware runs this on every
	// request) carrying a different name — must NOT clobber the local rename.
	preserved, err := s.UpsertOrganization(ctx, orgCode, "Some Other Name From JWT")
	if err != nil {
		t.Fatalf("second UpsertOrganization: %v", err)
	}
	if preserved.Name != "Acme Corp" {
		t.Errorf("expected local rename preserved (Acme Corp), got %s", preserved.Name)
	}
}

// ── UpsertUser ────────────────────────────────────────────────────────────────

func TestUpsertUser_CreatesOnFirstLogin(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	user, err := s.UpsertUser(ctx, org.ID, "kp_"+uuid.New().String(), "alice@acme.com", "Alice")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if user.ID == "" {
		t.Error("expected non-empty user ID")
	}
	if user.OrganizationID != org.ID {
		t.Errorf("expected organization_id %s, got %s", org.ID, user.OrganizationID)
	}
	if user.Email != "alice@acme.com" {
		t.Errorf("expected email alice@acme.com, got %s", user.Email)
	}
}

func TestUpsertUser_ReturnsSameIDOnSecondLogin(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	externalID := "kp_" + uuid.New().String()

	first, err := s.UpsertUser(ctx, org.ID, externalID, "alice@acme.com", "Alice")
	if err != nil {
		t.Fatalf("first UpsertUser: %v", err)
	}
	second, err := s.UpsertUser(ctx, org.ID, externalID, "alice@acme.com", "Alice")
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
	_, org := newOrgCtx(t, s)

	u := model.User{ID: "dev-user-" + uuid.New().String(), OrganizationID: org.ID, Email: "dev@axiaops.local", Name: "Dev User"}
	if err := s.EnsureUser(context.Background(), u); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()
	var externalID, email string
	err := conn.QueryRow(context.Background(),
		`SELECT external_id, email FROM axiaops.users WHERE id = $1`, u.ID,
	).Scan(&externalID, &email)
	if err != nil {
		t.Fatalf("fetch inserted row: %v", err)
	}
	if want := "dev:" + u.ID; externalID != want {
		t.Errorf("external_id: got %q, want %q", externalID, want)
	}
	if email != u.Email {
		t.Errorf("email: got %q, want %q", email, u.Email)
	}
}

func TestEnsureUser_UpdatesOnConflict(t *testing.T) {
	s := newTestStore(t)
	_, org1 := newOrgCtx(t, s)
	_, org2 := newOrgCtx(t, s)

	id := "dev-user-" + uuid.New().String()
	if err := s.EnsureUser(context.Background(), model.User{ID: id, OrganizationID: org1.ID, Email: "old@axiaops.local", Name: "Old"}); err != nil {
		t.Fatalf("first EnsureUser: %v", err)
	}
	if err := s.EnsureUser(context.Background(), model.User{ID: id, OrganizationID: org2.ID, Email: "new@axiaops.local", Name: "New"}); err != nil {
		t.Fatalf("second EnsureUser: %v", err)
	}

	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()
	var organizationID, email, name string
	err := conn.QueryRow(context.Background(),
		`SELECT organization_id, email, name FROM axiaops.users WHERE id = $1`, id,
	).Scan(&organizationID, &email, &name)
	if err != nil {
		t.Fatalf("fetch row: %v", err)
	}
	if organizationID != org2.ID {
		t.Errorf("organization_id: got %q, want %q (self-correcting update)", organizationID, org2.ID)
	}
	if email != "new@axiaops.local" {
		t.Errorf("email: got %q, want %q", email, "new@axiaops.local")
	}
}

// ── Account CRUD ──────────────────────────────────────────────────────────────

func testAccount(organizationID string) model.Account {
	return model.Account{
		ID:                uuid.New().String(),
		OrganizationID:    organizationID,
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
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS to scope ListAccounts to this organization")
	}
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	a := testAccount(org.ID)
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
	ctx, org := newOrgCtx(t, s)

	a := testAccount(org.ID)
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
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS to scope ListAccounts to this organization")
	}
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	a := testAccount(org.ID)
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
	ctx, org := newOrgCtx(t, s)

	a := testAccount(org.ID)
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
	ctx, org := newOrgCtx(t, s)

	a := testAccount(org.ID)
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

func TestAccount_OrganizationIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS enforcement")
	}
	s := newTestStore(t)
	ctxA, orgA := newOrgCtx(t, s)
	ctxB, _ := newOrgCtx(t, s)

	a := testAccount(orgA.ID)
	if err := s.SaveAccount(ctxA, a); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}

	accountsB, err := s.ListAccounts(ctxB)
	if err != nil {
		t.Fatalf("ListAccounts organization B: %v", err)
	}
	if len(accountsB) != 0 {
		t.Errorf("organization B should see 0 accounts, got %d", len(accountsB))
	}
}

func TestAccount_ScanIntervalHours(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	a := testAccount(org.ID)
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

// ── Role-based accounts (cross-account IAM role onboarding) ───────────────────

func testRoleAccount(organizationID string) model.Account {
	return model.Account{
		ID:                uuid.New().String(),
		OrganizationID:    organizationID,
		Provider:          "aws",
		Label:             "role account",
		AuthMethod:        model.AuthMethodRole,
		RoleARN:           "arn:aws:iam::123456789012:role/AxiaOpsIntegrationRole",
		ExternalID:        "axiaops-ext-9f2a4d1e8b73",
		Region:            "eu-central-1",
		Status:            model.AccountStatusConnected,
		ScanIntervalHours: 24,
		CreatedAt:         time.Now().UTC(),
	}
}

func TestAccount_RoleAuth_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	a := testRoleAccount(org.ID)
	if err := s.SaveAccount(ctx, a); err != nil {
		t.Fatalf("SaveAccount role: %v", err)
	}

	got, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount role: %v", err)
	}
	if got.AuthMethod != model.AuthMethodRole {
		t.Errorf("AuthMethod = %q, want %q", got.AuthMethod, model.AuthMethodRole)
	}
	if got.RoleARN != a.RoleARN {
		t.Errorf("RoleARN = %q, want %q", got.RoleARN, a.RoleARN)
	}
	if got.ExternalID != a.ExternalID {
		t.Errorf("ExternalID = %q, want %q", got.ExternalID, a.ExternalID)
	}
	if got.AccessKeyID != "" || got.SecretEncrypted != "" {
		t.Errorf("role-based account leaked access-key fields: AccessKeyID=%q SecretEncrypted=%q",
			got.AccessKeyID, got.SecretEncrypted)
	}
}

func TestAccount_AccessKey_DefaultAuthMethod(t *testing.T) {
	// testAccount() leaves AuthMethod empty. The SaveAccount SQL must default
	// it to 'access_key' so legacy callers (pre-MR3) keep working.
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	a := testAccount(org.ID)
	if err := s.SaveAccount(ctx, a); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}

	got, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.AuthMethod != model.AuthMethodAccessKey {
		t.Errorf("AuthMethod = %q, want %q", got.AuthMethod, model.AuthMethodAccessKey)
	}
	if got.RoleARN != "" || got.ExternalID != "" {
		t.Errorf("access-key account leaked role fields: RoleARN=%q ExternalID=%q",
			got.RoleARN, got.ExternalID)
	}
}

func TestAccount_RoleDraft_PendingStatus(t *testing.T) {
	// Drafts have ExternalID populated but RoleARN empty until verify lands.
	// The accounts_role_fields_present CHECK permits this only when
	// status='pending_role_setup'.
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	a := testRoleAccount(org.ID)
	a.RoleARN = ""
	a.Status = model.AccountStatusPendingRoleSetup

	if err := s.SaveAccount(ctx, a); err != nil {
		t.Fatalf("SaveAccount draft: %v", err)
	}

	got, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.Status != model.AccountStatusPendingRoleSetup {
		t.Errorf("Status = %q, want %q", got.Status, model.AccountStatusPendingRoleSetup)
	}
	if got.RoleARN != "" {
		t.Errorf("RoleARN should be empty for draft, got %q", got.RoleARN)
	}
	if got.ExternalID == "" {
		t.Error("ExternalID must be set on draft")
	}
}

func TestAccount_RoleAuth_RejectsMissingExternalID(t *testing.T) {
	// accounts_role_fields_present must reject auth_method='role' rows that
	// lack an external_id.
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	a := testRoleAccount(org.ID)
	a.ExternalID = ""

	err := s.SaveAccount(ctx, a)
	if err == nil {
		t.Fatal("SaveAccount should reject role account with empty external_id")
	}
	// Postgres returns the constraint name in the error message.
	if !strings.Contains(err.Error(), "accounts_role_fields_present") {
		t.Errorf("expected accounts_role_fields_present violation, got: %v", err)
	}
}

func TestAccount_RoleAuth_RejectsNonAWSProvider(t *testing.T) {
	// accounts_role_only_for_aws keeps Azure/GCP rows out of the role auth
	// method until those providers grow their own onboarding shape.
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	a := testRoleAccount(org.ID)
	a.Provider = "azure"

	err := s.SaveAccount(ctx, a)
	if err == nil {
		t.Fatal("SaveAccount should reject role account with provider=azure")
	}
	if !strings.Contains(err.Error(), "accounts_role_only_for_aws") {
		t.Errorf("expected accounts_role_only_for_aws violation, got: %v", err)
	}
}

func TestAccount_RoleAuth_RejectsInvalidAuthMethod(t *testing.T) {
	// "oidc" trips both accounts_auth_method_check (value not in the IN list)
	// and accounts_access_key_fields_present (value is neither 'access_key'
	// nor 'role', so neither branch satisfies the constraint). Postgres only
	// reports the first violation it hits, and the ordering is not stable —
	// assert that *some* check rejects the row, which is what we actually care
	// about.
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	a := testAccount(org.ID)
	a.AuthMethod = "oidc"

	err := s.SaveAccount(ctx, a)
	if err == nil {
		t.Fatal("SaveAccount should reject unknown auth_method")
	}
	if !strings.Contains(err.Error(), "check constraint") {
		t.Errorf("expected a CHECK constraint violation, got: %v", err)
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
	ctx, _ := newOrgCtx(t, s)

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
	ctx, _ := newOrgCtx(t, s)

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

func zombieSnapshot(organizationID, accountID string, cost float64, zombieCount int) model.ZombieSnapshot {
	return model.ZombieSnapshot{
		ID:               uuid.New().String(),
		OrganizationID:   organizationID,
		AccountID:        accountID,
		SnapshotAt:       time.Now().UTC(),
		ZombieCount:      zombieCount,
		TotalMonthlyCost: cost,
		Currency:         "USD",
	}
}

func TestSaveSnapshot_ListSnapshots_Roundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	snap := zombieSnapshot(org.ID, "acc-001", 150.00, 3)
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
	ctx, org := newOrgCtx(t, s)

	// Insert three snapshots with explicit timestamps spread one hour apart.
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	snapsToSave := []model.ZombieSnapshot{
		{ID: uuid.New().String(), OrganizationID: org.ID, AccountID: "acc-1", SnapshotAt: base.Add(2 * time.Hour), ZombieCount: 5, TotalMonthlyCost: 500, Currency: "USD"},
		{ID: uuid.New().String(), OrganizationID: org.ID, AccountID: "acc-1", SnapshotAt: base, ZombieCount: 1, TotalMonthlyCost: 100, Currency: "USD"},
		{ID: uuid.New().String(), OrganizationID: org.ID, AccountID: "acc-1", SnapshotAt: base.Add(time.Hour), ZombieCount: 3, TotalMonthlyCost: 300, Currency: "USD"},
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
	ctx, org := newOrgCtx(t, s)

	// Two snapshots for acc-A, one for acc-B.
	if err := s.SaveSnapshot(ctx, zombieSnapshot(org.ID, "acc-A", 100, 2)); err != nil {
		t.Fatalf("SaveSnapshot acc-A first: %v", err)
	}
	if err := s.SaveSnapshot(ctx, zombieSnapshot(org.ID, "acc-A", 200, 4)); err != nil {
		t.Fatalf("SaveSnapshot acc-A second: %v", err)
	}
	if err := s.SaveSnapshot(ctx, zombieSnapshot(org.ID, "acc-B", 50, 1)); err != nil {
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
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS to filter out other organizations' snapshots")
	}
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

	snaps, err := s.ListSnapshots(ctx, "")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("expected 0 snapshots for new organization, got %d", len(snaps))
	}
}

func TestSaveSnapshot_MissingOrganizationID_Errors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background() // no organization in context

	snap := model.ZombieSnapshot{
		ID:               uuid.New().String(),
		AccountID:        "acc-1",
		SnapshotAt:       time.Now().UTC(),
		ZombieCount:      1,
		TotalMonthlyCost: 50.00,
		Currency:         "USD",
	}
	if err := s.SaveSnapshot(ctx, snap); err == nil {
		t.Error("expected error when organization_id missing from context, got nil")
	}
}

func TestListSnapshots_MissingOrganizationID_Errors(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background() // no organization in context

	if _, err := s.ListSnapshots(ctx, ""); err == nil {
		t.Error("expected error when organization_id missing from context, got nil")
	}
}

func TestSnapshot_OrganizationIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL (non-superuser) for RLS enforcement")
	}
	s := newTestStore(t)

	ctxA, orgA := newOrgCtx(t, s)
	ctxB, _ := newOrgCtx(t, s)

	// Organization A saves a snapshot.
	if err := s.SaveSnapshot(ctxA, zombieSnapshot(orgA.ID, "acc-1", 100, 2)); err != nil {
		t.Fatalf("SaveSnapshot organization A: %v", err)
	}

	// Organization B should see none of Organization A's snapshots.
	snapsB, err := s.ListSnapshots(ctxB, "")
	if err != nil {
		t.Fatalf("ListSnapshots organization B: %v", err)
	}
	if len(snapsB) != 0 {
		t.Errorf("organization B should see 0 snapshots, got %d", len(snapsB))
	}
}

func TestSaveSnapshot_AccumulatesAcrossScans(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	// Simulate three consecutive scans — unlike zombie_records, snapshots must not be replaced.
	for i := 1; i <= 3; i++ {
		snap := zombieSnapshot(org.ID, "acc-1", float64(i)*100, i)
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
	ctx, _ := newOrgCtx(t, s)

	old := costRecord("AmazonEC2", "eu-central-1", 10.00)
	old.PeriodEnd = time.Now().UTC().AddDate(0, 0, -100)
	old.PeriodStart = old.PeriodEnd.AddDate(0, 0, -30)

	recent := costRecord("AmazonRDS", "eu-central-1", 20.00)
	recent.PeriodEnd = time.Now().UTC().AddDate(0, 0, -10)
	recent.PeriodStart = recent.PeriodEnd.AddDate(0, 0, -30)

	if _, _, err := s.Save(ctx, []model.CostRecord{old, recent}); err != nil {
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
	ctx, _ := newOrgCtx(t, s)

	recent := costRecord("AmazonEC2", "eu-central-1", 50.00)
	recent.PeriodEnd = time.Now().UTC().AddDate(0, 0, -10)
	recent.PeriodStart = recent.PeriodEnd.AddDate(0, 0, -30)
	if _, _, err := s.Save(ctx, []model.CostRecord{recent}); err != nil {
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
	ctx, org := newOrgCtx(t, s)

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
	// organization_id column should equal the organization we wrote under.
	if events[0].OrganizationID != org.ID {
		t.Errorf("organization_id: got %q, want %q", events[0].OrganizationID, org.ID)
	}
}

// Regression test: INET columns must round-trip through AuditLogList. The
// original implementation cast ip_address via the default binary codec, which
// pgx can't decode into the **string used for the nullable IP field. Fixture
// tests never populated an IP so the bug slipped past CI and surfaced only
// when the dashboard hit the real endpoint with a real client address.
func TestAuditLog_IPAddressRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

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

// actor_name is denormalised on write (migration 028). Three branches need
// coverage:
//   - caller supplies a name → persists verbatim
//   - caller supplies empty name → column stores '' (NOT NULL DEFAULT '')
//   - row written before column existed → existing rows still surface '' as
//     the read path projects the column with no fallback magic
//
// The frontend's fallback to actor_email depends on the empty-string branches
// behaving cleanly. A regression that drops the column from the SELECT would
// fail scan here loudly rather than as a UX glitch in production.
func TestAuditLog_CapturesActorNameOnWrite(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

	writeAudit(t, s, ctx, model.AuditEvent{
		Action:     model.AuditActionDismissZombie,
		UserID:     "user-alice",
		ActorEmail: "alice@acme.com",
		ActorName:  "Alice Engineer",
	})
	writeAudit(t, s, ctx, model.AuditEvent{
		Action:     model.AuditActionSnoozeZombie,
		UserID:     "user-bob",
		ActorEmail: "bob@acme.com",
		// ActorName intentionally omitted — '' must round-trip.
	})
	writeAudit(t, s, ctx, model.AuditEvent{
		Action:     model.AuditActionScanTriggered,
		ActorEmail: "system@axiaops.local",
		// No UserID + no name — system action shape.
	})

	events, err := s.AuditLogList(ctx, model.AuditFilter{})
	if err != nil {
		t.Fatalf("AuditLogList: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	byAction := map[string]model.AuditEvent{}
	for _, e := range events {
		byAction[e.Action] = e
	}
	if got := byAction[model.AuditActionDismissZombie].ActorName; got != "Alice Engineer" {
		t.Errorf("named actor: actor_name = %q, want %q", got, "Alice Engineer")
	}
	if got := byAction[model.AuditActionSnoozeZombie].ActorName; got != "" {
		t.Errorf("unnamed actor: actor_name = %q, want \"\"", got)
	}
	if got := byAction[model.AuditActionScanTriggered].ActorName; got != "" {
		t.Errorf("system action: actor_name = %q, want \"\"", got)
	}
}

func TestAuditLog_OrganizationIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("skipping: requires DATABASE_URL for RLS to scope queries")
	}
	s := newTestStore(t)
	ctxA, _ := newOrgCtx(t, s)
	ctxB, _ := newOrgCtx(t, s)

	writeAudit(t, s, ctxA, model.AuditEvent{Action: model.AuditActionDismissZombie, UserID: "u-a", ActorName: "Alice OrgA"})
	writeAudit(t, s, ctxB, model.AuditEvent{Action: model.AuditActionSnoozeZombie, UserID: "u-b"})

	aEvents, err := s.AuditLogList(ctxA, model.AuditFilter{})
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(aEvents) != 1 || aEvents[0].UserID != "u-a" {
		t.Errorf("organization A must see only its own rows, got %+v", aEvents)
	}
	if aEvents[0].ActorName != "Alice OrgA" {
		t.Errorf("org A's row should round-trip the captured actor_name, got %q", aEvents[0].ActorName)
	}
	bEvents, err := s.AuditLogList(ctxB, model.AuditFilter{})
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(bEvents) != 1 || bEvents[0].UserID != "u-b" {
		t.Errorf("organization B must see only its own rows, got %+v", bEvents)
	}
	// Sanity check: org A's captured name must not appear in org B's view —
	// because audit_log RLS blocks the entire row, not because of any read-side
	// JOIN guard. Field-level leakage isn't a category that exists in the
	// denormalised posture: there's no cross-table read.
	if bEvents[0].ActorName == "Alice OrgA" {
		t.Errorf("cross-org row leaked into org B: got %q", bEvents[0].ActorName)
	}
}

func TestAuditLog_Filters(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

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
	ctx, _ := newOrgCtx(t, s)

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
	ctx, _ := newOrgCtx(t, s)

	// Names are denormalised on write (migration 028), so they're captured
	// directly on the audit row — no need to seed users.name. The bystander's
	// name must survive untouched; the target's name must be cleared to ''
	// (parallel to actor_email → 'deleted-user').
	writeAudit(t, s, ctx, model.AuditEvent{
		Action: model.AuditActionDismissZombie, UserID: "target", ActorEmail: "target@acme.com", ActorName: "Target Person",
	})
	writeAudit(t, s, ctx, model.AuditEvent{
		Action: model.AuditActionSnoozeZombie, UserID: "target", ActorEmail: "target@acme.com", ActorName: "Target Person",
	})
	writeAudit(t, s, ctx, model.AuditEvent{
		Action: model.AuditActionAccountConnected, UserID: "bystander", ActorEmail: "other@acme.com", ActorName: "Bystander Person",
	})

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
			if e.ActorName != "Bystander Person" {
				t.Errorf("bystander actor_name should be untouched: got %q", e.ActorName)
			}
		default:
			if e.UserID != "" {
				t.Errorf("target row user_id should be empty, got %q", e.UserID)
			}
			if e.ActorEmail != "deleted-user" {
				t.Errorf("target row actor_email: got %q, want 'deleted-user'", e.ActorEmail)
			}
			// AnonymiseUser must also clear actor_name. If a future change removes
			// the actor_name=' '' assignment from the UPDATE statement, this row
			// would still carry "Target Person" — a GDPR violation, since the
			// column lives forever once written.
			if e.ActorName != "" {
				t.Errorf("anonymised row leaks actor_name: got %q, want \"\"", e.ActorName)
			}
		}
	}
}

func TestAuditLog_MissingOrganization_Errors(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.AuditLogWrite(context.Background(), model.AuditEvent{Action: model.AuditActionDismissZombie}); err == nil {
		t.Error("expected error when organization_id missing from ctx, got nil")
	}
	if _, err := s.AuditLogList(context.Background(), model.AuditFilter{}); err == nil {
		t.Error("expected list error when organization_id missing from ctx, got nil")
	}
}
