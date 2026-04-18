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
	"github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Bootstrap ensures the application database user exists and its password matches
// the one in appURL. Must be called before Migrate on every startup.
// Connects as the owner (ownerURL) to create/update the user, then syncs the
// password from DATABASE_URL — enabling credential rotation without manual steps.
// Uses advisory lock to prevent concurrent bootstrap calls.
func Bootstrap(ownerURL, appURL string) error {
	// Parse the app user's password out of DATABASE_URL.
	appCfg, err := pgxpool.ParseConfig(appURL)
	if err != nil {
		return fmt.Errorf("bootstrap: parse app url: %w", err)
	}
	appPassword := appCfg.ConnConfig.Password
	if appPassword == "" {
		return fmt.Errorf("bootstrap: DATABASE_URL contains no password")
	}

	db, err := sql.Open("postgres", ownerURL)
	if err != nil {
		return fmt.Errorf("bootstrap: open db: %w", err)
	}
	defer db.Close()

	// Acquire advisory lock to prevent concurrent bootstrap calls
	// Lock ID 123456789 is arbitrary but consistent across all bootstrap calls
	if _, err := db.Exec(`SELECT pg_advisory_lock(123456789)`); err != nil {
		return fmt.Errorf("bootstrap: acquire lock: %w", err)
	}
	defer func() {
		if err := db.Exec(`SELECT pg_advisory_unlock(123456789)`); err != nil {
			// Log but don't return error - unlock should not fail
			_ = fmt.Errorf("bootstrap: release lock: %w", err)
		}
	}()

	// Create the app user if it does not exist yet (password set below).
	if _, err := db.Exec(`DO $$ BEGIN
		IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'axiaops') THEN
			CREATE USER axiaops;
		END IF;
	END $$`); err != nil {
		return fmt.Errorf("bootstrap: create user: %w", err)
	}

	// Sync password from DATABASE_URL — idempotent, supports credential rotation.
	// ALTER USER does not accept protocol-level parameters ($1), so the password
	// is embedded as a safely-escaped literal via pq.QuoteLiteral.
	if _, err := db.Exec(`ALTER USER axiaops WITH PASSWORD ` + pq.QuoteLiteral(appPassword)); err != nil {
		return fmt.Errorf("bootstrap: set password: %w", err)
	}

	return nil
}

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
