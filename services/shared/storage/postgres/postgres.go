// Package postgres implements the Store interface using PostgreSQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// SaveGhosts replaces the tenant's ghost records for the specified accounts with the latest detection results.
func (s *Store) SaveGhosts(ctx context.Context, ghosts []model.GhostResource) error {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return fmt.Errorf("postgres: tenant_id missing from context")
	}

	if len(ghosts) == 0 {
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

	// Get unique internal account IDs from the ghosts being saved
	accountIDs := make(map[string]bool)
	for _, g := range ghosts {
		if g.InternalAccountID != "" {
			accountIDs[g.InternalAccountID] = true
		}
	}

	// Delete existing ghost records only for the accounts being updated
	for accountID := range accountIDs {
		if _, err := tx.Exec(ctx, `DELETE FROM ghost_records WHERE internal_account_id = $1`, accountID); err != nil {
			return fmt.Errorf("postgres: clear ghosts for account %s: %w", accountID, err)
		}
	}

	now := time.Now().UTC()
	for _, g := range ghosts {
		tags, err := json.Marshal(g.Tags)
		if err != nil {
			return fmt.Errorf("postgres: marshal tags: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO ghost_records
				(tenant_id, provider, account_id, internal_account_id, service, resource_type, region, resource_id, arn, tags,
				 monthly_cost, currency, period_start, period_end,
				 usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
			tenantID,
			g.Provider, g.AccountID, g.InternalAccountID, g.Service, g.ResourceType, g.Region, g.ResourceID, g.ARN, string(tags),
			g.MonthlyCost, g.Currency, g.PeriodStart, g.PeriodEnd,
			g.UsageMetric, g.UsageAvg, g.UsageUnit, g.Reason, g.Owner, now,
		)
		if err != nil {
			return fmt.Errorf("postgres: insert ghost: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// LoadGhosts returns ghost records for the tenant in ctx.
func (s *Store) LoadGhosts(ctx context.Context) ([]model.GhostResource, error) {
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
		FROM ghost_records
	`)
	if err != nil {
		return nil, fmt.Errorf("postgres: query ghosts: %w", err)
	}
	defer rows.Close()

	var ghosts []model.GhostResource
	for rows.Next() {
		var g model.GhostResource
		var tagsJSON []byte
		var internalAccountID *string
		if err := rows.Scan(
			&g.Provider, &g.AccountID, &internalAccountID, &g.Service, &g.ResourceType, &g.Region, &g.ResourceID, &g.ARN, &tagsJSON,
			&g.MonthlyCost, &g.Currency, &g.PeriodStart, &g.PeriodEnd,
			&g.UsageMetric, &g.UsageAvg, &g.UsageUnit, &g.Reason, &g.Owner,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan ghost: %w", err)
		}
		if internalAccountID != nil {
			g.InternalAccountID = *internalAccountID
		}
		if err := json.Unmarshal(tagsJSON, &g.Tags); err != nil {
			g.Tags = map[string]string{}
		}
		ghosts = append(ghosts, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ghosts, tx.Commit(ctx)
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
				 usage_metric, usage_avg, usage_unit, is_ghost, reason, owner, detected_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
			tenantID,
			r.Provider, r.AccountID, r.InternalAccountID, r.Service, r.ResourceType, r.Region, r.ResourceID, r.ARN, string(tags),
			r.MonthlyCost, r.Currency, r.PeriodStart, r.PeriodEnd,
			r.UsageMetric, r.UsageAvg, r.UsageUnit, r.IsGhost, r.Reason, r.Owner, now,
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
		       usage_metric, usage_avg, usage_unit, is_ghost, reason, owner
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
			&r.UsageMetric, &r.UsageAvg, &r.UsageUnit, &r.IsGhost, &r.Reason, &r.Owner,
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

	if filter.InternalAccountID != "" {
		query += fmt.Sprintf(" AND internal_account_id = $%d", argN)
		args = append(args, filter.InternalAccountID)
		argN++
	}
	if filter.Service != "" {
		query += fmt.Sprintf(" AND service = $%d", argN)
		args = append(args, filter.Service)
		argN++
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

// SaveSnapshot writes a ghost snapshot record (one per scan run).
func (s *Store) SaveSnapshot(ctx context.Context, snap model.GhostSnapshot) error {
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
		INSERT INTO ghost_snapshots
			(id, tenant_id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		snap.ID, tenantID, snap.AccountID, snap.SnapshotAt,
		snap.GhostCount, snap.TotalMonthlyCost, snap.Currency,
	)
	if err != nil {
		return fmt.Errorf("postgres: insert ghost_snapshot: %w", err)
	}
	return tx.Commit(ctx)
}

// ListSnapshots returns ghost snapshots for the tenant, oldest-first.
// If accountID is non-empty, only snapshots for that account are returned.
func (s *Store) ListSnapshots(ctx context.Context, accountID string) ([]model.GhostSnapshot, error) {
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
			SELECT id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency
			FROM ghost_snapshots
			WHERE account_id = $1
			ORDER BY snapshot_at ASC`,
			accountID,
		)
	} else {
		rows, err = tx.Query(ctx, `
			SELECT id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency
			FROM ghost_snapshots
			ORDER BY snapshot_at ASC`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: query ghost_snapshots: %w", err)
	}
	defer rows.Close()

	var snaps []model.GhostSnapshot
	for rows.Next() {
		var snap model.GhostSnapshot
		if err := rows.Scan(
			&snap.ID, &snap.AccountID, &snap.SnapshotAt,
			&snap.GhostCount, &snap.TotalMonthlyCost, &snap.Currency,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan ghost_snapshot: %w", err)
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
			INSERT INTO ghost_snapshot_services
				(id, snapshot_id, tenant_id, service, resource_type, ghost_count, monthly_cost, currency)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			svc.ID, svc.SnapshotID, tenantID, svc.Service, svc.ResourceType,
			svc.GhostCount, svc.MonthlyCost, svc.Currency,
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
func (s *Store) ListSnapshotsByService(ctx context.Context, service, resourceType, accountID string) ([]model.GhostSnapshot, error) {
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
		       SUM(gss.ghost_count)::int, SUM(gss.monthly_cost), gss.currency
		FROM ghost_snapshots gs
		JOIN ghost_snapshot_services gss ON gss.snapshot_id = gs.id
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

	var snaps []model.GhostSnapshot
	for rows.Next() {
		var snap model.GhostSnapshot
		if err := rows.Scan(
			&snap.ID, &snap.AccountID, &snap.SnapshotAt,
			&snap.GhostCount, &snap.TotalMonthlyCost, &snap.Currency,
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
		SELECT DISTINCT service FROM ghost_snapshot_services ORDER BY service`)
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
		SELECT DISTINCT resource_type FROM ghost_snapshot_services
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

// DismissGhost inserts a new dismiss or snooze record for a ghost resource.
// Returns ErrAlreadyDismissed if an active dismissal already exists for the fingerprint
// (enforced by the partial unique index dismissed_ghosts_active_fingerprint).
func (s *Store) DismissGhost(ctx context.Context, d model.DismissAction) (int64, error) {
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
		INSERT INTO dismissed_ghosts
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
		return 0, fmt.Errorf("postgres: insert dismissed_ghost: %w", err)
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
		UPDATE dismissed_ghosts
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
			FROM   dismissed_ghosts
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
			FROM   dismissed_ghosts
			WHERE  revoked_at IS NULL
			  AND  (action = 'dismiss' OR snoozed_until > NOW())
			ORDER BY created_at DESC`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: query dismissed_ghosts: %w", err)
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
			return nil, fmt.Errorf("postgres: scan dismissed_ghost: %w", err)
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
		UPDATE dismissed_ghosts
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
