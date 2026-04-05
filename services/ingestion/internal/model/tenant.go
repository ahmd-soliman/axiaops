package model

import "time"

// Tenant represents a customer organisation identified by their Kinde org_code.
// The internal ID is a UUID generated on first login — never exposed to Kinde.
type Tenant struct {
	ID        string    // internal UUID
	OrgCode   string    // Kinde org_code claim
	Name      string    // org display name from JWT (may be empty)
	CreatedAt time.Time
}

// User represents an individual who has logged in.
// Linked to a Tenant via TenantID.
type User struct {
	ID        string    // internal UUID
	TenantID  string    // FK → Tenant.ID
	KindeSub  string    // Kinde sub claim — stable user identifier
	Email     string    // from JWT email claim (may be empty)
	Name      string    // from JWT name claim (may be empty)
	CreatedAt time.Time
	LastSeen  time.Time
}
