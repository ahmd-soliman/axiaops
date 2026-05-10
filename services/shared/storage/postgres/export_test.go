package postgres

import (
	"context"
	"database/sql"
)

// RecoverOrphansForTest exposes the orphan resolver to black-box tests. The
// production caller is Migrate(), which always runs recoverOrphans inside
// withHistoryConn. The test variant runs the same loop on its own pinned
// connection so a test can craft migration_state / migration_history state
// and observe resolution without driving a full Migrate cycle.
func RecoverOrphansForTest(ctx context.Context, migrationURL string) error {
	return withHistoryConn(ctx, migrationURL, func(conn *sql.Conn) error {
		return recoverOrphans(ctx, conn)
	})
}

// MigrationStateReadForTest exposes the helper that reads
// axiaops.migration_state from the history-writer connection. Used by the
// regression test that confirms a missing migration_state table (fresh DB
// state, before migratepg.WithInstance has run) collapses to hasRow=false
// rather than erroring.
//
// Old name SchemaMigrationsStateForTest (pre-rename 2026-05-10).
func MigrationStateReadForTest(ctx context.Context, migrationURL string) (version int64, dirty bool, hasRow bool, err error) {
	err = withHistoryConn(ctx, migrationURL, func(conn *sql.Conn) error {
		var innerErr error
		version, dirty, hasRow, innerErr = migrationStateRead(ctx, conn)
		return innerErr
	})
	return version, dirty, hasRow, err
}
