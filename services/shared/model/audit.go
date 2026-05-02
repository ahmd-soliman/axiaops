// Package model — AuditEvent is a single row in the audit_log table, recording
// one user-initiated mutation. See docs/audit_trail_plan.md for the full design.
package model

import (
	"net"
	"time"
)

// AuditEvent is a single audit_log row. Only user-initiated mutating actions
// are recorded — reads and scheduled/automated scans are excluded.
type AuditEvent struct {
	ID             int64          `json:"id"`
	OrganizationID string         `json:"organization_id,omitempty"`
	UserID         string         `json:"user_id,omitempty"` // NULL after GDPR anonymisation
	ActorEmail     string         `json:"actor_email"`       // captured at event time
	Action         string         `json:"action"`            // one of AuditAction* constants
	ResourceType   string         `json:"resource_type,omitempty"`
	ResourceID     string         `json:"resource_id,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	RequestID      string         `json:"request_id,omitempty"`
	IPAddress      net.IP         `json:"ip_address,omitempty"`
	UserAgent      string         `json:"user_agent,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

// Audit action constants. Values match the action column in audit_log.
// Keep this list in sync with docs/audit_trail_plan.md §3.2.
//
// Rule: audit_log is mutation-only. A user viewing a command, reading a list,
// or opening a detail page does not belong here — CloudTrail handles real AWS
// actions, and product telemetry is a better home for UX analytics.
const (
	AuditActionDismissZombie           = "dismiss_zombie"
	AuditActionSnoozeZombie            = "snooze_zombie"
	AuditActionRevokeDismissal         = "revoke_dismissal"
	AuditActionScanTriggered           = "scan_triggered"
	AuditActionAccountConnected        = "account_connected"
	AuditActionAccountUpdated          = "account_updated"
	AuditActionAccountDeleted          = "account_deleted"
	AuditActionAccountRoleDraftCreated = "account_role_draft_created"
	AuditActionAccountRoleVerified     = "account_role_verified"
	AuditActionAccountRoleVerifyFailed = "account_role_verify_failed"
	AuditActionMemberInvited           = "member_invited"
	AuditActionMemberRoleChanged       = "member_role_changed"
	AuditActionMemberRemoved           = "member_removed"
	AuditActionOwnershipTransferred    = "ownership_transferred"
	// AuditActionOrganizationDeleted is written immediately before an organization
	// cascade delete (DELETE /v1/organizations/me). The row itself gets purged
	// with the rest of audit_log, so its only durable trace is the structured
	// slog line and the axiaops_organization_deletions_total Prometheus counter.
	AuditActionOrganizationDeleted = "organization_deleted"
	// AuditActionDataExported is written when an owner downloads the organization's
	// GDPR data export (GET /v1/export). The Metadata map carries the row
	// counts per table so a DSR audit can show *what* was exported, not just
	// that an export happened.
	AuditActionDataExported = "data_exported"
	// AuditActionOrganizationRenamed records a successful PATCH /v1/organizations/me.
	// Metadata carries old_name and new_name plus kinde_synced (whether the
	// outbound push to Kinde succeeded — false would only happen if the local
	// commit raced ahead of a Kinde failure that wasn't rolled back, which the
	// two-phase commit prevents but the field is reserved for safety).
	AuditActionOrganizationRenamed = "organization_renamed"
	// AuditActionOnboardingCompleted records the wizard reaching the final
	// step. Metadata carries steps_skipped (subset of "invite", "aws-account").
	AuditActionOnboardingCompleted = "onboarding_completed"
	// Phase B1 — native auth lifecycle. The login/logout actions are
	// intentionally absent: per docs/audit_trail_plan.md §2 audit_log is
	// mutation-only, and authn-success / authn-failure belong in the
	// auth-counter Prometheus metrics + slog (`axiaops_auth_login_total`).
	// What lives here is the *state-changing* side of authn.
	AuditActionUserPasswordChanged       = "user_password_changed"
	AuditActionUserPasswordResetIssued   = "user_password_reset_issued"
	AuditActionUserPasswordResetRedeemed = "user_password_reset_redeemed"
	AuditActionInvitationRedeemedNative  = "invitation_redeemed_native"
	AuditActionBootstrapCompleted        = "bootstrap_completed"
	AuditActionSessionRevokedByAdmin     = "session_revoked_by_admin"
	// Phase B1.6 — license-file TTL enforcement (docs/sso-implementation-plan.md
	// §4.9). The license JWT is verified at boot and re-classified hourly by
	// the runtime ticker; both code paths write into audit_log so an operator
	// reviewing the timeline sees the full lifecycle.
	//
	// AuditActionLicenseLoaded — written at every successful boot. Metadata
	// carries license_id, contract_id, customer_id, expires_at.
	// AuditActionLicenseInGracePeriod — written at boot OR when the runtime
	// ticker observes the valid → in_grace transition. Metadata carries
	// license_id, days_remaining, exp + grace_period_days.
	// AuditActionLicenseExpiredHardFail — written at boot only, immediately
	// before the process exits because state == expired (past grace).
	// AuditActionLicenseExpiredRuntime — written by the runtime ticker on the
	// in_grace → expired transition mid-flight. Distinct from hard_fail because
	// the process keeps running; the scan-gate is what flips, not the binary.
	// AuditActionLicenseRenewed — forward-compat for slice 4+ if we add
	// detection of "this boot's license_id differs from the previous boot's";
	// not wired yet.
	// AuditActionLicenseInvalidSignature — written at boot when JWT verification
	// fails (signature/alg/iss/aud/iat). The corresponding boot also exits.
	AuditActionLicenseLoaded           = "license_loaded"
	AuditActionLicenseInGracePeriod    = "license_in_grace_period"
	AuditActionLicenseExpiredHardFail  = "license_expired_hard_fail"
	AuditActionLicenseExpiredRuntime   = "license_expired_runtime"
	AuditActionLicenseRenewed          = "license_renewed"
	AuditActionLicenseInvalidSignature = "license_invalid_signature"
	// AuditActionSessionOrgSwitched is written to the FROM org's audit log
	// when a user POSTs /v1/auth/switch-org and rotates their session to a
	// different org they're a member of (B1.5 §4.7.4). Metadata carries
	// {from, to} — user_id is already on the audit row's actor field, so
	// duplicating it in metadata would just inflate the row. Underscore
	// spelling matches all other audit constants; plan §4.7.4 used dot
	// notation in prose, but consistency wins here.
	AuditActionSessionOrgSwitched = "session_org_switched"
	// Phase B2 — Native OIDC RP. Twelve actions span SSO config-time mutations
	// (created/updated/deleted/disabled/enforcement) and runtime-flow events
	// (login succeeded/failed, JIT-provisioned membership, domain verification
	// lifecycle). The login_succeeded / login_failed pair are the SSO mirror of
	// what the auth-counter Prometheus metrics already cover; they live in
	// audit_log as well because an org admin reviewing "who has been signing in
	// via SSO this week" needs the per-row trail, not just an aggregate.
	// Metadata carries connection_id, protocol, and a reason code where
	// applicable. See docs/sso-integration-design.md §4.5.
	AuditActionSSOConnectionCreated         = "sso_connection_created"
	AuditActionSSOConnectionUpdated         = "sso_connection_updated"
	AuditActionSSOConnectionDeleted         = "sso_connection_deleted"
	AuditActionSSOConnectionDisabled        = "sso_connection_disabled"
	AuditActionSSOEnforcementChanged        = "sso_enforcement_changed"
	AuditActionSSODomainVerificationStarted = "sso_domain_verification_started"
	AuditActionSSODomainVerified            = "sso_domain_verified"
	AuditActionSSODomainRevoked             = "sso_domain_revoked"
	AuditActionSSOGroupMappingChanged       = "sso_group_mapping_changed"
	AuditActionSSOLoginSucceeded            = "sso_login_succeeded"
	AuditActionSSOLoginFailed               = "sso_login_failed"
	AuditActionSSOJITProvisioned            = "sso_jit_provisioned"
	AuditActionSSOJITRoleUpdated            = "sso_jit_role_updated"
)

// ValidAuditActions is the authoritative set of action codes accepted on write
// and returned by GET /v1/audit filters.
var ValidAuditActions = map[string]bool{
	AuditActionDismissZombie:             true,
	AuditActionSnoozeZombie:              true,
	AuditActionRevokeDismissal:           true,
	AuditActionScanTriggered:             true,
	AuditActionAccountConnected:          true,
	AuditActionAccountUpdated:            true,
	AuditActionAccountDeleted:            true,
	AuditActionAccountRoleDraftCreated:   true,
	AuditActionAccountRoleVerified:       true,
	AuditActionAccountRoleVerifyFailed:   true,
	AuditActionMemberInvited:             true,
	AuditActionMemberRoleChanged:         true,
	AuditActionMemberRemoved:             true,
	AuditActionOwnershipTransferred:      true,
	AuditActionOrganizationDeleted:       true,
	AuditActionDataExported:              true,
	AuditActionOrganizationRenamed:       true,
	AuditActionOnboardingCompleted:       true,
	AuditActionUserPasswordChanged:       true,
	AuditActionUserPasswordResetIssued:   true,
	AuditActionUserPasswordResetRedeemed: true,
	AuditActionInvitationRedeemedNative:  true,
	AuditActionBootstrapCompleted:        true,
	AuditActionSessionRevokedByAdmin:     true,
	AuditActionLicenseLoaded:             true,
	AuditActionLicenseInGracePeriod:      true,
	AuditActionLicenseExpiredHardFail:    true,
	AuditActionLicenseExpiredRuntime:     true,
	AuditActionLicenseRenewed:            true,
	AuditActionLicenseInvalidSignature:   true,
	AuditActionSessionOrgSwitched:        true,
	// Phase B2 SSO actions
	AuditActionSSOConnectionCreated:         true,
	AuditActionSSOConnectionUpdated:         true,
	AuditActionSSOConnectionDeleted:         true,
	AuditActionSSOConnectionDisabled:        true,
	AuditActionSSOEnforcementChanged:        true,
	AuditActionSSODomainVerificationStarted: true,
	AuditActionSSODomainVerified:            true,
	AuditActionSSODomainRevoked:             true,
	AuditActionSSOGroupMappingChanged:       true,
	AuditActionSSOLoginSucceeded:            true,
	AuditActionSSOLoginFailed:               true,
	AuditActionSSOJITProvisioned:            true,
	AuditActionSSOJITRoleUpdated:            true,
}

// AuditFilter parameterises AuditLogList queries. Zero-value fields are not
// applied — a zero filter returns the full organization timeline (bounded by Limit).
type AuditFilter struct {
	UserID       string
	ResourceType string
	ResourceID   string
	Action       string
	Since        time.Time
	Until        time.Time
	// Limit is the max rows returned. Zero lets the store pick its maximum
	// (500). Callers that display to a user should set a sensible default —
	// the HTTP handler uses 50.
	Limit int
	// Cursor is the opaque pagination token from the previous page's
	// next_cursor; zero means "start from the newest row".
	Cursor AuditCursor
}

// AuditCursor is a (created_at, id) pair for stable pagination under concurrent
// inserts. A zero cursor means "start from the newest row".
//
// JSON tags intentionally omit `omitempty` — encoding a cursor with ID=0
// (unlikely but possible if audit_log_id_seq is ever reset in a test DB) must
// round-trip through JSON intact, not silently collapse to a "start over" cursor.
type AuditCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        int64     `json:"id"`
}

// IsZero reports whether the cursor has been set.
func (c AuditCursor) IsZero() bool {
	return c.ID == 0 && c.CreatedAt.IsZero()
}
