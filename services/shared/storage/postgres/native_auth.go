// native_auth.go — Phase B1 native auth Store implementations.
// Methods declared on *Store; satisfies storage.NativeAuthStore (embedded
// into storage.Store). See docs/sso-implementation-plan.md §4.1 / §4.3.

package postgres

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// bootstrapAdvisoryLockKey is the postgres advisory-lock key used to
// serialise replica-side install-token generation. The constant is
// arbitrary but must be stable across releases; do not change without a
// coordinated dual-write window.
const bootstrapAdvisoryLockKey = int64(0x4178696F4F70730A) // "AxiaOps\n" as int64

// ── Native-auth user mutations ──────────────────────────────────────────────

// CreateUserWithPassword inserts a user row with a password hash and a
// synthetic kinde_sub of the form "native:<id>". The synthetic prefix keeps
// the kinde_sub UNIQUE constraint usable while the column waits to be
// renamed (a future migration when the Kinde path is deleted — D2).
//
// Bypasses RLS via the admin pool because the bootstrap and invitation-redeem
// callers may be running before any organization context exists, and the
// users table itself has no RLS policy (it is org-scoped via the
// users.organization_id FK only).
//
// Returns storage.ErrUserEmailExists when email_lower collides.
func (s *Store) CreateUserWithPassword(ctx context.Context, u model.User) (model.User, error) {
	if u.OrganizationID == "" {
		return model.User{}, fmt.Errorf("postgres: create user with password: organization_id required")
	}
	if u.Email == "" {
		return model.User{}, fmt.Errorf("postgres: create user with password: email required")
	}
	if u.PasswordHash == "" {
		return model.User{}, fmt.Errorf("postgres: create user with password: password_hash required")
	}
	if u.ID == "" {
		u.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	kindeSub := "native:" + u.ID

	var out model.User
	err := s.adminPool.QueryRow(ctx, `
		INSERT INTO users (
			id, organization_id, kinde_sub, email, name,
			password_hash, password_set_at, created_at, last_seen
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)
		RETURNING id, organization_id, kinde_sub, email, name,
		          password_hash, password_set_at, created_at, last_seen`,
		u.ID, u.OrganizationID, kindeSub, u.Email, u.Name, u.PasswordHash, now,
	).Scan(
		&out.ID, &out.OrganizationID, &out.KindeSub, &out.Email, &out.Name,
		&out.PasswordHash, &out.PasswordSetAt, &out.CreatedAt, &out.LastSeen,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// 23505 = unique_violation. Two unique constraints can fire here:
			// users_email_lower_unique (the new one from migration 021) or the
			// existing UNIQUE on kinde_sub. The kinde_sub case is impossibly
			// rare (UUID collision), so treat any 23505 from this insert as
			// "email taken" — the much more likely cause.
			return model.User{}, storage.ErrUserEmailExists
		}
		return model.User{}, fmt.Errorf("postgres: create user with password: %w", err)
	}
	return out, nil
}

// UpdateUserPassword sets users.password_hash + users.password_set_at = NOW().
// Bypasses RLS — users has no RLS policy and the userID is the capability.
func (s *Store) UpdateUserPassword(ctx context.Context, userID, passwordHash string) error {
	if userID == "" || passwordHash == "" {
		return fmt.Errorf("postgres: update user password: userID and passwordHash required")
	}
	tag, err := s.adminPool.Exec(ctx, `
		UPDATE users
		SET password_hash = $1, password_set_at = NOW()
		WHERE id = $2`,
		passwordHash, userID,
	)
	if err != nil {
		return fmt.Errorf("postgres: update user password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrUserNotFound
	}
	return nil
}

// CountOrganizations returns the total org count across the cluster.
// Uses the admin pool — this is called at startup before any org context
// exists, and organizations has no RLS policy anyway.
func (s *Store) CountOrganizations(ctx context.Context) (int64, error) {
	var n int64
	err := s.adminPool.QueryRow(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: count organizations: %w", err)
	}
	return n, nil
}

// ── Sessions ────────────────────────────────────────────────────────────────

// CreateSession inserts a session row. Bypasses RLS — sessions has no RLS by
// design (lookup precedes the establishment of org context; see migration
// 021).
func (s *Store) CreateSession(ctx context.Context, in model.Session) (model.Session, error) {
	if in.ID == "" {
		return model.Session{}, fmt.Errorf("postgres: create session: id required")
	}
	if in.UserID == "" || in.OrganizationID == "" {
		return model.Session{}, fmt.Errorf("postgres: create session: user_id and organization_id required")
	}
	if in.SessionTokenHash == "" {
		return model.Session{}, fmt.Errorf("postgres: create session: session_token_hash required")
	}
	if in.ExpiresAt.IsZero() {
		return model.Session{}, fmt.Errorf("postgres: create session: expires_at required")
	}
	if in.AuthMode == "" {
		return model.Session{}, fmt.Errorf("postgres: create session: auth_mode required")
	}

	var ipArg any
	if in.IP != nil {
		ipArg = in.IP.String()
	}

	var out model.Session
	var ipStr *string
	err := s.adminPool.QueryRow(ctx, `
		INSERT INTO sessions (
			id, user_id, organization_id, auth_mode, session_token_hash,
			created_at, expires_at, last_seen_at, ip, user_agent_hash
		) VALUES (
			$1, $2, $3, $4, $5, NOW(), $6, NOW(), $7, $8
		)
		RETURNING id, user_id, organization_id, auth_mode, session_token_hash,
		          created_at, expires_at, revoked_at, last_seen_at,
		          host(ip), user_agent_hash`,
		in.ID, in.UserID, in.OrganizationID, string(in.AuthMode), in.SessionTokenHash,
		in.ExpiresAt, ipArg, in.UserAgentHash,
	).Scan(
		&out.ID, &out.UserID, &out.OrganizationID, (*string)(&out.AuthMode), &out.SessionTokenHash,
		&out.CreatedAt, &out.ExpiresAt, &out.RevokedAt, &out.LastSeenAt,
		&ipStr, &out.UserAgentHash,
	)
	if err != nil {
		return model.Session{}, fmt.Errorf("postgres: create session: %w", err)
	}
	if ipStr != nil {
		out.IP = net.ParseIP(*ipStr)
	}
	return out, nil
}

// GetSessionByTokenHash returns the session row whose session_token_hash
// matches. Does NOT filter by revoked/expired — the caller must call
// Session.Live() after read (architect C4).
func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash string) (model.Session, error) {
	if tokenHash == "" {
		return model.Session{}, storage.ErrSessionNotFound
	}
	var out model.Session
	var ipStr *string
	err := s.adminPool.QueryRow(ctx, `
		SELECT id, user_id, organization_id, auth_mode, session_token_hash,
		       created_at, expires_at, revoked_at, last_seen_at,
		       host(ip), user_agent_hash
		FROM sessions
		WHERE session_token_hash = $1`,
		tokenHash,
	).Scan(
		&out.ID, &out.UserID, &out.OrganizationID, (*string)(&out.AuthMode), &out.SessionTokenHash,
		&out.CreatedAt, &out.ExpiresAt, &out.RevokedAt, &out.LastSeenAt,
		&ipStr, &out.UserAgentHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Session{}, storage.ErrSessionNotFound
	}
	if err != nil {
		return model.Session{}, fmt.Errorf("postgres: get session: %w", err)
	}
	if ipStr != nil {
		out.IP = net.ParseIP(*ipStr)
	}
	return out, nil
}

// TouchSessionLastSeen updates last_seen_at = NOW() for the row identified by
// sessionID. Best-effort: errors are returned but the caller should continue.
func (s *Store) TouchSessionLastSeen(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("postgres: touch session: sessionID required")
	}
	_, err := s.adminPool.Exec(ctx, `
		UPDATE sessions SET last_seen_at = NOW() WHERE id = $1`,
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("postgres: touch session: %w", err)
	}
	return nil
}

// RevokeSession sets revoked_at = NOW() on the session identified by
// sessionID. Idempotent on already-revoked rows.
func (s *Store) RevokeSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("postgres: revoke session: sessionID required")
	}
	_, err := s.adminPool.Exec(ctx, `
		UPDATE sessions SET revoked_at = COALESCE(revoked_at, NOW()) WHERE id = $1`,
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("postgres: revoke session: %w", err)
	}
	return nil
}

// RevokeUserSessions revokes every live session belonging to userID and
// returns the list of token hashes that were revoked, so the caller can
// enumerate cache keys to delete (architect C4).
//
// Performed in a single transaction: SELECT FOR UPDATE → UPDATE → return the
// hashes. Concurrent writes that mint a new session for the same user *after*
// the SELECT but *before* the UPDATE will not be revoked (acceptable: the new
// session post-dates the revoke intent and a subsequent password reset will
// catch it).
func (s *Store) RevokeUserSessions(ctx context.Context, userID string) ([]string, error) {
	if userID == "" {
		return nil, fmt.Errorf("postgres: revoke user sessions: userID required")
	}
	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: revoke user sessions begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT session_token_hash FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		FOR UPDATE`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: revoke user sessions select: %w", err)
	}
	hashes := make([]string, 0, 8)
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: revoke user sessions scan: %w", err)
		}
		hashes = append(hashes, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: revoke user sessions rows: %w", err)
	}

	// Mirror the SELECT FOR UPDATE filter exactly. Without the
	// `expires_at > NOW()` clause, a session that expired between the SELECT
	// and the UPDATE would receive a `revoked_at` stamp without ending up in
	// `hashes` — the caller would then miss its cache entry. In practice the
	// window is sub-millisecond and an expired session already fails Live(),
	// but aligning the filters keeps the SELECT/UPDATE pair coherent.
	if _, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = NOW()
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()`,
		userID,
	); err != nil {
		return nil, fmt.Errorf("postgres: revoke user sessions update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: revoke user sessions commit: %w", err)
	}
	return hashes, nil
}

// ListUserSessionTokenHashes returns the token hashes of live sessions for
// userID, ordered oldest-first by created_at. The ordering is load-bearing
// for the per-user cap (architect C2 / plan §4.6): when the cap kicks in,
// the OLDEST session must be revoked, not an arbitrary one.
func (s *Store) ListUserSessionTokenHashes(ctx context.Context, userID string) ([]string, error) {
	if userID == "" {
		return nil, fmt.Errorf("postgres: list user session hashes: userID required")
	}
	rows, err := s.adminPool.Query(ctx, `
		SELECT session_token_hash FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY created_at ASC, id ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: list user session hashes: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0, 4)
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("postgres: list user session hashes scan: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list user session hashes rows: %w", err)
	}
	return out, nil
}

// CountSessionsForUser returns the count of live sessions for userID. Used to
// enforce SESSIONS_PER_USER_CAP (architect C2).
func (s *Store) CountSessionsForUser(ctx context.Context, userID string) (int, error) {
	if userID == "" {
		return 0, fmt.Errorf("postgres: count sessions: userID required")
	}
	var n int
	err := s.adminPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > NOW()`,
		userID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: count sessions: %w", err)
	}
	return n, nil
}

// SweepExpiredSessions deletes sessions where
//
//	expires_at < olderThan
//	OR (revoked_at IS NOT NULL AND revoked_at < olderThan)
//
// Cross-organization sweep; runs from the background ticker.
func (s *Store) SweepExpiredSessions(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.adminPool.Exec(ctx, `
		DELETE FROM sessions
		WHERE expires_at < $1
		   OR (revoked_at IS NOT NULL AND revoked_at < $1)`,
		olderThan,
	)
	if err != nil {
		return 0, fmt.Errorf("postgres: sweep expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ── Password resets ─────────────────────────────────────────────────────────

// CreatePasswordReset inserts a password_resets row. Bypasses RLS for the
// same capability-based reasoning as sessions.
func (s *Store) CreatePasswordReset(ctx context.Context, id, userID, organizationID, tokenHash, issuedByUserID string, expiresAt time.Time) error {
	if id == "" || userID == "" || organizationID == "" || tokenHash == "" {
		return fmt.Errorf("postgres: create password reset: required fields missing")
	}
	if expiresAt.IsZero() {
		return fmt.Errorf("postgres: create password reset: expires_at required")
	}
	var issuedByArg any
	if issuedByUserID != "" {
		issuedByArg = issuedByUserID
	}
	_, err := s.adminPool.Exec(ctx, `
		INSERT INTO password_resets (
			id, user_id, organization_id, token_hash, issued_by_user_id, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, NOW())`,
		id, userID, organizationID, tokenHash, issuedByArg, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: create password reset: %w", err)
	}
	return nil
}

// RedeemPasswordReset atomically validates the token and updates the user's
// password hash. See storage.NativeAuthStore.RedeemPasswordReset for the full
// contract.
func (s *Store) RedeemPasswordReset(ctx context.Context, tokenHash, newPasswordHash string) (string, error) {
	if tokenHash == "" || newPasswordHash == "" {
		return "", fmt.Errorf("postgres: redeem password reset: token_hash and new_password_hash required")
	}
	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("postgres: redeem password reset begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var resetID, userID string
	var expiresAt time.Time
	var redeemedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, expires_at, redeemed_at
		FROM password_resets
		WHERE token_hash = $1
		FOR UPDATE`,
		tokenHash,
	).Scan(&resetID, &userID, &expiresAt, &redeemedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", storage.ErrPasswordResetNotFound
	}
	if err != nil {
		return "", fmt.Errorf("postgres: redeem password reset lookup: %w", err)
	}
	if redeemedAt != nil {
		return "", storage.ErrPasswordResetNotFound
	}
	if !time.Now().UTC().Before(expiresAt) {
		return "", storage.ErrPasswordResetExpired
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET password_hash = $1, password_set_at = NOW() WHERE id = $2`,
		newPasswordHash, userID,
	); err != nil {
		return "", fmt.Errorf("postgres: redeem password reset update user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE password_resets SET redeemed_at = NOW() WHERE id = $1`,
		resetID,
	); err != nil {
		return "", fmt.Errorf("postgres: redeem password reset mark redeemed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("postgres: redeem password reset commit: %w", err)
	}
	return userID, nil
}

// ── Bootstrap singleton ─────────────────────────────────────────────────────

// CreateBootstrapState attempts to insert the singleton row. Multi-replica
// safe: pg_advisory_xact_lock ensures only one replica is in the
// CountOrganizations + INSERT critical section at once, and
// ON CONFLICT DO NOTHING guards against the unlikely case where the lock is
// released between calls (e.g. retry storm).
//
// Returns:
//   - (true, nil)                       — this caller wrote the row
//   - (false, nil)                      — another replica won the race; the
//                                         row already exists with a different
//                                         token hash
//   - (false, ErrBootstrapAlreadyDone)  — organizations already exists
func (s *Store) CreateBootstrapState(ctx context.Context, tokenHash, mintedByPod string) (bool, error) {
	if tokenHash == "" {
		return false, fmt.Errorf("postgres: create bootstrap state: token_hash required")
	}
	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres: create bootstrap begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, bootstrapAdvisoryLockKey); err != nil {
		return false, fmt.Errorf("postgres: create bootstrap advisory lock: %w", err)
	}

	var orgCount int64
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&orgCount); err != nil {
		return false, fmt.Errorf("postgres: create bootstrap org count: %w", err)
	}
	if orgCount > 0 {
		return false, storage.ErrBootstrapAlreadyDone
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO bootstrap_state (id, token_hash, minted_by_pod, created_at)
		VALUES ('singleton', $1, $2, NOW())
		ON CONFLICT (id) DO NOTHING`,
		tokenHash, mintedByPod,
	)
	if err != nil {
		return false, fmt.Errorf("postgres: create bootstrap insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres: create bootstrap commit: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// GetBootstrapState returns the singleton row if any. Returns
// ErrBootstrapAlreadyDone when the row is absent (callers that need to
// distinguish "never minted" from "already consumed" must additionally
// CountOrganizations).
func (s *Store) GetBootstrapState(ctx context.Context) (string, string, error) {
	var tokenHash, mintedByPod string
	err := s.adminPool.QueryRow(ctx, `
		SELECT token_hash, COALESCE(minted_by_pod, '')
		FROM bootstrap_state
		WHERE id = 'singleton'`,
	).Scan(&tokenHash, &mintedByPod)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", storage.ErrBootstrapAlreadyDone
	}
	if err != nil {
		return "", "", fmt.Errorf("postgres: get bootstrap state: %w", err)
	}
	return tokenHash, mintedByPod, nil
}

// ConsumeBootstrapState atomically validates the token and creates the
// first-owner organization, user, membership, and session. See
// storage.NativeAuthStore.ConsumeBootstrapState for the full contract.
func (s *Store) ConsumeBootstrapState(ctx context.Context, in storage.BootstrapConsume) (storage.BootstrapResult, error) {
	if in.TokenHash == "" {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap: token_hash required")
	}
	if in.OrganizationID == "" || in.UserID == "" || in.SessionID == "" {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap: pre-generated IDs required")
	}
	if in.UserPasswordHash == "" {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap: password hash required")
	}
	if in.SessionTokenHash == "" || in.SessionExpiresAt.IsZero() {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap: session inputs required")
	}

	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, bootstrapAdvisoryLockKey); err != nil {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap advisory lock: %w", err)
	}

	var storedHash string
	err = tx.QueryRow(ctx, `
		SELECT token_hash FROM bootstrap_state WHERE id = 'singleton' FOR UPDATE`,
	).Scan(&storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return storage.BootstrapResult{}, storage.ErrBootstrapAlreadyDone
	}
	if err != nil {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap lookup: %w", err)
	}

	// Constant-time compare. Defence in depth — the hashes are SHA-256 so
	// timing leakage on string compare is theoretical, but the cost is zero.
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(in.TokenHash)) != 1 {
		return storage.BootstrapResult{}, storage.ErrBootstrapTokenMismatch
	}

	now := time.Now().UTC()

	// 1. Organization (no RLS).
	if _, err := tx.Exec(ctx, `
		INSERT INTO organizations (id, org_code, name, created_at, onboarding_completed_at)
		VALUES ($1, $2, $3, $4, NULL)`,
		in.OrganizationID, "native:"+in.OrganizationID, in.OrganizationName, now,
	); err != nil {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap insert org: %w", err)
	}

	// 2. User (no RLS).
	kindeSub := "native:" + in.UserID
	var user model.User
	err = tx.QueryRow(ctx, `
		INSERT INTO users (
			id, organization_id, kinde_sub, email, name,
			password_hash, password_set_at, created_at, last_seen
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)
		RETURNING id, organization_id, kinde_sub, email, name,
		          password_hash, password_set_at, created_at, last_seen`,
		in.UserID, in.OrganizationID, kindeSub, in.UserEmail, in.UserName,
		in.UserPasswordHash, now,
	).Scan(
		&user.ID, &user.OrganizationID, &user.KindeSub, &user.Email, &user.Name,
		&user.PasswordHash, &user.PasswordSetAt, &user.CreatedAt, &user.LastSeen,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return storage.BootstrapResult{}, storage.ErrUserEmailExists
		}
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap insert user: %w", err)
	}

	// 3. Membership (RLS — set the org GUC first, scoped to this tx via local=true).
	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.organization_id', $1, true)`, in.OrganizationID,
	); err != nil {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap set org: %w", err)
	}
	membershipID := uuid.New().String()
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (id, organization_id, user_id, role, invited_by, created_at, updated_at)
		VALUES ($1, $2, $3, 'owner', NULL, NOW(), NOW())`,
		membershipID, in.OrganizationID, in.UserID,
	); err != nil {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap insert membership: %w", err)
	}

	// 4. Session (no RLS).
	var ipArg any
	if in.SessionIP != "" {
		ipArg = in.SessionIP
	}
	var session model.Session
	var ipStr *string
	err = tx.QueryRow(ctx, `
		INSERT INTO sessions (
			id, user_id, organization_id, auth_mode, session_token_hash,
			created_at, expires_at, last_seen_at, ip, user_agent_hash
		) VALUES (
			$1, $2, $3, 'bootstrap', $4, NOW(), $5, NOW(), $6, $7
		)
		RETURNING id, user_id, organization_id, auth_mode, session_token_hash,
		          created_at, expires_at, revoked_at, last_seen_at,
		          host(ip), user_agent_hash`,
		in.SessionID, in.UserID, in.OrganizationID, in.SessionTokenHash,
		in.SessionExpiresAt, ipArg, in.SessionUserAgentHash,
	).Scan(
		&session.ID, &session.UserID, &session.OrganizationID, (*string)(&session.AuthMode), &session.SessionTokenHash,
		&session.CreatedAt, &session.ExpiresAt, &session.RevokedAt, &session.LastSeenAt,
		&ipStr, &session.UserAgentHash,
	)
	if err != nil {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap insert session: %w", err)
	}
	if ipStr != nil {
		session.IP = net.ParseIP(*ipStr)
	}

	// 5. Delete the singleton — seals the bootstrap endpoint.
	if _, err := tx.Exec(ctx,
		`DELETE FROM bootstrap_state WHERE id = 'singleton'`,
	); err != nil {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap delete singleton: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return storage.BootstrapResult{}, fmt.Errorf("postgres: consume bootstrap commit: %w", err)
	}
	return storage.BootstrapResult{User: user, Session: session}, nil
}

// ── Native invitations ──────────────────────────────────────────────────────

// CreateNativeInvitation inserts a token-bearing pending_memberships row.
// Pre-checks email against memberships/users (same as CreatePendingInvitation).
// Returns ErrInvitationAlreadyMember / ErrUserExistsNoMembership where
// appropriate.
func (s *Store) CreateNativeInvitation(ctx context.Context, inv model.PendingInvitation) (model.PendingInvitation, bool, error) {
	if inv.OrganizationID == "" {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create native invitation: organization_id required")
	}
	if inv.Email == "" {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create native invitation: email required")
	}
	if inv.InviteTokenHash == "" {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create native invitation: invite_token_hash required")
	}
	if !model.ValidInvitationRoles[inv.Role] {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create native invitation: invalid role %q", inv.Role)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create native invitation begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return model.PendingInvitation{}, false, err
	}

	// Pre-check: is this email already a member or a known user?
	// Same logic as CreatePendingInvitation. UserExistsNoMembership is NOT
	// an error in B1.5 (existing user adding a second membership) but is in
	// B1; the handler decides which error model is currently active.
	var existingUserID, existingRole string
	err = tx.QueryRow(ctx, `
		SELECT u.id, COALESCE(m.role, '')
		FROM users u
		LEFT JOIN memberships m
		  ON m.user_id = u.id AND m.organization_id = $1
		WHERE u.organization_id = $1 AND lower(u.email) = lower($2)`,
		inv.OrganizationID, inv.Email,
	).Scan(&existingUserID, &existingRole)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create native invitation precheck: %w", err)
	case existingRole != "":
		return model.PendingInvitation{}, false, storage.ErrInvitationAlreadyMember
	default:
		return model.PendingInvitation{}, false, storage.ErrUserExistsNoMembership
	}

	id := inv.ID
	if id == "" {
		id = uuid.New().String()
	}
	now := time.Now().UTC()
	if inv.ExpiresAt.IsZero() {
		inv.ExpiresAt = now.Add(14 * 24 * time.Hour)
	}

	var inserted bool
	row := tx.QueryRow(ctx, `
		INSERT INTO pending_memberships (
			id, organization_id, email, role,
			invited_by_user_id, invited_by_email,
			status, expires_at, created_at, updated_at, invite_token_hash
		) VALUES (
			$1, $2, $3, $4, $5, $6, 'pending', $7, $8, $8, $9
		)
		ON CONFLICT (organization_id, lower(email)) WHERE status = 'pending'
		DO UPDATE SET
			role               = EXCLUDED.role,
			expires_at         = EXCLUDED.expires_at,
			invited_by_user_id = EXCLUDED.invited_by_user_id,
			invited_by_email   = EXCLUDED.invited_by_email,
			invite_token_hash  = EXCLUDED.invite_token_hash,
			updated_at         = NOW()
		RETURNING id, organization_id, email, role,
		          invited_by_user_id, invited_by_email,
		          status, kinde_invitation_id, kinde_user_id,
		          expires_at, created_at, updated_at,
		          COALESCE(invite_token_hash, ''),
		          (xmax = 0) AS inserted`,
		id, inv.OrganizationID, inv.Email, inv.Role,
		inv.InvitedByUserID, inv.InvitedByEmail,
		inv.ExpiresAt, now, inv.InviteTokenHash,
	)
	var out model.PendingInvitation
	if err := row.Scan(
		&out.ID, &out.OrganizationID, &out.Email, &out.Role,
		&out.InvitedByUserID, &out.InvitedByEmail,
		&out.Status, &out.KindeInvitationID, &out.KindeUserID,
		&out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt,
		&out.InviteTokenHash,
		&inserted,
	); err != nil {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create native invitation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create native invitation commit: %w", err)
	}
	return out, inserted, nil
}

// RedeemNativeInvitation atomically: validates the token row, ensures the
// referenced user exists (creating with the supplied password hash if not),
// inserts a memberships row, and DELETEs the pending_memberships row.
func (s *Store) RedeemNativeInvitation(ctx context.Context, in storage.NativeInviteRedeem) (model.User, model.Membership, error) {
	if in.TokenHash == "" {
		return model.User{}, model.Membership{}, storage.ErrInvitationNotFound
	}

	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var pendingID, organizationID, email, role, invitedBy string
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, email, role, invited_by_user_id
		FROM pending_memberships
		WHERE invite_token_hash = $1
		  AND status = 'pending'
		  AND expires_at > NOW()
		FOR UPDATE`,
		in.TokenHash,
	).Scan(&pendingID, &organizationID, &email, &role, &invitedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, model.Membership{}, storage.ErrInvitationNotFound
	}
	if err != nil {
		return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation lookup: %w", err)
	}

	// Resolve the user — match on lower(email) within this organization. If
	// no row matches, create one with the supplied password.
	var user model.User
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, kinde_sub, email, COALESCE(name, ''),
		       password_hash, password_set_at, created_at, last_seen
		FROM users
		WHERE organization_id = $1 AND lower(email) = lower($2)`,
		organizationID, email,
	).Scan(
		&user.ID, &user.OrganizationID, &user.KindeSub, &user.Email, &user.Name,
		&user.PasswordHash, &user.PasswordSetAt, &user.CreatedAt, &user.LastSeen,
	)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if in.UserID == "" || in.PasswordHash == "" {
			return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation: user_id and password_hash required for new user")
		}
		now := time.Now().UTC()
		kindeSub := "native:" + in.UserID
		err = tx.QueryRow(ctx, `
			INSERT INTO users (
				id, organization_id, kinde_sub, email, name,
				password_hash, password_set_at, created_at, last_seen
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)
			RETURNING id, organization_id, kinde_sub, email, name,
			          password_hash, password_set_at, created_at, last_seen`,
			in.UserID, organizationID, kindeSub, email, in.UserName, in.PasswordHash, now,
		).Scan(
			&user.ID, &user.OrganizationID, &user.KindeSub, &user.Email, &user.Name,
			&user.PasswordHash, &user.PasswordSetAt, &user.CreatedAt, &user.LastSeen,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return model.User{}, model.Membership{}, storage.ErrUserEmailExists
			}
			return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation insert user: %w", err)
		}
	case err != nil:
		return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation lookup user: %w", err)
	}

	// Set RLS context for the membership insert.
	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.organization_id', $1, true)`, organizationID,
	); err != nil {
		return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation set org: %w", err)
	}

	membershipID := uuid.New().String()
	insertNow := time.Now().UTC()
	membershipCreatedAt := insertNow
	membershipUpdatedAt := insertNow
	_, err = tx.Exec(ctx, `
		INSERT INTO memberships (id, organization_id, user_id, role, invited_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		membershipID, organizationID, user.ID, role, invitedBy, insertNow,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Membership already exists — odd (we should have caught this in
			// the precheck) but cleanly handleable: refetch the existing row
			// so the returned model.Membership reflects the durable record,
			// delete the pending row, and proceed.
			if derr := tx.QueryRow(ctx, `
				SELECT id, role, COALESCE(invited_by, ''), created_at, updated_at
				FROM memberships WHERE organization_id = $1 AND user_id = $2`,
				organizationID, user.ID,
			).Scan(&membershipID, &role, &invitedBy, &membershipCreatedAt, &membershipUpdatedAt); derr != nil {
				return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation refetch membership: %w", derr)
			}
		} else {
			return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation insert membership: %w", err)
		}
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM pending_memberships WHERE id = $1`, pendingID,
	); err != nil {
		return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation delete pending: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation commit: %w", err)
	}
	return user, model.Membership{
		ID:             membershipID,
		OrganizationID: organizationID,
		UserID:         user.ID,
		Role:           role,
		InvitedBy:      invitedBy,
		CreatedAt:      membershipCreatedAt,
		UpdatedAt:      membershipUpdatedAt,
	}, nil
}
