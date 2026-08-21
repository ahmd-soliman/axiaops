// staff.go — Platform admin plane (staff identity + RBAC) Store impl.
// Methods declared on *Store; satisfies storage.StaffStore (embedded into
// storage.Store). See docs/saas-platform-admin-design.md §4/§8 and
// docs/admin-portal-plan.md.
//
// Every method runs on s.adminPool with NO organization context — staff are
// cross-plane principals and these tables have no RLS (migration 032). The
// cross-org read methods (ListAllOrganizations / StaffTenantSummary) mirror
// ListAllAccounts: deliberately org-context-free so they span all tenants.

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

// ── Staff identity mutations ────────────────────────────────────────────────

// CreateStaffUser inserts a staff_users row + its role grants in one tx.
// Returns storage.ErrStaffEmailExists on a case-insensitive email collision.
func (s *Store) CreateStaffUser(ctx context.Context, in storage.CreateStaffUserInput) (model.StaffUser, error) {
	if in.Email == "" {
		return model.StaffUser{}, fmt.Errorf("postgres: create staff user: email required")
	}
	for _, r := range in.Roles {
		if !model.ValidStaffRole(r) {
			return model.StaffUser{}, fmt.Errorf("postgres: create staff user: invalid role %q", r)
		}
	}
	if in.ID == "" {
		in.ID = uuid.New().String()
	}

	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return model.StaffUser{}, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	var out model.StaffUser
	var passwordHash *string
	if in.PasswordHash != "" {
		passwordHash = &in.PasswordHash
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO staff_users (id, email, name, password_hash, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'active', $5, $5)
		RETURNING id, email, name, COALESCE(password_hash, ''), status, created_at, updated_at, last_seen`,
		in.ID, in.Email, in.Name, passwordHash, now,
	).Scan(
		&out.ID, &out.Email, &out.Name, &out.PasswordHash, &out.Status,
		&out.CreatedAt, &out.UpdatedAt, &out.LastSeen,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return model.StaffUser{}, storage.ErrStaffEmailExists
		}
		return model.StaffUser{}, fmt.Errorf("postgres: insert staff user: %w", err)
	}

	for _, r := range in.Roles {
		var grantedBy *string
		if in.GrantedBy != "" {
			grantedBy = &in.GrantedBy
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO staff_role_grants (staff_user_id, role, granted_by)
			VALUES ($1, $2, $3)
			ON CONFLICT (staff_user_id, role) DO NOTHING`,
			out.ID, string(r), grantedBy,
		); err != nil {
			return model.StaffUser{}, fmt.Errorf("postgres: grant staff role: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return model.StaffUser{}, fmt.Errorf("postgres: commit staff user: %w", err)
	}
	return out, nil
}

// staffUserSelectSQL is the column list shared by the lookup-by-email and
// lookup-by-id paths (both need the password hash for auth/resolution).
const staffUserSelectSQL = `
	SELECT id, email, name, COALESCE(password_hash, ''), status, created_at, updated_at, last_seen
	FROM staff_users`

func scanStaffUser(row pgx.Row) (model.StaffUser, error) {
	var u model.StaffUser
	err := row.Scan(
		&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Status,
		&u.CreatedAt, &u.UpdatedAt, &u.LastSeen,
	)
	return u, err
}

// loadStaffRoleGrants returns the grants for one staff user, oldest-first.
func (s *Store) loadStaffRoleGrants(ctx context.Context, q pgxQuerier, staffUserID string) ([]model.StaffRoleGrant, error) {
	rows, err := q.Query(ctx, `
		SELECT id, staff_user_id, role, COALESCE(granted_by, ''), created_at
		FROM staff_role_grants
		WHERE staff_user_id = $1
		ORDER BY created_at ASC, id ASC`,
		staffUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: query staff role grants: %w", err)
	}
	defer rows.Close()

	grants := make([]model.StaffRoleGrant, 0, 4)
	for rows.Next() {
		var g model.StaffRoleGrant
		var role string
		if err := rows.Scan(&g.ID, &g.StaffUserID, &role, &g.GrantedBy, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan staff role grant: %w", err)
		}
		g.Role = model.StaffRole(role)
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// pgxQuerier is the read subset shared by *pgxpool.Pool and pgx.Tx, letting
// loadStaffRoleGrants run on either.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// LookupStaffUserByEmail resolves the staff user (with password hash) + roles
// by case-insensitive email. Returns storage.ErrStaffNotFound when absent.
func (s *Store) LookupStaffUserByEmail(ctx context.Context, email string) (model.StaffUser, []model.StaffRoleGrant, error) {
	if email == "" {
		return model.StaffUser{}, nil, storage.ErrStaffNotFound
	}
	u, err := scanStaffUser(s.adminPool.QueryRow(ctx, staffUserSelectSQL+` WHERE lower(email) = lower($1)`, email))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.StaffUser{}, nil, storage.ErrStaffNotFound
	}
	if err != nil {
		return model.StaffUser{}, nil, fmt.Errorf("postgres: lookup staff user by email: %w", err)
	}
	grants, err := s.loadStaffRoleGrants(ctx, s.adminPool, u.ID)
	if err != nil {
		return model.StaffUser{}, nil, err
	}
	return u, grants, nil
}

// GetStaffUserByID resolves a staff principal + roles by id (session path).
func (s *Store) GetStaffUserByID(ctx context.Context, id string) (model.StaffUser, []model.StaffRoleGrant, error) {
	if id == "" {
		return model.StaffUser{}, nil, storage.ErrStaffNotFound
	}
	u, err := scanStaffUser(s.adminPool.QueryRow(ctx, staffUserSelectSQL+` WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.StaffUser{}, nil, storage.ErrStaffNotFound
	}
	if err != nil {
		return model.StaffUser{}, nil, fmt.Errorf("postgres: get staff user by id: %w", err)
	}
	grants, err := s.loadStaffRoleGrants(ctx, s.adminPool, u.ID)
	if err != nil {
		return model.StaffUser{}, nil, err
	}
	return u, grants, nil
}

// ListStaffUsers returns every staff user (no password hash) for the
// superadmin console, each paired with its grants (index-aligned slices).
func (s *Store) ListStaffUsers(ctx context.Context) ([]model.StaffUser, [][]model.StaffRoleGrant, error) {
	rows, err := s.adminPool.Query(ctx, `
		SELECT id, email, name, status, created_at, updated_at, last_seen
		FROM staff_users
		ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: list staff users: %w", err)
	}
	defer rows.Close()

	var users []model.StaffUser
	for rows.Next() {
		var u model.StaffUser
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.LastSeen); err != nil {
			return nil, nil, fmt.Errorf("postgres: scan staff user: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("postgres: list staff users rows: %w", err)
	}

	grantsByUser := make([][]model.StaffRoleGrant, len(users))
	for i, u := range users {
		g, err := s.loadStaffRoleGrants(ctx, s.adminPool, u.ID)
		if err != nil {
			return nil, nil, err
		}
		grantsByUser[i] = g
	}
	return users, grantsByUser, nil
}

// GrantStaffRole adds a (staff, role) grant idempotently.
func (s *Store) GrantStaffRole(ctx context.Context, staffUserID string, role model.StaffRole, grantedBy string) error {
	if !model.ValidStaffRole(role) {
		return fmt.Errorf("postgres: grant staff role: invalid role %q", role)
	}
	var grantedByArg *string
	if grantedBy != "" {
		grantedByArg = &grantedBy
	}
	tag, err := s.adminPool.Exec(ctx, `
		INSERT INTO staff_role_grants (staff_user_id, role, granted_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (staff_user_id, role) DO NOTHING`,
		staffUserID, string(role), grantedByArg,
	)
	if err != nil {
		// 23503 = foreign_key_violation: the staff_user_id doesn't exist.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return storage.ErrStaffNotFound
		}
		return fmt.Errorf("postgres: grant staff role: %w", err)
	}
	_ = tag
	return nil
}

// RevokeStaffRole removes a (staff, role) grant. No error when absent. For the
// superadmin role it enforces the last-superadmin guard atomically: it locks
// all superadmin grant rows FOR UPDATE so concurrent revokes serialise, then
// refuses (ErrLastStaffSuperadmin) when the target holds the only superadmin
// grant. Non-superadmin revokes take the fast single-statement path.
func (s *Store) RevokeStaffRole(ctx context.Context, staffUserID string, role model.StaffRole) error {
	if role != model.StaffRoleSuperadmin {
		if _, err := s.adminPool.Exec(ctx, `
			DELETE FROM staff_role_grants WHERE staff_user_id = $1 AND role = $2`,
			staffUserID, string(role),
		); err != nil {
			return fmt.Errorf("postgres: revoke staff role: %w", err)
		}
		return nil
	}

	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock every superadmin grant row; counting them under the lock makes the
	// guard race-free (a concurrent revoke blocks here until we commit).
	rows, err := tx.Query(ctx, `
		SELECT staff_user_id FROM staff_role_grants
		WHERE role = 'superadmin' FOR UPDATE`)
	if err != nil {
		return fmt.Errorf("postgres: lock superadmin grants: %w", err)
	}
	total := 0
	targetHolds := false
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("postgres: scan superadmin grant: %w", err)
		}
		total++
		if id == staffUserID {
			targetHolds = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: iterate superadmin grants: %w", err)
	}

	if !targetHolds {
		return tx.Commit(ctx) // nothing to revoke — no-op, no guard trip
	}
	if total <= 1 {
		return storage.ErrLastStaffSuperadmin
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM staff_role_grants WHERE staff_user_id = $1 AND role = 'superadmin'`,
		staffUserID,
	); err != nil {
		return fmt.Errorf("postgres: revoke superadmin: %w", err)
	}
	return tx.Commit(ctx)
}

// CountStaffWithRole returns how many staff hold a given role (last-superadmin
// guard).
func (s *Store) CountStaffWithRole(ctx context.Context, role model.StaffRole) (int, error) {
	var n int
	err := s.adminPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM staff_role_grants WHERE role = $1`, string(role)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("postgres: count staff with role: %w", err)
	}
	return n, nil
}

// ── Cross-org reads for the admin console (org metadata only, §7.5) ─────────

// ListAllOrganizations returns every org, oldest-first. Admin pool, no org
// context (mirrors ListAllAccounts) — spans all tenants.
func (s *Store) ListAllOrganizations(ctx context.Context) ([]model.Organization, error) {
	rows, err := s.adminPool.Query(ctx, `
		SELECT id, org_code, name, created_at, onboarding_completed_at
		FROM organizations
		ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list all organizations: %w", err)
	}
	defer rows.Close()

	var orgs []model.Organization
	for rows.Next() {
		var o model.Organization
		if err := rows.Scan(&o.ID, &o.OrgCode, &o.Name, &o.CreatedAt, &o.OnboardingCompletedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan organization: %w", err)
		}
		orgs = append(orgs, o)
	}
	return orgs, rows.Err()
}

// StaffTenantSummary returns one org's non-FinOps summary: metadata + account
// count + latest-snapshot aggregates. Returns storage.ErrOrganizationNotFound
// when the org id is unknown. The latest-snapshot fields are zero/nil when the
// org has never been scanned.
func (s *Store) StaffTenantSummary(ctx context.Context, organizationID string) (model.StaffTenantSummary, error) {
	if organizationID == "" {
		return model.StaffTenantSummary{}, storage.ErrOrganizationNotFound
	}

	var sum model.StaffTenantSummary
	err := s.adminPool.QueryRow(ctx, `
		SELECT id, org_code, name, created_at, onboarding_completed_at
		FROM organizations WHERE id = $1`,
		organizationID,
	).Scan(&sum.OrganizationID, &sum.OrgCode, &sum.Name, &sum.CreatedAt, &sum.OnboardingCompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.StaffTenantSummary{}, storage.ErrOrganizationNotFound
	}
	if err != nil {
		return model.StaffTenantSummary{}, fmt.Errorf("postgres: staff tenant summary org: %w", err)
	}

	if err := s.adminPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM accounts WHERE organization_id = $1`,
		organizationID,
	).Scan(&sum.AccountCount); err != nil {
		return model.StaffTenantSummary{}, fmt.Errorf("postgres: staff tenant summary accounts: %w", err)
	}

	// Latest snapshot across all accounts in the org. nil when never scanned.
	var lastScan *time.Time
	var zombieCount *int
	var monthlyCost *float64
	err = s.adminPool.QueryRow(ctx, `
		SELECT snapshot_at, zombie_count, total_monthly_cost
		FROM zombie_snapshots
		WHERE organization_id = $1
		ORDER BY snapshot_at DESC, id DESC
		LIMIT 1`,
		organizationID,
	).Scan(&lastScan, &zombieCount, &monthlyCost)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return model.StaffTenantSummary{}, fmt.Errorf("postgres: staff tenant summary snapshot: %w", err)
	}
	sum.LastScanAt = lastScan
	if zombieCount != nil {
		sum.LatestTotalZombies = *zombieCount
	}
	if monthlyCost != nil {
		sum.LatestPotentialSavings = *monthlyCost
	}
	return sum, nil
}
