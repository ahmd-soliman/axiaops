package model

import "time"

// Organization represents a customer organisation identified by their Kinde org_code.
// The internal ID is a UUID generated on first login — never exposed to Kinde.
type Organization struct {
	ID                    string // internal UUID
	OrgCode               string // Kinde org_code claim
	Name                  string // org display name from JWT (may be empty)
	CreatedAt             time.Time
	OnboardingCompletedAt *time.Time // nil → wizard pending; non-nil → completed
}

// User represents an individual who has logged in.
// Linked to an Organization vian OrganizationID.
type User struct {
	ID             string // internal UUID
	OrganizationID string // FK → Organization.ID
	KindeSub       string // Kinde sub claim — stable user identifier (empty under AUTH_PROVIDER=native)
	Email          string // from JWT email claim or native signup
	Name           string
	CreatedAt      time.Time
	LastSeen       time.Time

	// Native-auth fields (Phase B1, AUTH_PROVIDER=native|both). PasswordHash
	// is empty for users provisioned via Kinde and for SSO-JIT users; both
	// authenticate through other paths. PasswordSetAt is non-nil iff
	// PasswordHash is non-empty.
	PasswordHash  string
	PasswordSetAt *time.Time
}
