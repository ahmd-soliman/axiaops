package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Migration 015 invariants — checked at the SQL layer (no Store methods yet).
//
// The backfill itself is verified implicitly: if migration 015 misbehaves on
// existing data, TestMain panics and no tests run. Here we exercise the
// remaining invariants that protect future writes.

func TestMemberships_OneOwnerPerTenant(t *testing.T) {
	conn := setup(t)
	ctx := context.Background()

	tenantID, userA, userB := newTenantWithUsers(t, conn)

	// First owner insert succeeds.
	if _, err := conn.Exec(ctx, `
		INSERT INTO axiaops.memberships (id, organization_id, user_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'owner', NOW(), NOW())`,
		uuid.NewString(), tenantID, userA); err != nil {
		t.Fatalf("first owner insert: %v", err)
	}

	// Second owner insert in the same tenant must fail (partial unique index).
	_, err := conn.Exec(ctx, `
		INSERT INTO axiaops.memberships (id, organization_id, user_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'owner', NOW(), NOW())`,
		uuid.NewString(), tenantID, userB)
	if err == nil {
		t.Fatal("expected second owner insert to fail, got nil")
	}
	if !strings.Contains(err.Error(), "memberships_one_owner_per_organization") {
		t.Fatalf("expected partial unique index violation, got: %v", err)
	}
}

func TestMemberships_RoleCheckConstraint(t *testing.T) {
	conn := setup(t)
	ctx := context.Background()

	tenantID, userA, _ := newTenantWithUsers(t, conn)

	_, err := conn.Exec(ctx, `
		INSERT INTO axiaops.memberships (id, organization_id, user_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'superadmin', NOW(), NOW())`,
		uuid.NewString(), tenantID, userA)
	if err == nil {
		t.Fatal("expected CHECK constraint violation for invalid role, got nil")
	}
	if !strings.Contains(err.Error(), "memberships_role_check") {
		t.Fatalf("expected role check violation, got: %v", err)
	}
}

func TestMemberships_UserTenantUnique(t *testing.T) {
	conn := setup(t)
	ctx := context.Background()

	tenantID, userA, _ := newTenantWithUsers(t, conn)

	if _, err := conn.Exec(ctx, `
		INSERT INTO axiaops.memberships (id, organization_id, user_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'admin', NOW(), NOW())`,
		uuid.NewString(), tenantID, userA); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	_, err := conn.Exec(ctx, `
		INSERT INTO axiaops.memberships (id, organization_id, user_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'viewer', NOW(), NOW())`,
		uuid.NewString(), tenantID, userA)
	if err == nil {
		t.Fatal("expected UNIQUE(organization_id, user_id) violation, got nil")
	}
	if !strings.Contains(err.Error(), "memberships_organization_id_user_id_key") {
		t.Fatalf("expected unique violation, got: %v", err)
	}
}

func TestMemberships_DeletingUserCascadesMembership(t *testing.T) {
	conn := setup(t)
	ctx := context.Background()

	tenantID, userA, _ := newTenantWithUsers(t, conn)

	mID := uuid.NewString()
	if _, err := conn.Exec(ctx, `
		INSERT INTO axiaops.memberships (id, organization_id, user_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'admin', NOW(), NOW())`,
		mID, tenantID, userA); err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	if _, err := conn.Exec(ctx, `DELETE FROM axiaops.users WHERE id = $1`, userA); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	var count int
	if err := conn.QueryRow(ctx,
		`SELECT COUNT(*) FROM axiaops.memberships WHERE id = $1`, mID,
	).Scan(&count); err != nil {
		t.Fatalf("count after cascade: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected membership to cascade-delete with user, got count=%d", count)
	}
}

// newTenantWithUsers seeds a tenant with two users, returning their IDs.
// Uses the raw owner connection — bypasses RLS, suitable for setup.
func newTenantWithUsers(t *testing.T, conn *pgx.Conn) (tenantID, userAID, userBID string) {
	t.Helper()
	ctx := context.Background()

	tenantID = "t-" + uuid.NewString()
	orgCode := "org-" + uuid.NewString()
	if _, err := conn.Exec(ctx, `
		INSERT INTO axiaops.organizations (id, org_code, name, created_at)
		VALUES ($1, $2, 'Test Org', NOW())`,
		tenantID, orgCode); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	userAID = "u-" + uuid.NewString()
	userBID = "u-" + uuid.NewString()
	for _, uid := range []string{userAID, userBID} {
		if _, err := conn.Exec(ctx, `
			INSERT INTO axiaops.users (id, organization_id, kinde_sub, email, name, created_at, last_seen)
			VALUES ($1, $2, $3, '', '', NOW(), NOW())`,
			uid, tenantID, "kinde-"+uid); err != nil {
			t.Fatalf("seed user %s: %v", uid, err)
		}
	}
	return tenantID, userAID, userBID
}
