package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// CreatePendingInvitation inserts a pending_memberships row, or upserts an
// existing pending row for (organization_id, lower(email)) — refreshing
// expires_at and updating role on re-invite. The bool return is true when a
// new row was inserted, false when an existing pending row was updated.
//
// Pre-checks the email against memberships + users. Returns
// ErrInvitationAlreadyMember when the email already has an active membership,
// or ErrUserExistsNoMembership when the email matches a known user without
// membership in this organization.
func (s *Store) CreatePendingInvitation(ctx context.Context, inv model.PendingInvitation) (model.PendingInvitation, bool, error) {
	if inv.OrganizationID == "" {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create pending invitation: organization_id required")
	}
	if inv.Email == "" {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create pending invitation: email required")
	}
	if !model.ValidInvitationRoles[inv.Role] {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create pending invitation: invalid role %q", inv.Role)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create pending invitation begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return model.PendingInvitation{}, false, err
	}

	// Pre-check: is this email already a member or a known user IN THIS ORG?
	// Deliberately org-local — a cross-org existing user (home org ≠ this org)
	// is intentionally treated as a fresh invitee here; the existing-user case
	// is resolved at redeem time (RedeemNativeInvitation looks the email up
	// globally on the runtime pool). App-pool read under org context
	// (setOrganization above): the explicit organization_id filter and the
	// users_organization_isolation policy (migration 035) agree — both keyed on
	// the request org, which the handler sources identically for ctx and
	// inv.OrganizationID.
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
		// no user exists → continue with insert/upsert
	case err != nil:
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create pending invitation precheck: %w", err)
	case existingRole != "":
		return model.PendingInvitation{}, false, storage.ErrInvitationAlreadyMember
	default:
		// User exists but no membership — caller should use POST /v1/memberships.
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

	// INSERT, on conflict against the partial unique index for pending rows
	// refresh role and expires_at and bump updated_at. The bool inserted is
	// derived from xmax — 0 on a real insert, non-zero on an UPDATE.
	var inserted bool
	row := tx.QueryRow(ctx, `
		INSERT INTO pending_memberships (
			id, organization_id, email, role,
			invited_by_user_id, invited_by_email,
			status, expires_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, 'pending', $7, $8, $8
		)
		ON CONFLICT (organization_id, lower(email)) WHERE status = 'pending'
		DO UPDATE SET
			role               = EXCLUDED.role,
			expires_at         = EXCLUDED.expires_at,
			invited_by_user_id = EXCLUDED.invited_by_user_id,
			invited_by_email   = EXCLUDED.invited_by_email,
			updated_at         = NOW()
		RETURNING id, organization_id, email, role,
		          invited_by_user_id, invited_by_email,
		          status,
		          expires_at, created_at, updated_at,
		          (xmax = 0) AS inserted`,
		id, inv.OrganizationID, inv.Email, inv.Role,
		inv.InvitedByUserID, inv.InvitedByEmail,
		inv.ExpiresAt, now,
	)
	var out model.PendingInvitation
	if err := row.Scan(
		&out.ID, &out.OrganizationID, &out.Email, &out.Role,
		&out.InvitedByUserID, &out.InvitedByEmail,
		&out.Status,
		&out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt,
		&inserted,
	); err != nil {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create pending invitation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return model.PendingInvitation{}, false, fmt.Errorf("postgres: create pending invitation commit: %w", err)
	}
	return out, inserted, nil
}

// ListPendingInvitations returns invitations for the organization in ctx
// filtered by status. status="" returns only status='pending' rows.
func (s *Store) ListPendingInvitations(ctx context.Context, status string) ([]model.PendingInvitation, error) {
	if status == "" {
		status = model.InvitationStatusPending
	}
	if !model.ValidInvitationStatuses[status] {
		return nil, fmt.Errorf("postgres: list pending invitations: invalid status %q", status)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list pending invitations begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id, organization_id, email, role,
		       invited_by_user_id, invited_by_email,
		       status,
		       expires_at, created_at, updated_at
		FROM pending_memberships
		WHERE status = $1
		ORDER BY created_at DESC`,
		status,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: list pending invitations: %w", err)
	}
	defer rows.Close()

	out := []model.PendingInvitation{}
	for rows.Next() {
		var inv model.PendingInvitation
		if err := rows.Scan(
			&inv.ID, &inv.OrganizationID, &inv.Email, &inv.Role,
			&inv.InvitedByUserID, &inv.InvitedByEmail,
			&inv.Status,
			&inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: list pending invitations scan: %w", err)
		}
		out = append(out, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list pending invitations rows: %w", err)
	}
	return out, tx.Commit(ctx)
}

// GetPendingInvitation returns a single invitation by ID for the organization in ctx.
func (s *Store) GetPendingInvitation(ctx context.Context, id string) (model.PendingInvitation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.PendingInvitation{}, fmt.Errorf("postgres: get pending invitation begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return model.PendingInvitation{}, err
	}

	var inv model.PendingInvitation
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, email, role,
		       invited_by_user_id, invited_by_email,
		       status,
		       expires_at, created_at, updated_at
		FROM pending_memberships
		WHERE id = $1`,
		id,
	).Scan(
		&inv.ID, &inv.OrganizationID, &inv.Email, &inv.Role,
		&inv.InvitedByUserID, &inv.InvitedByEmail,
		&inv.Status,
		&inv.ExpiresAt, &inv.CreatedAt, &inv.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.PendingInvitation{}, storage.ErrInvitationNotFound
	}
	if err != nil {
		return model.PendingInvitation{}, fmt.Errorf("postgres: get pending invitation: %w", err)
	}
	return inv, tx.Commit(ctx)
}

// RevokePendingInvitation flips status to 'revoked' for a pending row.
// Returns ErrInvitationNotFound if no row, ErrInvitationNotPending if already
// revoked or expired.
func (s *Store) RevokePendingInvitation(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: revoke pending invitation begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM pending_memberships WHERE id = $1 FOR UPDATE`, id,
	).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return storage.ErrInvitationNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: revoke pending invitation lock: %w", err)
	}
	if status != model.InvitationStatusPending {
		return storage.ErrInvitationNotPending
	}

	if _, err := tx.Exec(ctx,
		`UPDATE pending_memberships SET status = 'revoked', updated_at = NOW() WHERE id = $1`, id,
	); err != nil {
		return fmt.Errorf("postgres: revoke pending invitation: %w", err)
	}
	return tx.Commit(ctx)
}

// RedeemPendingInvitation atomically inserts a memberships row and DELETES the
// matching pending_memberships row in one transaction. Match key is
// (organization_id, lower(email)) WHERE status='pending' AND expires_at > NOW().
// Returns true on redemption, (false, nil) when no match (silent no-op).
//
// Hot path — called from auth middleware on every authenticated request after
// EnsureFirstMembership. Sub-millisecond on cache hits courtesy of the partial
// unique index.
func (s *Store) RedeemPendingInvitation(ctx context.Context, organizationID, userID, email string) (bool, error) {
	if organizationID == "" || userID == "" || email == "" {
		// Soft-fail: any missing field means we can't match. Not an error —
		// happens for self-signup owners and dev-mode users with no email claim.
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres: redeem pending invitation begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true)`, organizationID); err != nil {
		return false, fmt.Errorf("postgres: redeem set organization: %w", err)
	}

	var pendingID, role, invitedBy string
	err = tx.QueryRow(ctx, `
		SELECT id, role, invited_by_user_id
		FROM pending_memberships
		WHERE organization_id = $1
		  AND lower(email) = lower($2)
		  AND status = 'pending'
		  AND expires_at > NOW()
		FOR UPDATE`,
		organizationID, email,
	).Scan(&pendingID, &role, &invitedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("postgres: redeem pending invitation lookup: %w", err)
	}

	// Insert the membership. The (organization_id, user_id) UNIQUE constraint on
	// memberships means a second concurrent redemption (impossible in practice
	// because the pending row is FOR UPDATE-locked) would surface 23505 — treat
	// as redeemed-by-someone-else, no-op.
	_, err = tx.Exec(ctx, `
		INSERT INTO memberships (id, organization_id, user_id, role, invited_by, provisioned_via, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'invitation', NOW(), NOW())`,
		uuid.New().String(), organizationID, userID, role, invitedBy,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Membership already exists — user redeemed via another path.
			// Still delete the pending row to keep the index sparse.
			if _, derr := tx.Exec(ctx, `DELETE FROM pending_memberships WHERE id = $1`, pendingID); derr != nil {
				return false, fmt.Errorf("postgres: redeem cleanup: %w", derr)
			}
			if cerr := tx.Commit(ctx); cerr != nil {
				return false, fmt.Errorf("postgres: redeem commit: %w", cerr)
			}
			return false, nil
		}
		return false, fmt.Errorf("postgres: redeem insert membership: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM pending_memberships WHERE id = $1`, pendingID); err != nil {
		return false, fmt.Errorf("postgres: redeem delete pending: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres: redeem commit: %w", err)
	}
	return true, nil
}

// ExpirePendingInvitations flips status to 'expired' for ripe pending rows.
// Cross-organization sweep — uses adminPool to bypass RLS. Idempotent.
func (s *Store) ExpirePendingInvitations(ctx context.Context) (int64, error) {
	tag, err := s.adminPool.Exec(ctx, `
		UPDATE pending_memberships
		SET status = 'expired', updated_at = NOW()
		WHERE status = 'pending' AND expires_at <= NOW()`,
	)
	if err != nil {
		return 0, fmt.Errorf("postgres: expire pending invitations: %w", err)
	}
	return tag.RowsAffected(), nil
}

