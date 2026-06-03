// Package storage defines the Store interface for persisting cost records.
// PostgreSQL is the only storage implementation.
// without changing any other code.
package storage

import (
	"context"
	"errors"
	"time"

	"axiaops.io/shared/model"
)

// ErrAlreadyDismissed is returned when a zombie resource already has an active
// dismissal or snooze.  Callers should surface this as HTTP 409 Conflict.
var ErrAlreadyDismissed = errors.New("storage: resource already has an active dismissal")

// ErrMembershipNotFound is returned by membership reads/mutations when the
// target row does not exist. Surface as HTTP 404.
var ErrMembershipNotFound = errors.New("storage: membership not found")

// ErrUserNotFound is returned by user lookups (e.g. GetUserByEmail) when no
// row matches. Surface as HTTP 404 — used by the invite-by-email flow to
// distinguish "user has not logged in yet" from real errors.
var ErrUserNotFound = errors.New("storage: user not found")

// ErrLastOwner is returned by membership mutations that would leave an organization
// with zero owners. Surface as HTTP 409.
var ErrLastOwner = errors.New("storage: cannot remove or demote the last owner")

// ErrMembershipExists is returned by SaveMembership when a membership already
// exists for (organization_id, user_id). Surface as HTTP 409.
var ErrMembershipExists = errors.New("storage: membership already exists for user in organization")

// ErrOrganizationNotFound is returned by organization reads/mutations when
// the target row does not exist. Surface as HTTP 404.
var ErrOrganizationNotFound = errors.New("storage: organization not found")

// ErrInvitationNotFound is returned by invitation reads/mutations when the
// target row does not exist. Surface as HTTP 404.
var ErrInvitationNotFound = errors.New("storage: invitation not found")

// ErrInvitationNotPending is returned by RevokePendingInvitation when the row
// is already revoked or expired. Surface as HTTP 410.
var ErrInvitationNotPending = errors.New("storage: invitation is not in pending state")

// ErrInvitationAlreadyMember is returned by CreatePendingInvitation when the
// email already has an active membership in the organization. Surface as HTTP 409.
var ErrInvitationAlreadyMember = errors.New("storage: email already has a membership in this organization")

// ErrUserExistsNoMembership is returned by CreatePendingInvitation when the
// email matches an existing user without a membership in this organization.
// Surface as HTTP 409 with a hint to use POST /v1/memberships.
var ErrUserExistsNoMembership = errors.New("storage: email matches an existing user without membership — use POST /v1/memberships")

// ErrChannelNotFound is returned by GetNotificationChannel / DeleteNotificationChannel
// when no channel with the given ID exists in the request organization.
var ErrChannelNotFound = errors.New("storage: notification channel not found")

// ErrSSOConnectionNotFound is returned by SSO connection reads/mutations when
// the row does not exist (or is RLS-invisible to the request org). Surface as 404.
var ErrSSOConnectionNotFound = errors.New("storage: sso connection not found")

// ErrSSODomainNotFound is returned by SSO domain reads/mutations when the row
// does not exist (or no verified row matches a discoverer lookup). Surface as 404.
var ErrSSODomainNotFound = errors.New("storage: sso domain not found")

type ctxKey string

const organizationKey ctxKey = "organization_id"

// WithOrganizationID returns a context carrying the given organization ID.
// The PostgreSQL store reads this to set app.organization_id for Row-Level Security.
func WithOrganizationID(ctx context.Context, organizationID string) context.Context {
	return context.WithValue(ctx, organizationKey, organizationID)
}

// OrganizationIDFromCtx returns the organization ID stored in the context, or "".
func OrganizationIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(organizationKey).(string)
	return v
}

// CostFilter specifies criteria for listing cost records.
type CostFilter struct {
	InternalAccountID string // optional: filter by internal_account_id (system account ID)
	AWSAccountID      string // optional: filter by account_id (AWS account ID) — for backward compatibility with old records
	Service           string // optional: filter by service name
	Days              int    // optional: trailing lookback window in days (default: 30). Ignored when Since/Until are set.

	// Since/Until select an absolute window on period_start (both inclusive).
	// When either is non-zero the absolute window takes precedence over Days,
	// so the caller can request a fixed calendar range (e.g. the first half of
	// a month) rather than a trailing "last N days" window. Both zero → Days.
	Since time.Time
	Until time.Time
}

// Store persists and retrieves cost records, organizations, and users.
type Store interface {
	// NativeAuthStore — Phase B1 native auth methods. See
	// storage_native_auth.go for the full method set.
	NativeAuthStore

	// Save upserts a batch of cost records, refreshing amount/currency/tags/
	// fetched_at/internal_account_id when an existing conflict-key row is
	// touched. Returns (inserted, updated) counts so callers can split
	// metrics by which path each record took. See
	// docs/cost-records-upsert-plan.md for rationale.
	Save(ctx context.Context, records []model.CostRecord) (inserted, updated int64, err error)

	// SaveZombies replaces all zombie records with the latest detection results.
	// Called by the ingestion job after each analysis run.
	// ctx must carry an organization ID via WithOrganizationID when using PostgreSQL.
	SaveZombies(ctx context.Context, zombies []model.ZombieResource) error

	// LoadZombies returns zombie records for the organization in ctx.
	// Called by the API service per request.
	// ctx must carry an organization ID via WithOrganizationID when using PostgreSQL.
	LoadZombies(ctx context.Context) ([]model.ZombieResource, error)

	// UpsertOrganization creates an organization on first login or returns
	// the existing one. Keyed on org_code, a stable string identifier set
	// at organization creation time.
	UpsertOrganization(ctx context.Context, orgCode, name string) (model.Organization, error)

	// GetOrganizationByID returns the organization with the given UUID, or
	// ErrOrganizationNotFound when no row matches. Pure read — does not
	// mutate. Used by /v1/me under native auth where the request already
	// carries the real organization_id and upserting by org_code would
	// silently create a phantom row.
	GetOrganizationByID(ctx context.Context, id string) (model.Organization, error)

	// RenameOrganization updates the organization name for the org in ctx.
	// AxiaOps owns the name field after first insert; this is the only path
	// that updates it (UpsertOrganization is now insert-only on name).
	// Returns ErrOrganizationNotFound when no row matches.
	RenameOrganization(ctx context.Context, name string) error

	// MarkOnboardingComplete sets onboarding_completed_at = NOW() on the
	// organization in ctx if not already set. Returns the timestamp on the row
	// (existing one if already complete, or the just-set one). Idempotent.
	MarkOnboardingComplete(ctx context.Context) (time.Time, error)

	// EnsureOrganization creates an organization with a caller-supplied id if no row with
	// that id exists yet. Unlike UpsertOrganization, the id is pinned (not a UUID)
	// and the row is never modified on conflict. Used by dev mode at startup
	// to guarantee a known-id organization row for FK references.
	EnsureOrganization(ctx context.Context, id, orgCode, name string) error

	// UpsertUser creates a user on first login or updates last_seen.
	// Keyed on the stable external identifier (SSO `sub` for SSO users;
	// `native:<id>` synthetic prefix for native-auth users; `dev:<id>`
	// for DEV_MODE users).
	UpsertUser(ctx context.Context, organizationID, externalID, email, name string) (model.User, error)

	// EnsureUser creates a user with a caller-supplied id, or updates
	// organization_id/email/name/last_seen if a row with that id already exists.
	// Unlike UpsertUser the id is pinned by the caller (not generated). Used
	// by dev mode at startup so DevBypass can inject a stable user_id alongside
	// the dev organization_id.
	//
	// Only u.ID, u.OrganizationID, u.Email, and u.Name are read. ExternalID is
	// derived by the implementation (synthetic "dev:<id>" for the Postgres
	// impl). Timestamps are set to NOW().
	EnsureUser(ctx context.Context, u model.User) error

	// SaveAccount inserts or replaces a connected cloud account for an organization.
	SaveAccount(ctx context.Context, a model.Account) error

	// ListAccounts returns all connected accounts for the organization in ctx.
	ListAccounts(ctx context.Context) ([]model.Account, error)

	// ListAllAccounts returns accounts for ALL organizations, bypassing row-level security.
	// Used internally by the scheduled scan scheduler to check all accounts across all organizations.
	// WARNING: This must only be called from trusted internal code (e.g., background jobs).
	// Never call with untrusted input. ctx.organization_id is ignored if present.
	ListAllAccounts(ctx context.Context) ([]model.Account, error)

	// GetAccount returns a single account by ID for the organization in ctx.
	GetAccount(ctx context.Context, id string) (model.Account, error)

	// DeleteAccount removes an account by ID for the organization in ctx.
	DeleteAccount(ctx context.Context, id string) error

	// UpdateAccountStatus sets the status and last_scanned_at for an account.
	UpdateAccountStatus(ctx context.Context, id, status string) error

	// SetAccountError marks the account as failed and stores a human-readable
	// reason in error_message. Called from the scan loop and the role-verify
	// failure path so the dashboard can surface a single source of truth for
	// "why is this account broken" without operators reading logs.
	SetAccountError(ctx context.Context, id, message string) error

	// TryMarkAccountScanning sets status to scanning only if not already scanning.
	// Returns true when the row was updated; false when another scan is in progress.
	TryMarkAccountScanning(ctx context.Context, id string) (bool, error)

	// SaveResources replaces all resource records with the latest inventory.
	// Called by the ingestion job after each analysis run.
	// ctx must carry an organization ID via WithOrganizationID when using PostgreSQL.
	SaveResources(ctx context.Context, resources []model.ResourceRecord) error

	// LoadResources returns all resource records for the organization in ctx.
	// Called by the API service per request.
	// ctx must carry an organization ID via WithOrganizationID when using PostgreSQL.
	LoadResources(ctx context.Context) ([]model.ResourceRecord, error)

	// ListCostRecords returns cost records for the organization in ctx, filtered by account, service, and time window.
	// Records with amount > 0 are returned, ordered by period_start (newest first) then amount (largest first).
	// If filter.Days is 0 or negative, defaults to 30 days.
	// ctx must carry an organization ID via WithOrganizationID when using PostgreSQL.
	ListCostRecords(ctx context.Context, filter CostFilter) ([]model.CostRecord, error)

	// SaveSnapshot writes a zombie snapshot after each ingestion scan.
	// Snapshots are never replaced — they accumulate to form the savings history.
	// ctx must carry an organization ID via WithOrganizationID when using PostgreSQL.
	SaveSnapshot(ctx context.Context, snap model.ZombieSnapshot) error

	// ListSnapshots returns zombie snapshots for the organization in ctx, ordered
	// oldest-first. If accountID is non-empty, only snapshots for that account
	// are returned.
	ListSnapshots(ctx context.Context, accountID string) ([]model.ZombieSnapshot, error)

	// SaveSnapshotServices writes per-service breakdown rows for a snapshot.
	SaveSnapshotServices(ctx context.Context, services []model.SnapshotService) error

	// ListSnapshotsByService returns zombie snapshots filtered by service,
	// ordered oldest-first. Each snapshot's cost/count reflects only the given
	// service. If resourceType is non-empty, further filters to that sub-type;
	// otherwise aggregates all resource types for the service.
	// If accountID is non-empty, also filters by account.
	ListSnapshotsByService(ctx context.Context, service, resourceType, accountID string) ([]model.ZombieSnapshot, error)

	// ListTrendServices returns the distinct services that have snapshot data
	// for the organization, useful for populating filter UI.
	ListTrendServices(ctx context.Context) ([]string, error)

	// ListTrendResourceTypes returns distinct resource types for a given service
	// that have snapshot data for the organization.
	ListTrendResourceTypes(ctx context.Context, service string) ([]string, error)

	// DeleteOldCostRecords removes cost records older than the given cutoff for all organizations.
	// Returns the number of rows deleted.
	DeleteOldCostRecords(ctx context.Context, cutoff time.Time) (int64, error)

	// DismissZombie records a dismiss or snooze action for a zombie resource.
	// Returns the new dismissal ID.
	// Returns ErrAlreadyDismissed if an active dismissal already exists for the fingerprint.
	DismissZombie(ctx context.Context, d model.DismissAction) (int64, error)

	// RevokeDismissal soft-deletes an active dismissal (sets revoked_at / revoked_by).
	// Returns an error if the dismissal does not exist or is already revoked.
	RevokeDismissal(ctx context.Context, id int64, revokedBy string) error

	// ListActiveDismissals returns all active (non-revoked, non-expired) dismissals
	// for the organization in ctx.  If accountID is non-empty, only that account is returned.
	ListActiveDismissals(ctx context.Context, accountID string) ([]model.DismissAction, error)

	// ExpireSnoozes marks snoozed records whose snoozed_until has passed as revoked.
	// This is a cross-organization operation called by the background maintenance worker.
	// Returns the number of records expired.
	ExpireSnoozes(ctx context.Context) (int64, error)

	// AuditLogWrite records a user-initiated mutation in audit_log.
	// ctx must carry an organization ID via WithOrganizationID.  Returns the new row ID.
	// Callers must treat audit writes as best-effort — log the error, bump the
	// axiaops_audit_writes_total{status="failed"} counter, and continue. Failing
	// the underlying user operation because an audit row couldn't be written is
	// a worse outcome than a missing audit row.
	AuditLogWrite(ctx context.Context, e model.AuditEvent) (int64, error)

	// AuditLogList returns audit events for the organization in ctx in
	// (created_at DESC, id DESC) order. Zero-valued filter fields are not
	// applied. Limit is capped at 500 by the implementation.
	AuditLogList(ctx context.Context, f model.AuditFilter) ([]model.AuditEvent, error)

	// AuditLogAnonymiseUser nulls user_id and replaces actor_email with a
	// tombstone marker for all rows matching (organization_id, user_id) — used by
	// user-deletion and organization-deletion (GDPR) paths.  Returns the number of
	// rows modified.
	AuditLogAnonymiseUser(ctx context.Context, userID string) (int64, error)

	// ── Memberships (RBAC Phase 1, see docs/rbac-design.md) ──────────────────

	// RoleOf returns the role string ("owner"|"admin"|"member"|"viewer") for
	// (organizationID, userID), or "" with nil error when no membership row exists.
	// The implementation must enforce RLS — i.e. open its own transaction
	// and SET LOCAL app.organization_id before SELECTing. Called from the auth
	// middleware on every request, so must be cheap.
	RoleOf(ctx context.Context, organizationID, userID string) (string, error)

	// ListMemberships returns all memberships in the organization in ctx, joined
	// with users for email/name. Used by the admin user-management screen.
	ListMemberships(ctx context.Context) ([]model.MembershipWithUser, error)

	// ListUserMemberships returns every active membership for the given user,
	// across all organizations they belong to, joined with organization
	// metadata for display. Bypasses RLS via the admin pool — by definition
	// the result spans organizations, so a per-org RLS filter cannot apply.
	//
	// Safety: callers MUST pass a userID that came from a validated auth
	// context (session or JWT). Never accept this from request input — the
	// query returns one user's full org list, which is sensitive. Used by
	// `/v1/me` to populate the org-switcher payload (B1.5) and by
	// `/v1/auth/select-org` to validate that the caller actually belongs to
	// the org they picked at login time.
	//
	// Returns an empty slice (not an error) when the user exists but has no
	// memberships — same convention as RoleOf returning "" for the no-row
	// case. Sorted by created_at ASC so the picker's display order is stable
	// across calls.
	ListUserMemberships(ctx context.Context, userID string) ([]model.MembershipWithOrganization, error)

	// GetMembership returns a single membership by ID for the organization in ctx.
	// Returns ErrMembershipNotFound if the row does not exist (or belongs to
	// another organization — RLS hides it the same way).
	GetMembership(ctx context.Context, id string) (model.Membership, error)

	// GetMembershipByOrgUser returns the membership for (organizationID, userID)
	// in one query. Used by JIT reconciliation to avoid an O(n) walk of
	// ListMemberships when the user already has a membership but their group
	// claim resolved to a different role. Returns ErrMembershipNotFound when
	// no row matches. Bypasses RLS via the admin pool — JIT runs in a flow
	// where the org context is established but cross-checking the unique
	// (org, user) pair shouldn't require a transaction round-trip.
	GetMembershipByOrgUser(ctx context.Context, organizationID, userID string) (model.Membership, error)

	// SaveMembership inserts a new membership. Returns ErrMembershipExists
	// when (organization_id, user_id) collides. The caller generates the ID.
	SaveMembership(ctx context.Context, m model.Membership) error

	// UpdateMembershipRole changes the role of an existing membership.
	// Returns ErrMembershipNotFound when the row does not exist; returns
	// ErrLastOwner when the change would leave the organization with zero owners.
	UpdateMembershipRole(ctx context.Context, id, newRole string) error

	// DeleteMembership removes a membership. Returns ErrMembershipNotFound
	// when the row does not exist; returns ErrLastOwner when the deletion
	// would leave the organization with zero owners.
	DeleteMembership(ctx context.Context, id string) error

	// TransferOwnership atomically demotes the current owner to admin and
	// promotes the target user to owner, both within the organization in ctx.
	// Returns ErrMembershipNotFound when the target user has no membership
	// in this organization.
	TransferOwnership(ctx context.Context, toUserID string) error

	// EnsureFirstMembership inserts an owner row for (organizationID, userID) only
	// if no membership row exists yet for this organization.
	// Idempotent and race-safe: the partial unique index in migration 015
	// rejects a second concurrent insert. Returns true when this call
	// inserted the row, false if a membership already existed.
	EnsureFirstMembership(ctx context.Context, organizationID, userID string) (bool, error)

	// EnsureDevMembership creates an owner row for (organizationID, userID) on
	// startup in DEV_MODE. Mirrors EnsureUser's raw-pool shortcut (RLS
	// would prevent reading app.organization_id at startup before any handler runs).
	// Idempotent. role is one of the four defined roles.
	EnsureDevMembership(ctx context.Context, organizationID, userID, role string) error

	// GetUserByEmail looks up a user by email for the organization in ctx. Used by
	// the invite-by-email flow. Returns ErrUserNotFound when no match.
	GetUserByEmail(ctx context.Context, email string) (model.User, error)

	// GetUserByID looks up a user by their internal UUID. Org-agnostic — the
	// users table is global (a user with cross-org memberships has one row;
	// users.organization_id tracks the *primary* org and is not used to scope
	// this lookup). Returns ErrUserNotFound when no match. Used by /v1/me to
	// surface the caller's display name regardless of which org they're
	// currently bound to.
	GetUserByID(ctx context.Context, id string) (model.User, error)

	// GetUserSSOConnectionID returns the users.sso_connection_id value for
	// the given user (empty string when the column is NULL — a native-only
	// user, or an SSO user whose connection has been deleted with
	// ON DELETE SET NULL). Returns ErrUserNotFound when no row matches.
	// Single-purpose lookup so the SSO RP-Initiated Logout resolver doesn't
	// need to drag the whole User struct (and the 25 SELECT sites that read
	// it) through a column-add migration.
	GetUserSSOConnectionID(ctx context.Context, userID string) (string, error)

	// SetUserSSOConnection writes users.sso_connection_id. Called from the
	// OIDC callback after UpsertUser so the SSO RP-Initiated Logout resolver
	// can later answer "which IdP issued this session?". Passing an empty
	// connectionID clears the column (set NULL) — used by tests; the OIDC
	// callback always passes a non-empty value. Returns ErrUserNotFound when
	// no row matches. Narrow setter rather than folding into UpsertUser so
	// the native-bootstrap caller (which has no connection) doesn't have to
	// thread an empty string through every call.
	SetUserSSOConnection(ctx context.Context, userID, connectionID string) error

	// ── Pending invitations (see docs/invitation-flow.md) ────────────────────

	// CreatePendingInvitation inserts a pending_memberships row, or upserts an
	// existing pending row for (organization_id, lower(email)) — refreshing
	// expires_at and updating role on re-invite. The bool return is true when a
	// new row was inserted, false when an existing pending row was updated
	// (allows the handler to return 201 vs 200 respectively).
	//
	// Pre-checks the (organization, email) combination against memberships +
	// users and returns ErrInvitationAlreadyMember (email is already a member)
	// or ErrUserExistsNoMembership (email is a known user without membership)
	// before hitting the partial unique index. ctx must carry organization_id.
	CreatePendingInvitation(ctx context.Context, inv model.PendingInvitation) (model.PendingInvitation, bool, error)

	// ListPendingInvitations returns invitations for the organization in ctx
	// filtered by status. status="" returns only status='pending' rows.
	ListPendingInvitations(ctx context.Context, status string) ([]model.PendingInvitation, error)

	// GetPendingInvitation returns a single invitation by ID for the
	// organization in ctx. Returns ErrInvitationNotFound when missing.
	GetPendingInvitation(ctx context.Context, id string) (model.PendingInvitation, error)

	// RevokePendingInvitation flips status to 'revoked'. Returns
	// ErrInvitationNotFound if no row, ErrInvitationNotPending if already
	// revoked/expired. Idempotent on subsequent revoke calls only via the
	// not-pending sentinel — handler maps that to 410.
	RevokePendingInvitation(ctx context.Context, id string) error

	// RedeemPendingInvitation atomically inserts a memberships row and DELETES
	// the matching pending_memberships row, in one transaction. The match key
	// is (organization_id, lower(email)) WHERE status='pending' AND
	// expires_at > NOW(). Returns true on redemption, (false, nil) if no
	// matching pending row exists (silent no-op — correct for users not in
	// the pending set, e.g. self-signup owners).
	//
	// Called by the auth middleware on every authenticated request after
	// EnsureFirstMembership. Must be cheap — sub-millisecond on the hot path.
	// ctx organization_id is set by the caller; userID and email are sourced
	// from JWT claims.
	RedeemPendingInvitation(ctx context.Context, organizationID, userID, email string) (bool, error)

	// ExpirePendingInvitations flips status to 'expired' for ripe pending rows
	// (expires_at <= NOW()). Cross-organization sweep — bypasses RLS via the
	// admin pool. Returns the number of rows updated. Idempotent.
	ExpirePendingInvitations(ctx context.Context) (int64, error)

	// ── GDPR — right to erasure (see docs/rbac-design.md §10) ────────────────

	// DeleteUser hard-deletes a user across the entire system as part of the
	// per-user right-to-erasure flow. Steps:
	//   1. Anonymise the user's audit_log footprint across ALL organizations
	//      (sets user_id = NULL, actor_email = 'deleted-user').
	//   2. Cascade-deletes all memberships (memberships.user_id has
	//      ON DELETE CASCADE).
	//   3. Deletes the users row.
	// Returns ErrLastOwner if the user is the sole owner of any organization —
	// the caller must transfer ownership or delete those organizations first.
	// Bypasses RLS (uses the admin pool) because the operation spans organizations
	// and audit_log requires DELETE/UPDATE privileges the app role lacks.
	// Does not touch users whose users.organization_id column points at an organization being
	// deleted in the same flow — DeleteOrganizationCascade handles that case.
	DeleteUser(ctx context.Context, userID string) error

	// DeleteOrganizationCascade hard-deletes an organization and every row scoped to it,
	// in FK-safe order, in a single transaction. Used by the per-organization
	// right-to-erasure flow (DELETE /v1/organizations/me). Steps:
	//   1. Anonymise audit_log entries (in OTHER organizations) for users whose
	//      users.organization_id = organizationID — those users are about to be deleted
	//      and their attribution must be erased everywhere, not just here.
	//   2. Delete per-organization data: dismissed_zombies, zombie_snapshot_services,
	//      zombie_snapshots, zombie_records, resource_records, cost_records,
	//      accounts, audit_log.
	//   3. Delete users with users.organization_id = organizationID; CASCADE removes
	//      their memberships in this and other organizations.
	//   4. Delete the organizations row; CASCADE removes any remaining memberships
	//      held by users from other organizations.
	// Bypasses RLS (uses the admin pool). The call should be preceded by an
	// audit_log write recording the deletion intent — that record is purged
	// along with everything else, but a Prometheus counter and structured
	// log line should remain as the operations trail.
	DeleteOrganizationCascade(ctx context.Context, organizationID string) error

	// ── Phase B2 — SSO ──────────────────────────────────────────────────────
	// All SSO methods require WithOrganizationID(ctx, ...) before the call,
	// EXCEPT GetVerifiedSSODomainByName which is called pre-auth (no org
	// context exists) and uses the admin pool to bypass RLS.

	// CreateSSOConnection inserts a draft connection. ID auto-generated if empty.
	CreateSSOConnection(ctx context.Context, c model.SSOConnection) (model.SSOConnection, error)
	// GetSSOConnection returns a single connection scoped to the request org.
	// Returns ErrSSOConnectionNotFound on miss.
	GetSSOConnection(ctx context.Context, id string) (model.SSOConnection, error)
	// GetSSOConnectionByID is the pre-auth lookup used by the OIDC ceremony
	// (initiate + callback). The caller has only the connection ID from the
	// URL path — no organization context exists yet — so RLS must be bypassed.
	// Returns ErrSSOConnectionNotFound on miss. The connection's
	// OrganizationID is the caller's path back into RLS-scoped operations
	// once the ceremony has authenticated the user.
	GetSSOConnectionByID(ctx context.Context, id string) (model.SSOConnection, error)
	// ListSSOConnections returns all connections in the request org, newest first.
	ListSSOConnections(ctx context.Context) ([]model.SSOConnection, error)
	// UpdateSSOConnection persists mutable fields (label, status, enforcement,
	// default_role, IdP/protocol fields). Caller is responsible for handler-level
	// validation; the DB CHECK on (status='active' AND protocol='oidc') →
	// non-empty client_secret_ciphertext is the last line of defence.
	UpdateSSOConnection(ctx context.Context, c model.SSOConnection) error
	// DeleteSSOConnection removes a connection and (via FK CASCADE) its domains
	// and group mappings. Returns ErrSSOConnectionNotFound on miss.
	DeleteSSOConnection(ctx context.Context, id string) error

	// CreateSSODomain inserts a pending domain row. ID + verification_token
	// auto-generated if empty. Caller must populate sso_connection_id; the FK
	// guarantees the connection belongs to the same org as the domain row.
	CreateSSODomain(ctx context.Context, d model.SSODomain) (model.SSODomain, error)
	// GetSSODomain returns a single domain scoped to the request org.
	// Returns ErrSSODomainNotFound on miss.
	GetSSODomain(ctx context.Context, id string) (model.SSODomain, error)
	// GetVerifiedSSODomainByName looks up a verified domain row by lower(domain).
	// Pre-auth path — uses the admin pool to bypass RLS. Returns
	// ErrSSODomainNotFound when no verified row exists for the domain.
	GetVerifiedSSODomainByName(ctx context.Context, domain string) (model.SSODomain, error)
	// ListSSODomains returns all domains in the request org, newest first.
	ListSSODomains(ctx context.Context) ([]model.SSODomain, error)
	// UpdateSSODomainStatus advances the lifecycle (pending→verified, verified→stale,
	// any→revoked). When transitioning to verified, the caller supplies verifiedAt
	// and expiresAt; for other transitions, those args are zero-valued and ignored.
	UpdateSSODomainStatus(ctx context.Context, id, status string, verifiedAt, expiresAt time.Time) error
	// DeleteSSODomain removes a single domain row.
	DeleteSSODomain(ctx context.Context, id string) error
	// SweepStaleSSODomains marks expired verified rows as stale. Returns count
	// affected. Cross-org sweep — uses the admin pool.
	SweepStaleSSODomains(ctx context.Context, now time.Time) (int64, error)

	// ListSSOGroupMappings returns mappings for a connection in the request org.
	ListSSOGroupMappings(ctx context.Context, connectionID string) ([]model.SSOGroupMapping, error)
	// ReplaceSSOGroupMappings is the only mutation path: PUT semantics. Wipes
	// the existing set for the connection and inserts the supplied list in one
	// transaction. Atomically replaces — partial-failure leaves the prior set
	// intact. Caller must populate OrganizationID + SSOConnectionID on each
	// row; ID auto-generated if empty.
	ReplaceSSOGroupMappings(ctx context.Context, connectionID string, mappings []model.SSOGroupMapping) error

	// ── Notification channels (docs/notifications-plan.md) ───────────────────
	// All methods require WithOrganizationID(ctx, ...) before the call and run
	// on the RLS-bound app pool. There is deliberately no cross-org enumerator
	// (no ListAll*) — the dispatcher fires per-org inside a scan that already
	// carries organization_id on its context, so RLS isolation must hold.

	// SaveNotificationChannel inserts or replaces a channel for the request org
	// (upsert on id, mirroring SaveAccount). The caller supplies a fully-formed
	// struct; PATCH is read-modify-Save in the handler. ID auto-generated by the
	// DB default when empty on first insert.
	SaveNotificationChannel(ctx context.Context, ch model.NotificationChannel) error
	// ListNotificationChannels returns all channels in the request org, newest first.
	ListNotificationChannels(ctx context.Context) ([]model.NotificationChannel, error)
	// ListEnabledNotificationChannels returns only enabled channels for the
	// request org — the dispatcher's post-scan hot path. RLS-bound (NOT the admin
	// pool): ctx already carries organization_id by the time a scan reaches it.
	ListEnabledNotificationChannels(ctx context.Context) ([]model.NotificationChannel, error)
	// GetNotificationChannel returns a single channel by ID in the request org.
	// Returns ErrChannelNotFound on miss.
	GetNotificationChannel(ctx context.Context, id string) (model.NotificationChannel, error)
	// DeleteNotificationChannel removes a channel by ID in the request org; its
	// dispatch rows go with it via FK CASCADE. Returns ErrChannelNotFound on miss.
	DeleteNotificationChannel(ctx context.Context, id string) error

	// SaveNotificationDispatch inserts one dispatch (send-attempt) row for the
	// request org. ID auto-generated by the DB default when empty.
	SaveNotificationDispatch(ctx context.Context, d model.NotificationDispatch) error
	// ListNotificationDispatches returns the newest dispatch rows for a channel
	// in the request org, capped at limit (<=0 applies a sane default).
	ListNotificationDispatches(ctx context.Context, channelID string, limit int) ([]model.NotificationDispatch, error)

	// DeleteOldNotificationDispatches removes dispatch rows created before the
	// given cutoff for all organizations (RLS-bypass). Returns rows deleted.
	DeleteOldNotificationDispatches(ctx context.Context, cutoff time.Time) (int64, error)

	// Close releases any resources held by the store.
	Close() error
}
