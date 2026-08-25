package model

import "time"

// Membership joins a User to an Organization with a specific role. A user can
// belong to multiple organizations; the (organization_id, user_id) pair is
// unique. See docs/AUTHENTICATION.md (§2) for the role model rationale.
type Membership struct {
	ID             string
	OrganizationID string
	UserID         string
	Role           string // one of: owner, admin, member, viewer
	InvitedBy      string // FK → User.ID; empty for backfilled and bootstrap rows
	// ProvisionedVia records how the membership got created — one of
	// model.ProvisionedVia* (manual / invitation / jit / scim / legacy).
	// SaveMembership writes this column; callers that don't set it get
	// the column default ('manual'). JIT and SCIM callers MUST set it
	// explicitly so admin team-review surfaces the correct provenance.
	// Added by migration 022 (Phase B2). See services/shared/model/sso.go.
	ProvisionedVia string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// MembershipWithUser is the read-side projection used by the user-management
// UI: a Membership plus the corresponding user's email and name. Returned by
// Store.ListMemberships.
type MembershipWithUser struct {
	Membership
	Email string
	Name  string
}

// MembershipWithOrganization is the read-side projection used by the multi-org
// access UI (org picker after login, org switcher in nav): a Membership plus
// the corresponding organization's display fields. Returned by
// Store.ListUserMemberships. The dual of MembershipWithUser — same shape, the
// "other side" of the join.
type MembershipWithOrganization struct {
	Membership
	OrganizationName    string
	OrganizationOrgCode string
}
