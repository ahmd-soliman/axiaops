package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/lib/pq"
)

// migrationHistoryLockID is the wrapper-level session advisory lock held for
// the entire duration of a Migrate / MigrateDown / MigrateForce call.
//
// 0x417869614F70734D == ASCII "AxiaOpsM" — full brand "AxiaOps" (7 bytes) +
// "M" for Migration. Exactly 8 bytes to fit int64. Distinct from any
// golang-migrate-internal lock ID to avoid self-deadlock.
//
// Sibling-lock convention: replace the trailing letter for new wrapper-level
// locks (bootstrapAdvisoryLockID below uses "AxiaOpsB"). Keeps the namespace
// scannable in pg_locks and prevents number reuse across concerns.
const migrationHistoryLockID int64 = 0x417869614F70734D

// bootstrapAdvisoryLockID is the session advisory lock guarding
// postgres.Bootstrap. Same naming convention as migrationHistoryLockID.
//
// 0x417869614F707342 == ASCII "AxiaOpsB". Replaces the prior arbitrary
// 123456789 — the literal was indistinguishable from any other ad-hoc
// advisory lock in pg_locks; this name makes Bootstrap holds attributable
// when an operator is debugging a stuck startup.
const bootstrapAdvisoryLockID int64 = 0x417869614F707342

const maxErrorMessageBytes = 4096

const migrationHistoryDDL = `
CREATE TABLE IF NOT EXISTS axiaops.migration_history (
    id                            BIGSERIAL   PRIMARY KEY,
    version                       BIGINT      NOT NULL,
    name                          TEXT        NOT NULL,
    direction                     TEXT        NOT NULL
                                  CHECK (direction IN ('up','down','force')),
    status                        TEXT        NOT NULL
                                  CHECK (status IN ('started','succeeded','failed','backfilled')),
    started_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at                   TIMESTAMPTZ,
    duration_ms                   BIGINT,
    error_message                 TEXT
                                  CHECK (length(error_message) <= 4096),
    file_sha256                   TEXT
                                  CHECK (file_sha256 ~ '^[0-9a-f]{64}$'),
    applied_by_actor              TEXT,
    applied_by_image              TEXT,
    migration_state_dirty_after   BOOLEAN
);

CREATE INDEX IF NOT EXISTS migration_history_version_idx
    ON axiaops.migration_history (version, started_at DESC);

CREATE INDEX IF NOT EXISTS migration_history_started_at_idx
    ON axiaops.migration_history (started_at DESC);

-- The 'migration_history_v' convenience view used to live here. Removed
-- 2026-05-10: it was SELECT * + LEFT(file_sha256, 8) and nothing else —
-- no joins, no WHERE, no aggregate. The 8-char short hash is computed
-- Go-side in the CLI printer instead. Bootstrap idempotently DROPs any
-- leftover view (see postgres.Bootstrap in migrate.go for the call).

GRANT SELECT ON axiaops.migration_history TO axiaops;
`

// migrationFile is one (version, name, direction) tuple resolved from the
// embedded migrations FS.
type migrationFile struct {
	version uint
	name    string
	upPath  string // empty if no .up.sql
	dnPath  string // empty if no .down.sql
}

type migrationFSIndex struct {
	// byVersion is keyed by the integer version prefix (e.g. 24 for 024_*).
	byVersion map[uint]*migrationFile
	// versions is the sorted ascending list of all versions present.
	versions []uint
}

var migrationFilenameRegexp = regexp.MustCompile(`^(\d+)_([A-Za-z0-9_-]+)\.(up|down)\.sql$`)

// indexEmbeddedMigrations walks the embedded migrations directory once and
// produces an index that the wrapper consults to compute V before each Steps()
// call. Filename shape: "NNN_name.{up,down}.sql"; non-matching files are
// ignored (not an error — we share the directory only with .sql files today,
// but we keep the parser tolerant).
func indexEmbeddedMigrations(efs fs.FS, root string) (*migrationFSIndex, error) {
	idx := &migrationFSIndex{byVersion: map[uint]*migrationFile{}}
	err := fs.WalkDir(efs, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := pathBase(path)
		m := migrationFilenameRegexp.FindStringSubmatch(base)
		if m == nil {
			return nil
		}
		v64, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			return fmt.Errorf("indexEmbeddedMigrations: parse version from %q: %w", base, err)
		}
		v := uint(v64)
		mf, ok := idx.byVersion[v]
		if !ok {
			mf = &migrationFile{version: v, name: m[2]}
			idx.byVersion[v] = mf
		}
		if mf.name != m[2] {
			return fmt.Errorf("indexEmbeddedMigrations: version %d has conflicting names %q vs %q", v, mf.name, m[2])
		}
		switch m[3] {
		case "up":
			mf.upPath = path
		case "down":
			mf.dnPath = path
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for v := range idx.byVersion {
		idx.versions = append(idx.versions, v)
	}
	sort.Slice(idx.versions, func(i, j int) bool { return idx.versions[i] < idx.versions[j] })
	return idx, nil
}

func pathBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// nextPendingUp returns the smallest version greater than current that has a
// .up.sql file. ok=false means nothing pending.
func (i *migrationFSIndex) nextPendingUp(current uint, hasCurrent bool) (uint, string, bool) {
	for _, v := range i.versions {
		if hasCurrent && v <= current {
			continue
		}
		mf := i.byVersion[v]
		if mf.upPath != "" {
			return v, mf.name, true
		}
	}
	return 0, "", false
}

// lookupDown returns (name, true) when V has a .down.sql in the index.
func (i *migrationFSIndex) lookupDown(v uint) (string, bool) {
	mf, ok := i.byVersion[v]
	if !ok || mf.dnPath == "" {
		return "", false
	}
	return mf.name, true
}

// readBytes returns the raw bytes of a migration file plus its SHA-256.
func (i *migrationFSIndex) readBytes(efs fs.FS, v uint, dir string) ([]byte, string, error) {
	mf, ok := i.byVersion[v]
	if !ok {
		return nil, "", fmt.Errorf("migration_history: version %d not in fsIndex", v)
	}
	var path string
	switch dir {
	case "up":
		path = mf.upPath
	case "down":
		path = mf.dnPath
	default:
		return nil, "", fmt.Errorf("migration_history: unknown direction %q", dir)
	}
	if path == "" {
		return nil, "", fmt.Errorf("migration_history: no %s file for version %d", dir, v)
	}
	b, err := fs.ReadFile(efs, path)
	if err != nil {
		return nil, "", fmt.Errorf("migration_history: read %s: %w", path, err)
	}
	return b, fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

// runtimeIdentity is the (actor, image) pair stamped on every history row.
type runtimeIdentity struct {
	actor string
	image string
}

func resolveRuntimeIdentity() runtimeIdentity {
	actor := os.Getenv("MIGRATION_ACTOR_LABEL")
	if actor == "" {
		if h, err := os.Hostname(); err == nil {
			actor = h
		} else {
			actor = "unknown"
		}
	}
	version := os.Getenv("APP_VERSION")
	commit := os.Getenv("APP_COMMIT_SHA")
	if version == "" || commit == "" {
		slog.Warn("migration_history: applied_by_image will be 'unknown@unknown'; drift forensics degraded",
			"app_version_set", version != "",
			"app_commit_sha_set", commit != "",
		)
		if version == "" {
			version = "unknown"
		}
		if commit == "" {
			commit = "unknown"
		}
	}
	return runtimeIdentity{actor: actor, image: version + "@" + commit}
}

// truncateError trims an error string to the on-disk hard cap, preserving the
// head of the message because that is where pq error codes and table names
// live.
func truncateError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > maxErrorMessageBytes {
		s = s[:maxErrorMessageBytes]
	}
	return s
}

// recordStarted INSERTs the pre-step row and commits it on its own
// transaction, durable on disk before Steps(±1) runs.
func recordStarted(ctx context.Context, conn *sql.Conn, version uint, name, direction, sha string, ident runtimeIdentity) (int64, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("migration_history: begin started: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var nullableSha sql.NullString
	if sha != "" {
		nullableSha = sql.NullString{String: sha, Valid: true}
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO axiaops.migration_history
		    (version, name, direction, status, file_sha256, applied_by_actor, applied_by_image)
		VALUES ($1, $2, $3, 'started', $4, $5, $6)
		RETURNING id
	`, int64(version), name, direction, nullableSha, ident.actor, ident.image).Scan(&id); err != nil {
		return 0, fmt.Errorf("migration_history: insert started: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("migration_history: commit started: %w", err)
	}
	return id, nil
}

// migrationStateRead reads (version, dirty) directly from
// axiaops.migration_state on the history-writer connection. golang-migrate's
// own m.Version() reads via its driver connection; the wrapper sometimes
// needs the post-step state from a different transaction.
//
// (Renamed from schemaMigrationsState 2026-05-10 alongside the table rename
// from schema_migrations → migration_state.)
//
// hasRow=false in three indistinguishable-from-the-wrapper's-POV cases:
//
//  1. Table has no row at all (fresh DB after Bootstrap, before the first
//     m.Steps()).
//  2. Table itself does not exist yet (truly fresh DB before
//     migratepg.WithInstance has run — Bootstrap creates the axiaops schema
//     and migration_history but golang-migrate creates migration_state
//     lazily on first WithInstance call). Detected via SQLSTATE 42P01
//     (undefined_table).
//  3. (Hypothetical) row count > 0 but Scan returned ErrNoRows. Not reachable.
//
// All three collapse to "no migration has ever run on this DB" — the right
// signal for orphan recovery / backfill, neither of which should fire on a
// pristine DB.
func migrationStateRead(ctx context.Context, conn *sql.Conn) (version int64, dirty bool, hasRow bool, err error) {
	row := conn.QueryRowContext(ctx, `SELECT version, dirty FROM axiaops.migration_state LIMIT 1`)
	switch err := row.Scan(&version, &dirty); {
	case err == nil:
		return version, dirty, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, false, nil
	default:
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "42P01" {
			return 0, false, false, nil
		}
		return 0, false, false, fmt.Errorf("migration_history: read migration_state: %w", err)
	}
}

// completeRow finalises a started row with the post-step outcome.
//   - On success path: status='succeeded', error_message NULL, dirty snapshot populated.
//   - On failure path: status='failed', error_message populated, dirty snapshot populated.
//   - On orphan recovery (never-started) path: pass dirtyValid=false to write NULL into
//     migration_state_dirty_after — the post-step UPDATE never ran.
//
// Uses context.Background() rather than the caller's ctx because the
// migration step has already returned: cancelling between Steps() and the
// bookkeeping UPDATE would leave the row stuck at 'started', which is
// exactly the orphan we'd then need to recover next boot. The DB call here
// is a single small UPDATE — letting it race a request-scoped timeout would
// trade a finished migration's correctness for nothing.
func completeRow(_ context.Context, conn *sql.Conn, id int64, status string, started time.Time, errMsg string, dirty bool, dirtyValid bool) error {
	finished := time.Now().UTC()
	durationMS := finished.Sub(started).Milliseconds()
	var nullableErr sql.NullString
	if errMsg != "" {
		nullableErr = sql.NullString{String: errMsg, Valid: true}
	}
	var nullableDirty sql.NullBool
	if dirtyValid {
		nullableDirty = sql.NullBool{Bool: dirty, Valid: true}
	}
	if _, err := conn.ExecContext(context.Background(), `
		UPDATE axiaops.migration_history
		SET status = $1,
		    finished_at = $2,
		    duration_ms = $3,
		    error_message = $4,
		    migration_state_dirty_after = $5
		WHERE id = $6
	`, status, finished, durationMS, nullableErr, nullableDirty, id); err != nil {
		return fmt.Errorf("migration_history: update id=%d to %s: %w", id, status, err)
	}
	return nil
}

// completeRowOrphan finalises a row whose post-step UPDATE never ran. Unlike
// completeRow, duration_ms is NULL (timing is lost) and started_at is read
// from the existing row to keep finished_at >= started_at.
//
// Same context-Background() rationale as completeRow.
func completeRowOrphan(_ context.Context, conn *sql.Conn, id int64, status, errMsg string, dirty bool, dirtyValid bool) error {
	var nullableErr sql.NullString
	if errMsg != "" {
		nullableErr = sql.NullString{String: errMsg, Valid: true}
	}
	var nullableDirty sql.NullBool
	if dirtyValid {
		nullableDirty = sql.NullBool{Bool: dirty, Valid: true}
	}
	if _, err := conn.ExecContext(context.Background(), `
		UPDATE axiaops.migration_history
		SET status = $1,
		    finished_at = now(),
		    duration_ms = NULL,
		    error_message = $2,
		    migration_state_dirty_after = $3
		WHERE id = $4
	`, status, nullableErr, nullableDirty, id); err != nil {
		return fmt.Errorf("migration_history: orphan update id=%d to %s: %w", id, status, err)
	}
	return nil
}

// recoverOrphans fixes every status='started' row with finished_at IS NULL.
// Called once at the top of every Migrate / MigrateDown call after the
// wrapper-level advisory lock is acquired.
func recoverOrphans(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, `
		SELECT id, version, direction
		FROM axiaops.migration_history
		WHERE status = 'started' AND finished_at IS NULL
		ORDER BY id ASC
	`)
	if err != nil {
		return fmt.Errorf("migration_history: select orphans: %w", err)
	}
	type orphan struct {
		id        int64
		version   int64
		direction string
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.version, &o.direction); err != nil {
			_ = rows.Close()
			return fmt.Errorf("migration_history: scan orphan: %w", err)
		}
		orphans = append(orphans, o)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migration_history: iterate orphans: %w", err)
	}
	if len(orphans) == 0 {
		return nil
	}
	smVersion, smDirty, smHasRow, err := migrationStateRead(ctx, conn)
	if err != nil {
		return err
	}
	for _, o := range orphans {
		if err := resolveOrphan(ctx, conn, o.id, o.version, o.direction, smVersion, smDirty, smHasRow); err != nil {
			return err
		}
	}
	return nil
}

// resolveOrphan applies the §Failure modes truth table.
//
//	direction | current_version | dirty | verdict
//	up        | == V            | false | succeeded (post-step UPDATE lost)
//	up        | == V            | true  | failed (dirty); UPDATE lost
//	up        | == V-1          | false | never started; retry next iteration
//	down      | == V-1          | false | succeeded (post-step UPDATE lost)
//	down      | == V            | true  | failed (dirty); UPDATE lost
//	down      | == V            | false | never started; retry next iteration
//	any       | else            | any   | warn + treat as lost-UPDATE success
func resolveOrphan(ctx context.Context, conn *sql.Conn, id int64, v int64, direction string, smVersion int64, smDirty, smHasRow bool) error {
	upTarget := v
	upPreState := v - 1
	downTarget := v - 1
	downPreState := v

	// "Never started" only makes sense if migration_state actually has a
	// row matching the pre-state. If migration_state has no row at all,
	// neither the up-V-1 nor down-V branches can match (the migration
	// could not have run).
	if smHasRow {
		switch direction {
		case "up":
			if smVersion == upTarget && !smDirty {
				return completeRowOrphan(ctx, conn, id, "succeeded",
					"timing lost; recovered post-crash", false, true)
			}
			if smVersion == upTarget && smDirty {
				return completeRowOrphan(ctx, conn, id, "failed",
					"migration failed; details lost in wrapper crash", true, true)
			}
			if smVersion == upPreState && !smDirty {
				return completeRowOrphan(ctx, conn, id, "failed",
					"wrapper killed before migration step", false, false)
			}
		case "down":
			if smVersion == downTarget && !smDirty {
				return completeRowOrphan(ctx, conn, id, "succeeded",
					"timing lost; recovered post-crash", false, true)
			}
			if smVersion == downPreState && smDirty {
				return completeRowOrphan(ctx, conn, id, "failed",
					"migration failed; details lost in wrapper crash", true, true)
			}
			if smVersion == downPreState && !smDirty {
				return completeRowOrphan(ctx, conn, id, "failed",
					"wrapper killed before migration step", false, false)
			}
		}
	}

	// Fallthrough: indeterminate state. Per design §Failure modes the row is
	// resolved "as for up-success above, plus a warning" — i.e.
	// dirty_after=false, not the observed value. Recording the actual
	// (possibly dirty) state would make this row indistinguishable from a
	// real failed-dirty resolution and break operators grepping by status.
	slog.Warn("migration_history: orphan in indeterminate state; treating as lost-UPDATE success",
		"id", id, "version", v, "direction", direction,
		"migration_state_version", smVersion, "migration_state_dirty", smDirty, "migration_state_has_row", smHasRow,
	)
	return completeRowOrphan(ctx, conn, id, "succeeded",
		fmt.Sprintf("indeterminate state; recovered post-crash (sm_version=%d sm_dirty=%t)", smVersion, smDirty),
		false, true)
}

// backfillIfEmpty inserts one 'backfilled' row per applied version when
// migration_history is empty AND migration_state has at least one row.
// No-op otherwise.
//
// MUST be called inside withHistoryConn — the empty/non-empty check and the
// follow-up INSERTs are not transactionally fused, so two concurrent boots
// would otherwise race and double-backfill. The wrapper-level advisory lock
// is the serialisation primitive.
func backfillIfEmpty(ctx context.Context, conn *sql.Conn, efs fs.FS, idx *migrationFSIndex, ident runtimeIdentity) error {
	var historyCount int64
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM axiaops.migration_history`).Scan(&historyCount); err != nil {
		return fmt.Errorf("migration_history: count history: %w", err)
	}
	if historyCount > 0 {
		return nil
	}
	smVersion, _, smHasRow, err := migrationStateRead(ctx, conn)
	if err != nil {
		return err
	}
	if !smHasRow {
		return nil
	}
	// Backfill every embedded version with v <= smVersion that has a .up.sql.
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migration_history: backfill begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO axiaops.migration_history
		    (version, name, direction, status, started_at, finished_at,
		     duration_ms, file_sha256, applied_by_actor, applied_by_image)
		VALUES ($1, $2, 'up', 'backfilled', now(), now(),
		        NULL, $3, 'backfill', $4)
	`)
	if err != nil {
		return fmt.Errorf("migration_history: backfill prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	count := 0
	for _, v := range idx.versions {
		if int64(v) > smVersion {
			break
		}
		mf := idx.byVersion[v]
		if mf.upPath == "" {
			continue
		}
		_, sha, err := idx.readBytes(efs, v, "up")
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, int64(v), mf.name, sha, ident.image); err != nil {
			return fmt.Errorf("migration_history: backfill insert v=%d: %w", v, err)
		}
		count++
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration_history: backfill commit: %w", err)
	}
	slog.Info("migration_history: backfilled applied versions", "count", count, "migration_state_version", smVersion)
	return nil
}

// detectDrift compares the live SHA-256 of every embedded .up.sql with the
// most recent succeeded non-backfilled checksum recorded for that version.
// Mismatch increments the drift counter and logs a warning. With
// MIGRATION_HISTORY_STRICT=true a single mismatch returns an error so the
// boot fails.
func detectDrift(ctx context.Context, conn *sql.Conn, efs fs.FS, idx *migrationFSIndex) error {
	strict := os.Getenv("MIGRATION_HISTORY_STRICT") == "true"
	type drift struct {
		version  uint
		expected string
		observed string
	}
	var drifts []drift
	for _, v := range idx.versions {
		mf := idx.byVersion[v]
		if mf.upPath == "" {
			continue
		}
		_, observed, err := idx.readBytes(efs, v, "up")
		if err != nil {
			return err
		}
		var expected sql.NullString
		err = conn.QueryRowContext(ctx, `
			SELECT file_sha256
			FROM axiaops.migration_history
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
			return fmt.Errorf("migration_history: drift read v=%d: %w", v, err)
		}
		if !expected.Valid || expected.String == observed {
			continue
		}
		drifts = append(drifts, drift{version: v, expected: expected.String, observed: observed})
		// Emitted to slog only — drift detection runs inside Migrate(),
		// which only the short-lived migrate / axiaopsctl binaries call.
		// They have no /metrics endpoint and exit before any scraper could
		// reach them, so a Prometheus counter would be dead code. The log
		// flows through the same observability pipeline that already
		// alerts on warn-level events.
		slog.Warn("migration_history: file checksum drift detected",
			"version", v, "name", mf.name,
			"expected_sha256", expected.String, "observed_sha256", observed,
			"strict", strict,
		)
	}
	if len(drifts) > 0 && strict {
		var versions []string
		for _, d := range drifts {
			versions = append(versions, strconv.FormatUint(uint64(d.version), 10))
		}
		return fmt.Errorf("migration_history: STRICT drift refusing to start; versions=%s", strings.Join(versions, ","))
	}
	return nil
}

// runUpLoop applies all pending up migrations one step at a time, recording a
// history row around each. Caller has already acquired the wrapper-level
// advisory lock and finished orphan recovery / backfill / drift detection.
func runUpLoop(ctx context.Context, conn *sql.Conn, m *migrate.Migrate, efs fs.FS, idx *migrationFSIndex, ident runtimeIdentity) error {
	for {
		current, _, vErr := m.Version()
		hasCurrent := true
		if vErr != nil {
			if errors.Is(vErr, migrate.ErrNilVersion) {
				hasCurrent = false
			} else {
				return fmt.Errorf("migration_history: read pre-step version: %w", vErr)
			}
		}
		v, name, ok := idx.nextPendingUp(current, hasCurrent)
		if !ok {
			return nil
		}
		_, sha, err := idx.readBytes(efs, v, "up")
		if err != nil {
			return err
		}
		started := time.Now().UTC()
		id, err := recordStarted(ctx, conn, v, name, "up", sha, ident)
		if err != nil {
			return err
		}
		stepErr := m.Steps(1)
		var shortLimit migrate.ErrShortLimit
		if errors.Is(stepErr, migrate.ErrNoChange) || errors.As(stepErr, &shortLimit) {
			// Library disagrees with our index. Resolve the started row and exit.
			if cerr := completeRow(ctx, conn, id, "failed", started,
				"library returned no-change despite indexed pending version", false, false); cerr != nil {
				return cerr
			}
			return fmt.Errorf("migration_history: library returned %w for indexed pending v=%d", stepErr, v)
		}
		_, dirty, smHasRow, smErr := migrationStateRead(ctx, conn)
		if smErr != nil {
			return smErr
		}
		if stepErr != nil {
			if cerr := completeRow(ctx, conn, id, "failed", started, truncateError(stepErr), dirty, smHasRow); cerr != nil {
				return cerr
			}
			return fmt.Errorf("migration_history: up step v=%d: %w", v, stepErr)
		}
		if cerr := completeRow(ctx, conn, id, "succeeded", started, "", dirty, smHasRow); cerr != nil {
			return cerr
		}
	}
}

// runDownSteps rolls back exactly n steps (Steps(-1) called n times), each
// wrapped in a started/succeeded|failed history row.
func runDownSteps(ctx context.Context, conn *sql.Conn, m *migrate.Migrate, efs fs.FS, idx *migrationFSIndex, n int, ident runtimeIdentity) error {
	if n <= 0 {
		return fmt.Errorf("migration_history: down steps must be positive, got %d", n)
	}
	for i := 0; i < n; i++ {
		current, _, vErr := m.Version()
		if vErr != nil {
			if errors.Is(vErr, migrate.ErrNilVersion) {
				return nil
			}
			return fmt.Errorf("migration_history: read pre-down version: %w", vErr)
		}
		v := current
		name, hasDown := idx.lookupDown(v)
		if !hasDown {
			return fmt.Errorf("migration_history: no .down.sql in fsIndex for v=%d", v)
		}
		_, sha, err := idx.readBytes(efs, v, "down")
		if err != nil {
			return err
		}
		started := time.Now().UTC()
		id, err := recordStarted(ctx, conn, v, name, "down", sha, ident)
		if err != nil {
			return err
		}
		stepErr := m.Steps(-1)
		_, dirty, smHasRow, smErr := migrationStateRead(ctx, conn)
		if smErr != nil {
			return smErr
		}
		if stepErr != nil {
			if cerr := completeRow(ctx, conn, id, "failed", started, truncateError(stepErr), dirty, smHasRow); cerr != nil {
				return cerr
			}
			return fmt.Errorf("migration_history: down step v=%d→%d: %w", v, int64(v)-1, stepErr)
		}
		if cerr := completeRow(ctx, conn, id, "succeeded", started, "", dirty, smHasRow); cerr != nil {
			return cerr
		}
	}
	return nil
}

// recordForce writes a single terminal force history row. Caller is
// responsible for invoking m.Force(version) under the same advisory lock,
// before this call.
//
// migration_state_dirty_after is read back from the live table rather than
// hardcoded — golang-migrate's current Force semantics are
// SetVersion(N, false), so the snapshot is always false today, but reading
// it keeps the column self-consistent across all code paths if upstream
// semantics ever change.
func recordForce(ctx context.Context, conn *sql.Conn, version int64, idx *migrationFSIndex, ident runtimeIdentity) error {
	name := "unknown"
	if mf, ok := idx.byVersion[uint(version)]; ok {
		name = mf.name
	}
	_, dirty, smHasRow, err := migrationStateRead(ctx, conn)
	if err != nil {
		return err
	}
	var nullableDirty sql.NullBool
	if smHasRow {
		nullableDirty = sql.NullBool{Bool: dirty, Valid: true}
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO axiaops.migration_history
		    (version, name, direction, status, started_at, finished_at,
		     duration_ms, file_sha256, applied_by_actor, applied_by_image,
		     migration_state_dirty_after)
		VALUES ($1, $2, 'force', 'succeeded', now(), now(),
		        NULL, NULL, $3, $4, $5)
	`, version, name, ident.actor, ident.image, nullableDirty); err != nil {
		return fmt.Errorf("migration_history: insert force row: %w", err)
	}
	return nil
}

// withHistoryConn opens a dedicated single-connection *sql.DB on the migration
// URL, pins a *sql.Conn from it, acquires the wrapper-level advisory lock,
// runs fn, then releases the lock and closes the connection.
//
// All history bookkeeping must use this conn — never the migrate driver's
// connection. The lock holds for the entire fn duration so concurrent
// wrappers serialise.
func withHistoryConn(ctx context.Context, migrationURL string, fn func(conn *sql.Conn) error) (err error) {
	db, openErr := sql.Open("postgres", migrationURL)
	if openErr != nil {
		return fmt.Errorf("migration_history: open history db: %w", openErr)
	}
	defer func() {
		if cerr := db.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("migration_history: close history db: %w", cerr)
		}
	}()
	conn, connErr := db.Conn(ctx)
	if connErr != nil {
		return fmt.Errorf("migration_history: pin history conn: %w", connErr)
	}
	defer func() {
		if cerr := conn.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("migration_history: release history conn: %w", cerr)
		}
	}()
	if _, lockErr := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationHistoryLockID); lockErr != nil {
		return fmt.Errorf("migration_history: acquire wrapper lock: %w", lockErr)
	}
	defer func() {
		// Use a fresh background context so cancellation during fn doesn't
		// leak the session-level lock — the conn still holds it until close
		// regardless, but explicit unlock keeps semantics clean.
		//
		// pg_advisory_unlock returns boolean — true if this session held
		// the lock, false otherwise. A false here means the backend died
		// and database/sql silently reconnected to a fresh one that never
		// held our lock. The advisory lock is gone either way (PG drops
		// session locks on backend death), but logging the false makes
		// "we crashed mid-operation" diagnosable instead of invisible.
		var unlocked bool
		if uErr := conn.QueryRowContext(context.Background(),
			`SELECT pg_advisory_unlock($1)`, migrationHistoryLockID,
		).Scan(&unlocked); uErr != nil && err == nil {
			err = fmt.Errorf("migration_history: release wrapper lock: %w", uErr)
		} else if uErr == nil && !unlocked {
			slog.Warn("migration_history: pg_advisory_unlock returned false; backend likely reconnected mid-operation")
		}
	}()
	return fn(conn)
}
