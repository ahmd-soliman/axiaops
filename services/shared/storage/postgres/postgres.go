// Package postgres implements the Store interface using PostgreSQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// Store is a PostgreSQL-backed implementation of storage.Store.
type Store struct {
	pool      *pgxpool.Pool
	adminPool *pgxpool.Pool // RLS-bypass connection (axiaops_runtime via per-table policies in prod; falls back to the app pool in dev/tests). Cross-org / pre-auth reads — see docs/AUTHENTICATION.md (§5).
}

// New connects to PostgreSQL as the application user (axiaops).
// Schema and tables are created by postgres.Migrate (versioned migrations) — not here.
// search_path is set to axiaops on every connection via AfterConnect.
// URL format: postgres://axiaops:axiaops@host:5432/dbname
func New(ctx context.Context, url string) (*Store, error) {
	return NewWithOwner(ctx, url, "")
}

// NewWithOwner opens both the app pool (url) and a separate owner pool (ownerURL).
// ListAllAccounts uses the owner pool to bypass RLS without granting BYPASSRLS to the app user.
// If ownerURL is empty or equal to url, ownerPool falls back to pool (tests/simple setups).
func NewWithOwner(ctx context.Context, url, ownerURL string) (*Store, error) {
	pool, err := newPool(ctx, url)
	if err != nil {
		return nil, err
	}
	adminPool := pool
	if ownerURL != "" && ownerURL != url {
		adminPool, err = newPool(ctx, ownerURL)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("postgres: owner pool: %w", err)
		}
	}
	return &Store{pool: pool, adminPool: adminPool}, nil
}

// NewWithRuntimeAdmin opens the app pool (appURL) plus a least-privilege
// RLS-bypass pool (runtimeAdminURL → the axiaops_runtime role; see
// docs/AUTHENTICATION.md (§5)). This is the production seam (resolves
// TODO(#107)): the runtime no longer needs the schema-owner connection, only a
// role that bypasses RLS via per-table permissive policies — no DDL, no
// ownership. If runtimeAdminURL is empty or equal to appURL the bypass pool
// falls back to the app pool (DEV_MODE single-pool / tests).
func NewWithRuntimeAdmin(ctx context.Context, appURL, runtimeAdminURL string) (*Store, error) {
	s, err := NewWithOwner(ctx, appURL, runtimeAdminURL)
	if err != nil {
		return nil, err
	}
	// Readiness assertion (TODO(#107)): when a distinct bypass pool is
	// configured, force a real connection so a bad URL / unreachable role /
	// wrong credentials fails startup loudly rather than on the first cross-org
	// read. The cross-org bypass behaviour itself is pinned by
	// runtime_admin_test.go.
	if s.adminPool != s.pool {
		if err := s.adminPool.Ping(ctx); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("postgres: runtime-admin pool: %w", err)
		}
	}
	return s, nil
}

func newPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET search_path TO axiaops")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, nil
}

// setOrganization sets the app.organization_id session variable for Row-Level Security.
// Must be called inside a transaction so the setting is scoped to that tx.
func setOrganization(ctx context.Context, tx pgx.Tx) error {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return fmt.Errorf("postgres: organization_id missing from context")
	}
	_, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true)`, organizationID)
	return err
}

// Save upserts cost records in a single transaction. Rows whose conflict key
// (organization_id, provider, account_id, service, region, resource_id,
// period_start, period_end) already exists have their amount, currency, tags,
// fetched_at, and internal_account_id refreshed from the incoming payload —
// this is how AWS Cost Explorer's late-settled NetAmortizedCost for day-1 of a
// billing period reaches the database under the rolling 30-day re-fetch
// window.
//
// The internal_account_id column uses COALESCE so a re-fetch that omits the
// field never clobbers a populated legacy value (the column was added in
// migration 010 without NOT NULL).
//
// Returns the count of rows that were fresh inserts and the count that were
// updates, discriminated via the PostgreSQL upsert idiom RETURNING (xmax = 0):
// xmax is 0 for a brand-new row and non-zero for a row touched by the current
// transaction's update path.
func (s *Store) Save(ctx context.Context, records []model.CostRecord) (inserted, updated int64, err error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return 0, 0, fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return 0, 0, err
	}

	for _, r := range records {
		tags, err := json.Marshal(r.Tags)
		if err != nil {
			return 0, 0, fmt.Errorf("postgres: marshal tags: %w", err)
		}
		var wasInsert bool
		err = tx.QueryRow(ctx, `
			INSERT INTO cost_records
				(organization_id, provider, account_id, internal_account_id, service, region, resource_id, amount, currency,
				 period_start, period_end, tags, fetched_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (organization_id, provider, account_id, service, region, resource_id, period_start, period_end)
			DO UPDATE SET
				amount              = EXCLUDED.amount,
				currency            = EXCLUDED.currency,
				tags                = EXCLUDED.tags,
				fetched_at          = EXCLUDED.fetched_at,
				internal_account_id = COALESCE(EXCLUDED.internal_account_id, cost_records.internal_account_id)
			RETURNING (xmax = 0)`,
			organizationID,
			r.Provider, r.AccountID, r.InternalAccountID, r.Service, r.Region, r.ResourceID,
			r.Amount, r.Currency,
			r.PeriodStart, r.PeriodEnd,
			string(tags), r.FetchedAt,
		).Scan(&wasInsert)
		if err != nil {
			return 0, 0, fmt.Errorf("postgres: upsert cost record: %w", err)
		}
		if wasInsert {
			inserted++
		} else {
			updated++
		}
	}

	return inserted, updated, tx.Commit(ctx)
}

// SaveZombies replaces the organization's zombie records for the specified accounts with the latest detection results.
func (s *Store) SaveZombies(ctx context.Context, zombies []model.ZombieResource) error {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return fmt.Errorf("postgres: organization_id missing from context")
	}

	if len(zombies) == 0 {
		return nil // Nothing to save
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	// Get unique internal account IDs from the zombies being saved
	accountIDs := make(map[string]bool)
	for _, z := range zombies {
		if z.InternalAccountID != "" {
			accountIDs[z.InternalAccountID] = true
		}
	}

	// Delete existing zombie records only for the accounts being updated
	for accountID := range accountIDs {
		if _, err := tx.Exec(ctx, `DELETE FROM zombie_records WHERE organization_id = $1 AND internal_account_id = $2`, organizationID, accountID); err != nil {
			return fmt.Errorf("postgres: clear zombies for account %s: %w", accountID, err)
		}
	}

	now := time.Now().UTC()
	for _, z := range zombies {
		tags, err := json.Marshal(z.Tags)
		if err != nil {
			return fmt.Errorf("postgres: marshal tags: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO zombie_records
				(organization_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, arn, tags,
				 monthly_cost, currency, period_start, period_end,
				 usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
			organizationID,
			z.Provider, z.AccountID, z.InternalAccountID, z.Service, z.ResourceType, z.Region, z.ResourceID, z.ARN, string(tags),
			z.MonthlyCost, z.Currency, z.PeriodStart, z.PeriodEnd,
			z.UsageMetric, z.UsageAvg, z.UsageUnit, z.Reason, z.Owner, now,
		)
		if err != nil {
			return fmt.Errorf("postgres: insert zombie: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// LoadZombies returns zombie records for the organization in ctx.
func (s *Store) LoadZombies(ctx context.Context) ([]model.ZombieResource, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return nil, fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT provider, account_id, internal_account_id, service, resource_type, region, resource_id, arn, tags,
		       monthly_cost, currency, period_start, period_end,
		       usage_metric, usage_avg, usage_unit, reason, owner
		FROM zombie_records
	`)
	if err != nil {
		return nil, fmt.Errorf("postgres: query zombies: %w", err)
	}
	defer rows.Close()

	var zombies []model.ZombieResource
	for rows.Next() {
		var z model.ZombieResource
		var tagsJSON []byte
		var internalAccountID *string
		if err := rows.Scan(
			&z.Provider, &z.AccountID, &internalAccountID, &z.Service, &z.ResourceType, &z.Region, &z.ResourceID, &z.ARN, &tagsJSON,
			&z.MonthlyCost, &z.Currency, &z.PeriodStart, &z.PeriodEnd,
			&z.UsageMetric, &z.UsageAvg, &z.UsageUnit, &z.Reason, &z.Owner,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan zombie: %w", err)
		}
		if internalAccountID != nil {
			z.InternalAccountID = *internalAccountID
		}
		if err := json.Unmarshal(tagsJSON, &z.Tags); err != nil {
			z.Tags = map[string]string{}
		}
		zombies = append(zombies, z)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return zombies, tx.Commit(ctx)
}

// UpsertOrganization creates an organization on first login or returns the
// existing one.
//
// The on-conflict clause is a no-op (`SET org_code = EXCLUDED.org_code`) —
// once a row exists, AxiaOps owns the `name` field and renames go through
// PATCH /v1/organizations/me. Without this, an upsert called with an empty
// or stale name argument would clobber the local value.
func (s *Store) UpsertOrganization(ctx context.Context, orgCode, name string) (model.Organization, error) {
	now := time.Now().UTC()
	id := uuid.New().String()

	_, err := s.pool.Exec(ctx, `
		INSERT INTO organizations (id, org_code, name, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_code) DO UPDATE SET org_code = EXCLUDED.org_code`,
		id, orgCode, name, now,
	)
	if err != nil {
		return model.Organization{}, fmt.Errorf("postgres: upsert organization: %w", err)
	}

	var t model.Organization
	err = s.pool.QueryRow(ctx,
		`SELECT id, org_code, name, created_at, onboarding_completed_at FROM organizations WHERE org_code = $1`, orgCode,
	).Scan(&t.ID, &t.OrgCode, &t.Name, &t.CreatedAt, &t.OnboardingCompletedAt)
	if err != nil {
		return model.Organization{}, fmt.Errorf("postgres: fetch organization: %w", err)
	}

	return t, nil
}

// GetOrganizationByID returns the organization with the given UUID. Bypasses
// RLS via adminPool — /v1/me is the primary caller and runs before the
// per-request RLS context exists in handler scope.
func (s *Store) GetOrganizationByID(ctx context.Context, id string) (model.Organization, error) {
	if id == "" {
		return model.Organization{}, storage.ErrOrganizationNotFound
	}
	var t model.Organization
	err := s.adminPool.QueryRow(ctx,
		`SELECT id, org_code, name, created_at, onboarding_completed_at FROM organizations WHERE id = $1`, id,
	).Scan(&t.ID, &t.OrgCode, &t.Name, &t.CreatedAt, &t.OnboardingCompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Organization{}, storage.ErrOrganizationNotFound
	}
	if err != nil {
		return model.Organization{}, fmt.Errorf("postgres: get organization by id: %w", err)
	}
	return t, nil
}

// RenameOrganization updates the organization name for the org in ctx.
// PATCH /v1/organizations/me is the only caller.
func (s *Store) RenameOrganization(ctx context.Context, name string) error {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return fmt.Errorf("postgres: rename organization: organization_id missing from context")
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE organizations SET name = $1 WHERE id = $2`, name, organizationID,
	)
	if err != nil {
		return fmt.Errorf("postgres: rename organization: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrOrganizationNotFound
	}
	return nil
}

// MarkOnboardingComplete sets onboarding_completed_at = NOW() if NULL.
// Idempotent — returns the existing timestamp when already set.
func (s *Store) MarkOnboardingComplete(ctx context.Context) (time.Time, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return time.Time{}, fmt.Errorf("postgres: mark onboarding complete: organization_id missing from context")
	}
	var completed time.Time
	err := s.pool.QueryRow(ctx, `
		UPDATE organizations
		SET onboarding_completed_at = COALESCE(onboarding_completed_at, NOW())
		WHERE id = $1
		RETURNING onboarding_completed_at`,
		organizationID,
	).Scan(&completed)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, storage.ErrOrganizationNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("postgres: mark onboarding complete: %w", err)
	}
	return completed, nil
}

// EnsureOrganization inserts an organization with a caller-supplied id if no row with that
// id exists yet. Unlike UpsertOrganization, the id is pinned and the row is never
// modified on conflict. Used by dev mode to guarantee a known-id organization row
// so that FK references from accounts/zombies/etc. resolve without requiring
// a prior write path to have auto-created the row.
//
// Dev orgs are inserted with onboarding_completed_at = NOW() so the wizard is
// skipped by default. To exercise the wizard locally, set the column back to
// NULL via psql (or `make seed-fresh` if added).
func (s *Store) EnsureOrganization(ctx context.Context, id, orgCode, name string) error {
	now := time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO organizations (id, org_code, name, created_at, onboarding_completed_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (id) DO NOTHING`,
		id, orgCode, name, now,
	)
	if err != nil {
		return fmt.Errorf("postgres: ensure organization: %w", err)
	}
	return nil
}

// EnsureUser inserts a user with a caller-supplied id, or updates organization_id,
// email, name, and last_seen if the row already exists. The id is pinned (not
// generated). Used by dev mode at startup to guarantee a known-id user row so
// DevBypass can inject user_id onto the request context.
//
// A synthetic external_id of the form "dev:<id>" is used so the UNIQUE
// constraint on external_id stays usable for non-SSO users (real SSO users
// get the IdP-issued `sub` claim).
//
// Conflict handling is DO UPDATE (not DO NOTHING) so that rotating DEV_ORGANIZATION_ID
// or DEV_USER_EMAIL across runs self-corrects the existing row — otherwise the
// stored organization_id would silently diverge from the organization id DevBypass injects
// onto every request.
//
// Runs on the runtime-bypass pool inside a transaction that also sets
// app.organization_id from u.OrganizationID (migration 035 put RLS on users).
// Two configs to satisfy: (a) deployed/start-dev where adminPool is a bypass
// role (axiaops_runtime / owner) — the users_runtime_bypass policy permits the
// write and lets the ON CONFLICT DO UPDATE self-correct a rotated
// DEV_ORGANIZATION_ID across orgs; (b) the degenerate single-pool DEV_MODE
// (RUNTIME_ADMIN_DATABASE_URL unset → adminPool falls back to the RLS-bound app
// role) where the GUC is what satisfies the WITH CHECK on a first insert. The
// cross-org self-correct only works in config (a); single-pool DEV_MODE cannot
// rotate an existing user across orgs without a DB reset, which is acceptable
// for that degenerate path.
func (s *Store) EnsureUser(ctx context.Context, u model.User) error {
	now := time.Now().UTC()
	externalID := "dev:" + u.ID
	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: ensure user begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// EnsureUser runs before any request context exists, so it sets the GUC from
	// the model rather than via ctx (setOrganization reads ctx).
	if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true)`, u.OrganizationID); err != nil {
		return fmt.Errorf("postgres: ensure user set org: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO users (id, organization_id, external_id, email, name, created_at, last_seen)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		ON CONFLICT (id) DO UPDATE SET
			organization_id = EXCLUDED.organization_id,
			email     = EXCLUDED.email,
			name      = EXCLUDED.name,
			last_seen = EXCLUDED.last_seen`,
		u.ID, u.OrganizationID, externalID, u.Email, u.Name, now,
	)
	if err != nil {
		return fmt.Errorf("postgres: ensure user: %w", err)
	}
	return tx.Commit(ctx)
}

// UpsertUser creates a user on first login or updates email, name, and last_seen.
//
// Runs on the runtime-bypass pool (adminPool): this is the SSO-callback first-
// login write, which runs before the request has a DB org context set and keys
// the row by external_id (the IdP `sub`), not by the request org. Under the
// users RLS policy (migration 035) an app-pool INSERT with no GUC set would be
// rejected by the WITH CHECK clause; the runtime role's users_runtime_bypass
// policy lets this through. DEV_MODE never reaches here (auth is bypassed).
func (s *Store) UpsertUser(ctx context.Context, organizationID, externalID, email, name string) (model.User, error) {
	now := time.Now().UTC()
	id := uuid.New().String()

	_, err := s.adminPool.Exec(ctx, `
		INSERT INTO users (id, organization_id, external_id, email, name, created_at, last_seen)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (external_id) DO UPDATE SET
			email     = EXCLUDED.email,
			name      = EXCLUDED.name,
			last_seen = EXCLUDED.last_seen`,
		id, organizationID, externalID, email, name, now, now,
	)
	if err != nil {
		return model.User{}, fmt.Errorf("postgres: upsert user: %w", err)
	}

	var u model.User
	err = s.adminPool.QueryRow(ctx,
		`SELECT id, organization_id, external_id, email, name, created_at, last_seen FROM users WHERE external_id = $1`, externalID,
	).Scan(&u.ID, &u.OrganizationID, &u.ExternalID, &u.Email, &u.Name, &u.CreatedAt, &u.LastSeen)
	if err != nil {
		return model.User{}, fmt.Errorf("postgres: fetch user: %w", err)
	}
	return u, nil
}

// SaveAccount inserts or replaces a connected cloud account.
func (s *Store) SaveAccount(ctx context.Context, a model.Account) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	// NULLIF($, '') keeps nullable columns honest: role-based accounts persist
	// access_key_id/secret_encrypted as NULL (not ''), and access-key accounts
	// persist role_arn/external_id as NULL. The CHECK constraints in
	// migration 019_account_role_auth rely on IS NOT NULL semantics, which
	// empty strings would silently subvert.
	_, err = tx.Exec(ctx, `
		INSERT INTO accounts
			(id, organization_id, provider, label, account_id,
			 auth_method, access_key_id, secret_encrypted, role_arn, external_id,
			 region, status, scan_interval_hours, error_message, created_at)
		VALUES ($1, $2, $3, $4, $5,
			COALESCE(NULLIF($6,''),'access_key'), NULLIF($7,''), NULLIF($8,''), NULLIF($9,''), NULLIF($10,''),
			$11, $12, $13, NULLIF($14,''), $15)
		ON CONFLICT (id) DO UPDATE SET
			label               = EXCLUDED.label,
			account_id          = EXCLUDED.account_id,
			auth_method         = EXCLUDED.auth_method,
			access_key_id       = EXCLUDED.access_key_id,
			secret_encrypted    = EXCLUDED.secret_encrypted,
			role_arn            = EXCLUDED.role_arn,
			external_id         = EXCLUDED.external_id,
			region              = EXCLUDED.region,
			status              = EXCLUDED.status,
			scan_interval_hours = EXCLUDED.scan_interval_hours,
			error_message       = EXCLUDED.error_message`,
		a.ID, a.OrganizationID, a.Provider, a.Label, a.AccountID,
		a.AuthMethod, a.AccessKeyID, a.SecretEncrypted, a.RoleARN, a.ExternalID,
		a.Region, a.Status, a.ScanIntervalHours, a.ErrorMessage, a.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save account: %w", err)
	}
	return tx.Commit(ctx)
}

// ListAccounts returns all accounts for the organization in ctx.
func (s *Store) ListAccounts(ctx context.Context) ([]model.Account, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, accountSelectSQL+` ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []model.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accounts, tx.Commit(ctx)
}

// ListAllAccounts returns accounts for ALL organizations, bypassing row-level security.
// Used internally by the scheduled scan scheduler. Does not respect organization_id in context.
func (s *Store) ListAllAccounts(ctx context.Context) ([]model.Account, error) {
	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// NOTE: Deliberately NOT calling setOrganization(ctx, tx) here.
	// This allows the query to return accounts from all organizations.
	// Only use this method for trusted internal operations (scheduler, background jobs).

	rows, err := tx.Query(ctx, accountSelectSQL+` ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []model.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accounts, tx.Commit(ctx)
}

// GetAccount returns a single account by ID for the organization in ctx.
func (s *Store) GetAccount(ctx context.Context, id string) (model.Account, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Account{}, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return model.Account{}, err
	}

	row := tx.QueryRow(ctx, accountSelectSQL+` WHERE id = $1`, id)
	a, err := scanAccount(row)
	if err != nil {
		return model.Account{}, err
	}
	return a, tx.Commit(ctx)
}

// accountSelectSQL is the column list shared by GetAccount, ListAccounts, and
// ListAllAccounts. COALESCE turns nullable columns (access_key_id,
// secret_encrypted, role_arn, external_id, error_message) into empty strings
// so callers can keep using plain string fields on model.Account.
const accountSelectSQL = `
	SELECT id, organization_id, provider, label, account_id,
	       auth_method,
	       COALESCE(access_key_id, '')    AS access_key_id,
	       COALESCE(secret_encrypted, '') AS secret_encrypted,
	       COALESCE(role_arn, '')         AS role_arn,
	       COALESCE(external_id, '')      AS external_id,
	       region, status, last_scanned_at, scan_interval_hours,
	       COALESCE(error_message, '')    AS error_message,
	       created_at
	FROM accounts`

// rowScanner is the subset of pgx.Row / pgx.Rows used by scanAccount.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(r rowScanner) (model.Account, error) {
	var a model.Account
	err := r.Scan(
		&a.ID, &a.OrganizationID, &a.Provider, &a.Label, &a.AccountID,
		&a.AuthMethod,
		&a.AccessKeyID, &a.SecretEncrypted,
		&a.RoleARN, &a.ExternalID,
		&a.Region, &a.Status, &a.LastScannedAt, &a.ScanIntervalHours,
		&a.ErrorMessage,
		&a.CreatedAt,
	)
	if err != nil {
		return model.Account{}, err
	}
	return a, nil
}

// DeleteAccount removes an account by ID for the organization in ctx.
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete account: %w", err)
	}
	return tx.Commit(ctx)
}

// SetAccountError sets status='error' and writes a human-readable reason
// into error_message in one transaction. NULLIF turns empty strings into NULL
// to match the column's nullable contract.
func (s *Store) SetAccountError(ctx context.Context, id, message string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE accounts
		   SET status = 'error',
		       error_message = NULLIF($1, ''),
		       last_scanned_at = NOW()
		 WHERE id = $2`, message, id)
	if err != nil {
		return fmt.Errorf("postgres: set account error: %w", err)
	}
	return tx.Commit(ctx)
}

// UpdateAccountStatus sets status and last_scanned_at for an account. When
// the new status is anything other than 'error', error_message is cleared so
// stale failure reasons do not linger after a recovery.
func (s *Store) UpdateAccountStatus(ctx context.Context, id, status string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE accounts
		   SET status = $1,
		       error_message = CASE WHEN $1 = 'error' THEN error_message ELSE NULL END,
		       last_scanned_at = NOW()
		 WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("postgres: update account status: %w", err)
	}
	return tx.Commit(ctx)
}

// TryMarkAccountScanning sets status to scanning only if the account is not already scanning.
func (s *Store) TryMarkAccountScanning(ctx context.Context, id string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return false, err
	}

	tag, err := tx.Exec(ctx, `
		UPDATE accounts SET status = 'scanning', last_scanned_at = NOW()
		WHERE id = $1 AND status <> 'scanning'`, id)
	if err != nil {
		return false, fmt.Errorf("postgres: try mark account scanning: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres: commit: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SaveResources replaces resource records for the accounts present in the
// provided slice, leaving all other accounts' records untouched.
func (s *Store) SaveResources(ctx context.Context, resources []model.ResourceRecord) error {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	// Collect the unique internal account IDs present in this batch and delete only
	// their existing records, so other accounts' data is not affected.
	accountIDs := make(map[string]bool)
	for _, r := range resources {
		if r.InternalAccountID != "" {
			accountIDs[r.InternalAccountID] = true
		}
	}
	for accountID := range accountIDs {
		if _, err := tx.Exec(ctx, `DELETE FROM resource_records WHERE internal_account_id = $1`, accountID); err != nil {
			return fmt.Errorf("postgres: clear resource_records for account %s: %w", accountID, err)
		}
	}

	now := time.Now().UTC()
	for _, r := range resources {
		tags, err := json.Marshal(r.Tags)
		if err != nil {
			return fmt.Errorf("postgres: marshal tags: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO resource_records
				(organization_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, arn, tags,
				 monthly_cost, currency, period_start, period_end,
				 usage_metric, usage_avg, usage_unit, is_zombie, reason, owner, detected_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
			organizationID,
			r.Provider, r.AccountID, r.InternalAccountID, r.Service, r.ResourceType, r.Region, r.ResourceID, r.ARN, string(tags),
			r.MonthlyCost, r.Currency, r.PeriodStart, r.PeriodEnd,
			r.UsageMetric, r.UsageAvg, r.UsageUnit, r.IsZombie, r.Reason, r.Owner, now,
		)
		if err != nil {
			return fmt.Errorf("postgres: insert resource_record: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// LoadResources returns all resource records for the organization in ctx.
func (s *Store) LoadResources(ctx context.Context) ([]model.ResourceRecord, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return nil, fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT provider, account_id, internal_account_id, service, resource_type, region, resource_id, arn, tags,
		       monthly_cost, currency, period_start, period_end,
		       usage_metric, usage_avg, usage_unit, is_zombie, reason, owner
		FROM resource_records
	`)
	if err != nil {
		return nil, fmt.Errorf("postgres: query resource_records: %w", err)
	}
	defer rows.Close()

	var resources []model.ResourceRecord
	for rows.Next() {
		var r model.ResourceRecord
		var tagsJSON []byte
		var internalAccountID *string
		if err := rows.Scan(
			&r.Provider, &r.AccountID, &internalAccountID, &r.Service, &r.ResourceType, &r.Region, &r.ResourceID, &r.ARN, &tagsJSON,
			&r.MonthlyCost, &r.Currency, &r.PeriodStart, &r.PeriodEnd,
			&r.UsageMetric, &r.UsageAvg, &r.UsageUnit, &r.IsZombie, &r.Reason, &r.Owner,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan resource_record: %w", err)
		}
		if internalAccountID != nil {
			r.InternalAccountID = *internalAccountID
		}
		if err := json.Unmarshal(tagsJSON, &r.Tags); err != nil {
			r.Tags = map[string]string{}
		}
		resources = append(resources, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return resources, tx.Commit(ctx)
}

// ListCostRecords returns cost records for the organization in ctx, filtered by the given criteria.
// Records with amount > 0 are returned, ordered by period_start (newest first) then amount (largest first).
func (s *Store) ListCostRecords(ctx context.Context, filter storage.CostFilter) ([]model.CostRecord, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return nil, fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	query := `SELECT provider, account_id, internal_account_id, service, region, resource_id,
	                 amount, currency, period_start, period_end, tags, fetched_at
	          FROM cost_records
	          WHERE amount > 0`
	var args []any
	argN := 1

	// Absolute calendar window (Since/Until on period_start, both inclusive)
	// takes precedence over the trailing Days window when either bound is set.
	if !filter.Since.IsZero() || !filter.Until.IsZero() {
		if !filter.Since.IsZero() {
			query += fmt.Sprintf(" AND period_start >= $%d", argN)
			args = append(args, filter.Since)
			argN++
		}
		if !filter.Until.IsZero() {
			query += fmt.Sprintf(" AND period_start <= $%d", argN)
			args = append(args, filter.Until)
			argN++
		}
	} else {
		days := filter.Days
		if days <= 0 {
			days = 30
		}
		query += fmt.Sprintf(" AND period_end >= NOW() - make_interval(days => $%d)", argN)
		args = append(args, days)
		argN++
	}

	// Filter by account: match either internal_account_id (new records) or account_id (old records with NULL internal_account_id)
	if filter.InternalAccountID != "" || filter.AWSAccountID != "" {
		if filter.InternalAccountID != "" && filter.AWSAccountID != "" {
			// If both are provided, match either one
			query += fmt.Sprintf(" AND (internal_account_id = $%d OR account_id = $%d)", argN, argN+1)
			args = append(args, filter.InternalAccountID, filter.AWSAccountID)
			argN += 2
		} else if filter.InternalAccountID != "" {
			// Only internal account ID provided
			query += fmt.Sprintf(" AND internal_account_id = $%d", argN)
			args = append(args, filter.InternalAccountID)
			argN++
		} else {
			// Only AWS account ID provided
			query += fmt.Sprintf(" AND account_id = $%d", argN)
			args = append(args, filter.AWSAccountID)
			argN++
		}
	}

	if filter.Service != "" {
		query += fmt.Sprintf(" AND service = $%d", argN)
		args = append(args, filter.Service)
	}

	query += " ORDER BY period_start DESC, amount DESC"

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: query cost_records: %w", err)
	}
	defer rows.Close()

	var records []model.CostRecord
	for rows.Next() {
		var r model.CostRecord
		var tagsJSON []byte
		if err := rows.Scan(
			&r.Provider, &r.AccountID, &r.InternalAccountID, &r.Service, &r.Region, &r.ResourceID,
			&r.Amount, &r.Currency, &r.PeriodStart, &r.PeriodEnd, &tagsJSON, &r.FetchedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan cost_record: %w", err)
		}
		if err := json.Unmarshal(tagsJSON, &r.Tags); err != nil {
			r.Tags = map[string]string{}
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return records, tx.Commit(ctx)
}

// SaveSnapshot writes a zombie snapshot record (one per scan run).
func (s *Store) SaveSnapshot(ctx context.Context, snap model.ZombieSnapshot) error {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO zombie_snapshots
			(id, organization_id, account_id, snapshot_at, zombie_count, total_monthly_cost, currency)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		snap.ID, organizationID, snap.AccountID, snap.SnapshotAt,
		snap.ZombieCount, snap.TotalMonthlyCost, snap.Currency,
	)
	if err != nil {
		return fmt.Errorf("postgres: insert zombie_snapshot: %w", err)
	}
	return tx.Commit(ctx)
}

// ListSnapshots returns zombie snapshots for the organization, oldest-first.
// If accountID is non-empty, only snapshots for that account are returned.
func (s *Store) ListSnapshots(ctx context.Context, accountID string) ([]model.ZombieSnapshot, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return nil, fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	var rows pgx.Rows
	if accountID != "" {
		rows, err = tx.Query(ctx, `
			SELECT id, account_id, snapshot_at, zombie_count, total_monthly_cost, currency
			FROM zombie_snapshots
			WHERE account_id = $1
			ORDER BY snapshot_at ASC`,
			accountID,
		)
	} else {
		rows, err = tx.Query(ctx, `
			SELECT id, account_id, snapshot_at, zombie_count, total_monthly_cost, currency
			FROM zombie_snapshots
			ORDER BY snapshot_at ASC`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: query zombie_snapshots: %w", err)
	}
	defer rows.Close()

	var snaps []model.ZombieSnapshot
	for rows.Next() {
		var snap model.ZombieSnapshot
		if err := rows.Scan(
			&snap.ID, &snap.AccountID, &snap.SnapshotAt,
			&snap.ZombieCount, &snap.TotalMonthlyCost, &snap.Currency,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan zombie_snapshot: %w", err)
		}
		snaps = append(snaps, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return snaps, tx.Commit(ctx)
}

// SaveSnapshotServices writes per-service breakdown rows for a snapshot.
func (s *Store) SaveSnapshotServices(ctx context.Context, services []model.SnapshotService) error {
	if len(services) == 0 {
		return nil
	}

	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	for _, svc := range services {
		_, err := tx.Exec(ctx, `
			INSERT INTO zombie_snapshot_services
				(id, snapshot_id, organization_id, service, resource_type, zombie_count, monthly_cost, currency)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			svc.ID, svc.SnapshotID, organizationID, svc.Service, svc.ResourceType,
			svc.ZombieCount, svc.MonthlyCost, svc.Currency,
		)
		if err != nil {
			return fmt.Errorf("postgres: insert snapshot_service: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// ListSnapshotsByService returns snapshots filtered by service, oldest-first.
// Each snapshot's cost/count reflects only the given service.
// When resourceType is non-empty, only that sub-type is returned; otherwise
// all resource types for the service are aggregated (SUM).
func (s *Store) ListSnapshotsByService(ctx context.Context, service, resourceType, accountID string) ([]model.ZombieSnapshot, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return nil, fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	// Build query dynamically based on filters.
	// When no resource_type is specified we GROUP BY snapshot to aggregate
	// across all resource types for the service.
	query := `
		SELECT gs.id, gs.account_id, gs.snapshot_at,
		       SUM(gss.zombie_count)::int, SUM(gss.monthly_cost), gss.currency
		FROM zombie_snapshots gs
		JOIN zombie_snapshot_services gss ON gss.snapshot_id = gs.id
		WHERE gss.service = $1`

	args := []any{service}
	argN := 2

	if resourceType != "" {
		query += fmt.Sprintf(" AND gss.resource_type = $%d", argN)
		args = append(args, resourceType)
		argN++
	}
	if accountID != "" {
		query += fmt.Sprintf(" AND gs.account_id = $%d", argN)
		args = append(args, accountID)
	}

	query += `
		GROUP BY gs.id, gs.account_id, gs.snapshot_at, gss.currency
		ORDER BY gs.snapshot_at ASC`

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: query snapshots by service: %w", err)
	}
	defer rows.Close()

	var snaps []model.ZombieSnapshot
	for rows.Next() {
		var snap model.ZombieSnapshot
		if err := rows.Scan(
			&snap.ID, &snap.AccountID, &snap.SnapshotAt,
			&snap.ZombieCount, &snap.TotalMonthlyCost, &snap.Currency,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan snapshot_by_service: %w", err)
		}
		snaps = append(snaps, snap)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return snaps, tx.Commit(ctx)
}

// ListTrendServices returns distinct services that have snapshot data for the organization.
func (s *Store) ListTrendServices(ctx context.Context) ([]string, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return nil, fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT DISTINCT service FROM zombie_snapshot_services ORDER BY service`)
	if err != nil {
		return nil, fmt.Errorf("postgres: query trend services: %w", err)
	}
	defer rows.Close()

	var services []string
	for rows.Next() {
		var svc string
		if err := rows.Scan(&svc); err != nil {
			return nil, fmt.Errorf("postgres: scan trend service: %w", err)
		}
		services = append(services, svc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return services, tx.Commit(ctx)
}

// ListTrendResourceTypes returns distinct resource types for a given service
// that have snapshot data for the organization.
func (s *Store) ListTrendResourceTypes(ctx context.Context, service string) ([]string, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return nil, fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT DISTINCT resource_type FROM zombie_snapshot_services
		WHERE service = $1 AND resource_type != ''
		ORDER BY resource_type`, service)
	if err != nil {
		return nil, fmt.Errorf("postgres: query trend resource types: %w", err)
	}
	defer rows.Close()

	var types []string
	for rows.Next() {
		var rt string
		if err := rows.Scan(&rt); err != nil {
			return nil, fmt.Errorf("postgres: scan trend resource type: %w", err)
		}
		types = append(types, rt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return types, tx.Commit(ctx)
}

// DeleteOldCostRecords deletes cost records with period_end before cutoff across all organizations.
// Uses adminPool to bypass RLS — this is a maintenance operation across all organizations.
func (s *Store) DeleteOldCostRecords(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.adminPool.Exec(ctx, `DELETE FROM cost_records WHERE period_end < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres: delete old cost_records: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DismissZombie inserts a new dismiss or snooze record for a zombie resource.
// Returns ErrAlreadyDismissed if an active dismissal already exists for the fingerprint
// (enforced by the partial unique index dismissed_zombies_active_fingerprint).
func (s *Store) DismissZombie(ctx context.Context, d model.DismissAction) (int64, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return 0, fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return 0, err
	}

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO dismissed_zombies
			(organization_id, account_id, provider, service, region, resource_id,
			 action, reason, note, snoozed_until, dismissed_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		RETURNING id`,
		organizationID, d.AccountID, d.Provider, d.Service, d.Region, d.ResourceID,
		d.Action, d.Reason, d.Note, d.SnoozedUntil, d.DismissedBy,
	).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return 0, storage.ErrAlreadyDismissed
		}
		return 0, fmt.Errorf("postgres: insert dismissed_zombie: %w", err)
	}
	return id, tx.Commit(ctx)
}

// RevokeDismissal soft-deletes an active dismissal by setting revoked_at / revoked_by.
func (s *Store) RevokeDismissal(ctx context.Context, id int64, revokedBy string) error {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `
		UPDATE dismissed_zombies
		SET    revoked_at = NOW(), revoked_by = $1
		WHERE  id = $2 AND revoked_at IS NULL`,
		revokedBy, id,
	)
	if err != nil {
		return fmt.Errorf("postgres: revoke dismissal: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: dismissal %d not found or already revoked", id)
	}
	return tx.Commit(ctx)
}

// ListActiveDismissals returns all active (non-revoked, non-expired) dismissals
// for the organization in ctx.  If accountID is non-empty, filters to that account only.
func (s *Store) ListActiveDismissals(ctx context.Context, accountID string) ([]model.DismissAction, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return nil, fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	// LEFT JOIN zombie_records on the dismissal fingerprint to surface the
	// last-known monthly_cost / currency. SaveZombies replaces zombie_records
	// wholesale per (organization_id, internal_account_id), so at most one
	// row matches each dismissal — no aggregation needed. Orphaned dismissals
	// (resource gone from the latest scan) get NULL → nil pointer in Go →
	// omitted from the JSON response.
	const selectCols = `dz.id, dz.account_id, dz.provider, dz.service, dz.region, dz.resource_id,
		       dz.action, dz.reason, dz.note, dz.snoozed_until, dz.dismissed_by, dz.created_at,
		       dz.revoked_at, dz.revoked_by,
		       zr.monthly_cost, zr.currency`
	const joinClause = `FROM   dismissed_zombies dz
		LEFT JOIN zombie_records zr
		       ON zr.organization_id     = dz.organization_id
		      AND zr.internal_account_id = dz.account_id
		      AND zr.provider            = dz.provider
		      AND zr.service             = dz.service
		      AND zr.region              = dz.region
		      AND zr.resource_id         = dz.resource_id`

	var rows pgx.Rows
	if accountID != "" {
		rows, err = tx.Query(ctx, `
			SELECT `+selectCols+`
			`+joinClause+`
			WHERE  dz.revoked_at IS NULL
			  AND  (dz.action = 'dismiss' OR dz.snoozed_until > NOW())
			  AND  dz.account_id = $1
			ORDER BY dz.created_at DESC`,
			accountID,
		)
	} else {
		rows, err = tx.Query(ctx, `
			SELECT `+selectCols+`
			`+joinClause+`
			WHERE  dz.revoked_at IS NULL
			  AND  (dz.action = 'dismiss' OR dz.snoozed_until > NOW())
			ORDER BY dz.created_at DESC`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: query dismissed_zombies: %w", err)
	}
	defer rows.Close()

	var out []model.DismissAction
	for rows.Next() {
		var d model.DismissAction
		var currency *string
		if err := rows.Scan(
			&d.ID, &d.AccountID, &d.Provider, &d.Service, &d.Region, &d.ResourceID,
			&d.Action, &d.Reason, &d.Note, &d.SnoozedUntil, &d.DismissedBy, &d.CreatedAt,
			&d.RevokedAt, &d.RevokedBy,
			&d.MonthlyCost, &currency,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan dismissed_zombie: %w", err)
		}
		if currency != nil {
			d.Currency = *currency
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate dismissed_zombies: %w", err)
	}
	return out, tx.Commit(ctx)
}

// ExpireSnoozes marks snoozed records whose snoozed_until has passed as revoked.
// This is a cross-organization maintenance operation — uses adminPool to bypass RLS.
// Returns the number of records expired.
func (s *Store) ExpireSnoozes(ctx context.Context) (int64, error) {
	tag, err := s.adminPool.Exec(ctx, `
		UPDATE dismissed_zombies
		SET    revoked_at = NOW(), revoked_by = 'system:snooze_expiry'
		WHERE  action = 'snooze'
		  AND  revoked_at IS NULL
		  AND  snoozed_until < NOW()`,
	)
	if err != nil {
		return 0, fmt.Errorf("postgres: expire snoozes: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ── Audit log ─────────────────────────────────────────────────────────────────

// auditListMaxLimit caps the per-page size returned by AuditLogList regardless
// of what the caller requests, to stop accidental full-table scans.
const auditListMaxLimit = 500

// AuditLogWrite inserts one audit event for the organization in ctx. Callers must
// treat this as best-effort — see Store interface doc for why.
func (s *Store) AuditLogWrite(ctx context.Context, e model.AuditEvent) (int64, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return 0, fmt.Errorf("postgres: organization_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setOrganization(ctx, tx); err != nil {
		return 0, err
	}

	metadata := e.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return 0, fmt.Errorf("postgres: marshal audit metadata: %w", err)
	}

	var ipArg any
	if len(e.IPAddress) > 0 {
		ipArg = e.IPAddress.String()
	}
	var userIDArg any
	if e.UserID != "" {
		userIDArg = e.UserID
	}

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO audit_log (
			organization_id, user_id, actor_email, actor_name, action,
			resource_type, resource_id, reason, metadata,
			request_id, ip_address, user_agent
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`,
		organizationID, userIDArg, e.ActorEmail, e.ActorName, e.Action,
		e.ResourceType, e.ResourceID, e.Reason, string(metadataJSON),
		e.RequestID, ipArg, e.UserAgent,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("postgres: insert audit row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: commit audit row: %w", err)
	}
	return id, nil
}

// AuditLogList returns audit events for the organization in ctx, newest first.
// Filter fields compose with AND; zero values are ignored. Pagination uses a
// (created_at, id) cursor so inserts during paging don't shift rows.
func (s *Store) AuditLogList(ctx context.Context, f model.AuditFilter) ([]model.AuditEvent, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return nil, fmt.Errorf("postgres: organization_id missing from context")
	}

	limit := f.Limit
	if limit <= 0 || limit > auditListMaxLimit {
		limit = auditListMaxLimit
	}

	// Build WHERE clause with positional placeholders.
	args := []any{organizationID}
	where := []string{"organization_id = $1"}
	add := func(clause string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.UserID != "" {
		add("user_id = $%d", f.UserID)
	}
	if f.ResourceType != "" {
		add("resource_type = $%d", f.ResourceType)
	}
	if f.ResourceID != "" {
		add("resource_id = $%d", f.ResourceID)
	}
	if f.Action != "" {
		add("action = $%d", f.Action)
	}
	if !f.Since.IsZero() {
		add("created_at >= $%d", f.Since)
	}
	if !f.Until.IsZero() {
		add("created_at < $%d", f.Until)
	}
	if !f.Cursor.IsZero() {
		// (created_at, id) < (cursor.created_at, cursor.id) in lexicographic order
		// — pgx doesn't expand row values as placeholders, so spell it out.
		args = append(args, f.Cursor.CreatedAt, f.Cursor.ID)
		where = append(where, fmt.Sprintf(
			"(created_at < $%d OR (created_at = $%d AND id < $%d))",
			len(args)-1, len(args)-1, len(args),
		))
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return nil, err
	}

	// ip_address is cast to text so pgx uses the text codec (which supports
	// nullable **string). The default binary codec for INET decodes into
	// netip.Prefix or net.IPNet, neither of which fit our nullable-string
	// field without a typed wrapper. The cast is cheap — inet → text is O(1).
	//
	// actor_name is denormalised on write (migration 028) — captured at event
	// time so audit history survives later renames and GDPR anonymisation,
	// symmetrical to actor_email. AnonymiseUser clears it to '' alongside
	// rewriting actor_email to 'deleted-user'.
	query := fmt.Sprintf(`
		SELECT id, organization_id, user_id, actor_email, actor_name, action,
		       resource_type, resource_id, reason, metadata,
		       request_id, host(ip_address) AS ip_address, user_agent, created_at
		FROM audit_log
		WHERE %s
		ORDER BY created_at DESC, id DESC
		LIMIT %d`,
		strings.Join(where, " AND "), limit,
	)
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: query audit_log: %w", err)
	}
	defer rows.Close()

	var out []model.AuditEvent
	for rows.Next() {
		var e model.AuditEvent
		var userID, ipAddr *string
		var metadataJSON []byte
		if err := rows.Scan(
			&e.ID, &e.OrganizationID, &userID, &e.ActorEmail, &e.ActorName, &e.Action,
			&e.ResourceType, &e.ResourceID, &e.Reason, &metadataJSON,
			&e.RequestID, &ipAddr, &e.UserAgent, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan audit row: %w", err)
		}
		if userID != nil {
			e.UserID = *userID
		}
		if ipAddr != nil {
			// host() always returns a parseable address for valid INET, but
			// guard against ParseIP returning nil so a future migration that
			// changes the cast cannot crash the scan loop. On unparseable
			// input we leave IPAddress unset rather than store a nil net.IP.
			if parsed := net.ParseIP(*ipAddr); parsed != nil {
				e.IPAddress = parsed
			}
		}
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &e.Metadata); err != nil {
				return nil, fmt.Errorf("postgres: unmarshal audit metadata: %w", err)
			}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate audit rows: %w", err)
	}
	// Commit even on read-only tx so setOrganization's SET doesn't leak. If Commit
	// fails we can't trust that the rows we assembled were read under a
	// consistent snapshot — surface the error rather than returning stale data.
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: commit audit list tx: %w", err)
	}
	return out, nil
}

// AuditLogAnonymiseUser nulls user_id, replaces actor_email with the
// 'deleted-user' sentinel, and clears actor_name. Called from the GDPR
// user-delete path; actor_name uses ” rather than the email's sentinel
// because the frontend already falls back to actor_email when name is empty,
// so a parallel sentinel would just push 'deleted-user' onto the name row
// of the UI redundantly.
func (s *Store) AuditLogAnonymiseUser(ctx context.Context, userID string) (int64, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return 0, fmt.Errorf("postgres: organization_id missing from context")
	}
	if userID == "" {
		return 0, fmt.Errorf("postgres: user_id required for anonymise")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return 0, err
	}

	// The WHERE also includes organization_id even though RLS would enforce it — this
	// is a GDPR-critical path, so belt-and-suspenders organization scoping is cheap
	// insurance against an RLS misconfiguration slipping through code review.
	tag, err := tx.Exec(ctx, `
		UPDATE audit_log
		SET user_id = NULL, actor_email = 'deleted-user', actor_name = ''
		WHERE organization_id = $1 AND user_id = $2`,
		organizationID, userID,
	)
	if err != nil {
		return 0, fmt.Errorf("postgres: anonymise audit rows: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: commit anonymise: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ── Memberships ─────────────────────────────────────────────────────────────
//
// RBAC Phase 1. See docs/AUTHENTICATION.md (§2) for the role/permission model and
// enforcement semantics. All membership reads/writes go through RLS — an organization
// can only see and mutate its own rows. The middleware path opens its own
// transaction (rather than reading from adminPool) so RLS stays the last line
// of defence even on the auth-check fast path.

// RoleOf returns the role for (organizationID, userID), or "" with nil error when
// no membership row exists. Called from the auth middleware on every request,
// so it stays a single short transaction.
func (s *Store) RoleOf(ctx context.Context, organizationID, userID string) (string, error) {
	if organizationID == "" || userID == "" {
		return "", nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("postgres: role_of begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true)`, organizationID); err != nil {
		return "", fmt.Errorf("postgres: role_of set organization: %w", err)
	}

	var role string
	err = tx.QueryRow(ctx,
		`SELECT role FROM memberships WHERE organization_id = $1 AND user_id = $2`,
		organizationID, userID,
	).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres: role_of query: %w", err)
	}
	// Read-only — defer Rollback handles cleanup. Skipping Commit shaves a
	// round-trip on the auth fast path (called per request).
	return role, nil
}

// ListMemberships returns memberships in the organization in ctx joined with users.
//
// Runs on the runtime-bypass pool with an explicit organization_id filter,
// mirroring LookupMembership. It cannot use the app pool: a cross-org member's
// users row carries their HOME organization_id (the multi-org B1.5 model), so
// the users_organization_isolation policy (migration 035) would filter that row
// out of the LEFT JOIN and silently blank the member's email/name. The explicit
// `WHERE m.organization_id = $1` enforces org scoping in place of RLS.
func (s *Store) ListMemberships(ctx context.Context) ([]model.MembershipWithUser, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return nil, fmt.Errorf("postgres: list memberships: organization_id missing from context")
	}

	rows, err := s.adminPool.Query(ctx, `
		SELECT m.id, m.organization_id, m.user_id, m.role, COALESCE(m.invited_by, ''),
		       m.provisioned_via, m.created_at, m.updated_at,
		       COALESCE(u.email, ''), COALESCE(u.name, '')
		FROM memberships m
		LEFT JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1
		ORDER BY m.created_at ASC, m.id ASC`,
		organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: list memberships query: %w", err)
	}
	defer rows.Close()

	var out []model.MembershipWithUser
	for rows.Next() {
		var mu model.MembershipWithUser
		if err := rows.Scan(
			&mu.ID, &mu.OrganizationID, &mu.UserID, &mu.Role, &mu.InvitedBy,
			&mu.ProvisionedVia, &mu.CreatedAt, &mu.UpdatedAt, &mu.Email, &mu.Name,
		); err != nil {
			return nil, fmt.Errorf("postgres: list memberships scan: %w", err)
		}
		out = append(out, mu)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list memberships rows: %w", err)
	}
	return out, nil
}

// ListUserMemberships returns every active membership for the given user
// across all organizations they belong to, joined with organization metadata.
// Bypasses RLS via adminPool — by definition the result spans organizations,
// so a per-org RLS filter cannot apply. Caller is responsible for ensuring
// userID came from a validated auth context (see Store.ListUserMemberships
// doc comment for the safety contract).
func (s *Store) ListUserMemberships(ctx context.Context, userID string) ([]model.MembershipWithOrganization, error) {
	if userID == "" {
		return nil, nil
	}
	rows, err := s.adminPool.Query(ctx, `
		SELECT m.id, m.organization_id, m.user_id, m.role, COALESCE(m.invited_by, ''),
		       m.provisioned_via, m.created_at, m.updated_at,
		       COALESCE(o.name, ''), COALESCE(o.org_code, '')
		FROM memberships m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.user_id = $1
		ORDER BY m.created_at ASC, m.id ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list user memberships: %w", err)
	}
	defer rows.Close()

	var out []model.MembershipWithOrganization
	for rows.Next() {
		var mo model.MembershipWithOrganization
		if err := rows.Scan(
			&mo.ID, &mo.OrganizationID, &mo.UserID, &mo.Role, &mo.InvitedBy,
			&mo.ProvisionedVia, &mo.CreatedAt, &mo.UpdatedAt,
			&mo.OrganizationName, &mo.OrganizationOrgCode,
		); err != nil {
			return nil, fmt.Errorf("postgres: list user memberships scan: %w", err)
		}
		out = append(out, mo)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list user memberships rows: %w", err)
	}
	return out, nil
}

// GetMembership returns a single membership by ID for the organization in ctx.
func (s *Store) GetMembership(ctx context.Context, id string) (model.Membership, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Membership{}, fmt.Errorf("postgres: get membership begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return model.Membership{}, err
	}

	var m model.Membership
	err = tx.QueryRow(ctx, `
		SELECT id, organization_id, user_id, role, COALESCE(invited_by, ''),
		       provisioned_via, created_at, updated_at
		FROM memberships WHERE id = $1`, id,
	).Scan(&m.ID, &m.OrganizationID, &m.UserID, &m.Role, &m.InvitedBy,
		&m.ProvisionedVia, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Membership{}, storage.ErrMembershipNotFound
	}
	if err != nil {
		return model.Membership{}, fmt.Errorf("postgres: get membership: %w", err)
	}
	return m, tx.Commit(ctx)
}

// GetMembershipByOrgUser returns the membership for (organizationID, userID).
// Uses the admin pool — JIT reconciliation calls this from the OIDC callback
// flow where the org context is being established and the lookup needs to
// not depend on a transaction setup.
func (s *Store) GetMembershipByOrgUser(ctx context.Context, organizationID, userID string) (model.Membership, error) {
	var m model.Membership
	err := s.adminPool.QueryRow(ctx, `
		SELECT id, organization_id, user_id, role, COALESCE(invited_by, ''),
		       provisioned_via, created_at, updated_at
		FROM memberships
		WHERE organization_id = $1 AND user_id = $2`,
		organizationID, userID,
	).Scan(&m.ID, &m.OrganizationID, &m.UserID, &m.Role, &m.InvitedBy,
		&m.ProvisionedVia, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Membership{}, storage.ErrMembershipNotFound
	}
	if err != nil {
		return model.Membership{}, fmt.Errorf("postgres: get membership by org/user: %w", err)
	}
	return m, nil
}

// SaveMembership inserts a new membership row.
func (s *Store) SaveMembership(ctx context.Context, m model.Membership) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: save membership begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	now := time.Now().UTC()
	id := m.ID
	if id == "" {
		id = uuid.New().String()
	}
	var invitedBy any
	if m.InvitedBy != "" {
		invitedBy = m.InvitedBy
	}
	provisionedVia := m.ProvisionedVia
	if provisionedVia == "" {
		// Default mirrors the migration 022 column default for the explicit
		// POST /v1/memberships path (and any other caller that doesn't set
		// it). JIT and SCIM callers MUST set ProvisionedVia explicitly.
		provisionedVia = model.ProvisionedViaManual
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO memberships (id, organization_id, user_id, role, invited_by, provisioned_via, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		id, m.OrganizationID, m.UserID, m.Role, invitedBy, provisionedVia, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return storage.ErrMembershipExists
		}
		return fmt.Errorf("postgres: save membership: %w", err)
	}
	return tx.Commit(ctx)
}

// UpdateMembershipRole changes the role of an existing membership, enforcing
// the last-owner guard at SQL level via a CTE so the check runs inside the
// same transaction.
func (s *Store) UpdateMembershipRole(ctx context.Context, id, newRole string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: update role begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	// Lock the target row and read current state.
	var currentRole, organizationID string
	err = tx.QueryRow(ctx,
		`SELECT role, organization_id FROM memberships WHERE id = $1 FOR UPDATE`, id,
	).Scan(&currentRole, &organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return storage.ErrMembershipNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: update role lock: %w", err)
	}

	// Last-owner guard: demoting the last owner is rejected.
	if currentRole == "owner" && newRole != "owner" {
		var ownerCount int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM memberships WHERE organization_id = $1 AND role = 'owner'`, organizationID,
		).Scan(&ownerCount); err != nil {
			return fmt.Errorf("postgres: update role count owners: %w", err)
		}
		if ownerCount <= 1 {
			return storage.ErrLastOwner
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE memberships SET role = $1, updated_at = NOW() WHERE id = $2`, newRole, id,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Promotion to owner racing with another owner.
			return storage.ErrLastOwner
		}
		return fmt.Errorf("postgres: update role: %w", err)
	}
	return tx.Commit(ctx)
}

// DeleteMembership removes a membership row, enforcing the last-owner guard.
func (s *Store) DeleteMembership(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete membership begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	var role, organizationID string
	err = tx.QueryRow(ctx,
		`SELECT role, organization_id FROM memberships WHERE id = $1 FOR UPDATE`, id,
	).Scan(&role, &organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return storage.ErrMembershipNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: delete membership lock: %w", err)
	}

	if role == "owner" {
		var ownerCount int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM memberships WHERE organization_id = $1 AND role = 'owner'`, organizationID,
		).Scan(&ownerCount); err != nil {
			return fmt.Errorf("postgres: delete membership count owners: %w", err)
		}
		if ownerCount <= 1 {
			return storage.ErrLastOwner
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE id = $1`, id); err != nil {
		return fmt.Errorf("postgres: delete membership: %w", err)
	}
	return tx.Commit(ctx)
}

// TransferOwnership atomically demotes the current owner to admin and promotes
// the target user to owner within the organization in ctx.
func (s *Store) TransferOwnership(ctx context.Context, toUserID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: transfer ownership begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setOrganization(ctx, tx); err != nil {
		return err
	}

	organizationID := storage.OrganizationIDFromCtx(ctx)

	// Verify target membership exists in this organization.
	var targetID, targetRole string
	err = tx.QueryRow(ctx,
		`SELECT id, role FROM memberships WHERE organization_id = $1 AND user_id = $2 FOR UPDATE`,
		organizationID, toUserID,
	).Scan(&targetID, &targetRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return storage.ErrMembershipNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: transfer ownership target: %w", err)
	}

	// Demote current owner first to free the partial unique index.
	if _, err := tx.Exec(ctx, `
		UPDATE memberships
		SET role = 'admin', updated_at = NOW()
		WHERE organization_id = $1 AND role = 'owner'`,
		organizationID,
	); err != nil {
		return fmt.Errorf("postgres: transfer ownership demote: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE memberships SET role = 'owner', updated_at = NOW() WHERE id = $1`,
		targetID,
	); err != nil {
		return fmt.Errorf("postgres: transfer ownership promote: %w", err)
	}
	return tx.Commit(ctx)
}

// EnsureFirstMembership inserts an owner row only when no membership exists
// for the organization. The partial unique index is the race-safe backstop:
// a second concurrent INSERT loses on the index, the caller sees err with
// constraint code 23505 → swallowed → ok=false.
//
// Opens its own transaction and sets app.organization_id so the INSERT satisfies
// the WITH CHECK clause of the memberships RLS policy. Works whether the
// process connects as the owner (BYPASSRLS) or the app role (RLS-enforced).
func (s *Store) EnsureFirstMembership(ctx context.Context, organizationID, userID string) (bool, error) {
	if organizationID == "" || userID == "" {
		return false, fmt.Errorf("postgres: ensure first membership: organizationID and userID required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres: ensure first membership begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true)`, organizationID); err != nil {
		return false, fmt.Errorf("postgres: ensure first membership set organization: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO memberships (id, organization_id, user_id, role, provisioned_via, created_at, updated_at)
		SELECT $1, $2, $3, 'owner', 'manual', NOW(), NOW()
		WHERE NOT EXISTS (SELECT 1 FROM memberships WHERE organization_id = $2)`,
		uuid.New().String(), organizationID, userID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		// Race: another request inserted the owner between WHERE NOT EXISTS and INSERT.
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return false, nil
		}
		return false, fmt.Errorf("postgres: ensure first membership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("postgres: ensure first membership commit: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// EnsureDevMembership creates an owner-or-other row for (organizationID, userID) on
// startup. Idempotent. Opens its own transaction and sets app.organization_id so
// the INSERT satisfies the memberships RLS policy regardless of whether the
// process connects as the owner role (BYPASSRLS) or the app role.
func (s *Store) EnsureDevMembership(ctx context.Context, organizationID, userID, role string) error {
	switch role {
	case "owner", "admin", "member", "viewer":
	default:
		return fmt.Errorf("postgres: ensure dev membership: invalid role %q", role)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: ensure dev membership begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true)`, organizationID); err != nil {
		return fmt.Errorf("postgres: ensure dev membership set organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (id, organization_id, user_id, role, provisioned_via, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'manual', NOW(), NOW())
		ON CONFLICT (organization_id, user_id) DO UPDATE SET
			role       = EXCLUDED.role,
			updated_at = NOW()`,
		uuid.New().String(), organizationID, userID, role,
	); err != nil {
		return fmt.Errorf("postgres: ensure dev membership: %w", err)
	}
	return tx.Commit(ctx)
}

// GetUserByID looks up a user by internal UUID, org-agnostic. users.organization_id
// tracks the user's primary org and is intentionally NOT used in the WHERE clause
// so a cross-org member can read their own row from any org context (e.g. /v1/me).
// users IS RLS-scoped (migration 035), so this read runs on the runtime-bypass
// pool — the org-isolation policy would otherwise filter a cross-org member's row.
func (s *Store) GetUserByID(ctx context.Context, id string) (model.User, error) {
	if id == "" {
		return model.User{}, storage.ErrUserNotFound
	}
	var u model.User
	// Runtime-bypass pool: a by-PK lookup that legitimately spans orgs — /v1/me
	// must resolve a cross-org member's own row regardless of the request's org
	// context, so the users_organization_isolation policy (migration 035) would
	// wrongly filter it on the app pool. Isolation here is the PK + the
	// authenticated caller's identity, not RLS.
	err := s.adminPool.QueryRow(ctx, `
		SELECT id, organization_id, external_id, email, name, created_at, last_seen
		FROM users
		WHERE id = $1`,
		id,
	).Scan(&u.ID, &u.OrganizationID, &u.ExternalID, &u.Email, &u.Name, &u.CreatedAt, &u.LastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, storage.ErrUserNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("postgres: get user by id: %w", err)
	}
	return u, nil
}

// SetUserSSOConnection writes users.sso_connection_id. Empty connectionID
// clears the column to NULL. Returns ErrUserNotFound when no row matches.
// See the Store interface comment for why this is a separate setter rather
// than a field on UpsertUser.
func (s *Store) SetUserSSOConnection(ctx context.Context, userID, connectionID string) error {
	if userID == "" {
		return storage.ErrUserNotFound
	}
	// NULLIF($, '') maps the empty-string sentinel to a SQL NULL so the
	// `ON DELETE SET NULL` foreign-key invariant on users.sso_connection_id
	// stays uniform: every "no connection" row holds NULL, never ''.
	// Runtime-bypass pool (by-PK write, runs in the SSO callback before a request
	// org context exists). The users RLS WITH CHECK (migration 035) would reject
	// an app-pool UPDATE with no GUC set.
	tag, err := s.adminPool.Exec(ctx, `
		UPDATE users
		SET sso_connection_id = NULLIF($2, '')
		WHERE id = $1`,
		userID, connectionID,
	)
	if err != nil {
		return fmt.Errorf("postgres: set user sso connection id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrUserNotFound
	}
	return nil
}

// GetUserSSOConnectionID returns users.sso_connection_id (empty when NULL).
// Single-purpose lookup for the SSO RP-Initiated Logout resolver — see the
// Store interface comment for why we don't fold this into GetUserByID.
func (s *Store) GetUserSSOConnectionID(ctx context.Context, userID string) (string, error) {
	if userID == "" {
		return "", storage.ErrUserNotFound
	}
	var connID *string
	// Runtime-bypass pool: by-PK read in the RP-Initiated Logout resolver, which
	// runs without a request org context (mirrors GetUserByID).
	err := s.adminPool.QueryRow(ctx, `
		SELECT sso_connection_id
		FROM users
		WHERE id = $1`,
		userID,
	).Scan(&connID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", storage.ErrUserNotFound
	}
	if err != nil {
		return "", fmt.Errorf("postgres: get user sso connection id: %w", err)
	}
	if connID == nil {
		return "", nil
	}
	return *connID, nil
}

// GetUserByEmail looks up a user by email within the organization in ctx. Used by
// the invite-by-email flow.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (model.User, error) {
	organizationID := storage.OrganizationIDFromCtx(ctx)
	if organizationID == "" {
		return model.User{}, fmt.Errorf("postgres: organization_id missing from context")
	}
	var u model.User
	// Runtime-bypass pool with explicit organization_id scoping in the WHERE
	// clause: the lookup is org-scoped, but it runs before a DB org context is
	// set, so the explicit filter — not RLS — enforces isolation (the
	// users_organization_isolation policy from migration 035 is the fail-closed
	// backstop for any future app-pool regression).
	err := s.adminPool.QueryRow(ctx, `
		SELECT id, organization_id, external_id, email, name, created_at, last_seen
		FROM users
		WHERE organization_id = $1 AND lower(email) = lower($2)`,
		organizationID, email,
	).Scan(&u.ID, &u.OrganizationID, &u.ExternalID, &u.Email, &u.Name, &u.CreatedAt, &u.LastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, storage.ErrUserNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("postgres: get user by email: %w", err)
	}
	return u, nil
}

// ── GDPR — right to erasure ─────────────────────────────────────────────────
//
// Both methods here use adminPool because:
//   * audit_log only grants UPDATE to the app role (no DELETE) — the migration
//     locks down purges to the owner role on purpose.
//   * the operations span organizations (anonymising audit entries elsewhere when a
//     user is deleted), so RLS would block half the work.
// See docs/ARCHITECTURE.md (§6, Right-to-erasure paths).

// DeleteUser hard-deletes a user as part of right-to-erasure. Anonymises
// the user's audit footprint across all organizations, then deletes the user row;
// memberships in this and other organizations cascade away automatically.
func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	if userID == "" {
		return fmt.Errorf("postgres: delete user: userID required")
	}
	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete user begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Refuse if the user is the sole owner of any organization — they must transfer
	// ownership or delete those organizations first. Same invariant the membership
	// flow protects with ErrLastOwner.
	var orphanCount int
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM memberships m1
		WHERE m1.user_id = $1 AND m1.role = 'owner'
		  AND NOT EXISTS (
		    SELECT 1 FROM memberships m2
		    WHERE m2.organization_id = m1.organization_id
		      AND m2.role = 'owner'
		      AND m2.user_id <> $1
		  )`, userID).Scan(&orphanCount)
	if err != nil {
		return fmt.Errorf("postgres: delete user owner check: %w", err)
	}
	if orphanCount > 0 {
		return storage.ErrLastOwner
	}

	if _, err := tx.Exec(ctx, `
		UPDATE audit_log
		SET user_id = NULL, actor_email = 'deleted-user'
		WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("postgres: delete user anonymise audit: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("postgres: delete user: %w", err)
	}
	return tx.Commit(ctx)
}

// DeleteOrganizationCascade purges every row scoped to organizationID in FK-safe order
// and then drops the organization row itself — this is the one path that
// purges audit_log.
func (s *Store) DeleteOrganizationCascade(ctx context.Context, organizationID string) error {
	if organizationID == "" {
		return fmt.Errorf("postgres: delete organization: organizationID required")
	}
	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: delete organization begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Anonymise audit entries (in OTHER organizations) for users whose primary
	// organization is the one being deleted — those users are about to be removed
	// from the system entirely, so their attribution in any organization they
	// were a member of must also be anonymised.
	if _, err := tx.Exec(ctx, `
		UPDATE audit_log
		SET user_id = NULL, actor_email = 'deleted-user'
		WHERE organization_id <> $1
		  AND user_id IN (SELECT id FROM users WHERE organization_id = $1)`,
		organizationID,
	); err != nil {
		return fmt.Errorf("postgres: cascade anonymise audit: %w", err)
	}

	// Per-organization data, FK-safe order. notification_dispatches MUST precede
	// notification_channels (dispatches.channel_id → channels.id) — though both
	// also cascade from the final organizations delete, this explicit per-table
	// loop runs first, so the order has to be honoured here too.
	tables := []string{
		"dismissed_zombies",
		"zombie_snapshot_services",
		"notification_dispatches",
		"notification_channels",
		"zombie_snapshots",
		"zombie_records",
		"resource_records",
		"cost_records",
		"accounts",
		"audit_log",
	}
	for _, t := range tables {
		// #nosec — table name is a hardcoded literal from the slice above,
		// not user input. pgx parameter binding does not support identifiers.
		if _, err := tx.Exec(ctx, "DELETE FROM "+t+" WHERE organization_id = $1", organizationID); err != nil {
			return fmt.Errorf("postgres: cascade delete %s: %w", t, err)
		}
	}

	// Users whose primary organization is this one go away entirely. CASCADE on
	// memberships.user_id removes their memberships in all other organizations too.
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE organization_id = $1`, organizationID); err != nil {
		return fmt.Errorf("postgres: cascade delete users: %w", err)
	}

	// Finally drop the organization; CASCADE on memberships.organization_id sweeps any
	// remaining membership rows held by users whose primary organization is elsewhere.
	if _, err := tx.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, organizationID); err != nil {
		return fmt.Errorf("postgres: cascade delete organization: %w", err)
	}
	return tx.Commit(ctx)
}

// Ping verifies the database connection is still alive.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) Close() error {
	s.pool.Close()
	if s.adminPool != s.pool {
		s.adminPool.Close()
	}
	return nil
}
