package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate runs all pending database migrations.
// Safe to call on every startup — already-applied migrations are skipped.
// Uses an advisory lock so concurrent calls (e.g. api + ingestion starting together) are safe.
func Migrate(dbURL string) error {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return fmt.Errorf("migrate: open db: %w", err)
	}
	defer func() {
		if cerr := db.Close(); cerr != nil {
			err = fmt.Errorf("migrate: close db: %w", cerr)
		}
	}()

	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrate: load source: %w", err)
	}

	driver, err := migratepg.WithInstance(db, &migratepg.Config{
		SchemaName:      "public",
		MigrationsTable: "schema_migrations",
	})
	if err != nil {
		return fmt.Errorf("migrate: driver init: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return fmt.Errorf("migrate: init: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			err = fmt.Errorf("migrate: close source: %w", srcErr)
		} else if dbErr != nil {
			err = fmt.Errorf("migrate: close db driver: %w", dbErr)
		}
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}

// ResetStuckScans resets accounts stuck in "scanning" status for longer than
// stuckAfter back to "error". Uses the admin URL (superuser) which bypasses
// RLS — safe to call on startup and from a periodic background ticker.
func ResetStuckScans(ctx context.Context, adminURL string, stuckAfter time.Duration) (int64, error) {
	pool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		return 0, fmt.Errorf("postgres: admin connect: %w", err)
	}
	defer pool.Close()

	cutoff := time.Now().UTC().Add(-stuckAfter)
	tag, err := pool.Exec(ctx, `
		UPDATE axiaops.accounts SET status = 'error'
		WHERE status = 'scanning' AND last_scanned_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("postgres: reset stuck scans: %w", err)
	}
	return tag.RowsAffected(), nil
}
