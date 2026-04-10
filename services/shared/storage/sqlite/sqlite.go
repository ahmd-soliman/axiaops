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

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
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
);

CREATE TABLE IF NOT EXISTS accounts (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL REFERENCES tenants(id),
    provider          TEXT NOT NULL,
    label             TEXT NOT NULL DEFAULT '',
    access_key_id     TEXT NOT NULL,
    secret_encrypted  TEXT NOT NULL,
    region            TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'connected',
    last_scanned_at   DATETIME,
    created_at        DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS resource_records (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    provider     TEXT    NOT NULL,
    account_id   TEXT    NOT NULL,
    service      TEXT    NOT NULL,
    region       TEXT    NOT NULL,
    resource_id  TEXT    NOT NULL,
    tags         TEXT,
    monthly_cost REAL    NOT NULL,
    currency     TEXT    NOT NULL,
    period_start DATETIME NOT NULL,
    period_end   DATETIME NOT NULL,
    usage_metric TEXT    NOT NULL DEFAULT '',
    usage_avg    REAL    NOT NULL DEFAULT 0,
    usage_unit   TEXT    NOT NULL DEFAULT '',
    is_ghost     INTEGER NOT NULL DEFAULT 0,
    reason       TEXT    NOT NULL DEFAULT '',
    owner        TEXT    NOT NULL DEFAULT '',
    detected_at  DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS ghost_records (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    provider     TEXT    NOT NULL,
    account_id   TEXT    NOT NULL,
    service      TEXT    NOT NULL,
    region       TEXT    NOT NULL,
    resource_id  TEXT    NOT NULL,
    tags         TEXT,
    monthly_cost REAL    NOT NULL,
    currency     TEXT    NOT NULL,
    period_start DATETIME NOT NULL,
    period_end   DATETIME NOT NULL,
    usage_metric TEXT    NOT NULL,
    usage_avg    REAL    NOT NULL,
    usage_unit   TEXT    NOT NULL,
    reason       TEXT    NOT NULL,
    owner        TEXT    NOT NULL,
    detected_at  DATETIME NOT NULL
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

// SaveGhosts replaces all ghost records with the latest detection results.
func (s *Store) SaveGhosts(ctx context.Context, ghosts []model.GhostResource) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM ghost_records`); err != nil {
		return fmt.Errorf("sqlite: clear ghosts: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO ghost_records
			(provider, account_id, service, region, resource_id, tags,
			 monthly_cost, currency, period_start, period_end,
			 usage_metric, usage_avg, usage_unit, reason, owner, detected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("sqlite: prepare: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, g := range ghosts {
		tags, err := json.Marshal(g.Tags)
		if err != nil {
			return fmt.Errorf("sqlite: marshal tags: %w", err)
		}
		_, err = stmt.ExecContext(ctx,
			g.Provider, g.AccountID, g.Service, g.Region, g.ResourceID, string(tags),
			g.MonthlyCost, g.Currency, g.PeriodStart, g.PeriodEnd,
			g.UsageMetric, g.UsageAvg, g.UsageUnit, g.Reason, g.Owner, now,
		)
		if err != nil {
			return fmt.Errorf("sqlite: insert ghost: %w", err)
		}
	}

	return tx.Commit()
}

// LoadGhosts returns all ghost records from the last ingestion run.
func (s *Store) LoadGhosts(ctx context.Context) ([]model.GhostResource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, account_id, service, region, resource_id, tags,
		       monthly_cost, currency, period_start, period_end,
		       usage_metric, usage_avg, usage_unit, reason, owner
		FROM ghost_records
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query ghosts: %w", err)
	}
	defer rows.Close()

	var ghosts []model.GhostResource
	for rows.Next() {
		var g model.GhostResource
		var tagsJSON string
		if err := rows.Scan(
			&g.Provider, &g.AccountID, &g.Service, &g.Region, &g.ResourceID, &tagsJSON,
			&g.MonthlyCost, &g.Currency, &g.PeriodStart, &g.PeriodEnd,
			&g.UsageMetric, &g.UsageAvg, &g.UsageUnit, &g.Reason, &g.Owner,
		); err != nil {
			return nil, fmt.Errorf("sqlite: scan ghost: %w", err)
		}
		if err := json.Unmarshal([]byte(tagsJSON), &g.Tags); err != nil {
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

// SaveAccount inserts or replaces a connected cloud account.
func (s *Store) SaveAccount(ctx context.Context, a model.Account) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO accounts
			(id, tenant_id, provider, label, access_key_id, secret_encrypted, region, status, last_scanned_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider         = excluded.provider,
			label            = excluded.label,
			access_key_id    = excluded.access_key_id,
			secret_encrypted = excluded.secret_encrypted,
			region           = excluded.region,
			status           = excluded.status,
			last_scanned_at  = excluded.last_scanned_at
	`, a.ID, a.TenantID, a.Provider, a.Label, a.AccessKeyID, a.SecretEncrypted,
		a.Region, a.Status, a.LastScannedAt, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("sqlite: save account: %w", err)
	}
	return nil
}

// ListAccounts returns all accounts for the tenant in ctx.
func (s *Store) ListAccounts(ctx context.Context) ([]model.Account, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, provider, label, access_key_id, region, status, last_scanned_at, created_at
		FROM accounts WHERE tenant_id = ?
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list accounts: %w", err)
	}
	defer rows.Close()

	var accounts []model.Account
	for rows.Next() {
		var a model.Account
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Provider, &a.Label, &a.AccessKeyID,
			&a.Region, &a.Status, &a.LastScannedAt, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan account: %w", err)
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

// GetAccount returns a single account by ID for the tenant in ctx.
func (s *Store) GetAccount(ctx context.Context, id string) (model.Account, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	var a model.Account
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, provider, label, access_key_id, secret_encrypted, region, status, last_scanned_at, created_at
		FROM accounts WHERE id = ? AND tenant_id = ?
	`, id, tenantID).Scan(&a.ID, &a.TenantID, &a.Provider, &a.Label, &a.AccessKeyID,
		&a.SecretEncrypted, &a.Region, &a.Status, &a.LastScannedAt, &a.CreatedAt)
	if err == sql.ErrNoRows {
		return model.Account{}, fmt.Errorf("sqlite: account not found")
	}
	if err != nil {
		return model.Account{}, fmt.Errorf("sqlite: get account: %w", err)
	}
	return a, nil
}

// DeleteAccount removes an account by ID for the tenant in ctx.
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	tenantID := storage.TenantIDFromCtx(ctx)
	_, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ? AND tenant_id = ?`, id, tenantID)
	if err != nil {
		return fmt.Errorf("sqlite: delete account: %w", err)
	}
	return nil
}

// UpdateAccountStatus sets the status and last_scanned_at for an account.
func (s *Store) UpdateAccountStatus(ctx context.Context, id, status string) error {
	tenantID := storage.TenantIDFromCtx(ctx)
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET status = ?, last_scanned_at = ? WHERE id = ? AND tenant_id = ?`,
		status, now, id, tenantID)
	if err != nil {
		return fmt.Errorf("sqlite: update account status: %w", err)
	}
	return nil
}

// TryMarkAccountScanning sets status to scanning only if the account is not already scanning.
func (s *Store) TryMarkAccountScanning(ctx context.Context, id string) (bool, error) {
	tenantID := storage.TenantIDFromCtx(ctx)
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE accounts SET status = ?, last_scanned_at = ?
		WHERE id = ? AND tenant_id = ? AND status != ?`,
		"scanning", now, id, tenantID, "scanning")
	if err != nil {
		return false, fmt.Errorf("sqlite: try mark account scanning: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite: rows affected: %w", err)
	}
	return n == 1, nil
}

// SaveResources replaces all resource records with the latest inventory.
func (s *Store) SaveResources(ctx context.Context, resources []model.ResourceRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM resource_records`); err != nil {
		return fmt.Errorf("sqlite: clear resource_records: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO resource_records
			(provider, account_id, service, region, resource_id, tags,
			 monthly_cost, currency, period_start, period_end,
			 usage_metric, usage_avg, usage_unit, is_ghost, reason, owner, detected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("sqlite: prepare: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, r := range resources {
		tags, err := json.Marshal(r.Tags)
		if err != nil {
			return fmt.Errorf("sqlite: marshal tags: %w", err)
		}
		isGhost := 0
		if r.IsGhost {
			isGhost = 1
		}
		_, err = stmt.ExecContext(ctx,
			r.Provider, r.AccountID, r.Service, r.Region, r.ResourceID, string(tags),
			r.MonthlyCost, r.Currency, r.PeriodStart, r.PeriodEnd,
			r.UsageMetric, r.UsageAvg, r.UsageUnit, isGhost, r.Reason, r.Owner, now,
		)
		if err != nil {
			return fmt.Errorf("sqlite: insert resource_record: %w", err)
		}
	}

	return tx.Commit()
}

// LoadResources returns all resource records from the last ingestion run.
func (s *Store) LoadResources(ctx context.Context) ([]model.ResourceRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT provider, account_id, service, region, resource_id, tags,
		       monthly_cost, currency, period_start, period_end,
		       usage_metric, usage_avg, usage_unit, is_ghost, reason, owner
		FROM resource_records
	`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: query resource_records: %w", err)
	}
	defer rows.Close()

	var resources []model.ResourceRecord
	for rows.Next() {
		var r model.ResourceRecord
		var tagsJSON string
		var isGhost int
		if err := rows.Scan(
			&r.Provider, &r.AccountID, &r.Service, &r.Region, &r.ResourceID, &tagsJSON,
			&r.MonthlyCost, &r.Currency, &r.PeriodStart, &r.PeriodEnd,
			&r.UsageMetric, &r.UsageAvg, &r.UsageUnit, &isGhost, &r.Reason, &r.Owner,
		); err != nil {
			return nil, fmt.Errorf("sqlite: scan resource_record: %w", err)
		}
		r.IsGhost = isGhost == 1
		if err := json.Unmarshal([]byte(tagsJSON), &r.Tags); err != nil {
			r.Tags = map[string]string{}
		}
		resources = append(resources, r)
	}
	return resources, rows.Err()
}

// SaveSnapshot is a no-op in SQLite — snapshots are only supported in PostgreSQL.
func (s *Store) SaveSnapshot(_ context.Context, _ model.GhostSnapshot) error {
	return nil
}

// ListSnapshots returns nil in SQLite — snapshots are only supported in PostgreSQL.
func (s *Store) ListSnapshots(_ context.Context, _ string) ([]model.GhostSnapshot, error) {
	return nil, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
