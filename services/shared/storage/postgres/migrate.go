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
//     migration_state tracking table inside it
//   - axiaops.migration_history table + view + grants exist (must be created
//     here, before 000_init.up.sql, because the ALTER DEFAULT PRIVILEGES in
//     000_init applies only to tables created after that statement runs —
//     migration_history pre-dates it and therefore needs an explicit GRANT.
//     See docs/ARCHITECTURE.md (§5, Migration system).)
//
// Must be called before Migrate on every startup. Connects as the owner
// (ownerURL) to create/update the user and schema, then syncs the password
// from DATABASE_URL — enabling credential rotation without manual steps.
// runtimeAdminURL is the least-privilege RLS-bypass role connection
// (axiaops_runtime, see docs/AUTHENTICATION.md (§5)); when non-empty its
// LOGIN + password are synced here the same way (the role's privileges +
// per-table bypass policies live in migration 029). Empty skips the sync.
// Uses advisory lock to prevent concurrent bootstrap calls.
func Bootstrap(ownerURL, appURL, runtimeAdminURL string) error {
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

	// Create + sync the runtime RLS-bypass role (axiaops_runtime) when a runtime
	// URL is configured. Mirrors the app-user handling above: the role's
	// privileges + per-table bypass policies live in migration 029, but its
	// LOGIN + password are synced here so credential rotation needs no manual
	// steps. Empty runtimeAdminURL (DEV_MODE single-pool, or an env that has not
	// split the role out yet) skips — migration 029 still creates the role
	// NOLOGIN so its grants/policies apply regardless.
	if runtimeAdminURL != "" {
		rtCfg, err := pgxpool.ParseConfig(runtimeAdminURL)
		if err != nil {
			return fmt.Errorf("bootstrap: parse runtime-admin url: %w", err)
		}
		rtPassword := rtCfg.ConnConfig.Password
		if rtPassword == "" {
			return fmt.Errorf("bootstrap: RUNTIME_ADMIN_DATABASE_URL contains no password")
		}
		if _, err := db.Exec(`DO $$ BEGIN
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'axiaops_runtime') THEN
				CREATE ROLE axiaops_runtime LOGIN;
			END IF;
		END $$`); err != nil {
			return fmt.Errorf("bootstrap: create runtime role: %w", err)
		}
		if _, err := db.Exec(`ALTER ROLE axiaops_runtime WITH LOGIN PASSWORD ` + pq.QuoteLiteral(rtPassword)); err != nil {
			return fmt.Errorf("bootstrap: set runtime role password: %w", err)
		}
	}

	// Create the axiaops schema if it does not exist yet. This must happen
	// before Migrate() runs, because golang-migrate creates its bookkeeping
	// table inside this schema before applying the first migration —
	// chicken-and-egg otherwise. Owned by the connecting role
	// (axiaops_owner), not the app user.
	if _, err := db.Exec(`CREATE SCHEMA IF NOT EXISTS axiaops`); err != nil {
		return fmt.Errorf("bootstrap: create schema: %w", err)
	}

	// Atomic rename guard: schema_migrations → migration_state.
	//
	// Pre-rename history: golang-migrate's compiled-in default
	// MigrationsTable is "schema_migrations" (a misnomer — the table holds
	// exactly one row, version + dirty bit, but the plural name was
	// inherited from Rails' multi-row schema). We renamed it to
	// migration_state, which honestly reflects single-row cardinality and
	// pairs cleanly with the migration_history audit table.
	//
	// This guard runs BEFORE migratepg.WithInstance, which means:
	//   - On an existing DB that has schema_migrations applied: the
	//     ALTER TABLE RENAME flips the table name. Privileges + COMMENT
	//     carry through automatically (Postgres semantics). WithInstance
	//     then sees migration_state and uses it.
	//   - On a fresh DB: neither table exists. The IF EXISTS guard is
	//     false, this block is a no-op. WithInstance creates
	//     migration_state from scratch.
	//   - On a re-boot of an already-renamed DB: schema_migrations no
	//     longer exists, migration_state does. The IF EXISTS guard is
	//     false. No-op.
	//
	// The DO block is idempotent across all three cases.
	if _, err := db.Exec(`
		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'axiaops' AND table_name = 'schema_migrations'
			) AND NOT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'axiaops' AND table_name = 'migration_state'
			) THEN
				ALTER TABLE axiaops.schema_migrations RENAME TO migration_state;
			END IF;
		END $$;
	`); err != nil {
		return fmt.Errorf("bootstrap: rename schema_migrations to migration_state: %w", err)
	}

	// Atomic column rename inside migration_history:
	// schema_migrations_dirty_after → migration_state_dirty_after.
	//
	// Companion to the table rename above. The column name was anchored
	// on the OLD metadata table name; the rename keeps the schema
	// internally consistent. Must run BEFORE migrationHistoryDDL because
	// that DDL's CREATE OR REPLACE VIEW references the new column name —
	// without this rename, the view recreation would fail on existing DBs
	// with the old column name still in place.
	//
	// Idempotent across all three states: column is old (rename), column
	// is new (no-op via IF EXISTS guard), table doesn't exist yet (no-op
	// because the column query returns no rows).
	if _, err := db.Exec(`
		DO $$
		BEGIN
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'axiaops'
				  AND table_name   = 'migration_history'
				  AND column_name  = 'schema_migrations_dirty_after'
			) THEN
				ALTER TABLE axiaops.migration_history
					RENAME COLUMN schema_migrations_dirty_after TO migration_state_dirty_after;
			END IF;
		END $$;
	`); err != nil {
		return fmt.Errorf("bootstrap: rename schema_migrations_dirty_after column: %w", err)
	}

	// Create migration_history (table + grants) before golang-migrate
	// gets a chance to run 000_init. Idempotent: every statement is IF NOT
	// EXISTS / OR REPLACE.
	if _, err := db.Exec(migrationHistoryDDL); err != nil {
		return fmt.Errorf("bootstrap: migration history ddl: %w", err)
	}

	// Drop the legacy migration_history_v view if it still exists. The view
	// was a SELECT * + LEFT(file_sha256, 8) wrapper that didn't earn its
	// keep — see the comment in migrationHistoryDDL. IF EXISTS keeps this
	// idempotent (no-op once it's been dropped, and never created on fresh
	// DBs anymore).
	if _, err := db.Exec(`DROP VIEW IF EXISTS axiaops.migration_history_v`); err != nil {
		return fmt.Errorf("bootstrap: drop legacy migration_history_v view: %w", err)
	}

	return nil
}

// Migrate applies all pending up migrations one step at a time, recording each
// in axiaops.migration_history (see docs/ARCHITECTURE.md (§5, Migration system)).
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
		// migration_state exists at this point — migratepg.WithInstance
		// inside openMigrate creates it lazily on first call (with the
		// renamed-by-Bootstrap table on existing DBs, or fresh on new ones).
		// Stamp the COMMENT so a reader doing `\d+ axiaops.migration_state`
		// in psql sees the table's role at a glance.
		if err := commentMigrationState(ctx, conn); err != nil {
			return err
		}
		return runUpLoop(ctx, conn, m, migrationsFS, idx, ident)
	})
}

// commentMigrationState is idempotent — Postgres' COMMENT ON TABLE
// overwrites any existing comment and never errors on a no-op. Runs on
// every Migrate() call so the comment stays in sync if the helper string
// is ever updated. Old name was commentSchemaMigrations (pre-rename).
func commentMigrationState(ctx context.Context, conn *sql.Conn) error {
	const stmt = `COMMENT ON TABLE axiaops.migration_state IS ` +
		`'golang-migrate state pointer (one row: version + dirty bit; renamed ` +
		`from schema_migrations 2026-05-10). For per-event audit (every ` +
		`up/down/force with file SHA-256 + timing) see axiaops.migration_history.'`
	if _, err := conn.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("migrate: comment migration_state: %w", err)
	}
	return nil
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
// 'force' history row. No DDL is executed — only migration_state is
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
		SchemaName: "axiaops",
		// Renamed from the golang-migrate default "schema_migrations" —
		// see Bootstrap's atomic rename guard. The table holds exactly
		// one row (version, dirty), so the singular name is honest.
		MigrationsTable: "migration_state",
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
// stuckAfter back to "error". Uses the runtime-admin URL (axiaops_runtime — a
// DML-only RLS-bypass role via per-table policies, no DDL) so this cross-org
// maintenance bypasses RLS — safe on startup and from a periodic ticker.
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
