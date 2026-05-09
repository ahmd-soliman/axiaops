package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Bootstrap ensures the prerequisites for Migrate are in place:
//   - the application database user exists and its password matches appURL
//   - the axiaops schema exists so golang-migrate can create its
//     schema_migrations tracking table inside it
//   - axiaops.migration_history table + view + grants exist (must be created
//     here, before 000_init.up.sql, because the ALTER DEFAULT PRIVILEGES in
//     000_init applies only to tables created after that statement runs —
//     migration_history pre-dates it and therefore needs an explicit GRANT.
//     See docs/migration-history-table-design.md §Bootstrap.)
//
// Must be called before Migrate on every startup. Connects as the owner
// (ownerURL) to create/update the user and schema, then syncs the password
// from DATABASE_URL — enabling credential rotation without manual steps.
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
	defer func() {
		if cerr := db.Close(); cerr != nil {
			slog.Warn("bootstrap: close db failed", "error", cerr)
		}
	}()

	// Acquire the wrapper-level advisory lock so concurrent boots serialise
	// on Bootstrap. Lock ID is the sibling of migrationHistoryLockID — see
	// migration_history.go for the naming convention.
	if _, err := db.Exec(`SELECT pg_advisory_lock($1)`, bootstrapAdvisoryLockID); err != nil {
		return fmt.Errorf("bootstrap: acquire lock: %w", err)
	}
	defer func() {
		// pg_advisory_unlock returns bool. Scan it so a backend reconnect
		// (which would silently land us in a session that doesn't hold
		// the lock) is observable instead of invisible.
		var unlocked bool
		if uErr := db.QueryRow(`SELECT pg_advisory_unlock($1)`, bootstrapAdvisoryLockID).Scan(&unlocked); uErr != nil {
			slog.Warn("bootstrap: release lock failed", "error", uErr)
		} else if !unlocked {
			slog.Warn("bootstrap: pg_advisory_unlock returned false; backend likely reconnected mid-Bootstrap")
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

	// Create the axiaops schema if it does not exist yet. This must happen
	// before Migrate() runs, because golang-migrate creates its
	// schema_migrations tracking table inside this schema before applying the
	// first migration — chicken-and-egg otherwise.
	// Owned by the connecting role (axiaops_owner), not the app user.
	if _, err := db.Exec(`CREATE SCHEMA IF NOT EXISTS axiaops`); err != nil {
		return fmt.Errorf("bootstrap: create schema: %w", err)
	}

	// Create migration_history (table + view + grants) before golang-migrate
	// gets a chance to run 000_init. Idempotent: every statement is IF NOT
	// EXISTS / OR REPLACE.
	if _, err := db.Exec(migrationHistoryDDL); err != nil {
		return fmt.Errorf("bootstrap: migration history ddl: %w", err)
	}

	return nil
}

// Migrate applies all pending up migrations one step at a time, recording each
// in axiaops.migration_history (see docs/migration-history-table-design.md).
// Safe to call on every startup — already-applied migrations are skipped.
// A wrapper-level session advisory lock serialises concurrent wrappers; the
// orphan-recovery / backfill / drift-detection passes run inside that lock.
func Migrate(migrationURL string) error {
	ctx := context.Background()
	idx, err := indexEmbeddedMigrations(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrate: index embedded migrations: %w", err)
	}
	ident := resolveRuntimeIdentity()

	return withHistoryConn(ctx, migrationURL, func(conn *sql.Conn) error {
		if err := recoverOrphans(ctx, conn); err != nil {
			return err
		}
		if err := backfillIfEmpty(ctx, conn, migrationsFS, idx, ident); err != nil {
			return err
		}
		if err := detectDrift(ctx, conn, migrationsFS, idx); err != nil {
			return err
		}
		m, closeFn, err := openMigrate(migrationURL)
		if err != nil {
			return err
		}
		defer closeFn()
		return runUpLoop(ctx, conn, m, migrationsFS, idx, ident)
	})
}

// MigrateDown rolls back n steps with full history recording. Operator-only;
// invoked via `axiaopsctl migrate down N`.
func MigrateDown(migrationURL string, n int) error {
	ctx := context.Background()
	idx, err := indexEmbeddedMigrations(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrate down: index embedded migrations: %w", err)
	}
	ident := resolveRuntimeIdentity()

	return withHistoryConn(ctx, migrationURL, func(conn *sql.Conn) error {
		if err := recoverOrphans(ctx, conn); err != nil {
			return err
		}
		m, closeFn, err := openMigrate(migrationURL)
		if err != nil {
			return err
		}
		defer closeFn()
		return runDownSteps(ctx, conn, m, migrationsFS, idx, n, ident)
	})
}

// MigrateForce runs migrate.Force(version) and writes a single terminal
// 'force' history row. No DDL is executed — only schema_migrations is
// rewritten. Operator-only; invoked via `axiaopsctl migrate force N`.
func MigrateForce(migrationURL string, version int) error {
	ctx := context.Background()
	idx, err := indexEmbeddedMigrations(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrate force: index embedded migrations: %w", err)
	}
	ident := resolveRuntimeIdentity()

	return withHistoryConn(ctx, migrationURL, func(conn *sql.Conn) error {
		if err := recoverOrphans(ctx, conn); err != nil {
			return err
		}
		m, closeFn, err := openMigrate(migrationURL)
		if err != nil {
			return err
		}
		defer closeFn()
		if err := m.Force(version); err != nil {
			return fmt.Errorf("migrate force: %w", err)
		}
		return recordForce(ctx, conn, int64(version), idx, ident)
	})
}

// openMigrate returns a *migrate.Migrate built against the embedded migrations
// FS and a *sql.DB owned by golang-migrate for the call's lifetime. The
// returned closer must run before the caller's history-conn defers, otherwise
// migrate's driver close errors race with our advisory unlock.
func openMigrate(migrationURL string) (*migrate.Migrate, func(), error) {
	db, err := sql.Open("postgres", migrationURL)
	if err != nil {
		return nil, nil, fmt.Errorf("migrate: open db: %w", err)
	}
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate: load source: %w", err)
	}
	driver, err := migratepg.WithInstance(db, &migratepg.Config{
		SchemaName:      "axiaops",
		MigrationsTable: "schema_migrations",
	})
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate: driver init: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrate: init: %w", err)
	}
	closer := func() {
		// m.Close() closes the source + driver; it does NOT close db.
		_, _ = m.Close()
		_ = db.Close()
	}
	return m, closer, nil
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
