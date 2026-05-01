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
	err := sso.JITProvisionMembership(t.Context(), nil, "org-1", "user-1", "owner")
	if err == nil {
		t.Fatal("JITProvisionMembership(owner) succeeded; want ErrJITOwnerForbidden")
	}
	if err != sso.ErrJITOwnerForbidden {
		t.Errorf("JITProvisionMembership(owner) error = %v; want ErrJITOwnerForbidden", err)
	}
}

// TestJITProvisionMembership_SetsProvisionedViaJIT proves the JIT seam writes
// `provisioned_via='jit'` into the SaveMembership call. Without this the
// admin team-review UX surfaces JIT-provisioned users as `manual` —
// defeating the whole point of the column added in migration 022.
func TestJITProvisionMembership_SetsProvisionedViaJIT(t *testing.T) {
	store := &captureJITStore{}
	if err := sso.JITProvisionMembership(t.Context(), store, "org-1", "user-1", "member"); err != nil {
		t.Fatalf("JITProvisionMembership: %v", err)
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
