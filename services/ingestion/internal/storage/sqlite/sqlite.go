// Package sqlite implements the Store interface using a local SQLite database.
// Used in development — swap for PostgreSQL in production without changing
// any other code.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"axiaops.io/ingestion/internal/model"
)

const schema = `
CREATE TABLE IF NOT EXISTS cost_records (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    provider     TEXT    NOT NULL,
    account_id   TEXT    NOT NULL,
    service      TEXT    NOT NULL,
    region       TEXT    NOT NULL,
    resource_id  TEXT,
    amount       REAL    NOT NULL,
    currency     TEXT    NOT NULL,
    period_start DATETIME NOT NULL,
    period_end   DATETIME NOT NULL,
    tags         TEXT,
    fetched_at   DATETIME NOT NULL,
    UNIQUE(provider, account_id, service, region, period_start, period_end)
);

CREATE TABLE IF NOT EXISTS tenants (
    id         TEXT PRIMARY KEY,
    org_code   TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL REFERENCES tenants(id),
    kinde_sub  TEXT NOT NULL UNIQUE,
    email      TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL,
    last_seen  DATETIME NOT NULL
);`

// Store is a SQLite-backed implementation of storage.Store.
type Store struct {
	db *sql.DB
}

// New opens (or creates) a SQLite database at the given path and applies
// the schema.
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("sqlite: apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Save inserts cost records in a single transaction, skipping duplicates.
// Returns the number of records actually inserted.
func (s *Store) Save(ctx context.Context, records []model.CostRecord) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO cost_records
			(provider, account_id, service, region, resource_id, amount, currency,
			 period_start, period_end, tags, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, fmt.Errorf("sqlite: prepare: %w", err)
	}
	defer stmt.Close()

	var inserted int64
	for _, r := range records {
		tags, err := json.Marshal(r.Tags)
		if err != nil {
			return 0, fmt.Errorf("sqlite: marshal tags: %w", err)
		}
		res, err := stmt.ExecContext(ctx,
			r.Provider, r.AccountID, r.Service, r.Region, r.ResourceID,
			r.Amount, r.Currency,
			r.PeriodStart, r.PeriodEnd,
			string(tags), r.FetchedAt,
		)
		if err != nil {
			return 0, fmt.Errorf("sqlite: insert: %w", err)
		}
		n, _ := res.RowsAffected()
		inserted += n
	}

	return inserted, tx.Commit()
}

// UpsertTenant creates a tenant on first login or returns the existing one.
func (s *Store) UpsertTenant(ctx context.Context, orgCode, name string) (model.Tenant, error) {
	now := time.Now().UTC()
	id := uuid.New().String()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tenants (id, org_code, name, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(org_code) DO UPDATE SET name = excluded.name
	`, id, orgCode, name, now)
	if err != nil {
		return model.Tenant{}, fmt.Errorf("sqlite: upsert tenant: %w", err)
	}

	var t model.Tenant
	err = s.db.QueryRowContext(ctx,
		`SELECT id, org_code, name, created_at FROM tenants WHERE org_code = ?`, orgCode,
	).Scan(&t.ID, &t.OrgCode, &t.Name, &t.CreatedAt)
	if err != nil {
		return model.Tenant{}, fmt.Errorf("sqlite: fetch tenant: %w", err)
	}
	return t, nil
}

// UpsertUser creates a user on first login or updates last_seen on subsequent logins.
func (s *Store) UpsertUser(ctx context.Context, tenantID, kindeSub, email, name string) (model.User, error) {
	now := time.Now().UTC()
	id := uuid.New().String()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, tenant_id, kinde_sub, email, name, created_at, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(kinde_sub) DO UPDATE SET
			email     = excluded.email,
			name      = excluded.name,
			last_seen = excluded.last_seen
	`, id, tenantID, kindeSub, email, name, now, now)
	if err != nil {
		return model.User{}, fmt.Errorf("sqlite: upsert user: %w", err)
	}

	var u model.User
	err = s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, kinde_sub, email, name, created_at, last_seen FROM users WHERE kinde_sub = ?`, kindeSub,
	).Scan(&u.ID, &u.TenantID, &u.KindeSub, &u.Email, &u.Name, &u.CreatedAt, &u.LastSeen)
	if err != nil {
		return model.User{}, fmt.Errorf("sqlite: fetch user: %w", err)
	}
	return u, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
