package model

import "time"

// PendingInvitation represents an email-based team invitation awaiting first-login
// redemption.
//
// Lifecycle: created by POST /v1/invitations → row inserted with status='pending'.
// On the invitee's first authenticated request, the auth middleware calls
// RedeemPendingInvitation which atomically inserts a memberships row and DELETES
// the pending row in one transaction. Revocation flips status='revoked'.
// Expiry is enforced at the read-side (RedeemPendingInvitation filters
// expires_at > NOW()); a future background sweeper flips ripe rows to 'expired'.
type PendingInvitation struct {
	ID              string    // UUID
	OrganizationID  string    // FK → Organization.ID
	Email           string    // raw email; matched case-insensitively via lower()
	Role            string    // one of: admin, member, viewer (never owner)
	InvitedByUserID string    // FK → User.ID; the inviter
	InvitedByEmail  string    // captured at invite time; survives inviter deletion
	Status          string    // one of: pending, expired, revoked
	ExpiresAt       time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time

	// InviteTokenHash is hex(SHA-256(plaintext token)). NOT NULL since
	// migration 024 — every pending_memberships row carries one. Used by
	// RedeemNativeInvitation as the lookup key. The plaintext token never
	// leaves the handler that minted it — never logged, never persisted.
	InviteTokenHash string
}

// Invitation status constants. Values match the status column in pending_memberships.
const (
	InvitationStatusPending = "pending"
	InvitationStatusExpired = "expired"
	InvitationStatusRevoked = "revoked"
)

// ValidInvitationStatuses is the authoritative set of status codes accepted on
// queries (e.g. GET /v1/invitations?status=pending).
var ValidInvitationStatuses = map[string]bool{
	InvitationStatusPending: true,
	InvitationStatusExpired: true,
	InvitationStatusRevoked: true,
}

// ValidInvitationRoles is the authoritative set of role values accepted on
// invitation creation. Owner is intentionally excluded — owners come from
// EnsureFirstMembership / TransferOwnership, never an invitation.
var ValidInvitationRoles = map[string]bool{
	"admin":  true,
	"member": true,
	"viewer": true,
}
