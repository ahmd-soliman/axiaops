package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage/postgres"
)

// These tests pin the least-privilege runtime RLS-bypass role (axiaops_runtime,
// migration 029 + docs/AUTHENTICATION.md (§5)): it must bypass RLS across
// organizations (via per-table permissive policies, NOT the BYPASSRLS attribute
// — which RDS cannot grant), but must NOT be able to do DDL or own objects.
//
// They require RUNTIME_ADMIN_DATABASE_URL pointing at the axiaops_runtime role;
// TestMain's Bootstrap syncs its LOGIN + password from that URL. Skipped when
// the var is unset (e.g. a plain `go test` with only MIGRATION_DATABASE_URL).

func runtimeAdminURLOrSkip(t *testing.T) string {
	t.Helper()
	url := os.Getenv("RUNTIME_ADMIN_DATABASE_URL")
	if url == "" {
		t.Skip("RUNTIME_ADMIN_DATABASE_URL not set — skipping runtime-admin role tests")
	}
	return url
}

func connectRuntimeAdmin(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), runtimeAdminURLOrSkip(t))
	if err != nil {
		t.Fatalf("connect as axiaops_runtime: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

// TestRuntimeAdmin_BypassesRLSAcrossOrgs proves the role reads AND writes rows
// from every organization with no app.organization_id set, while the RLS-bound
// app role sees nothing without an org context.
func TestRuntimeAdmin_BypassesRLSAcrossOrgs(t *testing.T) {
	runtimeAdminURLOrSkip(t)
	s := newTestStore(t) // truncates tables, opens the store
	ctx := context.Background()

	ctx1, _ := newOrgCtx(t, s)
	ctx2, _ := newOrgCtx(t, s)
	if _, _, err := s.Save(ctx1, []model.CostRecord{costRecord("AmazonEC2", "eu-central-1", 100)}); err != nil {
		t.Fatalf("seed org1: %v", err)
	}
	if _, _, err := s.Save(ctx2, []model.CostRecord{costRecord("AmazonRDS", "eu-central-1", 200)}); err != nil {
		t.Fatalf("seed org2: %v", err)
	}

	rt := connectRuntimeAdmin(t)

	// Read: the runtime role sees both orgs' rows with no org context.
	var rtCount int
	if err := rt.QueryRow(ctx, `SELECT count(*) FROM axiaops.cost_records`).Scan(&rtCount); err != nil {
		t.Fatalf("runtime count: %v", err)
	}
	if rtCount != 2 {
		t.Errorf("runtime role should see both orgs' cost_records via the bypass policy; got %d, want 2", rtCount)
	}

	// Control: the app role with no org context sees nothing (RLS enforced).
	// Only meaningful when DATABASE_URL is the non-superuser app role.
	if rlsEnforced() {
		app, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
		if err != nil {
			t.Fatalf("connect as app role: %v", err)
		}
		defer func() { _ = app.Close(ctx) }()
		var appCount int
		if err := app.QueryRow(ctx, `SELECT count(*) FROM axiaops.cost_records`).Scan(&appCount); err != nil {
			t.Fatalf("app count: %v", err)
		}
		if appCount != 0 {
			t.Errorf("app role with no org context must see 0 rows (RLS), got %d", appCount)
		}
	}

	// Write: the runtime role updates + deletes across orgs with no org context.
	tag, err := rt.Exec(ctx, `UPDATE axiaops.cost_records SET amount = 999`)
	if err != nil {
		t.Fatalf("runtime cross-org UPDATE: %v", err)
	}
	if tag.RowsAffected() != 2 {
		t.Errorf("runtime UPDATE should touch both orgs' rows; affected %d, want 2", tag.RowsAffected())
	}
	tag, err = rt.Exec(ctx, `DELETE FROM axiaops.cost_records`)
	if err != nil {
		t.Fatalf("runtime cross-org DELETE: %v", err)
	}
	if tag.RowsAffected() != 2 {
		t.Errorf("runtime DELETE should touch both orgs' rows; affected %d, want 2", tag.RowsAffected())
	}
}

// TestRuntimeAdmin_CannotDDL proves the role has no schema-shaping power: it
// cannot create/drop/alter/truncate tables or create roles. Every attempt must
// fail with SQLSTATE 42501 (insufficient_privilege), not silently succeed.
func TestRuntimeAdmin_CannotDDL(t *testing.T) {
	rt := connectRuntimeAdmin(t)
	ctx := context.Background()
	cases := []struct{ name, stmt string }{
		{"create table", `CREATE TABLE axiaops.rt_probe (id int)`},
		{"drop table", `DROP TABLE axiaops.accounts`},
		{"alter table", `ALTER TABLE axiaops.accounts ADD COLUMN rt_probe int`},
		{"truncate", `TRUNCATE axiaops.accounts`},
		{"create role", `CREATE ROLE rt_probe_role`},
	}
	for _, tc := range cases {
		_, err := rt.Exec(ctx, tc.stmt)
		if err == nil {
			t.Errorf("%s: expected permission denied, got nil", tc.name)
			continue
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("%s: expected SQLSTATE 42501 (insufficient_privilege), got %v", tc.name, err)
		}
	}
}

// TestRuntimeAdmin_CanDeleteAuditLog pins that the runtime role holds DELETE on
// audit_log, which the GDPR org-cascade purge (DELETE /v1/organizations/me, run
// on the bypass pool) needs.
//
// NB: the app role (axiaops) ALSO holds DELETE on audit_log in practice — the
// 000_init ALTER DEFAULT PRIVILEGES grant fires when audit_log is created and
// nothing revokes it, so the "no DELETE for the app role" intent documented
// elsewhere was never actually enforced. The runtime role's DELETE is therefore no new
// exposure; tightening the app role's grant to that intent is out of scope here.
func TestRuntimeAdmin_CanDeleteAuditLog(t *testing.T) {
	runtimeAdminURLOrSkip(t)
	conn := connectTestDB(t) // owner
	defer func() { _ = conn.Close(context.Background()) }()

	var runtimeCan bool
	if err := conn.QueryRow(context.Background(),
		`SELECT has_table_privilege('axiaops_runtime', 'axiaops.audit_log', 'DELETE')`).Scan(&runtimeCan); err != nil {
		t.Fatalf("runtime audit_log DELETE priv: %v", err)
	}
	if !runtimeCan {
		t.Error("axiaops_runtime must have DELETE on audit_log (GDPR org-cascade purge)")
	}
}

// TestRuntimeAdmin_PolicyCoversAllRLSTables is the invariant that guards future
// migrations: every RLS-enabled table in the axiaops schema must carry a
// <table>_runtime_bypass policy, or the runtime role silently gets zero rows
// there. If this fails, add the policy in the migration that created the table.
func TestRuntimeAdmin_PolicyCoversAllRLSTables(t *testing.T) {
	runtimeAdminURLOrSkip(t)
	conn := connectTestDB(t) // owner
	defer func() { _ = conn.Close(context.Background()) }()

	rows, err := conn.Query(context.Background(), `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'axiaops' AND c.relkind = 'r' AND c.relrowsecurity
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_policies p
		      WHERE p.schemaname = 'axiaops'
		        AND p.tablename = c.relname
		        AND p.policyname = c.relname || '_runtime_bypass'
		  )
		ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("query RLS tables missing bypass policy: %v", err)
	}
	defer rows.Close()

	var missing []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		missing = append(missing, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(missing) > 0 {
		t.Errorf("RLS-enabled tables missing a _runtime_bypass policy (add it in the table's migration): %v", missing)
	}
}

// TestNewWithRuntimeAdmin_Probe covers the constructor seam (resolves
// TODO(#107)): it succeeds against the real runtime URL and its readiness Ping
// fails fast on an unreachable bypass connection rather than deferring the
// failure to the first cross-org read.
func TestNewWithRuntimeAdmin_Probe(t *testing.T) {
	rtURL := runtimeAdminURLOrSkip(t)
	appURL := storeURL(t)
	ctx := context.Background()

	s, err := postgres.NewWithRuntimeAdmin(ctx, appURL, rtURL)
	if err != nil {
		t.Fatalf("NewWithRuntimeAdmin with valid runtime URL: %v", err)
	}
	_ = s.Close()

	_, err = postgres.NewWithRuntimeAdmin(ctx, appURL,
		"postgres://axiaops_runtime:wrong@127.0.0.1:1/axiaops?sslmode=disable")
	if err == nil {
		t.Error("NewWithRuntimeAdmin should fail the readiness probe on an unreachable runtime URL")
	}
}
