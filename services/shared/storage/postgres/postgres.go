// Package postgres implements the Store interface using PostgreSQL.
// Used in production — swap for SQLite in development by omitting DATABASE_URL.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// Store is a PostgreSQL-backed implementation of storage.Store.
type Store struct {
	pool *pgxpool.Pool
}

// New connects to PostgreSQL as the application user (axiaops).
// Schema and tables are created by init.sql on first Docker startup — not here.
// search_path is set to axiaops on every connection via AfterConnect.
// URL format: postgres://axiaops:axiaops@host:5432/dbname
func New(ctx context.Context, url string) (*Store, error) {
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
	return &Store{pool: pool}, nil
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
	defer tx.Rollback(ctx)

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
				(tenant_id, provider, account_id, service, region, resource_id, amount, currency,
				 period_start, period_end, tags, fetched_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (tenant_id, provider, account_id, service, region, period_start, period_end)
			DO NOTHING`,
			tenantID,
			r.Provider, r.AccountID, r.Service, r.Region, r.ResourceID,
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

// SaveGhosts replaces the tenant's ghost records with the latest detection results.
func (s *Store) SaveGhosts(ctx context.Context, ghosts []model.GhostResource) error {
	tenantID := storage.TenantIDFromCtx(ctx)
	if tenantID == "" {
		return fmt.Errorf("postgres: tenant_id missing from context")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := setTenant(ctx, tx); err != nil {
		return err
	}

	// RLS ensures only this tenant's rows are deleted.
	if _, err := tx.Exec(ctx, `DELETE FROM ghost_records`); err != nil {
		return fmt.Errorf("postgres: clear ghosts: %w", err)
	}

	now := time.Now().UTC()
	for _, g := range ghosts {
		tags, err := json.Marshal(g.Tags)
		if err != nil {
			return fmt.Errorf("postgres: marshal tags: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO ghost_records
				(tenant_id, provider, account_id, service, region, resource_id, tags,
				 monthly_cost, currency, period_start, period_end,
				 usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
			tenantID,
			g.Provider, g.AccountID, g.Service, g.Region, g.ResourceID, string(tags),
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
	defer tx.Rollback(ctx)

	if err := setTenant(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT provider, account_id, service, region, resource_id, tags,
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
		if err := rows.Scan(
			&g.Provider, &g.AccountID, &g.Service, &g.Region, &g.ResourceID, &tagsJSON,
			&g.MonthlyCost, &g.Currency, &g.PeriodStart, &g.PeriodEnd,
			&g.UsageMetric, &g.UsageAvg, &g.UsageUnit, &g.Reason, &g.Owner,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan ghost: %w", err)
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
	defer tx.Rollback(ctx)

	if err := setTenant(ctx, tx); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO accounts
			(id, tenant_id, provider, label, access_key_id, secret_encrypted, region, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			label            = EXCLUDED.label,
			access_key_id    = EXCLUDED.access_key_id,
			secret_encrypted = EXCLUDED.secret_encrypted,
			region           = EXCLUDED.region,
			status           = EXCLUDED.status`,
		a.ID, a.TenantID, a.Provider, a.Label,
		a.AccessKeyID, a.SecretEncrypted, a.Region, a.Status, a.CreatedAt,
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
	defer tx.Rollback(ctx)

	if err := setTenant(ctx, tx); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, provider, label, access_key_id, secret_encrypted,
		       region, status, last_scanned_at, created_at
		FROM accounts ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []model.Account
	for rows.Next() {
		var a model.Account
		if err := rows.Scan(
			&a.ID, &a.TenantID, &a.Provider, &a.Label, &a.AccessKeyID, &a.SecretEncrypted,
			&a.Region, &a.Status, &a.LastScannedAt, &a.CreatedAt,
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
	defer tx.Rollback(ctx)

	if err := setTenant(ctx, tx); err != nil {
		return model.Account{}, err
	}

	var a model.Account
	err = tx.QueryRow(ctx, `
		SELECT id, tenant_id, provider, label, access_key_id, secret_encrypted,
		       region, status, last_scanned_at, created_at
		FROM accounts WHERE id = $1`, id,
	).Scan(&a.ID, &a.TenantID, &a.Provider, &a.Label, &a.AccessKeyID, &a.SecretEncrypted,
		&a.Region, &a.Status, &a.LastScannedAt, &a.CreatedAt)
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
	defer tx.Rollback(ctx)

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
	defer tx.Rollback(ctx)

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

func (s *Store) Close() error {
	s.pool.Close()
	return nil
}
