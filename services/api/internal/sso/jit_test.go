package sso_test

import (
	"context"
	"testing"

	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// captureJITStore is a tiny JITMembershipStore that records the most recent
// SaveMembership call so the test can assert provenance is set correctly.
//
// Only the new-insert path is covered here. The reconcile path (where
// SaveMembership returns ErrMembershipExists, JIT then walks
// GetMembershipByOrgUser → UpdateMembershipRole) is not exercised by this
// mock — UpdateMembershipRole returns nil without recording. Add coverage
// when the OIDC ceremony slice wires the runtime caller.
type captureJITStore struct {
	lastSavedMembership model.Membership
}

func (c *captureJITStore) SaveMembership(_ context.Context, m model.Membership) error {
	c.lastSavedMembership = m
	return nil
}
func (c *captureJITStore) GetMembershipByOrgUser(_ context.Context, _, _ string) (model.Membership, error) {
	return model.Membership{}, storage.ErrMembershipNotFound
}
func (c *captureJITStore) UpdateMembershipRole(_ context.Context, _, _ string) error {
	return nil
}

func mapping(group, role string) model.SSOGroupMapping {
	return model.SSOGroupMapping{GroupExternalID: group, Role: role}
}

// TestJITResolveRole_Precedence covers the architect S6 / plan §5.2 tie-break
// rule: admin > member > viewer. Owner is unreachable via JIT regardless of
// what the mappings claim.
func TestJITResolveRole_Precedence(t *testing.T) {
	tests := []struct {
		name        string
		mappings    []model.SSOGroupMapping
		userGroups  []string
		defaultRole string
		want        string
	}{
		{
			name: "single mapping match → that role",
			mappings: []model.SSOGroupMapping{
				mapping("g-eng", "member"),
			},
			userGroups:  []string{"g-eng"},
			defaultRole: "viewer",
			want:        "member",
		},
		{
			name: "two mappings, different roles → admin wins (highest priority)",
			mappings: []model.SSOGroupMapping{
				mapping("g-eng", "member"),
				mapping("g-platform", "admin"),
			},
			userGroups:  []string{"g-eng", "g-platform"},
			defaultRole: "viewer",
			want:        "admin",
		},
		{
			name: "two mappings, same role → that role (no error)",
			mappings: []model.SSOGroupMapping{
				mapping("g-eng-a", "member"),
				mapping("g-eng-b", "member"),
			},
			userGroups:  []string{"g-eng-a", "g-eng-b"},
			defaultRole: "viewer",
			want:        "member",
		},
		{
			name: "no group match → defaultRole",
			mappings: []model.SSOGroupMapping{
				mapping("g-eng", "admin"),
			},
			userGroups:  []string{"g-marketing"},
			defaultRole: "viewer",
			want:        "viewer",
		},
		{
			name:        "empty mappings → defaultRole",
			mappings:    []model.SSOGroupMapping{},
			userGroups:  []string{"g-eng"},
			defaultRole: "member",
			want:        "member",
		},
		{
			name: "empty userGroups → defaultRole",
			mappings: []model.SSOGroupMapping{
				mapping("g-eng", "admin"),
			},
			userGroups:  nil,
			defaultRole: "viewer",
			want:        "viewer",
		},
		{
			name: "owner mapping (defence-in-depth) → ignored, defaultRole returned",
			mappings: []model.SSOGroupMapping{
				mapping("g-rebels", "owner"),
			},
			userGroups:  []string{"g-rebels"},
			defaultRole: "viewer",
			want:        "viewer",
		},
		{
			name: "mapping role matches but user groups differ in case → no match",
			mappings: []model.SSOGroupMapping{
				mapping("g-Eng", "admin"),
			},
			userGroups:  []string{"g-eng"},
			defaultRole: "viewer",
			want:        "viewer",
		},
		{
			name: "viewer mapping is matched even when default is viewer (no-op but exercised)",
			mappings: []model.SSOGroupMapping{
				mapping("g-readers", "viewer"),
			},
			userGroups:  []string{"g-readers"},
			defaultRole: "viewer",
			want:        "viewer",
		},
		{
			name:        "default role empty falls through to viewer",
			mappings:    nil,
			userGroups:  nil,
			defaultRole: "",
			want:        "viewer",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sso.JITResolveRole(tc.mappings, tc.userGroups, tc.defaultRole)
			if got != tc.want {
				t.Errorf("JITResolveRole = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestJITProvisionMembership_OwnerForbidden asserts the seam refuses to
// promote anyone to owner via JIT — sticky owner property (plan §5.2).
func TestJITProvisionMembership_OwnerForbidden(t *testing.T) {
	outcome, err := sso.JITProvisionMembership(t.Context(), nil, "org-1", "user-1", "owner")
	if err == nil {
		t.Fatal("JITProvisionMembership(owner) succeeded; want ErrJITOwnerForbidden")
	}
	if err != sso.ErrJITOwnerForbidden {
		t.Errorf("JITProvisionMembership(owner) error = %v; want ErrJITOwnerForbidden", err)
	}
	if outcome != sso.JITOutcomeNoop {
		t.Errorf("outcome on owner reject: got %v want JITOutcomeNoop", outcome)
	}
}

// TestJITProvisionMembership_SetsProvisionedViaJIT proves the JIT seam writes
// `provisioned_via='jit'` into the SaveMembership call. Without this the
// admin team-review UX surfaces JIT-provisioned users as `manual` —
// defeating the whole point of the column added in migration 022.
func TestJITProvisionMembership_SetsProvisionedViaJIT(t *testing.T) {
	store := &captureJITStore{}
	outcome, err := sso.JITProvisionMembership(t.Context(), store, "org-1", "user-1", "member")
	if err != nil {
		t.Fatalf("JITProvisionMembership: %v", err)
	}
	if outcome != sso.JITOutcomeCreated {
		t.Errorf("outcome on first-time provision: got %v want JITOutcomeCreated", outcome)
	}
	if got, want := store.lastSavedMembership.ProvisionedVia, model.ProvisionedViaJIT; got != want {
		t.Errorf("SaveMembership.ProvisionedVia = %q; want %q", got, want)
	}
	if got, want := store.lastSavedMembership.Role, "member"; got != want {
		t.Errorf("SaveMembership.Role = %q; want %q", got, want)
	}
	if got, want := store.lastSavedMembership.OrganizationID, "org-1"; got != want {
		t.Errorf("SaveMembership.OrganizationID = %q; want %q", got, want)
	}
}

// reconcileJITStore exercises the reconcile path: SaveMembership returns
// ErrMembershipExists, JIT calls GetMembershipByOrgUser to look up the
// existing row, then conditionally calls UpdateMembershipRole. Tests of the
// provenance guard need this richer mock.
type reconcileJITStore struct {
	existing       model.Membership
	updateCalled   bool
	updatedID      string
	updatedRole    string
}

func (r *reconcileJITStore) SaveMembership(_ context.Context, _ model.Membership) error {
	return storage.ErrMembershipExists
}
func (r *reconcileJITStore) GetMembershipByOrgUser(_ context.Context, _, _ string) (model.Membership, error) {
	return r.existing, nil
}
func (r *reconcileJITStore) UpdateMembershipRole(_ context.Context, id, role string) error {
	r.updateCalled = true
	r.updatedID = id
	r.updatedRole = role
	return nil
}

// TestJITProvisionMembership_ProvenanceGuard_SkipsAdminPlacedRows pins the
// post-merge race fix (review on !76, structural fix on top of !77): JIT
// must never overwrite the role on an admin-placed membership
// (provisioned_via in {manual, invitation, scim, legacy}) even when the
// SSO group claims now resolve to a different role. Closes the cross-flow
// race where a user simultaneously hits POST /v1/auth/invitations/redeem
// and the SSO callback for the same org+email — the loser of the
// FOR-UPDATE on pending_memberships used to silently downgrade the admin
// role here. With the guard, the loser sees the existing
// provisioned_via='invitation' row and noops.
func TestJITProvisionMembership_ProvenanceGuard_SkipsAdminPlacedRows(t *testing.T) {
	cases := []struct {
		name           string
		provisionedVia string
	}{
		{"manual", model.ProvisionedViaManual},
		{"invitation", model.ProvisionedViaInvitation},
		{"scim", model.ProvisionedViaSCIM},
		{"legacy", model.ProvisionedViaLegacy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &reconcileJITStore{
				existing: model.Membership{
					ID:             "m-1",
					UserID:         "user-1",
					OrganizationID: "org-1",
					Role:           "admin", // admin-chosen role
					ProvisionedVia: tc.provisionedVia,
				},
			}
			// SSO group resolves to "member" — would normally downgrade.
			outcome, err := sso.JITProvisionMembership(t.Context(), store, "org-1", "user-1", "member")
			if err != nil {
				t.Fatalf("JITProvisionMembership: %v", err)
			}
			if outcome != sso.JITOutcomeNoop {
				t.Errorf("outcome on %s-provisioned existing row: got %v want JITOutcomeNoop", tc.provisionedVia, outcome)
			}
			if store.updateCalled {
				t.Errorf("UpdateMembershipRole called on a provisioned_via=%q row — admin role silently overwritten (the bug this guard prevents)", tc.provisionedVia)
			}
		})
	}
}

// TestJITProvisionMembership_ProvenanceGuard_AllowsJITRowUpdate confirms the
// guard does NOT regress the intended JIT-on-relogin reconciliation: when
// the existing membership was itself JIT-placed, group claim changes still
// propagate via UpdateMembershipRole.
func TestJITProvisionMembership_ProvenanceGuard_AllowsJITRowUpdate(t *testing.T) {
	store := &reconcileJITStore{
		existing: model.Membership{
			ID:             "m-1",
			UserID:         "user-1",
			OrganizationID: "org-1",
			Role:           "viewer",
			ProvisionedVia: model.ProvisionedViaJIT,
		},
	}
	outcome, err := sso.JITProvisionMembership(t.Context(), store, "org-1", "user-1", "admin")
	if err != nil {
		t.Fatalf("JITProvisionMembership: %v", err)
	}
	if outcome != sso.JITOutcomeUpdated {
		t.Errorf("outcome on JIT-row role change: got %v want JITOutcomeUpdated", outcome)
	}
	if !store.updateCalled || store.updatedID != "m-1" || store.updatedRole != "admin" {
		t.Errorf("UpdateMembershipRole not called correctly: called=%v id=%q role=%q",
			store.updateCalled, store.updatedID, store.updatedRole)
	}
}
