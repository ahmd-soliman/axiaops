// native_auth.go — native auth Store implementations.
// Methods declared on *Store; satisfies storage.NativeAuthStore (embedded
// into storage.Store).

package postgres

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"strings"
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

// sessionMintAdvisoryLockClass namespaces the per-user advisory locks taken
// inside CreateSessionEnforcingCap. The two-argument form
// pg_advisory_xact_lock(int4, int4) lives in a separate keyspace from the
// single-int8 locks above, so this constant cannot collide with the
// bootstrap or migration-history locks. Hashing the user_id with hashtext
// into the second int4 gives per-user contention only — two mints for
// DIFFERENT users never serialise on each other (modulo the rare int4 hash
// collision, which is harmless: the worse-case is a millisecond wait for an
// unrelated user's tx to commit). Stable across releases.
const sessionMintAdvisoryLockClass = int32(0x4D696E74) // "Mint" as int32

// ── Native-auth user mutations ──────────────────────────────────────────────

// CreateUserWithPassword inserts a user row with a password hash and a
// synthetic external_id of the form "native:<id>". The synthetic prefix
// keeps the external_id UNIQUE constraint usable for non-SSO users (real
// SSO users get the IdP-issued `sub` claim instead).
//
// Bypasses RLS via the runtime-bypass pool because the bootstrap and
// invitation-redeem callers may be running before any organization context
// exists. Since migration 035 the users table is RLS-scoped
// (users_organization_isolation); this path relies on the runtime role's
// users_runtime_bypass policy, not on users being unprotected.
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
	externalID := "native:" + u.ID

	var out model.User
	err := s.adminPool.QueryRow(ctx, `
		INSERT INTO users (
			id, organization_id, external_id, email, name,
			password_hash, password_set_at, created_at, last_seen
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)
		RETURNING id, organization_id, external_id, email, name,
		          password_hash, password_set_at, created_at, last_seen`,
		u.ID, u.OrganizationID, externalID, u.Email, u.Name, u.PasswordHash, now,
	).Scan(
		&out.ID, &out.OrganizationID, &out.ExternalID, &out.Email, &out.Name,
		&out.PasswordHash, &out.PasswordSetAt, &out.CreatedAt, &out.LastSeen,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// 23505 = unique_violation. Two unique constraints can fire here:
			// users_email_lower_unique (the new one from migration 021) or the
			// existing UNIQUE on external_id. The external_id case is impossibly
			// rare (UUID collision), so treat any 23505 from this insert as
			// "email taken" — the much more likely cause.
			return model.User{}, storage.ErrUserEmailExists
		}
		return model.User{}, fmt.Errorf("postgres: create user with password: %w", err)
	}
	return out, nil
}

// UpdateUserPassword sets users.password_hash + users.password_set_at = NOW().
// Runs on the runtime-bypass pool: a by-PK write where the userID is the
// capability and no request org context is set (the users RLS WITH CHECK from
// migration 035 would otherwise reject an app-pool UPDATE with no GUC).
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

// UpdateUserName sets users.name and returns the previous value in one
// round-trip. Bypasses RLS for the same reason as UpdateUserPassword.
// Returns ErrUserNotFound when no row matches.
func (s *Store) UpdateUserName(ctx context.Context, userID, newName string) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("postgres: update user name: userID required")
	}
	// CTE captures the prior name before the UPDATE applies; the outer
	// SELECT returns it. Single statement, atomic read+write — no race
	// where a concurrent SELECT could see the new value but still be
	// returned the old one.
	var oldName string
	err := s.adminPool.QueryRow(ctx, `
		WITH prior AS (
			SELECT name FROM users WHERE id = $1
		),
		upd AS (
			UPDATE users SET name = $2 WHERE id = $1
			RETURNING id
		)
		SELECT prior.name FROM prior, upd`,
		userID, newName,
	).Scan(&oldName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", storage.ErrUserNotFound
		}
		return "", fmt.Errorf("postgres: update user name: %w", err)
	}
	return oldName, nil
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

// LookupUserByEmail returns the user (with password_hash) plus the list
// of live memberships across all organizations. Bypasses RLS — login is
// pre-org-context. Used by POST /v1/auth/login: zero memberships → 401,
// one → mint session, multi → 409 (B1.5 will branch to org picker).
func (s *Store) LookupUserByEmail(ctx context.Context, email string) (model.User, []model.Membership, error) {
	if email == "" {
		return model.User{}, nil, storage.ErrUserNotFound
	}

	var u model.User
	err := s.adminPool.QueryRow(ctx, `
		SELECT id, organization_id, external_id, email, name,
		       password_hash, password_set_at, created_at, last_seen
		FROM users
		WHERE lower(email) = lower($1)`,
		email,
	).Scan(
		&u.ID, &u.OrganizationID, &u.ExternalID, &u.Email, &u.Name,
		&u.PasswordHash, &u.PasswordSetAt, &u.CreatedAt, &u.LastSeen,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, nil, storage.ErrUserNotFound
	}
	if err != nil {
		return model.User{}, nil, fmt.Errorf("postgres: lookup user by email: %w", err)
	}

	rows, err := s.adminPool.Query(ctx, `
		SELECT id, organization_id, user_id, role,
		       COALESCE(invited_by, ''), created_at, updated_at
		FROM memberships
		WHERE user_id = $1
		ORDER BY created_at ASC`,
		u.ID,
	)
	if err != nil {
		return model.User{}, nil, fmt.Errorf("postgres: lookup user memberships: %w", err)
	}
	defer rows.Close()

	memberships := make([]model.Membership, 0, 2)
	for rows.Next() {
		var m model.Membership
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.UserID, &m.Role, &m.InvitedBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return model.User{}, nil, fmt.Errorf("postgres: scan membership: %w", err)
		}
		memberships = append(memberships, m)
	}
	if err := rows.Err(); err != nil {
		return model.User{}, nil, fmt.Errorf("postgres: lookup user memberships rows: %w", err)
	}
	return u, memberships, nil
}

// LookupMembership joins memberships + users in one SELECT. Bypasses RLS
// (uses adminPool) because the native auth provider runs this lookup
// BEFORE the request has an organization context — it's the lookup that
// resolves the role for that context. Equivalent reasoning to
// GetSessionByTokenHash; see migration 021 for the pattern.
//
// Returns ("", "", nil) when no membership row matches — the caller
// treats that as authentication failure (defence in depth: a session
// that points at a now-deleted membership must not authenticate).
func (s *Store) LookupMembership(ctx context.Context, organizationID, userID string) (string, string, string, error) {
	if organizationID == "" || userID == "" {
		return "", "", "", nil
	}
	// users.email and users.name are both NOT NULL DEFAULT '' (migration 001),
	// so no COALESCE is needed — empty strings round-trip as empty strings.
	var role, email, name string
	err := s.adminPool.QueryRow(ctx, `
		SELECT m.role, u.email, u.name
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1 AND m.user_id = $2`,
		organizationID, userID,
	).Scan(&role, &email, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", nil
	}
	if err != nil {
		return "", "", "", fmt.Errorf("postgres: lookup membership: %w", err)
	}
	return role, email, name, nil
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

	// id_token_encrypted is empty for native sessions; pass NULL via *string
	// rather than an empty TEXT so the column matches the "no IdP session to
	// invalidate" intent rather than an empty string sentinel.
	var idTokenArg any
	if in.IDTokenEncrypted != "" {
		idTokenArg = in.IDTokenEncrypted
	}

	var out model.Session
	var ipStr *string
	var idTokenEnc *string
	err := s.adminPool.QueryRow(ctx, `
		INSERT INTO sessions (
			id, user_id, organization_id, auth_mode, session_token_hash,
			created_at, expires_at, last_seen_at, ip, user_agent_hash,
			id_token_encrypted
		) VALUES (
			$1, $2, $3, $4, $5, NOW(), $6, NOW(), $7, $8, $9
		)
		RETURNING id, user_id, organization_id, auth_mode, session_token_hash,
		          created_at, expires_at, revoked_at, last_seen_at,
		          host(ip), user_agent_hash, id_token_encrypted`,
		in.ID, in.UserID, in.OrganizationID, string(in.AuthMode), in.SessionTokenHash,
		in.ExpiresAt, ipArg, in.UserAgentHash, idTokenArg,
	).Scan(
		&out.ID, &out.UserID, &out.OrganizationID, (*string)(&out.AuthMode), &out.SessionTokenHash,
		&out.CreatedAt, &out.ExpiresAt, &out.RevokedAt, &out.LastSeenAt,
		&ipStr, &out.UserAgentHash, &idTokenEnc,
	)
	if err != nil {
		return model.Session{}, fmt.Errorf("postgres: create session: %w", err)
	}
	if ipStr != nil {
		out.IP = net.ParseIP(*ipStr)
	}
	if idTokenEnc != nil {
		out.IDTokenEncrypted = *idTokenEnc
	}
	return out, nil
}

// CreateSessionEnforcingCap inserts a session row and, in the same
// transaction, revokes any oldest-first excess live sessions for the user
// so the post-commit count is exactly perUserCap. Closes the M-7 audit
// window from the 2026-05-09 audit: the previous implementation ran the
// cap-enforcement in a separate post-INSERT call, so a transient revoke
// failure (PG blip / cache error) left an over-cap leftover until the
// 5-minute sweep. Folding both into one tx makes the cap an atomic
// invariant — any reader sees either "no new session" (tx aborted) or
// "exactly cap sessions, newest survived" (tx committed).
//
// Returns the persisted session and the list of session_token_hashes
// that were revoked so the caller can evict cache entries post-commit
// (architect C4 — no scan/wildcard delete on the cache).
//
// When perUserCap <= 0 the cap step is skipped and this method is
// equivalent to CreateSession plus a transaction wrapper.
func (s *Store) CreateSessionEnforcingCap(ctx context.Context, in model.Session, perUserCap int) (model.Session, []string, error) {
	if in.ID == "" {
		return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap: id required")
	}
	if in.UserID == "" || in.OrganizationID == "" {
		return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap: user_id and organization_id required")
	}
	if in.SessionTokenHash == "" {
		return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap: session_token_hash required")
	}
	if in.ExpiresAt.IsZero() {
		return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap: expires_at required")
	}
	if in.AuthMode == "" {
		return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap: auth_mode required")
	}

	var ipArg any
	if in.IP != nil {
		ipArg = in.IP.String()
	}
	var idTokenArg any
	if in.IDTokenEncrypted != "" {
		idTokenArg = in.IDTokenEncrypted
	}

	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Per-user advisory lock — serialises concurrent mints for the SAME
	// user_id so the cap arithmetic is coherent under parallel logins.
	// Without it, two mints arriving while the user is exactly at cap can
	// each see liveOthers == cap (the new row is excluded via id <> $2)
	// and skip the revoke, leaving the user transiently at cap+2 until
	// the 5-minute sweep ticker. Released automatically at COMMIT/ROLLBACK
	// because this is the _xact_ variant — no defer-unlock needed. Mints
	// for different users hash to different second-arg ints and never
	// contend.
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock($1, hashtext($2))`,
		sessionMintAdvisoryLockClass, in.UserID,
	); err != nil {
		return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap lock: %w", err)
	}

	// INSERT the new row first. The cap-enforcement queries below
	// exclude it via `id <> out.ID` so the just-inserted row is never
	// itself a revoke candidate — the newest session always wins the
	// seat per architect C2 / plan §4.6. The exclusion is by ID rather
	// than created_at because two mints within the same NOW() tick
	// would otherwise tie on timestamp.
	var out model.Session
	var ipStr *string
	var idTokenEnc *string
	if err := tx.QueryRow(ctx, `
		INSERT INTO sessions (
			id, user_id, organization_id, auth_mode, session_token_hash,
			created_at, expires_at, last_seen_at, ip, user_agent_hash,
			id_token_encrypted
		) VALUES (
			$1, $2, $3, $4, $5, NOW(), $6, NOW(), $7, $8, $9
		)
		RETURNING id, user_id, organization_id, auth_mode, session_token_hash,
		          created_at, expires_at, revoked_at, last_seen_at,
		          host(ip), user_agent_hash, id_token_encrypted`,
		in.ID, in.UserID, in.OrganizationID, string(in.AuthMode), in.SessionTokenHash,
		in.ExpiresAt, ipArg, in.UserAgentHash, idTokenArg,
	).Scan(
		&out.ID, &out.UserID, &out.OrganizationID, (*string)(&out.AuthMode), &out.SessionTokenHash,
		&out.CreatedAt, &out.ExpiresAt, &out.RevokedAt, &out.LastSeenAt,
		&ipStr, &out.UserAgentHash, &idTokenEnc,
	); err != nil {
		return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap insert: %w", err)
	}
	if ipStr != nil {
		out.IP = net.ParseIP(*ipStr)
	}
	if idTokenEnc != nil {
		out.IDTokenEncrypted = *idTokenEnc
	}

	var revokedHashes []string
	if perUserCap > 0 {
		// Count live sessions for the user OTHER than the one we just
		// inserted. If that count is >= cap the user is now over the cap
		// (count + 1 > cap → revoke `count + 1 - cap` oldest peers).
		// The id-exclusion guarantees we never revoke the brand-new row
		// even in the pathological "cap = 0 after insert" case.
		var liveOthers int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM sessions
			WHERE user_id = $1
			  AND revoked_at IS NULL
			  AND expires_at > NOW()
			  AND id <> $2`,
			in.UserID, out.ID,
		).Scan(&liveOthers); err != nil {
			return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap count: %w", err)
		}
		// totalLive = liveOthers + 1 (the new row). Excess to revoke =
		// totalLive - cap, clamped to >= 0.
		excess := liveOthers + 1 - perUserCap
		if excess > 0 {
			// SELECT FOR UPDATE the oldest `excess` peer sessions (mirror
			// ListUserSessionTokenHashes ordering — oldest-first by
			// created_at, then id for stable ties). The lock prevents a
			// concurrent revoke from racing the UPDATE below.
			rows, err := tx.Query(ctx, `
				SELECT id, session_token_hash FROM sessions
				WHERE user_id = $1
				  AND revoked_at IS NULL
				  AND expires_at > NOW()
				  AND id <> $2
				ORDER BY created_at ASC, id ASC
				LIMIT $3
				FOR UPDATE`,
				in.UserID, out.ID, excess,
			)
			if err != nil {
				return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap select oldest: %w", err)
			}
			ids := make([]string, 0, excess)
			revokedHashes = make([]string, 0, excess)
			for rows.Next() {
				var id, h string
				if err := rows.Scan(&id, &h); err != nil {
					rows.Close()
					return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap scan oldest: %w", err)
				}
				ids = append(ids, id)
				revokedHashes = append(revokedHashes, h)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap rows oldest: %w", err)
			}

			if len(ids) > 0 {
				if _, err := tx.Exec(ctx, `
					UPDATE sessions
					   SET revoked_at = NOW()
					 WHERE id = ANY($1::text[])
					   AND revoked_at IS NULL`,
					ids,
				); err != nil {
					return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap revoke: %w", err)
				}
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return model.Session{}, nil, fmt.Errorf("postgres: create session enforcing cap commit: %w", err)
	}
	return out, revokedHashes, nil
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
	var idTokenEnc *string
	err := s.adminPool.QueryRow(ctx, `
		SELECT id, user_id, organization_id, auth_mode, session_token_hash,
		       created_at, expires_at, revoked_at, last_seen_at,
		       host(ip), user_agent_hash, id_token_encrypted
		FROM sessions
		WHERE session_token_hash = $1`,
		tokenHash,
	).Scan(
		&out.ID, &out.UserID, &out.OrganizationID, (*string)(&out.AuthMode), &out.SessionTokenHash,
		&out.CreatedAt, &out.ExpiresAt, &out.RevokedAt, &out.LastSeenAt,
		&ipStr, &out.UserAgentHash, &idTokenEnc,
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
	if idTokenEnc != nil {
		out.IDTokenEncrypted = *idTokenEnc
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
// contract. Returns (userID, organizationID) so the caller can audit
// under the correct org.
func (s *Store) RedeemPasswordReset(ctx context.Context, tokenHash, newPasswordHash string) (string, string, error) {
	if tokenHash == "" || newPasswordHash == "" {
		return "", "", fmt.Errorf("postgres: redeem password reset: token_hash and new_password_hash required")
	}
	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return "", "", fmt.Errorf("postgres: redeem password reset begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var resetID, userID, organizationID string
	var expiresAt time.Time
	var redeemedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, organization_id, expires_at, redeemed_at
		FROM password_resets
		WHERE token_hash = $1
		FOR UPDATE`,
		tokenHash,
	).Scan(&resetID, &userID, &organizationID, &expiresAt, &redeemedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", storage.ErrPasswordResetNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("postgres: redeem password reset lookup: %w", err)
	}
	if redeemedAt != nil {
		return "", "", storage.ErrPasswordResetNotFound
	}
	if !time.Now().UTC().Before(expiresAt) {
		return "", "", storage.ErrPasswordResetExpired
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET password_hash = $1, password_set_at = NOW() WHERE id = $2`,
		newPasswordHash, userID,
	); err != nil {
		return "", "", fmt.Errorf("postgres: redeem password reset update user: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE password_resets SET redeemed_at = NOW() WHERE id = $1`,
		resetID,
	); err != nil {
		return "", "", fmt.Errorf("postgres: redeem password reset mark redeemed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", fmt.Errorf("postgres: redeem password reset commit: %w", err)
	}
	return userID, organizationID, nil
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
//     row already exists with a different
//     token hash
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

	// 2. User — runtime-bypass tx, no org GUC set; the users_runtime_bypass
	//    policy (migration 035) permits the insert.
	externalID := "native:" + in.UserID
	var user model.User
	err = tx.QueryRow(ctx, `
		INSERT INTO users (
			id, organization_id, external_id, email, name,
			password_hash, password_set_at, created_at, last_seen
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)
		RETURNING id, organization_id, external_id, email, name,
		          password_hash, password_set_at, created_at, last_seen`,
		in.UserID, in.OrganizationID, externalID, in.UserEmail, in.UserName,
		in.UserPasswordHash, now,
	).Scan(
		&user.ID, &user.OrganizationID, &user.ExternalID, &user.Email, &user.Name,
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
		INSERT INTO memberships (id, organization_id, user_id, role, invited_by, provisioned_via, created_at, updated_at)
		VALUES ($1, $2, $3, 'owner', NULL, 'manual', NOW(), NOW())`,
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
		          status,
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
		&out.Status,
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

// LookupInvitationByToken is the read-only peek that discovers whether a
// pending invitation exists and whether the invited email matches a user
// that already exists globally. Used by both:
//
//  1. GET /v1/auth/invitations/preview (drives the AcceptInviteScreen
//     UI variation — "set a new password" vs "enter your existing
//     password"). The wire shape redacts ExistingUser.PasswordHash.
//  2. POST /v1/auth/invitations/redeem (handler verifies the supplied
//     password against ExistingUser.PasswordHash before calling
//     RedeemNativeInvitation with ExistingUserID set).
//
// Bypasses RLS — the user lookup must see across organisations.
func (s *Store) LookupInvitationByToken(ctx context.Context, tokenHash string) (storage.PeekedInvitation, error) {
	if tokenHash == "" {
		return storage.PeekedInvitation{}, storage.ErrInvitationNotFound
	}

	var p storage.PeekedInvitation
	err := s.adminPool.QueryRow(ctx, `
		SELECT pm.email, pm.organization_id, pm.role, COALESCE(pm.invited_by_user_id, ''),
		       COALESCE(o.name, '')
		FROM pending_memberships pm
		JOIN organizations o ON o.id = pm.organization_id
		WHERE pm.invite_token_hash = $1
		  AND pm.status = 'pending'
		  AND pm.expires_at > NOW()`,
		tokenHash,
	).Scan(&p.Email, &p.OrganizationID, &p.Role, &p.InvitedBy, &p.OrganizationName)
	if errors.Is(err, pgx.ErrNoRows) {
		return storage.PeekedInvitation{}, storage.ErrInvitationNotFound
	}
	if err != nil {
		return storage.PeekedInvitation{}, fmt.Errorf("postgres: peek invitation: %w", err)
	}

	// Existing-user lookup — global, not per-org. The users table has a
	// global lower(email) constraint (per the existing schema), so at
	// most one row matches.
	var u model.User
	err = s.adminPool.QueryRow(ctx, `
		SELECT id, organization_id, external_id, email, COALESCE(name, ''),
		       password_hash, password_set_at, created_at, last_seen
		FROM users
		WHERE lower(email) = lower($1)`,
		p.Email,
	).Scan(
		&u.ID, &u.OrganizationID, &u.ExternalID, &u.Email, &u.Name,
		&u.PasswordHash, &u.PasswordSetAt, &u.CreatedAt, &u.LastSeen,
	)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No existing user → new-user flow on redeem.
	case err != nil:
		return storage.PeekedInvitation{}, fmt.Errorf("postgres: peek invitation user lookup: %w", err)
	default:
		p.ExistingUser = &u
	}
	return p, nil
}

// RedeemNativeInvitation atomically: validates the token row, ensures the
// referenced user exists (creating with the supplied password hash if not),
// inserts a memberships row, and DELETEs the pending_memberships row.
//
// Two flows selected by the caller via NativeInviteRedeem.ExistingUserID:
//
//  1. New user (ExistingUserID == ""): require UserID + UserName +
//     PasswordHash. INSERT users → INSERT memberships → DELETE pending.
//  2. Existing user (ExistingUserID != ""): caller has already verified
//     the supplied password against the user's stored hash. INSERT
//     memberships against the existing user_id → DELETE pending. The
//     user row is NOT touched — name, password_hash, external_id all
//     stay as they were in the user's other organisation.
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

	// Resolve the user — two flows selected by ExistingUserID.
	var user model.User
	if in.ExistingUserID != "" {
		// Flow 2: existing user (B1.5 cross-org redemption). Caller already
		// verified the password against the user's stored hash. Just load
		// the row so the returned model.User reflects the current state.
		err = tx.QueryRow(ctx, `
			SELECT id, organization_id, external_id, email, COALESCE(name, ''),
			       password_hash, password_set_at, created_at, last_seen
			FROM users
			WHERE id = $1`,
			in.ExistingUserID,
		).Scan(
			&user.ID, &user.OrganizationID, &user.ExternalID, &user.Email, &user.Name,
			&user.PasswordHash, &user.PasswordSetAt, &user.CreatedAt, &user.LastSeen,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			// Race: caller saw the user in LookupInvitationByToken but
			// the row was deleted (right-to-erasure or admin action)
			// between peek and redeem. Surface as invitation-not-found
			// — the caller's existing-user assumption is no longer
			// valid; they'd need to redeem as a new user.
			return model.User{}, model.Membership{}, storage.ErrInvitationNotFound
		}
		if err != nil {
			return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation load existing user: %w", err)
		}
		// Defence in depth: confirm the email matches the invitation.
		// Catches a caller that supplied a spoofed ExistingUserID
		// belonging to a different email — without this guard, that
		// would silently add a membership for the wrong user.
		// Returns the dedicated sentinel so log triage distinguishes
		// "caller passed the wrong inputs" from "transient DB error".
		if !strings.EqualFold(user.Email, email) {
			return model.User{}, model.Membership{}, storage.ErrInvitationUserMismatch
		}
	} else {
		// Flow 1: new user. Require the inputs the INSERT needs.
		if in.UserID == "" || in.PasswordHash == "" {
			return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation: user_id and password_hash required for new user")
		}
		now := time.Now().UTC()
		externalID := "native:" + in.UserID
		err = tx.QueryRow(ctx, `
			INSERT INTO users (
				id, organization_id, external_id, email, name,
				password_hash, password_set_at, created_at, last_seen
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7)
			RETURNING id, organization_id, external_id, email, name,
			          password_hash, password_set_at, created_at, last_seen`,
			in.UserID, organizationID, externalID, email, in.UserName, in.PasswordHash, now,
		).Scan(
			&user.ID, &user.OrganizationID, &user.ExternalID, &user.Email, &user.Name,
			&user.PasswordHash, &user.PasswordSetAt, &user.CreatedAt, &user.LastSeen,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				// Race: a global user with this email was created between
				// the caller's peek (which saw no existing user) and
				// this INSERT. The caller should retry — peek would now
				// return ExistingUser != nil and route through Flow 2.
				return model.User{}, model.Membership{}, storage.ErrUserEmailExists
			}
			return model.User{}, model.Membership{}, fmt.Errorf("postgres: redeem native invitation insert user: %w", err)
		}
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
		INSERT INTO memberships (id, organization_id, user_id, role, invited_by, provisioned_via, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'invitation', $6, $6)`,
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
