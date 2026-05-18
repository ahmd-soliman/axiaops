// storage_native_auth.go — Phase B1 (native auth) Store interface additions.
// Kept in a separate file so the diff against the Kinde-era interface stays
// reviewable. The methods compose into the same Store interface via Go's
// support for interface declarations spanning multiple files.

package storage

import (
	"context"
	"errors"
	"time"

	"axiaops.io/shared/model"
)

// ── B1 native-auth sentinel errors ──────────────────────────────────────────

// ErrSessionNotFound is returned by GetSessionByTokenHash when no row matches
// or the row has been hard-deleted by SweepExpiredSessions. Callers MUST treat
// "not found" and "revoked" identically — both manifest as auth failure.
var ErrSessionNotFound = errors.New("storage: session not found")

// ErrPasswordResetNotFound is returned by RedeemPasswordReset when no row
// matches the supplied token hash, or the row has already been redeemed.
var ErrPasswordResetNotFound = errors.New("storage: password reset token not found or already redeemed")

// ErrPasswordResetExpired is returned by RedeemPasswordReset when the row
// matches but expires_at <= NOW(). Distinct from NotFound so the handler can
// emit a more helpful 410 Gone vs a generic 404.
var ErrPasswordResetExpired = errors.New("storage: password reset token expired")

// ErrUserEmailExists is returned by CreateUserWithPassword when email_lower
// collides with an existing row. Surface as HTTP 409.
var ErrUserEmailExists = errors.New("storage: email already registered")

// ErrInvitationUserMismatch is returned by RedeemNativeInvitation when the
// caller-supplied ExistingUserID resolves to a user whose email doesn't
// match the invitation's email. Should be unreachable in normal flow
// (handler always reads ExistingUserID from a peek that matched on email)
// — surfaces only as a defence-in-depth signal that someone called the
// storage method with mismatched inputs. Map to 500 in the handler.
var ErrInvitationUserMismatch = errors.New("storage: invitation existing-user email mismatch")

// ErrBootstrapAlreadyDone is returned by ConsumeBootstrapState when the
// singleton row has already been consumed (the bootstrap endpoint is sealed).
// Also returned by CreateBootstrapState when an organization already exists or
// a token has already been minted by another replica.
var ErrBootstrapAlreadyDone = errors.New("storage: bootstrap already complete")

// ErrBootstrapTokenMismatch is returned by ConsumeBootstrapState when the
// supplied token hash does not match the stored singleton. Distinct from
// ErrBootstrapAlreadyDone so the metrics can split sealed vs invalid_token.
var ErrBootstrapTokenMismatch = errors.New("storage: bootstrap token does not match")

// ── Native-auth user mutations ──────────────────────────────────────────────

// NativeAuthStore is the slice of Store dealing with native auth (Phase B1).
// Implementations live in services/shared/storage/postgres alongside the
// rest of the Store. The interface is split out only for readability; the
// concrete *postgres.Store satisfies both.
//
// All methods must be implemented inside a single transaction with
// `defer tx.Rollback(ctx)` immediately after Begin (CLAUDE.md convention).
type NativeAuthStore interface {
	// CreateUserWithPassword inserts a new user row with a non-empty password
	// hash and password_set_at = NOW(). The caller pre-hashes the password
	// (this layer never touches plaintext). Returns ErrUserEmailExists when
	// the email collides with an existing row (case-insensitive). Used by:
	//   - bootstrap (first owner)
	//   - native invitation redemption
	//
	// ctx organization_id MUST already be set; the user row is also tied to
	// organization_id via users.organization_id.
	CreateUserWithPassword(ctx context.Context, u model.User) (model.User, error)

	// UpdateUserPassword sets users.password_hash and users.password_set_at on
	// the row identified by userID. Used by the password-reset redeem path.
	// Does not invalidate sessions — that is RevokeUserSessions' job and the
	// caller composes both inside its own transaction.
	UpdateUserPassword(ctx context.Context, userID, passwordHash string) error

	// CountOrganizations returns the total number of rows in the organizations
	// table across all organizations. Used by the bootstrap installer to
	// decide whether to mint an install token. Bypasses RLS (uses the admin
	// pool) because at startup no org context exists.
	CountOrganizations(ctx context.Context) (int64, error)

	// LookupMembership resolves the membership role and the user's email in
	// a single SELECT joining memberships + users. Used on the native-auth
	// request hot path: the auth provider needs both fields per request to
	// populate Identity{Role, Email}.
	//
	// Returns (role="", email="", nil) when no membership row exists — the
	// caller treats that as "no membership" and rejects the request.
	// Returns a non-nil error only on transient DB failure.
	LookupMembership(ctx context.Context, organizationID, userID string) (role, email string, err error)

	// LookupUserByEmail resolves the login candidate for the supplied
	// email — global lookup, bypassing RLS, because login has no org
	// context yet. Returns the user record (with password_hash for
	// argon2 verification) plus a list of the user's live memberships
	// across all organizations.
	//
	// Returns (zero, nil, ErrUserNotFound) when no user matches; an
	// empty memberships slice when the user exists but has no
	// organization (rare — a deleted org or pending invite that
	// hasn't been redeemed yet). Login uses the slice length to
	// distinguish single-org login (mint session) from multi-org
	// (B1.5: branch to the org picker via ListUserMemberships).
	LookupUserByEmail(ctx context.Context, email string) (model.User, []model.Membership, error)

	// ListUserMemberships is the org-picker join: same row set as the
	// memberships list returned by LookupUserByEmail, but joined with
	// organizations to carry the display name. Used by /v1/auth/login
	// when len(memberships) > 1 (B1.5) and by /v1/auth/select-org +
	// /v1/auth/switch-org to validate the chosen org. Bypasses RLS.
	// Detailed contract — including the safety note that callers MUST
	// pass a userID from validated auth context — lives on the same
	// method in the wider Store interface (storage.go).
	ListUserMemberships(ctx context.Context, userID string) ([]model.MembershipWithOrganization, error)

	// ── Sessions ────────────────────────────────────────────────────────────

	// CreateSession inserts a session row. The caller has already minted the
	// random plaintext token, hashed it (SHA-256), and computed expires_at.
	// Returns the persisted Session (with timestamps populated).
	CreateSession(ctx context.Context, s model.Session) (model.Session, error)

	// CreateSessionEnforcingCap inserts a session row AND atomically enforces
	// the per-user concurrent-session cap in the same transaction. When
	// `perUserCap > 0` and inserting the new row would push the user above
	// the cap, the oldest excess sessions are revoked (`revoked_at = NOW()`)
	// in the same transaction — so no transient over-cap state is visible
	// to any concurrent reader. When `perUserCap <= 0` the cap step is a
	// no-op and the call is equivalent to CreateSession.
	//
	// Returns the persisted Session and the list of session_token_hashes
	// that were revoked so the caller can evict the matching cache entries
	// after the transaction commits (architect C4: no scan/wildcard).
	//
	// Audit (M-7): folds the cap-enforcement into the same transaction as
	// the INSERT, closing the "11th login briefly visible" window from the
	// previous best-effort post-insert revoke. Failures propagate to the
	// caller — partial state is impossible because the whole tx rolls back.
	CreateSessionEnforcingCap(ctx context.Context, s model.Session, perUserCap int) (saved model.Session, revokedHashes []string, err error)

	// GetSessionByTokenHash looks up by SHA-256 hash. Returns ErrSessionNotFound
	// when no row matches. Does NOT filter by revoked/expired — callers must
	// re-check Live() after read (architect C4: cached value must gate liveness).
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (model.Session, error)

	// TouchSessionLastSeen updates last_seen_at = NOW() on the row identified
	// by sessionID. Called from the auth middleware ONLY on cache miss
	// (architect N3: write amplification check). Best-effort — caller logs
	// errors but does not fail the request.
	TouchSessionLastSeen(ctx context.Context, sessionID string) error

	// RevokeSession sets revoked_at = NOW() on the session identified by
	// sessionID. Idempotent on already-revoked rows (no error). Cache
	// invalidation is the caller's responsibility (auth/session.go does the
	// PG write first, then cache.Delete).
	RevokeSession(ctx context.Context, sessionID string) error

	// RevokeUserSessions revokes all live sessions for userID. Returns the
	// list of session token hashes that were revoked so the caller can
	// enumerate cache keys to delete (architect C4: no scan/wildcard delete).
	// Used by the password-reset redeem path and admin "log out everywhere".
	RevokeUserSessions(ctx context.Context, userID string) ([]string, error)

	// ListUserSessionTokenHashes returns the token hashes of all live
	// sessions for userID, ordered oldest-first by created_at. The ordering
	// is contractual — the per-user cap (architect C2) revokes the oldest
	// excess sessions, not an arbitrary subset. Used by RevokeUserSessions
	// internally and exposed for tests / cache-coherency checks.
	ListUserSessionTokenHashes(ctx context.Context, userID string) ([]string, error)

	// CountSessionsForUser returns the number of currently-live sessions for
	// userID (revoked_at IS NULL AND expires_at > NOW()). Used to enforce
	// SESSIONS_PER_USER_CAP (architect C2).
	CountSessionsForUser(ctx context.Context, userID string) (int, error)

	// SweepExpiredSessions deletes session rows where
	//   expires_at < olderThan
	//   OR (revoked_at IS NOT NULL AND revoked_at < olderThan)
	// Returns the number of rows deleted. Cross-organization sweep, bypasses
	// RLS via the admin pool. Run from the background ticker in cmd/main.go.
	SweepExpiredSessions(ctx context.Context, olderThan time.Time) (int64, error)

	// ── Password resets ─────────────────────────────────────────────────────

	// CreatePasswordReset inserts a password_resets row. The caller has
	// minted the plaintext token, hashed it, and computed expires_at. The
	// returned struct carries the persisted timestamps but never the
	// plaintext (which the caller has retained for the OOB redemption URL).
	CreatePasswordReset(ctx context.Context, id, userID, organizationID, tokenHash, issuedByUserID string, expiresAt time.Time) error

	// RedeemPasswordReset atomically:
	//   1. Looks up the password_resets row by tokenHash.
	//   2. Validates expires_at > NOW() and redeemed_at IS NULL.
	//   3. Updates users.password_hash + users.password_set_at = NOW().
	//   4. Marks the password_resets row redeemed_at = NOW().
	// All in one transaction. Returns ErrPasswordResetNotFound or
	// ErrPasswordResetExpired on the obvious failure modes. The caller is
	// expected to RevokeUserSessions in the same logical operation (separate
	// transaction is fine — at most one stale session for a few ms).
	//
	// Returns both userID and organizationID so the caller can write
	// the audit row under the correct org context (audit_log requires
	// a non-empty organization_id). The redemption flow has no auth
	// context — the row itself is the only source of org identity.
	RedeemPasswordReset(ctx context.Context, tokenHash, newPasswordHash string) (userID, organizationID string, err error)

	// ── Bootstrap singleton ─────────────────────────────────────────────────

	// CreateBootstrapState attempts to insert the singleton bootstrap row
	// holding the install-token hash. Multi-replica safe (architect C5):
	// uses pg_advisory_xact_lock + ON CONFLICT DO NOTHING so exactly one
	// replica wins the race. Returns true when this caller wrote the row,
	// false when another replica won. Returns ErrBootstrapAlreadyDone when
	// the organizations table is non-empty (the install is already past
	// bootstrap).
	CreateBootstrapState(ctx context.Context, tokenHash, mintedByPod string) (won bool, err error)

	// GetBootstrapState returns the current singleton row, if any. Returns
	// (zero, ErrBootstrapAlreadyDone) when no row exists (i.e. the install
	// has been bootstrapped already, or no token has ever been minted —
	// caller distinguishes via CountOrganizations). Used by tests and the
	// startup banner-printing branch in main.go.
	GetBootstrapState(ctx context.Context) (tokenHash string, mintedByPod string, err error)

	// ConsumeBootstrapState atomically:
	//   1. Looks up the singleton row.
	//   2. Constant-time compares its token_hash with the supplied tokenHash.
	//   3. On match: creates the org, the owner user, the membership, and
	//      mints the bootstrap session — all inside ONE transaction with the
	//      DELETE of the bootstrap_state row. Returns the persisted user.
	//   4. On mismatch: returns ErrBootstrapTokenMismatch (no row deleted).
	//   5. When the singleton row is missing entirely: returns
	//      ErrBootstrapAlreadyDone.
	//
	// The caller has already validated the password policy and pre-hashed
	// the password. The session token plaintext is the caller's; this layer
	// receives only the hash and the metadata.
	ConsumeBootstrapState(ctx context.Context, in BootstrapConsume) (BootstrapResult, error)

	// ── Native invitations ──────────────────────────────────────────────────

	// CreateNativeInvitation inserts a token-bearing pending_memberships row.
	// The caller has minted the plaintext token, hashed it, and pre-checked
	// the email against memberships/users via the existing
	// CreatePendingInvitation pre-checks. Returns ErrInvitationAlreadyMember /
	// ErrUserExistsNoMembership for those cases.
	//
	// PRECONDITION: ctx MUST carry a valid organization ID via
	// storage.WithOrganizationID. Unlike the rest of the native-auth methods
	// (which bypass RLS via the admin pool because they predate any org
	// context), this method runs against pending_memberships under RLS — the
	// table is org-scoped and consistent with how the Kinde-era
	// CreatePendingInvitation operates.
	//
	// Returns the persisted invitation and a bool that is true on a fresh
	// insert, false when an existing pending row was upserted (token rotated).
	CreateNativeInvitation(ctx context.Context, inv model.PendingInvitation) (model.PendingInvitation, bool, error)

	// RedeemNativeInvitation atomically:
	//   1. Looks up the pending_memberships row by invite_token_hash.
	//   2. Validates status='pending' AND expires_at > NOW().
	//   3. Either creates a new user (with the supplied password hash + name)
	//      OR — if the email already matches a user row — adds a membership
	//      onto the existing user without touching the password.
	//   4. Inserts the memberships row.
	//   5. DELETEs the pending_memberships row.
	// All in one transaction. Returns the resolved user + the new membership.
	// Returns ErrInvitationNotFound when no row matches or the row is no
	// longer pending. Used under AUTH_PROVIDER=native.
	RedeemNativeInvitation(ctx context.Context, in NativeInviteRedeem) (model.User, model.Membership, error)

	// LookupInvitationByToken is the read-only peek that drives both the
	// preview endpoint and the redeem handler's flow-selection. It does
	// NOT consume the invitation token — the row stays pending. Returns
	// ErrInvitationNotFound if the row is missing, expired, or already
	// redeemed.
	//
	// `ExistingUser` is populated when a user with the invited email
	// already exists in any organisation (B1.5 cross-org redemption).
	// Bypasses RLS — the lookup must see users across orgs.
	//
	// Race window: between this peek and the subsequent
	// RedeemNativeInvitation, the token can be consumed by another
	// caller. RedeemNativeInvitation handles that with
	// ErrInvitationNotFound — the handler must propagate it as the
	// usual 410 Gone.
	LookupInvitationByToken(ctx context.Context, tokenHash string) (PeekedInvitation, error)
}

// BootstrapConsume is the input record for ConsumeBootstrapState. Kept as a
// struct (not positional args) so the inevitable additions (initial org name,
// invite quota, license tier hint) don't churn the interface.
type BootstrapConsume struct {
	TokenHash         string // hex(SHA-256(plaintext token))
	OrganizationID    string // pre-generated UUID
	OrganizationName  string
	UserID            string // pre-generated UUID
	UserEmail         string
	UserName          string
	UserPasswordHash  string // pre-hashed (argon2id) by caller
	SessionID         string
	SessionTokenHash  string
	SessionExpiresAt  time.Time
	SessionUserAgentHash string
	// SessionIP is captured as a string here to avoid pulling net into this
	// package's interface; the postgres impl converts to net.IP.
	SessionIP string
}

// BootstrapResult is what ConsumeBootstrapState returns to the caller — the
// persisted user (for /v1/me + audit row) and the persisted session (so the
// handler can drop the cookie).
type BootstrapResult struct {
	User    model.User
	Session model.Session
}

// NativeInviteRedeem is the input record for RedeemNativeInvitation.
//
// Two flows the caller chooses between BEFORE calling, by first invoking
// LookupInvitationByToken to discover whether a global user with the
// invited email already exists:
//
//  1. New user: leave ExistingUserID empty, supply UserID + UserName +
//     PasswordHash. Storage INSERTs the user and the membership.
//  2. Existing user (B1.5 multi-org): set ExistingUserID to the matched
//     user's id, omit UserID/UserName/PasswordHash (caller has already
//     verified the supplied password against the user's stored hash via
//     auth.Verify — the storage layer never touches plaintext). Storage
//     skips user INSERT and only adds the membership.
type NativeInviteRedeem struct {
	TokenHash      string
	ExistingUserID string // non-empty → flow (2); leaves the user row untouched
	UserID         string // flow (1) only
	UserName       string // flow (1) only
	PasswordHash   string // flow (1) only — pre-hashed (argon2id)
}

// PeekedInvitation is the read-only projection LookupInvitationByToken
// returns. Contains everything the redeem handler needs to decide
// between the new-user and existing-user flow, plus the public-facing
// fields the preview endpoint exposes (sans ExistingUser, which the
// handler keeps internal — the wire shape only carries existing_user
// as a boolean).
type PeekedInvitation struct {
	Email            string
	OrganizationID   string
	OrganizationName string
	Role             string
	InvitedBy        string

	// ExistingUser is non-nil when a user with this email already exists
	// globally (any organisation). The handler verifies the supplied
	// password against ExistingUser.PasswordHash before redeeming.
	// The PasswordHash MUST NOT be returned to the wire — the preview
	// endpoint redacts it.
	ExistingUser *model.User
}
