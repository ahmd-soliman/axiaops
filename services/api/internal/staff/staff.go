// Package staff implements the AxiaOps platform admin plane: staff identity,
// staff RBAC, and the read-only admin console handlers. It is the control/admin
// plane of docs/saas-platform-admin-design.md §3 — structurally separate from
// the tenant plane (package auth + internal/api). A staff principal belongs to
// NO organization and never spans planes.
//
// Composition: a SECOND binary (cmd/api-admin) wires this package via
// serverbuild.ComposeAdminServer, behind its own non-internet-facing ingress.
// The tenant server (cmd/main.go + ComposeServer) is untouched.
//
// Beta auth: native staff credentials (argon2id, reusing package auth's
// Hash/Verify). The design's end-state corporate-IdP login is a future Provider
// impl behind the same seam — staff_users, RBAC, and the console don't change.
package staff

import (
	"context"
	"net/http"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// Store is the slice of the storage layer the admin plane needs. Declared here
// (consumer-side) so handler tests can mock it; *postgres.Store satisfies it
// via the storage.StaffStore methods.
type Store interface {
	CreateStaffUser(ctx context.Context, in storage.CreateStaffUserInput) (model.StaffUser, error)
	LookupStaffUserByEmail(ctx context.Context, email string) (model.StaffUser, []model.StaffRoleGrant, error)
	GetStaffUserByID(ctx context.Context, id string) (model.StaffUser, []model.StaffRoleGrant, error)
	ListStaffUsers(ctx context.Context) ([]model.StaffUser, [][]model.StaffRoleGrant, error)
	GrantStaffRole(ctx context.Context, staffUserID string, role model.StaffRole, grantedBy string) error
	RevokeStaffRole(ctx context.Context, staffUserID string, role model.StaffRole) error
	CountStaffWithRole(ctx context.Context, role model.StaffRole) (int, error)
	ListAllOrganizations(ctx context.Context) ([]model.Organization, error)
	StaffTenantSummary(ctx context.Context, organizationID string) (model.StaffTenantSummary, error)
}

// Identity is a resolved staff principal — the admin-plane analogue of
// auth.Identity, deliberately WITHOUT an OrganizationID (staff are org-less).
type Identity struct {
	StaffUserID string
	Email       string
	Name        string
	Roles       []model.StaffRole
	// TokenHash is the cache-eviction key on logout.
	TokenHash string
}

// HasRole reports whether the principal holds role r.
func (i Identity) HasRole(r model.StaffRole) bool {
	for _, have := range i.Roles {
		if have == r {
			return true
		}
	}
	return false
}

// Provider authenticates an incoming admin-plane request to a staff Identity.
// Single-method seam mirroring auth.Provider so a corporate-IdP impl can be
// swapped in later without touching the admin middleware chain.
type Provider interface {
	Authenticate(r *http.Request) (Identity, error)
}

// ── request context ─────────────────────────────────────────────────────────

type contextKey string

const identityKey contextKey = "staff_identity"

// WithIdentity returns ctx carrying the resolved staff identity.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// FromContext returns the staff identity attached by WrapStaff, or (zero,
// false) when the request is not staff-authenticated.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey).(Identity)
	return id, ok
}
