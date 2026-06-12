package model

import "time"

// StaffRole is an AxiaOps-employee role in the platform admin plane. It is
// ORTHOGONAL to the tenant roles (owner/admin/member/viewer) — a staff
// principal is never a tenant member and vice-versa (design §3, "a principal
// never spans planes"). See docs/saas-platform-admin-design.md §4.2.
type StaffRole string

const (
	// StaffRoleSupport reads tenant entitlement summaries and (later, via a
	// break-glass grant) tenant data. No money, no mutations.
	StaffRoleSupport StaffRole = "support"
	// StaffRoleOps handles tenant lifecycle + platform health. No billing PII.
	StaffRoleOps StaffRole = "ops"
	// StaffRoleBilling owns entitlement + plan writes and billing PII. No
	// tenant FinOps-data reads.
	StaffRoleBilling StaffRole = "billing"
	// StaffRoleSuperadmin manages staff_users + grants. The only role that
	// can mint other staff.
	StaffRoleSuperadmin StaffRole = "superadmin"
)

// ValidStaffRole reports whether r is one of the baseline §4.2 roles. The
// still-open auditor/engineering tier (§11.2 #5) is deliberately absent.
func ValidStaffRole(r StaffRole) bool {
	switch r {
	case StaffRoleSupport, StaffRoleOps, StaffRoleBilling, StaffRoleSuperadmin:
		return true
	default:
		return false
	}
}

// StaffUser is an AxiaOps employee identity in the admin plane. It mirrors the
// shape of model.User but lives in its own table with no OrganizationID — staff
// belong to no tenant. PasswordHash is the argon2id PHC string for the beta's
// native-credential auth; it is empty/nil once a corporate-IdP staff.Provider
// owns the row (design §4.1, the IdP swap stays behind the auth.Provider seam).
type StaffUser struct {
	ID           string
	Email        string
	Name         string
	PasswordHash string // argon2id PHC; empty for IdP-only rows
	Status       string // "active" | "suspended"
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastSeen     *time.Time
}

// Active reports whether the staff member may authenticate.
func (s StaffUser) Active() bool { return s.Status == "active" }

// StaffRoleGrant is a single (staff principal → role) assignment.
type StaffRoleGrant struct {
	ID          string
	StaffUserID string
	Role        StaffRole
	GrantedBy   string // staff_user_id of the granter; empty for the bootstrap superadmin
	CreatedAt   time.Time
}

// StaffTenantSummary is the read-only, NON-tenant-data view of one org the
// admin console surfaces (design §7.5 — reading this summary is NOT a tenant
// FinOps-data read and needs no break-glass grant). It is computed from
// existing tables only. The per-tenant `entitlements` table now exists as a
// dormant Phase 2A scaffold (migration 033, model.Entitlement), but its
// plan/status/limits are NOT surfaced here yet — wiring entitlement into the
// admin console is part of the deferred Phase 2B (design §7.1 / §11.1
// decision 3).
type StaffTenantSummary struct {
	OrganizationID         string
	OrgCode                string
	Name                   string
	CreatedAt              time.Time
	OnboardingCompletedAt  *time.Time
	AccountCount           int
	LastScanAt             *time.Time
	LatestTotalZombies     int
	LatestPotentialSavings float64
}
