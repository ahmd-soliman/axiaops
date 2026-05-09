package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DriftRow is one (version, name, expected, observed) tuple for a version
// where the on-disk SHA-256 differs from the most recent succeeded recorded
// SHA. Backfilled rows are excluded — they baseline against on-disk and so
// always agree by construction.
type DriftRow struct {
	Version     uint
	Name        string
	ExpectedSHA string
	ObservedSHA string
}

// HistoryRow mirrors axiaops.migration_history_v for CLI rendering.
type HistoryRow struct {
	ID             int64
	Version        int64
	Name           string
	Direction      string
	Status         string
	StartedAt      time.Time
	FinishedAt     sql.NullTime
	DurationMS     sql.NullInt64
	ErrorMessage   sql.NullString
	AppliedByImage sql.NullString
	AppliedByActor sql.NullString
	DirtyAfter     sql.NullBool
	FileSHAShort   string
	FileSHA        sql.NullString
}

// QueryDrift opens a fresh connection and produces the drift table the CLI
// renders. Compares each embedded .up.sql file's SHA-256 against the most
// recent non-backfilled succeeded row's file_sha256.
func QueryDrift(ctx context.Context, migrationURL string) ([]DriftRow, error) {
	idx, err := indexEmbeddedMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("query drift: index: %w", err)
	}
	db, err := sql.Open("postgres", migrationURL)
	if err != nil {
		return nil, fmt.Errorf("query drift: open: %w", err)
	}
	defer func() { _ = db.Close() }()

	var drifts []DriftRow
	for _, v := range idx.versions {
		mf := idx.byVersion[v]
		if mf.upPath == "" {
			continue
		}
		_, observed, err := idx.readBytes(migrationsFS, v, "up")
		if err != nil {
			return nil, err
		}
		var expected sql.NullString
		// Read via migration_history_v so the CLI is decoupled from the
		// base table's exact column layout — design §Drift query.
		err = db.QueryRowContext(ctx, `
			SELECT file_sha256
			FROM axiaops.migration_history_v
			WHERE version = $1
			  AND direction = 'up'
			  AND status = 'succeeded'
			  AND file_sha256 IS NOT NULL
			ORDER BY id DESC
			LIMIT 1
		`, int64(v)).Scan(&expected)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return nil, fmt.Errorf("query drift v=%d: %w", v, err)
		}
		if !expected.Valid || expected.String == observed {
			continue
		}
		drifts = append(drifts, DriftRow{
			Version: v, Name: mf.name,
			ExpectedSHA: expected.String, ObservedSHA: observed,
		})
	}
	return drifts, nil
}

// QueryHistory returns rows of axiaops.migration_history_v ordered by
// (started_at, id) ASC. Pass a non-nil filterVersion to constrain to a single
// version.
func QueryHistory(ctx context.Context, migrationURL string, filterVersion *int64) ([]HistoryRow, error) {
	db, err := sql.Open("postgres", migrationURL)
	if err != nil {
		return nil, fmt.Errorf("query history: open: %w", err)
	}
	defer func() { _ = db.Close() }()

	const baseSelect = `
		SELECT id, version, name, direction, status, started_at, finished_at,
		       duration_ms, error_message, applied_by_image, applied_by_actor,
		       schema_migrations_dirty_after, file_sha256_short, file_sha256
		FROM axiaops.migration_history_v
	`
	var (
		rows *sql.Rows
		qerr error
	)
	if filterVersion != nil {
		rows, qerr = db.QueryContext(ctx, baseSelect+
			` WHERE version = $1 ORDER BY started_at ASC, id ASC`, *filterVersion)
	} else {
		rows, qerr = db.QueryContext(ctx, baseSelect+
			` ORDER BY started_at ASC, id ASC`)
	}
	if qerr != nil {
		return nil, fmt.Errorf("query history: select: %w", qerr)
	}
	defer func() { _ = rows.Close() }()

	var out []HistoryRow
	for rows.Next() {
		var r HistoryRow
		var shaShort sql.NullString
		if err := rows.Scan(
			&r.ID, &r.Version, &r.Name, &r.Direction, &r.Status, &r.StartedAt,
			&r.FinishedAt, &r.DurationMS, &r.ErrorMessage, &r.AppliedByImage,
			&r.AppliedByActor, &r.DirtyAfter, &shaShort, &r.FileSHA,
		); err != nil {
			return nil, fmt.Errorf("query history: scan: %w", err)
		}
		if shaShort.Valid {
			r.FileSHAShort = shaShort.String
		} else {
			r.FileSHAShort = "-"
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query history: iterate: %w", err)
	}
	return out, nil
}
