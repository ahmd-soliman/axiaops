package authz_test

import (
	"testing"

	"axiaops.io/shared/authz"
)

// The capability matrix from docs/AUTHENTICATION.md (§2) expressed as a single
// table. Rows: permissions. Columns: which roles must grant the permission.
// Anything outside the column set must fail-closed.
func TestAllows_CapabilityMatrix(t *testing.T) {
	cases := []struct {
		perm   authz.Permission
		grants []authz.Role
	}{
		// Read-only permissions: every role.
		{authz.PermAccountsRead, []authz.Role{authz.RoleViewer, authz.RoleMember, authz.RoleAdmin, authz.RoleOwner}},
		{authz.PermZombiesRead, []authz.Role{authz.RoleViewer, authz.RoleMember, authz.RoleAdmin, authz.RoleOwner}},
		{authz.PermSnapshotsRead, []authz.Role{authz.RoleViewer, authz.RoleMember, authz.RoleAdmin, authz.RoleOwner}},
		{authz.PermCostsRead, []authz.Role{authz.RoleViewer, authz.RoleMember, authz.RoleAdmin, authz.RoleOwner}},
		{authz.PermResourcesRead, []authz.Role{authz.RoleViewer, authz.RoleMember, authz.RoleAdmin, authz.RoleOwner}},
		{authz.PermAuditRead, []authz.Role{authz.RoleViewer, authz.RoleMember, authz.RoleAdmin, authz.RoleOwner}},
		{authz.PermMembersRead, []authz.Role{authz.RoleViewer, authz.RoleMember, authz.RoleAdmin, authz.RoleOwner}},

		// Member-only writes.
		{authz.PermAccountsWrite, []authz.Role{authz.RoleMember, authz.RoleAdmin, authz.RoleOwner}},
		{authz.PermAccountsScan, []authz.Role{authz.RoleMember, authz.RoleAdmin, authz.RoleOwner}},
		{authz.PermZombiesDismiss, []authz.Role{authz.RoleMember, authz.RoleAdmin, authz.RoleOwner}},

		// Admin-only.
		{authz.PermAccountsDelete, []authz.Role{authz.RoleAdmin, authz.RoleOwner}},
		{authz.PermMembersInvite, []authz.Role{authz.RoleAdmin, authz.RoleOwner}},
		{authz.PermMembersManageBasic, []authz.Role{authz.RoleAdmin, authz.RoleOwner}},

		// Owner-only.
		{authz.PermMembersManageAdmin, []authz.Role{authz.RoleOwner}},
		{authz.PermOrganizationTransfer, []authz.Role{authz.RoleOwner}},
		{authz.PermOrganizationDelete, []authz.Role{authz.RoleOwner}},
		{authz.PermDataExport, []authz.Role{authz.RoleOwner}},
	}

	allRoles := []authz.Role{authz.RoleViewer, authz.RoleMember, authz.RoleAdmin, authz.RoleOwner}

	for _, tc := range cases {
		grantSet := map[authz.Role]bool{}
		for _, r := range tc.grants {
			grantSet[r] = true
		}
		for _, r := range allRoles {
			got := authz.Allows(r, tc.perm)
			want := grantSet[r]
			if got != want {
				t.Errorf("Allows(%q, %q) = %v, want %v", r, tc.perm, got, want)
			}
		}
	}
}

func TestAllows_FailsClosed(t *testing.T) {
	if authz.Allows("", authz.PermAccountsRead) {
		t.Error("empty role must not grant any permission")
	}
	if authz.Allows("superuser", authz.PermAccountsRead) {
		t.Error("unknown role must not grant any permission")
	}
	if authz.Allows(authz.RoleOwner, "organization:nuke") {
		t.Error("unknown permission must not be granted to any role")
	}
}

func TestPermissionsOf_DeterministicOrder(t *testing.T) {
	first := authz.PermissionsOf(authz.RoleAdmin)
	second := authz.PermissionsOf(authz.RoleAdmin)
	if len(first) == 0 {
		t.Fatal("admin should have permissions")
	}
	if len(first) != len(second) {
		t.Fatalf("nondeterministic length: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("nondeterministic order at %d: %q vs %q", i, first[i], second[i])
		}
	}
}

func TestPermissionsOf_OwnerSupersetOfAdmin(t *testing.T) {
	owner := toSet(authz.PermissionsOf(authz.RoleOwner))
	admin := toSet(authz.PermissionsOf(authz.RoleAdmin))
	for p := range admin {
		if !owner[p] {
			t.Errorf("owner missing permission %q that admin has", p)
		}
	}
	if len(owner) <= len(admin) {
		t.Errorf("owner should grant strictly more than admin (owner=%d admin=%d)", len(owner), len(admin))
	}
}

func TestPermissionsOf_UnknownRoleReturnsNil(t *testing.T) {
	if got := authz.PermissionsOf("ceo"); got != nil {
		t.Errorf("expected nil for unknown role, got %v", got)
	}
}

func toSet(perms []authz.Permission) map[authz.Permission]bool {
	out := make(map[authz.Permission]bool, len(perms))
	for _, p := range perms {
		out[p] = true
	}
	return out
}
