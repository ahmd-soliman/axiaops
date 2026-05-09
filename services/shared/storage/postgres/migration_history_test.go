package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/lib/pq"

	"axiaops.io/shared/storage/postgres"
)

// migrationURLOrSkip returns MIGRATION_DATABASE_URL or skips. Reused here so
// the file stays self-contained.
func migrationURLOrSkip(t *testing.T) string {
	t.Helper()
	url := os.Getenv("MIGRATION_DATABASE_URL")
	if url == "" {
		t.Skip("MIGRATION_DATABASE_URL not set — skipping migration_history integration tests")
	}
	return url
}

func openOwner(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", migrationURLOrSkip(t))
	if err != nil {
		t.Fatalf("open owner db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func openApp(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set — skipping app-user grant tests")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("open app db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrationHistory_TableAndViewExist(t *testing.T) {
	db := openOwner(t)
	var tableExists bool
	if err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema='axiaops' AND table_name='migration_history'
		)`).Scan(&tableExists); err != nil {
		t.Fatalf("check table: %v", err)
	}
	if !tableExists {
		t.Fatal("axiaops.migration_history does not exist — Bootstrap regression")
	}
	var viewExists bool
	if err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.views
			WHERE table_schema='axiaops' AND table_name='migration_history_v'
		)`).Scan(&viewExists); err != nil {
		t.Fatalf("check view: %v", err)
	}
	if !viewExists {
		t.Fatal("axiaops.migration_history_v does not exist — Bootstrap regression")
	}
}

func TestMigrationHistory_AppUserCanSelectButNotInsert(t *testing.T) {
	db := openApp(t)
	if _, err := db.Query(`SELECT id FROM axiaops.migration_history LIMIT 1`); err != nil {
		t.Fatalf("app user SELECT failed: %v", err)
	}
	_, err := db.Exec(`
		INSERT INTO axiaops.migration_history (version, name, direction, status)
		VALUES (999, 'pwn', 'up', 'started')
	`)
	if err == nil {
		t.Fatal("expected permission-denied INSERT, got nil")
	}
	// Match by SQLSTATE rather than message text — pq error strings are
	// localised per the server's lc_messages.
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		t.Fatalf("expected *pq.Error, got: %T %v", err, err)
	}
	if pqErr.Code != "42501" { // insufficient_privilege
		t.Fatalf("expected SQLSTATE 42501, got %s: %v", pqErr.Code, err)
	}
}

func TestMigrationHistory_RowsExistForAppliedVersions(t *testing.T) {
	db := openOwner(t)
	// After TestMain runs Migrate, every applied version must have at least
	// one history row (succeeded or backfilled — both are valid endpoints).
	var versionsWithHistory int
	if err := db.QueryRow(`
		SELECT COUNT(DISTINCT mh.version)
		FROM axiaops.migration_history mh
		WHERE mh.direction = 'up'
		  AND mh.status IN ('succeeded','backfilled')
	`).Scan(&versionsWithHistory); err != nil {
		t.Fatalf("count history versions: %v", err)
	}
	if versionsWithHistory == 0 {
		t.Fatal("no migration_history rows after TestMain Migrate — wrapper not recording")
	}
	// schema_migrations should also be populated.
	var sm int64
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM axiaops.schema_migrations`).Scan(&sm, &dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if dirty {
		t.Fatalf("schema_migrations dirty=true after TestMain — Migrate is broken")
	}
}

func TestMigrationHistory_RerunMigrateAddsNoNewRows(t *testing.T) {
	url := migrationURLOrSkip(t)
	db := openOwner(t)
	var before int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM axiaops.migration_history`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if err := postgres.Migrate(url); err != nil {
		t.Fatalf("re-Migrate: %v", err)
	}
	var after int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM axiaops.migration_history`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("re-Migrate added rows: before=%d after=%d", before, after)
	}
}

func TestMigrationHistory_OrphanIndeterminateResolvesToSucceeded(t *testing.T) {
	url := migrationURLOrSkip(t)
	db := openOwner(t)
	// Phantom version=9999 — not in fsIndex, schema_migrations doesn't
	// reference it. Orphan resolver lands on the §Failure modes
	// "indeterminate" fallthrough which records succeeded + dirty_after=false
	// (treat-as-lost-UPDATE-success).
	var id int64
	if err := db.QueryRow(`
		INSERT INTO axiaops.migration_history
		    (version, name, direction, status, file_sha256)
		VALUES (9999, 'phantom_orphan', 'up', 'started', NULL)
		RETURNING id
	`).Scan(&id); err != nil {
		t.Fatalf("insert orphan: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM axiaops.migration_history WHERE id = $1`, id)
	})

	if err := postgres.RecoverOrphansForTest(context.Background(), url); err != nil {
		t.Fatalf("RecoverOrphansForTest: %v", err)
	}

	var status string
	var finished sql.NullTime
	var dirtyAfter sql.NullBool
	if err := db.QueryRow(`
		SELECT status, finished_at, schema_migrations_dirty_after
		FROM axiaops.migration_history WHERE id = $1
	`, id).Scan(&status, &finished, &dirtyAfter); err != nil {
		t.Fatalf("read orphan: %v", err)
	}
	if status != "succeeded" {
		t.Fatalf("indeterminate orphan: want succeeded, got %s", status)
	}
	if !finished.Valid {
		t.Fatal("orphan finished_at still NULL after recovery")
	}
	if !dirtyAfter.Valid || dirtyAfter.Bool != false {
		t.Fatalf("indeterminate orphan: want dirty_after=false, got valid=%v value=%v",
			dirtyAfter.Valid, dirtyAfter.Bool)
	}
}

// TestMigrationHistory_OrphanTruthTable exercises the six concrete rows of
// the §Failure modes truth table by faking schema_migrations into each
// pre-state and confirming the resolver writes the right terminal row.
//
// Each sub-test:
//  1. Reads the real schema_migrations state.
//  2. Forces it into the fixture's pre-state via UPDATE (no DDL is touched —
//     this only rewrites the bookkeeping table).
//  3. Inserts a synthetic 'started' row.
//  4. Runs RecoverOrphansForTest.
//  5. Asserts the row's terminal status / dirty_after.
//  6. Restores schema_migrations to the original state.
//
// The phantom version is well beyond any embedded migration so the resolver
// can't accidentally collide with the fsIndex. NOTE: this test mutates
// axiaops.schema_migrations transiently — keep it serial (no t.Parallel).
func TestMigrationHistory_OrphanTruthTable(t *testing.T) {
	url := migrationURLOrSkip(t)
	db := openOwner(t)

	const phantomV int64 = 9000

	// Stash + restore schema_migrations.
	var origVersion int64
	var origDirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM axiaops.schema_migrations`).Scan(&origVersion, &origDirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`UPDATE axiaops.schema_migrations SET version=$1, dirty=$2`, origVersion, origDirty)
	})

	type want struct {
		status     string
		dirtyAfter sql.NullBool
		errMsg     string
	}
	cases := []struct {
		name      string
		direction string
		smVersion int64
		smDirty   bool
		want      want
	}{
		{
			name: "up_succeeded_lost_update", direction: "up",
			smVersion: phantomV, smDirty: false,
			want: want{
				status:     "succeeded",
				dirtyAfter: sql.NullBool{Bool: false, Valid: true},
				errMsg:     "timing lost; recovered post-crash",
			},
		},
		{
			name: "up_failed_dirty", direction: "up",
			smVersion: phantomV, smDirty: true,
			want: want{
				status:     "failed",
				dirtyAfter: sql.NullBool{Bool: true, Valid: true},
				errMsg:     "migration failed; details lost in wrapper crash",
			},
		},
		{
			name: "up_never_started", direction: "up",
			smVersion: phantomV - 1, smDirty: false,
			want: want{
				status:     "failed",
				dirtyAfter: sql.NullBool{Valid: false}, // NULL
				errMsg:     "wrapper killed before migration step",
			},
		},
		{
			name: "down_succeeded_lost_update", direction: "down",
			smVersion: phantomV - 1, smDirty: false,
			want: want{
				status:     "succeeded",
				dirtyAfter: sql.NullBool{Bool: false, Valid: true},
				errMsg:     "timing lost; recovered post-crash",
			},
		},
		{
			name: "down_failed_dirty", direction: "down",
			smVersion: phantomV, smDirty: true,
			want: want{
				status:     "failed",
				dirtyAfter: sql.NullBool{Bool: true, Valid: true},
				errMsg:     "migration failed; details lost in wrapper crash",
			},
		},
		{
			name: "down_never_started", direction: "down",
			smVersion: phantomV, smDirty: false,
			want: want{
				status:     "failed",
				dirtyAfter: sql.NullBool{Valid: false}, // NULL
				errMsg:     "wrapper killed before migration step",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE axiaops.schema_migrations SET version=$1, dirty=$2`, tc.smVersion, tc.smDirty); err != nil {
				t.Fatalf("set schema_migrations: %v", err)
			}
			var id int64
			if err := db.QueryRow(`
				INSERT INTO axiaops.migration_history
				    (version, name, direction, status, file_sha256)
				VALUES ($1, 'truthtable', $2, 'started', NULL)
				RETURNING id
			`, phantomV, tc.direction).Scan(&id); err != nil {
				t.Fatalf("insert started: %v", err)
			}
			t.Cleanup(func() {
				_, _ = db.Exec(`DELETE FROM axiaops.migration_history WHERE id = $1`, id)
			})

			if err := postgres.RecoverOrphansForTest(context.Background(), url); err != nil {
				t.Fatalf("recover: %v", err)
			}

			var status string
			var dirty sql.NullBool
			var errMsg sql.NullString
			if err := db.QueryRow(`
				SELECT status, schema_migrations_dirty_after, error_message
				FROM axiaops.migration_history WHERE id = $1
			`, id).Scan(&status, &dirty, &errMsg); err != nil {
				t.Fatalf("read row: %v", err)
			}
			if status != tc.want.status {
				t.Fatalf("status: want %q got %q", tc.want.status, status)
			}
			if dirty.Valid != tc.want.dirtyAfter.Valid {
				t.Fatalf("dirty_after.Valid: want %v got %v", tc.want.dirtyAfter.Valid, dirty.Valid)
			}
			if dirty.Valid && dirty.Bool != tc.want.dirtyAfter.Bool {
				t.Fatalf("dirty_after: want %v got %v", tc.want.dirtyAfter.Bool, dirty.Bool)
			}
			if !errMsg.Valid || errMsg.String != tc.want.errMsg {
				t.Fatalf("error_message: want %q got %q (valid=%v)", tc.want.errMsg, errMsg.String, errMsg.Valid)
			}
		})
	}
}

func TestMigrationHistory_ForceWritesForceRow(t *testing.T) {
	url := migrationURLOrSkip(t)
	db := openOwner(t)
	var current int64
	if err := db.QueryRow(`SELECT version FROM axiaops.schema_migrations`).Scan(&current); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if err := postgres.MigrateForce(url, int(current)); err != nil {
		t.Fatalf("MigrateForce: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`
			DELETE FROM axiaops.migration_history
			WHERE direction='force' AND version=$1
		`, current)
	})

	var got int64
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM axiaops.migration_history
		WHERE direction='force' AND status='succeeded' AND version=$1
	`, current).Scan(&got); err != nil {
		t.Fatalf("count force rows: %v", err)
	}
	if got == 0 {
		t.Fatal("MigrateForce did not write a force history row")
	}
}

func TestMigrationHistory_ConcurrentMigrateSerialises(t *testing.T) {
	url := migrationURLOrSkip(t)
	db := openOwner(t)
	var before int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM axiaops.migration_history`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- postgres.Migrate(url)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Migrate failed: %v", err)
		}
	}
	var after int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM axiaops.migration_history`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("concurrent Migrate should be no-op (everything already applied); before=%d after=%d", before, after)
	}
}

func TestMigrationHistory_QueryHistoryReturnsRows(t *testing.T) {
	url := migrationURLOrSkip(t)
	rows, err := postgres.QueryHistory(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("QueryHistory: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("QueryHistory returned 0 rows; expected at least one applied migration")
	}
	// Verify ordering is stable (started_at ASC then id ASC).
	for i := 1; i < len(rows); i++ {
		prev, cur := rows[i-1], rows[i]
		if prev.StartedAt.After(cur.StartedAt) {
			t.Fatalf("rows not ordered by started_at ASC: row %d started %v after row %d %v",
				i-1, prev.StartedAt, i, cur.StartedAt)
		}
		if prev.StartedAt.Equal(cur.StartedAt) && prev.ID > cur.ID {
			t.Fatalf("same-time ties not broken by id ASC: id=%d before id=%d", prev.ID, cur.ID)
		}
	}
}

func TestMigrationHistory_StrictModeFailsOnDrift(t *testing.T) {
	url := migrationURLOrSkip(t)
	db := openOwner(t)

	// Pick any applied up version with a recorded checksum.
	var v int64
	var realSHA string
	err := db.QueryRow(`
		SELECT version, file_sha256
		FROM axiaops.migration_history
		WHERE direction='up' AND status IN ('succeeded','backfilled') AND file_sha256 IS NOT NULL
		ORDER BY version DESC LIMIT 1
	`).Scan(&v, &realSHA)
	if err != nil {
		t.Fatalf("pick recorded sha: %v", err)
	}
	// Insert a NEW succeeded row with a deliberately-wrong checksum so
	// detectDrift's "most recent succeeded" lookup returns the lie.
	// (Updating a backfilled row in place wouldn't be picked up — drift
	// detection ignores backfilled rows.)
	wrong := strings.Repeat("a", 64)
	var injectedID int64
	if err := db.QueryRow(`
		INSERT INTO axiaops.migration_history
		    (version, name, direction, status, file_sha256, finished_at, duration_ms,
		     applied_by_actor, applied_by_image, schema_migrations_dirty_after)
		VALUES ($1, 'drift_injected', 'up', 'succeeded', $2, now(), 0,
		        'test', 'test@test', false)
		RETURNING id
	`, v, wrong).Scan(&injectedID); err != nil {
		t.Fatalf("inject drift row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM axiaops.migration_history WHERE id = $1`, injectedID)
	})

	t.Setenv("MIGRATION_HISTORY_STRICT", "true")
	err = postgres.Migrate(url)
	if err == nil {
		t.Fatal("STRICT Migrate should error when checksum drift is present")
	}
	if !strings.Contains(err.Error(), "STRICT drift") {
		t.Fatalf("expected STRICT drift error, got: %v", err)
	}

	// Sanity: without STRICT, drift is a warning and Migrate succeeds.
	t.Setenv("MIGRATION_HISTORY_STRICT", "false")
	if err := postgres.Migrate(url); err != nil {
		t.Fatalf("non-STRICT Migrate must tolerate drift: %v", err)
	}
}

// TestMigrationHistory_QueryDrift: with no injected drift, QueryDrift returns
// an empty slice. (The drift-injection happens in StrictModeFailsOnDrift,
// which cleans up after itself.)
func TestMigrationHistory_QueryDriftEmpty(t *testing.T) {
	url := migrationURLOrSkip(t)
	rows, err := postgres.QueryDrift(context.Background(), url)
	if err != nil {
		t.Fatalf("QueryDrift: %v", err)
	}
	for _, r := range rows {
		t.Logf("unexpected drift: v=%d expected=%s observed=%s", r.Version, r.ExpectedSHA[:8], r.ObservedSHA[:8])
	}
	if len(rows) > 0 {
		t.Fatalf("QueryDrift returned %d rows; expected 0", len(rows))
	}
}

func TestMigrationHistory_DDLIdempotent(t *testing.T) {
	// Calling Bootstrap twice in a row must not error — same posture every
	// other DDL in Bootstrap has. Regression guard against forgetting
	// IF NOT EXISTS / OR REPLACE on a future schema change.
	url := migrationURLOrSkip(t)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL not set")
	}
	if err := postgres.Bootstrap(url, dbURL); err != nil {
		t.Fatalf("first Bootstrap: %v", err)
	}
	if err := postgres.Bootstrap(url, dbURL); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
}
