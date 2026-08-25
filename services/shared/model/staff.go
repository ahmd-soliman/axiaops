package model

import "time"

// StaffRole is an AxiaOps-employee role in the platform admin plane. It is
// ORTHOGONAL to the tenant roles (owner/admin/member/viewer) — a staff
// principal is never a tenant member and vice-versa (design §3, "a principal
// never spans planes"). See docs/saas-platform-admin-design.md §4.2.
type StaffRole string

const (
	// StaffRoleSupport reads tenant summaries and (later, via a break-glass
	// grant) tenant data. No money, no mutations.
	StaffRoleSupport StaffRole = "support"
	// StaffRoleOps handles tenant lifecycle + platform health.
	StaffRoleOps StaffRole = "ops"
	// StaffRoleBilling is reserved for a future billing-adjacent staff
	// function. No billing system exists today (AxiaOps' hosted instance is
	// free — see docs/open-source-decision.md); no tenant FinOps-data reads.
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
// FinOps-data read and needs no break-glass grant). Computed from existing
// tables only.
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
