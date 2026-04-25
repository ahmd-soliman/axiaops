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
	adminPool *pgxpool.Pool // owner connection — bypasses RLS, used only for ListAllAccounts
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

// setTenant sets the app.tenant_id session variable for Row-Level Security.
// Must be called inside a transaction so the setting is scoped to that tx.
func setTenant(ctx context.Context, tx pgx.Tx) error {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return fmt.Errorf("postgres: tenant_id missing from context")
	}
	_, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID)
	return err
}

// Save inserts cost records in a single transaction, skipping duplicates.
func (s *Store) Save(ctx context.Context, records []model.CostRecord) (int64, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return 0, fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
		return 0, err
	}

	var inserted int64
	for _, r := range records {
		tags, err := json.Marshal(r.Tags)
		if err != nil {
			return 0, fmt.Errorf("postgres: marshal tags: %w", err)
		}
		res, err := tx.Exec(ctx, `
			INSERT INTO cost_records
				(tenant_id, provider, account_id, internal_account_id, service, region, resource_id, amount, currency,
				 period_start, period_end, tags, fetched_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (tenant_id, provider, account_id, service, region, period_start, period_end)
			DO NOTHING`,
			tenantID,
			r.Provider, r.AccountID, r.InternalAccountID, r.Service, r.Region, r.ResourceID,
			r.Amount, r.Currency,
			r.PeriodStart, r.PeriodEnd,
			string(tags), r.FetchedAt,
		)
		if err != nil {
			return 0, fmt.Errorf("postgres: insert cost record: %w", err)
		}
		inserted += res.RowsAffected()
	}

	return inserted, tx.Commit(ctx)
}

// SaveZombies replaces the tenant's zombie records for the specified accounts with the latest detection results.
func (s *Store) SaveZombies(ctx context.Context, zombies []model.ZombieResource) error {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return fmt.Errorf("postgres: tenant_id missing from context")
	}

	if len(zombies) == 0 {
		return nil // Nothing to save
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
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
		if _, err := tx.Exec(ctx, `DELETE FROM zombie_records WHERE tenant_id = $1 AND internal_account_id = $2`, tenantID, accountID); err != nil {
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
				(tenant_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, arn, tags,
				 monthly_cost, currency, period_start, period_end,
				 usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
			tenantID,
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

// LoadZombies returns zombie records for the tenant in ctx.
func (s *Store) LoadZombies(ctx context.Context) ([]model.ZombieResource, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
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

// UpsertTenant creates a tenant on first login or returns the existing one.
func (s *Store) UpsertTenant(ctx context.Context, orgCode, name string) (model.Tenant, error) {
	now := time.Now().UTC()
	id := uuid.New().String()

	_, err := s.pool.Exec(ctx, `
		INSERT INTO tenants (id, org_code, name, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (org_code) DO UPDATE SET name = EXCLUDED.name`,
		id, orgCode, name, now,
	)
	if err != nil {
		return model.Tenant{}, fmt.Errorf("postgres: upsert tenant: %w", err)
	}

	var t model.Tenant
	err = s.pool.QueryRow(ctx,
		`SELECT id, org_code, name, created_at FROM tenants WHERE org_code = $1`, orgCode,
	).Scan(&t.ID, &t.OrgCode, &t.Name, &t.CreatedAt)
	if err != nil {
		return model.Tenant{}, fmt.Errorf("postgres: fetch tenant: %w", err)
	}
	return t, nil
}

// EnsureTenant inserts a tenant with a caller-supplied id if no row with that
// id exists yet. Unlike UpsertTenant, the id is pinned and the row is never
// modified on conflict. Used by dev mode to guarantee a known-id tenant row
// so that FK references from accounts/zombies/etc. resolve without requiring
// a prior write path to have auto-created the row.
func (s *Store) EnsureTenant(ctx context.Context, id, orgCode, name string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO tenants (id, org_code, name, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING`,
		id, orgCode, name, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("postgres: ensure tenant: %w", err)
	}
	return nil
}

// EnsureUser inserts a user with a caller-supplied id, or updates tenant_id,
// email, name, and last_seen if the row already exists. The id is pinned (not
// generated). Used by dev mode at startup to guarantee a known-id user row so
// DevBypass can inject user_id onto the request context.
//
// A synthetic kinde_sub of the form "dev:<id>" is used so the UNIQUE constraint
// on kinde_sub can coexist with real Kinde-issued users in the same database.
// Migration 013 adds a CHECK constraint enforcing this invariant.
//
// Conflict handling is DO UPDATE (not DO NOTHING) so that rotating DEV_TENANT_ID
// or DEV_USER_EMAIL across runs self-corrects the existing row — otherwise the
// stored tenant_id would silently diverge from the tenant id DevBypass injects
// onto every request.
//
// NOTE: this method uses the raw pool and does NOT set app.tenant_id. Safe here
// only because `users` has no RLS policy and this is a startup bootstrap call.
// Do NOT copy this pattern for handler-path writes to RLS-scoped tables —
// those must use storage.WithTenantID and the transaction pattern.
func (s *Store) EnsureUser(ctx context.Context, u model.User) error {
	now := time.Now().UTC()
	kindeSub := "dev:" + u.ID
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, tenant_id, kinde_sub, email, name, created_at, last_seen)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		ON CONFLICT (id) DO UPDATE SET
			tenant_id = EXCLUDED.tenant_id,
			email     = EXCLUDED.email,
			name      = EXCLUDED.name,
			last_seen = EXCLUDED.last_seen`,
		u.ID, u.TenantID, kindeSub, u.Email, u.Name, now,
	)
	if err != nil {
		return fmt.Errorf("postgres: ensure user: %w", err)
	}
	return nil
}

// UpsertUser creates a user on first login or updates email, name, and last_seen.
func (s *Store) UpsertUser(ctx context.Context, tenantID, kindeSub, email, name string) (model.User, error) {
	now := time.Now().UTC()
	id := uuid.New().String()

	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, tenant_id, kinde_sub, email, name, created_at, last_seen)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (kinde_sub) DO UPDATE SET
			email     = EXCLUDED.email,
			name      = EXCLUDED.name,
			last_seen = EXCLUDED.last_seen`,
		id, tenantID, kindeSub, email, name, now, now,
	)
	if err != nil {
		return model.User{}, fmt.Errorf("postgres: upsert user: %w", err)
	}

	var u model.User
	err = s.pool.QueryRow(ctx,
		`SELECT id, tenant_id, kinde_sub, email, name, created_at, last_seen FROM users WHERE kinde_sub = $1`, kindeSub,
	).Scan(&u.ID, &u.TenantID, &u.KindeSub, &u.Email, &u.Name, &u.CreatedAt, &u.LastSeen)
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

	if err := setTenant(ctx, tx); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO accounts
			(id, tenant_id, provider, label, account_id, access_key_id, secret_encrypted, region, status, scan_interval_hours, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			label               = EXCLUDED.label,
			account_id          = EXCLUDED.account_id,
			access_key_id       = EXCLUDED.access_key_id,
			secret_encrypted    = EXCLUDED.secret_encrypted,
			region              = EXCLUDED.region,
			status              = EXCLUDED.status,
			scan_interval_hours = EXCLUDED.scan_interval_hours`,
		a.ID, a.TenantID, a.Provider, a.Label, a.AccountID,
		a.AccessKeyID, a.SecretEncrypted, a.Region, a.Status, a.ScanIntervalHours, a.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: save account: %w", err)
	}
	return tx.Commit(ctx)
}

// ListAccounts returns all accounts for the tenant in ctx.
func (s *Store) ListAccounts(ctx context.Context) ([]model.Account, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, provider, label, account_id, access_key_id, secret_encrypted,
		       region, status, last_scanned_at, scan_interval_hours, created_at
		FROM accounts ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []model.Account
	for rows.Next() {
		var a model.Account
		if err := rows.Scan(
			&a.ID, &a.TenantID, &a.Provider, &a.Label, &a.AccountID, &a.AccessKeyID, &a.SecretEncrypted,
			&a.Region, &a.Status, &a.LastScannedAt, &a.ScanIntervalHours, &a.CreatedAt,
		); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accounts, tx.Commit(ctx)
}

// ListAllAccounts returns accounts for ALL tenants, bypassing row-level security.
// Used internally by the scheduled scan scheduler. Does not respect tenant_id in context.
func (s *Store) ListAllAccounts(ctx context.Context) ([]model.Account, error) {
	tx, err := s.adminPool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// NOTE: Deliberately NOT calling setTenant(ctx, tx) here.
	// This allows the query to return accounts from all tenants.
	// Only use this method for trusted internal operations (scheduler, background jobs).

	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, provider, label, account_id, access_key_id, secret_encrypted,
		       region, status, last_scanned_at, scan_interval_hours, created_at
		FROM accounts ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []model.Account
	for rows.Next() {
		var a model.Account
		if err := rows.Scan(
			&a.ID, &a.TenantID, &a.Provider, &a.Label, &a.AccountID, &a.AccessKeyID, &a.SecretEncrypted,
			&a.Region, &a.Status, &a.LastScannedAt, &a.ScanIntervalHours, &a.CreatedAt,
		); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accounts, tx.Commit(ctx)
}

// GetAccount returns a single account by ID for the tenant in ctx.
func (s *Store) GetAccount(ctx context.Context, id string) (model.Account, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Account{}, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
		return model.Account{}, err
	}

	var a model.Account
	err = tx.QueryRow(ctx, `
		SELECT id, tenant_id, provider, label, account_id, access_key_id, secret_encrypted,
		       region, status, last_scanned_at, scan_interval_hours, created_at
		FROM accounts WHERE id = $1`, id,
	).Scan(&a.ID, &a.TenantID, &a.Provider, &a.Label, &a.AccountID, &a.AccessKeyID, &a.SecretEncrypted,
		&a.Region, &a.Status, &a.LastScannedAt, &a.ScanIntervalHours, &a.CreatedAt)
	if err != nil {
		return model.Account{}, err
	}
	return a, tx.Commit(ctx)
}

// DeleteAccount removes an account by ID for the tenant in ctx.
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("postgres: delete account: %w", err)
	}
	return tx.Commit(ctx)
}

// UpdateAccountStatus sets status and last_scanned_at for an account.
func (s *Store) UpdateAccountStatus(ctx context.Context, id, status string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE accounts SET status = $1, last_scanned_at = NOW()
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

	if err := setTenant(ctx, tx); err != nil {
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
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
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
				(tenant_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, arn, tags,
				 monthly_cost, currency, period_start, period_end,
				 usage_metric, usage_avg, usage_unit, is_zombie, reason, owner, detected_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
			tenantID,
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

// LoadResources returns all resource records for the tenant in ctx.
func (s *Store) LoadResources(ctx context.Context) ([]model.ResourceRecord, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
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

// ListCostRecords returns cost records for the tenant in ctx, filtered by the given criteria.
// Records with amount > 0 are returned, ordered by period_start (newest first) then amount (largest first).
func (s *Store) ListCostRecords(ctx context.Context, filter storage.CostFilter) ([]model.CostRecord, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
		return nil, err
	}

	days := filter.Days
	if days <= 0 {
		days = 30
	}

	query := `SELECT provider, account_id, internal_account_id, service, region, resource_id,
	                 amount, currency, period_start, period_end, tags, fetched_at
	          FROM cost_records
	          WHERE amount > 0 AND period_end >= NOW() - make_interval(days => $1)`
	args := []any{days}
	argN := 2

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
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO zombie_snapshots
			(id, tenant_id, account_id, snapshot_at, zombie_count, total_monthly_cost, currency)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		snap.ID, tenantID, snap.AccountID, snap.SnapshotAt,
		snap.ZombieCount, snap.TotalMonthlyCost, snap.Currency,
	)
	if err != nil {
		return fmt.Errorf("postgres: insert zombie_snapshot: %w", err)
	}
	return tx.Commit(ctx)
}

// ListSnapshots returns zombie snapshots for the tenant, oldest-first.
// If accountID is non-empty, only snapshots for that account are returned.
func (s *Store) ListSnapshots(ctx context.Context, accountID string) ([]model.ZombieSnapshot, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
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

	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
		return err
	}

	for _, svc := range services {
		_, err := tx.Exec(ctx, `
			INSERT INTO zombie_snapshot_services
				(id, snapshot_id, tenant_id, service, resource_type, zombie_count, monthly_cost, currency)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			svc.ID, svc.SnapshotID, tenantID, svc.Service, svc.ResourceType,
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
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
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

// ListTrendServices returns distinct services that have snapshot data for the tenant.
func (s *Store) ListTrendServices(ctx context.Context) ([]string, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
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
// that have snapshot data for the tenant.
func (s *Store) ListTrendResourceTypes(ctx context.Context, service string) ([]string, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
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

// DeleteOldCostRecords deletes cost records with period_end before cutoff across all tenants.
// Uses adminPool to bypass RLS — this is a maintenance operation across all tenants.
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
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return 0, fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
		return 0, err
	}

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO dismissed_zombies
			(tenant_id, account_id, provider, service, region, resource_id,
			 action, reason, note, snoozed_until, dismissed_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		RETURNING id`,
		tenantID, d.AccountID, d.Provider, d.Service, d.Region, d.ResourceID,
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
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
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
// for the tenant in ctx.  If accountID is non-empty, filters to that account only.
func (s *Store) ListActiveDismissals(ctx context.Context, accountID string) ([]model.DismissAction, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
		return nil, err
	}

	var rows pgx.Rows
	if accountID != "" {
		rows, err = tx.Query(ctx, `
			SELECT id, account_id, provider, service, region, resource_id,
			       action, reason, note, snoozed_until, dismissed_by, created_at,
			       revoked_at, revoked_by
			FROM   dismissed_zombies
			WHERE  revoked_at IS NULL
			  AND  (action = 'dismiss' OR snoozed_until > NOW())
			  AND  account_id = $1
			ORDER BY created_at DESC`,
			accountID,
		)
	} else {
		rows, err = tx.Query(ctx, `
			SELECT id, account_id, provider, service, region, resource_id,
			       action, reason, note, snoozed_until, dismissed_by, created_at,
			       revoked_at, revoked_by
			FROM   dismissed_zombies
			WHERE  revoked_at IS NULL
			  AND  (action = 'dismiss' OR snoozed_until > NOW())
			ORDER BY created_at DESC`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: query dismissed_zombies: %w", err)
	}
	defer rows.Close()

	var out []model.DismissAction
	for rows.Next() {
		var d model.DismissAction
		if err := rows.Scan(
			&d.ID, &d.AccountID, &d.Provider, &d.Service, &d.Region, &d.ResourceID,
			&d.Action, &d.Reason, &d.Note, &d.SnoozedUntil, &d.DismissedBy, &d.CreatedAt,
			&d.RevokedAt, &d.RevokedBy,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan dismissed_zombie: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

// ExpireSnoozes marks snoozed records whose snoozed_until has passed as revoked.
// This is a cross-tenant maintenance operation — uses adminPool to bypass RLS.
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

// AuditLogWrite inserts one audit event for the tenant in ctx. Callers must
// treat this as best-effort — see Store interface doc for why.
func (s *Store) AuditLogWrite(ctx context.Context, e model.AuditEvent) (int64, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return 0, fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenant(ctx, tx); err != nil {
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
			tenant_id, user_id, actor_email, action,
			resource_type, resource_id, reason, metadata,
			request_id, ip_address, user_agent
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id`,
		tenantID, userIDArg, e.ActorEmail, e.Action,
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

// AuditLogList returns audit events for the tenant in ctx, newest first.
// Filter fields compose with AND; zero values are ignored. Pagination uses a
// (created_at, id) cursor so inserts during paging don't shift rows.
func (s *Store) AuditLogList(ctx context.Context, f model.AuditFilter) ([]model.AuditEvent, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("postgres: tenant_id missing from context")
	}

	limit := f.Limit
	if limit <= 0 || limit > auditListMaxLimit {
		limit = auditListMaxLimit
	}

	// Build WHERE clause with positional placeholders.
	args := []any{tenantID}
	where := []string{"tenant_id = $1"}
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
	if err := setTenant(ctx, tx); err != nil {
		return nil, err
	}

	// ip_address is cast to text so pgx uses the text codec (which supports
	// nullable **string). The default binary codec for INET decodes into
	// netip.Prefix or net.IPNet, neither of which fit our nullable-string
	// field without a typed wrapper. The cast is cheap — inet → text is O(1).
	query := fmt.Sprintf(`
		SELECT id, tenant_id, user_id, actor_email, action,
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
			&e.ID, &e.TenantID, &userID, &e.ActorEmail, &e.Action,
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
	// Commit even on read-only tx so setTenant's SET doesn't leak. If Commit
	// fails we can't trust that the rows we assembled were read under a
	// consistent snapshot — surface the error rather than returning stale data.
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres: commit audit list tx: %w", err)
	}
	return out, nil
}

// AuditLogAnonymiseUser nulls user_id and replaces actor_email for all rows
// matching (tenant_id, user_id). Called from the GDPR user-delete path.
func (s *Store) AuditLogAnonymiseUser(ctx context.Context, userID string) (int64, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return 0, fmt.Errorf("postgres: tenant_id missing from context")
	}
	if userID == "" {
		return 0, fmt.Errorf("postgres: user_id required for anonymise")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx); err != nil {
		return 0, err
	}

	// The WHERE also includes tenant_id even though RLS would enforce it — this
	// is a GDPR-critical path, so belt-and-suspenders tenant scoping is cheap
	// insurance against an RLS misconfiguration slipping through code review.
	tag, err := tx.Exec(ctx, `
		UPDATE audit_log
		SET user_id = NULL, actor_email = 'deleted-user'
		WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID,
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
// RBAC Phase 1. See docs/rbac-design.md §4 for the data model and §6 for
// enforcement semantics. All membership reads/writes go through RLS — a tenant
// can only see and mutate its own rows. The middleware path opens its own
// transaction (rather than reading from adminPool) so RLS stays the last line
// of defence even on the auth-check fast path.

// RoleOf returns the role for (tenantID, userID), or "" with nil error when
// no membership row exists. Called from the auth middleware on every request,
// so it stays a single short transaction.
func (s *Store) RoleOf(ctx context.Context, tenantID, userID string) (string, error) {
	if tenantID == "" || userID == "" {
		return "", nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("postgres: role_of begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return "", fmt.Errorf("postgres: role_of set tenant: %w", err)
	}

	var role string
	err = tx.QueryRow(ctx,
		`SELECT role FROM memberships WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID,
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

// ListMemberships returns memberships in the tenant in ctx joined with users.
func (s *Store) ListMemberships(ctx context.Context) ([]model.MembershipWithUser, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: list memberships begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT m.id, m.tenant_id, m.user_id, m.role, COALESCE(m.invited_by, ''),
		       m.created_at, m.updated_at, COALESCE(u.email, ''), COALESCE(u.name, '')
		FROM memberships m
		LEFT JOIN users u ON u.id = m.user_id
		ORDER BY m.created_at ASC, m.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list memberships query: %w", err)
	}
	defer rows.Close()

	var out []model.MembershipWithUser
	for rows.Next() {
		var mu model.MembershipWithUser
		if err := rows.Scan(
			&mu.ID, &mu.TenantID, &mu.UserID, &mu.Role, &mu.InvitedBy,
			&mu.CreatedAt, &mu.UpdatedAt, &mu.Email, &mu.Name,
		); err != nil {
			return nil, fmt.Errorf("postgres: list memberships scan: %w", err)
		}
		out = append(out, mu)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list memberships rows: %w", err)
	}
	return out, tx.Commit(ctx)
}

// GetMembership returns a single membership by ID for the tenant in ctx.
func (s *Store) GetMembership(ctx context.Context, id string) (model.Membership, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return model.Membership{}, fmt.Errorf("postgres: get membership begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx); err != nil {
		return model.Membership{}, err
	}

	var m model.Membership
	err = tx.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, role, COALESCE(invited_by, ''), created_at, updated_at
		FROM memberships WHERE id = $1`, id,
	).Scan(&m.ID, &m.TenantID, &m.UserID, &m.Role, &m.InvitedBy, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Membership{}, storage.ErrMembershipNotFound
	}
	if err != nil {
		return model.Membership{}, fmt.Errorf("postgres: get membership: %w", err)
	}
	return m, tx.Commit(ctx)
}

// SaveMembership inserts a new membership row.
func (s *Store) SaveMembership(ctx context.Context, m model.Membership) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: save membership begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx); err != nil {
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
	_, err = tx.Exec(ctx, `
		INSERT INTO memberships (id, tenant_id, user_id, role, invited_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		id, m.TenantID, m.UserID, m.Role, invitedBy, now,
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
	if err := setTenant(ctx, tx); err != nil {
		return err
	}

	// Lock the target row and read current state.
	var currentRole, tenantID string
	err = tx.QueryRow(ctx,
		`SELECT role, tenant_id FROM memberships WHERE id = $1 FOR UPDATE`, id,
	).Scan(&currentRole, &tenantID)
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
			`SELECT COUNT(*) FROM memberships WHERE tenant_id = $1 AND role = 'owner'`, tenantID,
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
	if err := setTenant(ctx, tx); err != nil {
		return err
	}

	var role, tenantID string
	err = tx.QueryRow(ctx,
		`SELECT role, tenant_id FROM memberships WHERE id = $1 FOR UPDATE`, id,
	).Scan(&role, &tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return storage.ErrMembershipNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: delete membership lock: %w", err)
	}

	if role == "owner" {
		var ownerCount int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM memberships WHERE tenant_id = $1 AND role = 'owner'`, tenantID,
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
// the target user to owner within the tenant in ctx.
func (s *Store) TransferOwnership(ctx context.Context, toUserID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: transfer ownership begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := setTenant(ctx, tx); err != nil {
		return err
	}

	tenantID := storage.TenantIDFromCtx(ctx)

	// Verify target membership exists in this tenant.
	var targetID, targetRole string
	err = tx.QueryRow(ctx,
		`SELECT id, role FROM memberships WHERE tenant_id = $1 AND user_id = $2 FOR UPDATE`,
		tenantID, toUserID,
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
		WHERE tenant_id = $1 AND role = 'owner'`,
		tenantID,
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
// for the tenant. The partial unique index is the race-safe backstop: a second
// concurrent INSERT in a brand-new Kinde org loses on the index, the caller
// sees err with constraint code 23505 → swallowed → ok=false.
//
// Opens its own transaction and sets app.tenant_id so the INSERT satisfies
// the WITH CHECK clause of the memberships RLS policy. Works whether the
// process connects as the owner (BYPASSRLS) or the app role (RLS-enforced).
func (s *Store) EnsureFirstMembership(ctx context.Context, tenantID, userID string) (bool, error) {
	if tenantID == "" || userID == "" {
		return false, fmt.Errorf("postgres: ensure first membership: tenantID and userID required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres: ensure first membership begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return false, fmt.Errorf("postgres: ensure first membership set tenant: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO memberships (id, tenant_id, user_id, role, created_at, updated_at)
		SELECT $1, $2, $3, 'owner', NOW(), NOW()
		WHERE NOT EXISTS (SELECT 1 FROM memberships WHERE tenant_id = $2)`,
		uuid.New().String(), tenantID, userID,
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

// EnsureDevMembership creates an owner-or-other row for (tenantID, userID) on
// startup. Idempotent. Opens its own transaction and sets app.tenant_id so
// the INSERT satisfies the memberships RLS policy regardless of whether the
// process connects as the owner role (BYPASSRLS) or the app role.
func (s *Store) EnsureDevMembership(ctx context.Context, tenantID, userID, role string) error {
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
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID); err != nil {
		return fmt.Errorf("postgres: ensure dev membership set tenant: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (id, tenant_id, user_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (tenant_id, user_id) DO UPDATE SET
			role       = EXCLUDED.role,
			updated_at = NOW()`,
		uuid.New().String(), tenantID, userID, role,
	); err != nil {
		return fmt.Errorf("postgres: ensure dev membership: %w", err)
	}
	return tx.Commit(ctx)
}

// GetUserByEmail looks up a user by email within the tenant in ctx. Used by
// the invite-by-email flow.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (model.User, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return model.User{}, fmt.Errorf("postgres: tenant_id missing from context")
	}
	var u model.User
	// users has no RLS; tenant scoping is explicit in the WHERE clause.
	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, kinde_sub, email, name, created_at, last_seen
		FROM users
		WHERE tenant_id = $1 AND lower(email) = lower($2)`,
		tenantID, email,
	).Scan(&u.ID, &u.TenantID, &u.KindeSub, &u.Email, &u.Name, &u.CreatedAt, &u.LastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, storage.ErrUserNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("postgres: get user by email: %w", err)
	}
	return u, nil
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
