package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// TestStaff_CreateLookupRoundtrip covers the create → lookup-by-email →
// lookup-by-id path, the grant set, and the case-insensitive email collision.
// All staff methods run on the admin pool with NO organization context — these
// are system tables (no RLS), so they must work without app.organization_id set
// (proven implicitly by passing a bare context.Background()).
func TestStaff_CreateLookupRoundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, err := s.CreateStaffUser(ctx, storage.CreateStaffUserInput{
		Email:        "Ada@AxiaOps.io",
		Name:         "Ada",
		PasswordHash: "$argon2id$fake",
		Roles:        []model.StaffRole{model.StaffRoleSupport, model.StaffRoleOps},
	})
	if err != nil {
		t.Fatalf("CreateStaffUser: %v", err)
	}
	if created.ID == "" || created.Status != "active" {
		t.Fatalf("unexpected created row: %+v", created)
	}

	// Lookup by email is case-insensitive.
	u, grants, err := s.LookupStaffUserByEmail(ctx, "ada@axiaops.io")
	if err != nil {
		t.Fatalf("LookupStaffUserByEmail: %v", err)
	}
	if u.ID != created.ID || u.PasswordHash != "$argon2id$fake" {
		t.Fatalf("lookup mismatch: %+v", u)
	}
	if len(grants) != 2 {
		t.Fatalf("want 2 grants, got %d", len(grants))
	}

	// Lookup by id resolves the same principal.
	byID, _, err := s.GetStaffUserByID(ctx, created.ID)
	if err != nil || byID.Email != created.Email {
		t.Fatalf("GetStaffUserByID: %+v err=%v", byID, err)
	}

	// Duplicate (case-folded) email → ErrStaffEmailExists.
	_, err = s.CreateStaffUser(ctx, storage.CreateStaffUserInput{
		Email: "ADA@axiaops.io", PasswordHash: "x", Roles: []model.StaffRole{model.StaffRoleSupport},
	})
	if !errors.Is(err, storage.ErrStaffEmailExists) {
		t.Fatalf("want ErrStaffEmailExists, got %v", err)
	}
}

func TestStaff_GrantRevokeAndCount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, err := s.CreateStaffUser(ctx, storage.CreateStaffUserInput{
		Email: "boss@axiaops.io", PasswordHash: "x", Roles: []model.StaffRole{model.StaffRoleSuperadmin},
	})
	if err != nil {
		t.Fatalf("CreateStaffUser: %v", err)
	}

	// Idempotent grant.
	if err := s.GrantStaffRole(ctx, created.ID, model.StaffRoleBilling, created.ID); err != nil {
		t.Fatalf("GrantStaffRole: %v", err)
	}
	if err := s.GrantStaffRole(ctx, created.ID, model.StaffRoleBilling, created.ID); err != nil {
		t.Fatalf("GrantStaffRole (idempotent): %v", err)
	}
	_, grants, _ := s.GetStaffUserByID(ctx, created.ID)
	if len(grants) != 2 { // superadmin + billing
		t.Fatalf("want 2 grants after idempotent re-grant, got %d", len(grants))
	}

	// Count + revoke.
	n, err := s.CountStaffWithRole(ctx, model.StaffRoleSuperadmin)
	if err != nil || n != 1 {
		t.Fatalf("CountStaffWithRole superadmin: n=%d err=%v", n, err)
	}
	if err := s.RevokeStaffRole(ctx, created.ID, model.StaffRoleBilling); err != nil {
		t.Fatalf("RevokeStaffRole: %v", err)
	}
	_, grants, _ = s.GetStaffUserByID(ctx, created.ID)
	if len(grants) != 1 {
		t.Fatalf("want 1 grant after revoke, got %d", len(grants))
	}

	// Grant to a non-existent staff user → ErrStaffNotFound.
	if err := s.GrantStaffRole(ctx, "no-such-id", model.StaffRoleOps, ""); !errors.Is(err, storage.ErrStaffNotFound) {
		t.Fatalf("want ErrStaffNotFound, got %v", err)
	}
}

// TestStaff_RevokeSuperadminGuard covers the atomic last-superadmin guard:
// revoking the only superadmin is refused; with two it succeeds; revoking a
// role the target doesn't hold is a clean no-op.
func TestStaff_RevokeSuperadminGuard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a, err := s.CreateStaffUser(ctx, storage.CreateStaffUserInput{
		Email: "sa1-" + uuid.New().String()[:8] + "@axiaops.io", PasswordHash: "x",
		Roles: []model.StaffRole{model.StaffRoleSuperadmin},
	})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}

	if n, _ := s.CountStaffWithRole(ctx, model.StaffRoleSuperadmin); n != 1 {
		t.Fatalf("precondition: want 1 superadmin after create, got %d", n)
	}

	// Only superadmin → revoke refused.
	if err := s.RevokeStaffRole(ctx, a.ID, model.StaffRoleSuperadmin); !errors.Is(err, storage.ErrLastStaffSuperadmin) {
		t.Fatalf("want ErrLastStaffSuperadmin, got %v", err)
	}

	// Revoking a role the target doesn't hold is a no-op (no guard trip).
	if err := s.RevokeStaffRole(ctx, a.ID, model.StaffRoleBilling); err != nil {
		t.Fatalf("no-op revoke should succeed, got %v", err)
	}

	// Add a second superadmin → now revoke succeeds.
	b, err := s.CreateStaffUser(ctx, storage.CreateStaffUserInput{
		Email: "sa2-" + uuid.New().String()[:8] + "@axiaops.io", PasswordHash: "x",
		Roles: []model.StaffRole{model.StaffRoleSuperadmin},
	})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if err := s.RevokeStaffRole(ctx, b.ID, model.StaffRoleSuperadmin); err != nil {
		t.Fatalf("revoke with two superadmins should succeed, got %v", err)
	}
	if n, _ := s.CountStaffWithRole(ctx, model.StaffRoleSuperadmin); n != 1 {
		t.Fatalf("want 1 superadmin remaining, got %d", n)
	}
}

// TestStaff_CrossOrgReads covers ListAllOrganizations + StaffTenantSummary —
// the admin console reads, which span all tenants on the admin pool with no
// org context.
func TestStaff_CrossOrgReads(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	orgA, err := s.UpsertOrganization(ctx, "staff-sum-a-"+uuid.New().String()[:8], "Acme")
	if err != nil {
		t.Fatalf("UpsertOrganization A: %v", err)
	}
	orgB, err := s.UpsertOrganization(ctx, "staff-sum-b-"+uuid.New().String()[:8], "Globex")
	if err != nil {
		t.Fatalf("UpsertOrganization B: %v", err)
	}

	orgs, err := s.ListAllOrganizations(ctx)
	if err != nil {
		t.Fatalf("ListAllOrganizations: %v", err)
	}
	if !containsOrg(orgs, orgA.ID) || !containsOrg(orgs, orgB.ID) {
		t.Fatalf("ListAllOrganizations missing seeded orgs (got %d)", len(orgs))
	}

	// Summary for a never-scanned org: zero counts, nil last-scan, no error.
	sum, err := s.StaffTenantSummary(ctx, orgA.ID)
	if err != nil {
		t.Fatalf("StaffTenantSummary: %v", err)
	}
	if sum.OrganizationID != orgA.ID || sum.Name != "Acme" {
		t.Fatalf("summary metadata mismatch: %+v", sum)
	}
	if sum.AccountCount != 0 || sum.LastScanAt != nil {
		t.Fatalf("never-scanned org should have zero accounts + nil last-scan: %+v", sum)
	}

	// Unknown org → ErrOrganizationNotFound.
	if _, err := s.StaffTenantSummary(ctx, "no-such-org"); !errors.Is(err, storage.ErrOrganizationNotFound) {
		t.Fatalf("want ErrOrganizationNotFound, got %v", err)
	}
}

func containsOrg(orgs []model.Organization, id string) bool {
	for _, o := range orgs {
		if o.ID == id {
			return true
		}
	}
	return false
}
