package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestUsersRLS_AppRoleIsolatedByOrg is the H-1 fix's behavioural proof
// (migration 035): the `users` table now carries the users_organization_isolation
// policy, so the app role (axiaops) sees only its own org's user rows. Before
// 035 a future app-pool `SELECT … FROM users` with no explicit org filter would
// have returned every user across every org (password_hash/email/sso_external_id
// included). This test connects as the raw app role and proves the policy fails
// closed for a foreign org and opens for the home org.
//
// Guarded on rlsEnforced(): it requires DATABASE_URL to be the non-owner app
// role (the owner bypasses RLS by ownership, which would mask the policy).
func TestUsersRLS_AppRoleIsolatedByOrg(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("requires DATABASE_URL (app role) for RLS — owner connection bypasses by ownership")
	}
	s := newTestStore(t)
	_, orgA := newOrgCtx(t, s)
	_, orgB := newOrgCtx(t, s)
	u := seedUser(t, s, orgA.ID, "rls@x.com") // home org = orgA

	ctx := context.Background()
	app, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatalf("connect as app role: %v", err)
	}
	defer func() { _ = app.Close(ctx) }()

	setOrg := func(orgID string) {
		t.Helper()
		if _, err := app.Exec(ctx, `SELECT set_config('app.organization_id', $1, false)`, orgID); err != nil {
			t.Fatalf("set_config app.organization_id=%s: %v", orgID, err)
		}
	}
	countUser := func() int {
		t.Helper()
		var n int
		if err := app.QueryRow(ctx, `SELECT count(*) FROM axiaops.users WHERE id = $1`, u.ID).Scan(&n); err != nil {
			t.Fatalf("count users: %v", err)
		}
		return n
	}

	// Foreign org context: the policy must hide the org A user entirely.
	setOrg(orgB.ID)
	if n := countUser(); n != 0 {
		t.Errorf("users RLS failed to isolate: app role in org B saw %d rows for an org A user, want 0", n)
	}

	// Home org context: the same user is visible.
	setOrg(orgA.ID)
	if n := countUser(); n != 1 {
		t.Errorf("app role in home org A should see the user; got %d rows, want 1", n)
	}
}

// TestUsersRLS_RuntimeBypassReadsCrossOrg pins the other half of migration 035:
// the axiaops_runtime role's users_runtime_bypass policy lets the runtime pool
// read users across orgs with no org context — the pre-auth/native-login/
// /v1/me path depends on it. (TestRuntimeAdmin_PolicyCoversAllRLSTables already
// pins that the policy EXISTS; this pins the behaviour through the role.)
func TestUsersRLS_RuntimeBypassReadsCrossOrg(t *testing.T) {
	runtimeURL := os.Getenv("RUNTIME_ADMIN_DATABASE_URL")
	if runtimeURL == "" {
		t.Skip("RUNTIME_ADMIN_DATABASE_URL not set — skipping runtime bypass behaviour test")
	}
	s := newTestStore(t)
	_, orgA := newOrgCtx(t, s)
	_, orgB := newOrgCtx(t, s)
	uA := seedUser(t, s, orgA.ID, "a@x.com")
	uB := seedUser(t, s, orgB.ID, "b@x.com")

	ctx := context.Background()
	rt, err := pgx.Connect(ctx, runtimeURL)
	if err != nil {
		t.Fatalf("connect as axiaops_runtime: %v", err)
	}
	defer func() { _ = rt.Close(ctx) }()

	// No app.organization_id set — the bypass policy must surface both orgs.
	var n int
	if err := rt.QueryRow(ctx,
		`SELECT count(*) FROM axiaops.users WHERE id IN ($1, $2)`, uA.ID, uB.ID,
	).Scan(&n); err != nil {
		t.Fatalf("runtime count users: %v", err)
	}
	if n != 2 {
		t.Errorf("runtime role should read both orgs' users via the bypass policy; got %d, want 2", n)
	}
}
