package model

import "time"

// Membership joins a User to an Organization with a specific role. A user can
// belong to multiple organizations; the (organization_id, user_id) pair is
// unique. See docs/rbac-design.md §4 for the data model rationale.
type Membership struct {
	ID             string
	OrganizationID string
	UserID         string
	Role           string // one of: owner, admin, member, viewer
	InvitedBy      string // FK → User.ID; empty for backfilled and bootstrap rows
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
