// Package sqlite implements the Store interface using a local SQLite database.
// Used in development — swap for PostgreSQL in production without changing
// any other code.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

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

func (s *Store) Close() error {
	return s.db.Close()
}
