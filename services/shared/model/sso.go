// Package model — SSO connection / domain / group mapping types backing
// the schema in migration 022_sso_core.up.sql.
package model

import "time"

// SSO protocol constants. The DB CHECK constraint on sso_connections.protocol
// keeps these in sync — adding a value here without a migration would fail at
// INSERT time, which is the desired property.
const (
	SSOProtocolOIDC = "oidc"
	SSOProtocolSAML = "saml"
)

// SSO connection status. Draft connections are not consulted at login —
// only `active` rows show up in the discoverer. `disabled` is a hard stop
// (logins fail) but the row is preserved so audit trails stay readable.
const (
	SSOStatusDraft    = "draft"
	SSOStatusActive   = "active"
	SSOStatusDisabled = "disabled"
)

// Per-organisation enforcement of SSO. Required blocks native-password
// sessions for the org (middleware enforces post-B2 slice 3a). See plan §5.2.
const (
	SSOEnforcementOptional  = "optional"
	SSOEnforcementPreferred = "preferred"
	SSOEnforcementRequired  = "required"
)

// Domain verification lifecycle. Pending → Verified → (Stale) on TTL elapse;
// owner can manually move to Revoked. Only Verified rows route logins.
const (
	SSODomainStatusPending  = "pending"
	SSODomainStatusVerified = "verified"
	SSODomainStatusStale    = "stale"
	SSODomainStatusRevoked  = "revoked"
)

// Membership.provisioned_via — added by migration 022. JIT and SCIM are SSO
// flows; manual covers the bootstrap owner and the explicit POST /v1/memberships
// path; invitation covers the redemption flow that already existed pre-B1.
//
// ProvisionedViaLegacy is the backfill marker for pre-B2 rows whose provenance
// is unrecoverable (`pending_memberships` rows are deleted on redemption, so
// we can't reconstruct whether an existing membership came in via invite vs.
// the manual paths). NEW code MUST set one of the four concrete values; never
// write 'legacy' from new code.
const (
	ProvisionedViaManual     = "manual"
	ProvisionedViaInvitation = "invitation"
	ProvisionedViaJIT        = "jit"
	ProvisionedViaSCIM       = "scim"
	ProvisionedViaLegacy     = "legacy"
)

// SSOConnection is one (organization, IdP) pair. The protocol-specific fields
// (oidc_*, saml_*) are populated according to Protocol; the unused side stays
// at zero values. OIDCClientSecretCiphertext is AES-256-GCM via crypto.Encrypt
// with the same ENCRYPTION_KEY used for accounts.aws_secret_key_ciphertext.
type SSOConnection struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`

	Protocol    string `json:"protocol"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	Enforcement string `json:"enforcement"`
	DefaultRole string `json:"default_role"`

	IdPIssuer      string `json:"idp_issuer,omitempty"`
	IdPMetadataURL string `json:"idp_metadata_url,omitempty"`
	IdPMetadataXML string `json:"idp_metadata_xml,omitempty"`

	OIDCClientID                string `json:"oidc_client_id,omitempty"`
	OIDCClientSecretCiphertext  []byte `json:"-"` // never returned in API responses
	OIDCDiscoveryURL            string `json:"oidc_discovery_url,omitempty"`
	OIDCTenantID                string `json:"oidc_tenant_id,omitempty"`

	// Phase C SAML fields. Populated by Phase C; carried in the model now so
	// CRUD on connections doesn't fork by phase.
	SAMLSSOURL                string     `json:"saml_sso_url,omitempty"`
	SAMLSigningCert           string     `json:"saml_signing_cert,omitempty"`
	SAMLPreviousCert          string     `json:"saml_previous_cert,omitempty"`
	SAMLPreviousCertExpiresAt *time.Time `json:"saml_previous_cert_expires_at,omitempty"`

	// ForceReauth controls the OIDC authorize URL's `prompt=login` parameter.
	// True (default per migration 023) forces the IdP to re-authenticate on
	// every ceremony — closes silent-identity-substitution bugs where the
	// IdP cookie outlives the AxiaOps logout. False suppresses prompt=login
	// for IdPs that enforce their own session policy (Azure AD conditional
	// access returns `interaction_required` if you ask for re-auth on a
	// locked session). See services/api/internal/sso/initiate.go.
	ForceReauth bool `json:"force_reauth"`

	// KindeConnectionID is empty under self-hosted (Option B) — handlers MUST
	// reject non-empty values on POST/PATCH per design §4.2. Populated only
	// under a future SaaS reactivation.
	KindeConnectionID string `json:"kinde_connection_id,omitempty"`

	// SCIM forward-compat. Filled in Phase E; never read in B2.
	SCIMTokenCiphertext []byte `json:"-"`
	SCIMEndpoint        string `json:"scim_endpoint,omitempty"`

	CreatedByUserID string    `json:"created_by_user_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// SSODomain is a verified email domain that routes logins to a connection.
// VerificationToken is the value the customer must publish in a TXT record
// (or pasted-in equivalent) — UNIQUE in the DB so a leaked token can't
// match an attacker-controlled domain row.
type SSODomain struct {
	ID                string     `json:"id"`
	OrganizationID    string     `json:"organization_id"`
	SSOConnectionID   string     `json:"sso_connection_id"`
	Domain            string     `json:"domain"`
	Status            string     `json:"status"`
	VerificationToken string     `json:"verification_token,omitempty"` // omitted from list responses; only returned on create
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	LastAssertedAt    *time.Time `json:"last_asserted_at,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// SSOGroupMapping binds an IdP group identifier to an AxiaOps role. The IdP
// group identifier is whatever the IdP sends — Entra GUID, Okta group name,
// generic SAML attribute value. Comparison is string-equal; case-sensitivity
// is preserved (Entra GUIDs are case-sensitive).
type SSOGroupMapping struct {
	ID               string    `json:"id"`
	OrganizationID   string    `json:"organization_id"`
	SSOConnectionID  string    `json:"sso_connection_id"`
	GroupExternalID  string    `json:"group_external_id"`
	GroupDisplayName string    `json:"group_display_name,omitempty"`
	Role             string    `json:"role"`
	CreatedAt        time.Time `json:"created_at"`
}
