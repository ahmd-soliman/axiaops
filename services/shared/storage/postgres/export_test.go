package postgres

import (
	"context"
	"database/sql"
)

// RecoverOrphansForTest exposes the orphan resolver to black-box tests. The
// production caller is Migrate(), which always runs recoverOrphans inside
// withHistoryConn. The test variant runs the same loop on its own pinned
// connection so a test can craft schema_migrations / migration_history state
// and observe resolution without driving a full Migrate cycle.
func RecoverOrphansForTest(ctx context.Context, migrationURL string) error {
	return withHistoryConn(ctx, migrationURL, func(conn *sql.Conn) error {
		return recoverOrphans(ctx, conn)
	})
}

// SchemaMigrationsStateForTest exposes the helper that reads
// axiaops.schema_migrations from the history-writer connection. Used by the
// regression test that confirms a missing schema_migrations table (fresh DB
// state, before migratepg.WithInstance has run) collapses to hasRow=false
// rather than erroring.
func SchemaMigrationsStateForTest(ctx context.Context, migrationURL string) (version int64, dirty bool, hasRow bool, err error) {
	err = withHistoryConn(ctx, migrationURL, func(conn *sql.Conn) error {
		var innerErr error
		version, dirty, hasRow, innerErr = schemaMigrationsState(ctx, conn)
		return innerErr
	})
	return version, dirty, hasRow, err
}
