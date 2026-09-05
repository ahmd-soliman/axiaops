package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// ── DeleteUser ──────────────────────────────────────────────────────────────

func TestDeleteUser_RefusesSoleOwner(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	userID := "u-" + uuid.New().String()
	if err := s.EnsureUser(ctx, model.User{ID: userID, OrganizationID: org.ID, Email: "owner@x.com"}); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if err := s.EnsureDevMembership(ctx, org.ID, userID, "owner"); err != nil {
		t.Fatalf("EnsureDevMembership: %v", err)
	}

	err := s.DeleteUser(ctx, userID)
	if !errors.Is(err, storage.ErrLastOwner) {
		t.Fatalf("expected ErrLastOwner, got %v", err)
	}

	// User row must still exist.
	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()
	var n int
	if err := conn.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM axiaops.users WHERE id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 1 {
		t.Errorf("user row should still exist; count=%d", n)
	}
}

func TestDeleteUser_AnonymisesAuditAndRemovesUser(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	// Two users so deleting one doesn't violate the sole-owner guard.
	ownerID := "u-" + uuid.New().String()
	leavingID := "u-" + uuid.New().String()
	if err := s.EnsureUser(ctx, model.User{ID: ownerID, OrganizationID: org.ID, Email: "owner@x.com"}); err != nil {
		t.Fatalf("EnsureUser owner: %v", err)
	}
	if err := s.EnsureUser(ctx, model.User{ID: leavingID, OrganizationID: org.ID, Email: "leaving@x.com"}); err != nil {
		t.Fatalf("EnsureUser leaving: %v", err)
	}
	if err := s.EnsureDevMembership(ctx, org.ID, ownerID, "owner"); err != nil {
		t.Fatalf("EnsureDevMembership owner: %v", err)
	}
	if err := s.EnsureDevMembership(ctx, org.ID, leavingID, "member"); err != nil {
		t.Fatalf("EnsureDevMembership leaving: %v", err)
	}

	// Drop a couple of audit rows for the leaving user.
	for i := 0; i < 2; i++ {
		if _, err := s.AuditLogWrite(ctx, model.AuditEvent{
			UserID:     leavingID,
			ActorEmail: "leaving@x.com",
			Action:     model.AuditActionDismissZombie,
		}); err != nil {
			t.Fatalf("AuditLogWrite: %v", err)
		}
	}

	if err := s.DeleteUser(ctx, leavingID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()

	var users, memberships, anonymisedAudit int
	if err := conn.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM axiaops.users WHERE id = $1`, leavingID).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 0 {
		t.Errorf("user row should be gone; count=%d", users)
	}
	if err := conn.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM axiaops.memberships WHERE user_id = $1`, leavingID).Scan(&memberships); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if memberships != 0 {
		t.Errorf("memberships should cascade away; count=%d", memberships)
	}
	if err := conn.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM axiaops.audit_log
		 WHERE organization_id = $1 AND user_id IS NULL AND actor_email = 'deleted-user'`,
		org.ID).Scan(&anonymisedAudit); err != nil {
		t.Fatalf("count anonymised: %v", err)
	}
	if anonymisedAudit != 2 {
		t.Errorf("expected 2 anonymised audit rows, got %d", anonymisedAudit)
	}
}

// ── DeleteOrganizationCascade ─────────────────────────────────────────────────────

func TestDeleteOrganizationCascade_PurgesEveryTable(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)

	// User + membership in this organization.
	userID := "u-" + uuid.New().String()
	if err := s.EnsureUser(ctx, model.User{ID: userID, OrganizationID: org.ID, Email: "u@x.com"}); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if err := s.EnsureDevMembership(ctx, org.ID, userID, "owner"); err != nil {
		t.Fatalf("EnsureDevMembership: %v", err)
	}

	// Cost record + account + audit row.
	if _, _, err := s.Save(ctx, []model.CostRecord{costRecord("AmazonEC2", "eu-central-1", 1.23)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.SaveAccount(ctx, model.Account{
		ID: "acc-" + uuid.New().String(), OrganizationID: org.ID,
		Provider: "aws", AccountID: "000000000000", Region: "eu-central-1", Status: "connected",
		// access-key fields are required by the accounts_access_key_fields_present
		// CHECK constraint introduced in migration 019. Use any non-empty values
		// — this test cares about cascade behaviour, not credential validity.
		AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretEncrypted: "stub",
		BillingSource: model.BillingSourceCostExplorer,
	}); err != nil {
		t.Fatalf("SaveAccount: %v", err)
	}
	if _, err := s.AuditLogWrite(ctx, model.AuditEvent{
		UserID: userID, ActorEmail: "u@x.com", Action: model.AuditActionAccountConnected,
	}); err != nil {
		t.Fatalf("AuditLogWrite: %v", err)
	}

	if err := s.DeleteOrganizationCascade(ctx, org.ID); err != nil {
		t.Fatalf("DeleteOrganizationCascade: %v", err)
	}

	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()

	for _, q := range []struct {
		label string
		sql   string
	}{
		{"organizations", `SELECT COUNT(*) FROM axiaops.organizations WHERE id = $1`},
		{"users", `SELECT COUNT(*) FROM axiaops.users WHERE organization_id = $1`},
		{"memberships", `SELECT COUNT(*) FROM axiaops.memberships WHERE organization_id = $1`},
		{"accounts", `SELECT COUNT(*) FROM axiaops.accounts WHERE organization_id = $1`},
		{"cost_records", `SELECT COUNT(*) FROM axiaops.cost_records WHERE organization_id = $1`},
		{"zombie_records", `SELECT COUNT(*) FROM axiaops.zombie_records WHERE organization_id = $1`},
		{"resource_records", `SELECT COUNT(*) FROM axiaops.resource_records WHERE organization_id = $1`},
		{"zombie_snapshots", `SELECT COUNT(*) FROM axiaops.zombie_snapshots WHERE organization_id = $1`},
		{"zombie_snapshot_services", `SELECT COUNT(*) FROM axiaops.zombie_snapshot_services WHERE organization_id = $1`},
		{"dismissed_zombies", `SELECT COUNT(*) FROM axiaops.dismissed_zombies WHERE organization_id = $1`},
		{"audit_log", `SELECT COUNT(*) FROM axiaops.audit_log WHERE organization_id = $1`},
	} {
		var n int
		if err := conn.QueryRow(context.Background(), q.sql, org.ID).Scan(&n); err != nil {
			t.Fatalf("%s count: %v", q.label, err)
		}
		if n != 0 {
			t.Errorf("%s should be empty after cascade; count=%d", q.label, n)
		}
	}
}

func TestDeleteOrganizationCascade_AnonymisesCrossOrganizationAudit(t *testing.T) {
	// User U has primary organization A. U is also a member of organization B and has
	// audit rows in B. When A is deleted (and U with it), U's audit rows in
	// B must be anonymised — right to erasure travels with the user.
	s := newTestStore(t)
	ctxA, orgA := newOrgCtx(t, s)
	ctxB, orgB := newOrgCtx(t, s)

	userID := "u-" + uuid.New().String()
	if err := s.EnsureUser(ctxA, model.User{ID: userID, OrganizationID: orgA.ID, Email: "u@x.com"}); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if err := s.EnsureDevMembership(ctxA, orgA.ID, userID, "owner"); err != nil {
		t.Fatalf("EnsureDevMembership A: %v", err)
	}
	if err := s.EnsureDevMembership(ctxB, orgB.ID, userID, "member"); err != nil {
		t.Fatalf("EnsureDevMembership B: %v", err)
	}
	if _, err := s.AuditLogWrite(ctxB, model.AuditEvent{
		UserID: userID, ActorEmail: "u@x.com", Action: model.AuditActionDismissZombie,
	}); err != nil {
		t.Fatalf("AuditLogWrite B: %v", err)
	}

	if err := s.DeleteOrganizationCascade(ctxA, orgA.ID); err != nil {
		t.Fatalf("DeleteOrganizationCascade: %v", err)
	}

	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()

	var anonymised int
	if err := conn.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM axiaops.audit_log
		 WHERE organization_id = $1 AND user_id IS NULL AND actor_email = 'deleted-user'`,
		orgB.ID).Scan(&anonymised); err != nil {
		t.Fatalf("count anonymised in B: %v", err)
	}
	if anonymised != 1 {
		t.Errorf("expected user's audit row in organization B to be anonymised; got %d", anonymised)
	}

	// Organization B itself should be untouched.
	var orgBStillThere int
	if err := conn.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM axiaops.organizations WHERE id = $1`, orgB.ID).Scan(&orgBStillThere); err != nil {
		t.Fatalf("count organization B: %v", err)
	}
	if orgBStillThere != 1 {
		t.Errorf("organization B should not be deleted by cascading A; got count=%d", orgBStillThere)
	}
}
