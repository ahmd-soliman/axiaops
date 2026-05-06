package model

import "time"

// Organization represents a customer organisation. The internal ID is a UUID
// generated on first install / SSO discovery; OrgCode is a stable string used
// in URLs and API calls, set at creation time.
type Organization struct {
	ID                    string // internal UUID
	OrgCode               string // stable string identifier
	Name                  string // org display name (may be empty)
	CreatedAt             time.Time
	OnboardingCompletedAt *time.Time // nil → wizard pending; non-nil → completed
}

// User represents an individual who has logged in.
// Linked to an Organization via OrganizationID.
type User struct {
	ID             string // internal UUID
	OrganizationID string // FK → Organization.ID
	// ExternalID is the stable identifier from the external IdP for SSO
	// users (the OIDC `sub` claim), or a synthetic prefix for non-SSO
	// users: "native:<uuid>" for password users (matching users.id) and
	// "dev:<uuid>" for DEV_MODE users. Never empty. Backed by the UNIQUE
	// users.external_id column (renamed from kinde_sub in migration 024).
	ExternalID string
	Email      string // from SSO claims or native signup
	Name       string
	CreatedAt  time.Time
	LastSeen   time.Time

	// Native-auth fields (Phase B1). PasswordHash is empty for SSO-JIT
	// users; they authenticate via OIDC instead. PasswordSetAt is non-nil
	// iff PasswordHash is non-empty.
	PasswordHash  string
	PasswordSetAt *time.Time
}
