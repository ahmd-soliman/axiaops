// storage_staff.go — Platform admin plane (staff identity + RBAC) Store
// interface additions. Split into its own file for the same reason as
// storage_native_auth.go: a reviewable diff. Composes into Store via Go's
// multi-file interface declarations.
//
// Every method here runs on the admin pool (RLS-bypass) with NO organization
// context — staff are cross-plane principals belonging to no tenant. The
// concrete *postgres.Store satisfies this alongside the rest of Store.
// See docs/saas-platform-admin-design.md §4/§8 and docs/admin-portal-plan.md.

package storage

import (
	"context"
	"errors"

	"axiaops.io/shared/model"
)

// ── Staff-plane sentinel errors ─────────────────────────────────────────────

// ErrStaffEmailExists is returned by CreateStaffUser when the case-insensitive
// email collides with an existing staff row. Surface as HTTP 409.
var ErrStaffEmailExists = errors.New("storage: staff email already registered")

// ErrStaffNotFound is returned by staff lookups when no row matches (or the
// row is suspended, for the auth path — callers MUST treat both as auth
// failure and collapse to a single 401).
var ErrStaffNotFound = errors.New("storage: staff user not found")

// ErrLastStaffSuperadmin is returned by RevokeStaffRole when removing the
// target's superadmin grant would leave the platform with zero superadmins.
// The guard is enforced atomically inside the store (a row lock over the
// superadmin grants) so concurrent revokes can't both pass. Surface as 409.
var ErrLastStaffSuperadmin = errors.New("storage: cannot revoke the last superadmin")

// CreateStaffUserInput is the argument to CreateStaffUser. PasswordHash is the
// already-computed argon2id PHC string (this layer never sees plaintext),
// optional for IdP-only rows. Roles are granted in the same transaction so a
// freshly-minted staff member is never left with zero roles.
type CreateStaffUserInput struct {
	ID           string // optional; generated when empty
	Email        string
	Name         string
	PasswordHash string
	Roles        []model.StaffRole
	GrantedBy    string // staff_user_id of the creator; empty for bootstrap
}

// StaffStore is the slice of Store dealing with the platform admin plane.
// Implementations live in services/shared/storage/postgres/staff.go.
type StaffStore interface {
	// CreateStaffUser inserts a staff_users row plus its role grants in one
	// transaction. Returns ErrStaffEmailExists on a case-insensitive email
	// collision. Runs on the admin pool — staff are org-less.
	CreateStaffUser(ctx context.Context, in CreateStaffUserInput) (model.StaffUser, error)

	// LookupStaffUserByEmail returns the staff user (with password_hash) plus
	// its role grants, keyed case-insensitively. The auth/login path. Returns
	// ErrStaffNotFound when no row matches.
	LookupStaffUserByEmail(ctx context.Context, email string) (model.StaffUser, []model.StaffRoleGrant, error)

	// GetStaffUserByID resolves a staff principal + roles by id — the
	// session-resolution path. Returns ErrStaffNotFound when no row matches.
	GetStaffUserByID(ctx context.Context, id string) (model.StaffUser, []model.StaffRoleGrant, error)

	// ListStaffUsers returns every staff user (no password hashes) for the
	// superadmin console, each paired with its role grants.
	ListStaffUsers(ctx context.Context) ([]model.StaffUser, [][]model.StaffRoleGrant, error)

	// GrantStaffRole adds a (staff, role) grant. Idempotent (ON CONFLICT DO
	// NOTHING on the UNIQUE(staff_user_id, role)).
	GrantStaffRole(ctx context.Context, staffUserID string, role model.StaffRole, grantedBy string) error

	// RevokeStaffRole removes a (staff, role) grant. No error when absent.
	// Enforces the last-superadmin guard atomically: revoking the target's
	// superadmin grant when it is the only superadmin grant in the system
	// returns ErrLastStaffSuperadmin (so the admin plane can never be
	// stranded, even under concurrent revokes).
	RevokeStaffRole(ctx context.Context, staffUserID string, role model.StaffRole) error

	// CountStaffWithRole returns how many staff hold a given role — used by the
	// last-superadmin guard so the plane is never stranded with zero superadmins.
	CountStaffWithRole(ctx context.Context, role model.StaffRole) (int, error)

	// ── Cross-org read methods for the admin console (design §7.5) ──────────
	// These return org METADATA only, never tenant FinOps data; reading them
	// is explicitly not a break-glass tenant-data read.

	// ListAllOrganizations returns every org, newest-last. Admin pool, no org
	// context (mirrors ListAllAccounts).
	ListAllOrganizations(ctx context.Context) ([]model.Organization, error)

	// StaffTenantSummary returns one org's non-FinOps summary: metadata +
	// account count + last-scan aggregates, computed from existing tables.
	// Returns ErrOrganizationNotFound (declared in storage.go) when the org id
	// is unknown.
	StaffTenantSummary(ctx context.Context, organizationID string) (model.StaffTenantSummary, error)
}
