// Package postgres implements the Store interface using PostgreSQL.
// Used in production — swap for SQLite in development by omitting DATABASE_URL.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"axiaops.io/shared/model"
)

const schema = `
CREATE TABLE IF NOT EXISTS tenants (
    id         TEXT        PRIMARY KEY,
    org_code   TEXT        NOT NULL UNIQUE,
    name       TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id         TEXT        PRIMARY KEY,
    tenant_id  TEXT        NOT NULL REFERENCES tenants(id),
    kinde_sub  TEXT        NOT NULL UNIQUE,
    email      TEXT        NOT NULL DEFAULT '',
    name       TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    last_seen  TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS cost_records (
    id           BIGSERIAL   PRIMARY KEY,
    provider     TEXT        NOT NULL,
    account_id   TEXT        NOT NULL,
    service      TEXT        NOT NULL,
    region       TEXT        NOT NULL,
    resource_id  TEXT,
    amount       NUMERIC     NOT NULL,
    currency     TEXT        NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end   TIMESTAMPTZ NOT NULL,
    tags         JSONB,
    fetched_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (provider, account_id, service, region, period_start, period_end)
);

CREATE TABLE IF NOT EXISTS ghost_records (
    id           BIGSERIAL   PRIMARY KEY,
    tenant_id    TEXT        REFERENCES tenants(id),
    provider     TEXT        NOT NULL,
    account_id   TEXT        NOT NULL,
    service      TEXT        NOT NULL,
    region       TEXT        NOT NULL,
    resource_id  TEXT        NOT NULL,
    tags         JSONB,
    monthly_cost NUMERIC     NOT NULL,
    currency     TEXT        NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end   TIMESTAMPTZ NOT NULL,
    usage_metric TEXT        NOT NULL,
    usage_avg    NUMERIC     NOT NULL,
    usage_unit   TEXT        NOT NULL,
    reason       TEXT        NOT NULL,
    owner        TEXT        NOT NULL,
    detected_at  TIMESTAMPTZ NOT NULL
);`

// Store is a PostgreSQL-backed implementation of storage.Store.
type Store struct {
	pool *pgxpool.Pool
}

// New connects to PostgreSQL using the given connection URL and applies the schema.
// URL format: postgres://user:password@host:5432/dbname
func New(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("postgres: apply schema: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Save inserts cost records in a single transaction, skipping duplicates.
func (s *Store) Save(ctx context.Context, records []model.CostRecord) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var inserted int64
	for _, r := range records {
		tags, err := json.Marshal(r.Tags)
		if err != nil {
			return 0, fmt.Errorf("postgres: marshal tags: %w", err)
		}
		res, err := tx.Exec(ctx, `
			INSERT INTO cost_records
				(provider, account_id, service, region, resource_id, amount, currency,
				 period_start, period_end, tags, fetched_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (provider, account_id, service, region, period_start, period_end)
			DO NOTHING`,
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

// SaveGhosts replaces all ghost records with the latest detection results.
func (s *Store) SaveGhosts(ctx context.Context, ghosts []model.GhostResource) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

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
				(provider, account_id, service, region, resource_id, tags,
				 monthly_cost, currency, period_start, period_end,
				 usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
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

// LoadGhosts returns all ghost records from the last ingestion run.
func (s *Store) LoadGhosts(ctx context.Context) ([]model.GhostResource, error) {
	rows, err := s.pool.Query(ctx, `
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
	return ghosts, rows.Err()
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

func (s *Store) Close() error {
	s.pool.Close()
	return nil
}
