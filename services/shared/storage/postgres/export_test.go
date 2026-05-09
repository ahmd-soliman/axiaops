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
