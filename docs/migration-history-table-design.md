# Migration History Table — Design

**Status:** Implemented | **Owner:** platform | **Companion to:** [`docs/migrations.md`](migrations.md)

> **Naming update 2026-05-10:** golang-migrate's bookkeeping table was renamed `schema_migrations` → `migration_state` (singular, accurately reflects single-row cardinality, pairs cleanly with `migration_history`). The migrate wrapper's Bootstrap layer ALTER-renames the table idempotently on existing DBs; on fresh DBs the new name is created from the start via `MigrationsTable: "migration_state"` in `migratepg.WithInstance`. References to the old name throughout this doc have been updated; the renaming rationale lives in `docs/migrations.md` §Schema Migrations Table.

## Goal

Keep a permanent, durable record of **every migration event** applied to an
AxiaOps database — every up, every down, every `force`, including failures —
with enough metadata to answer the questions `axiaops.migration_state` cannot:

- *When* was migration N applied to this env, and how long did it take?
- *Which build of the app* (binary SHA / image tag) ran it?
- *Which file* was applied — i.e. is the on-disk SHA today the same SHA that
  was applied two months ago?
- Did it ever roll back, then re-apply, on this database?

The trigger for this work was the 010.down / 024 search-path bug: each env had
the same `version=24, dirty=false` row, and yet the schemas drifted because the
files had been *edited* between applications and we had no way to see that from
the DB. A history table with file checksums turns "schema drift caused by
mutated migrations" from "discover by stack trace" into "tail one query."

### Row durability vs row mutability

The table is **durable at the event grain**: no row is ever deleted by the
wrapper, no event is forgotten or rewritten. Each migration event produces
exactly one row, identified by `id BIGSERIAL`.

The row's *lifecycle*, however, is not append-only — its `status` field
transitions exactly once, from `started` (set at INSERT, before `Steps(±1)`)
to one of `succeeded` / `failed` (set by a single UPDATE on the same row,
after the step returns). `backfilled` and `force` rows are inserted directly
at their terminal status and never UPDATEd. Pairing started/finished as two
separate rows would double the write volume and make orphan recovery span
two tables; the single-row-with-UPDATE shape is the deliberate trade-off.

The post-INSERT UPDATE means this table cannot be replicated to a downstream
audit sink by tailing INSERT-only logical replication — the UPDATEs would be
missed. Operators who want a strict append-only audit trail should ship rows
downstream by polling the table on a schedule, not by streaming WAL.

## Non-goals

The history table is **infra metadata**, not a domain audit. Things it
explicitly does *not* do:

- **No replacement for `migration_state`**. golang-migrate continues to own
  `axiaops.migration_state` (single row, version + dirty). We layer on top.
  Touching that table directly is still off-limits.
- **No row-counts, no schema diffs, no DDL parsing.** Recording how many rows
  the migration touched, or what columns it added, is out of scope.
- **No org scoping, no RLS.** This is per-database, not per-org. DML lives
  with `axiaops_owner`; the app user has SELECT only.
- **No integration with `axiaops.audit_log`.** Audit log is per-org and
  user-driven. Migration history is per-cluster and operator-driven.
- **No web UI**. CLI / SQL view is sufficient.
- **No multi-env aggregation.** "Compare migration history across dev-1,
  dev-2, staging" is a future-tooling concern.
- **No backfill of timing/actor for already-applied envs.** We backfill
  *checksums only* (see §Drift detection). Backfilled rows surface as
  `status='backfilled'` with NULL timing.

## Schema

One table, in the `axiaops` schema, owned by `axiaops_owner`. App user
(`axiaops`) gets `SELECT` only — never `INSERT/UPDATE/DELETE`.

```sql
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
    migration_state_dirty_after BOOLEAN
);

CREATE INDEX IF NOT EXISTS migration_history_version_idx
    ON axiaops.migration_history (version, started_at DESC);

CREATE INDEX IF NOT EXISTS migration_history_started_at_idx
    ON axiaops.migration_history (started_at DESC);
```

Privileges are issued by `Bootstrap()` (see §Bootstrap), not by a numbered
migration — the table exists before `000_init.up.sql` runs and so cannot rely
on the `ALTER DEFAULT PRIVILEGES` shipped there.

### Column-by-column rationale

| Column | Type | Null? | Default | Why |
|---|---|---|---|---|
| `id` | `BIGSERIAL` | NO | seq | Stable insertion order independent of `started_at` (so two history rows in the same millisecond still order deterministically). The same version can appear many times (up → down → up); `id` is the unique key, not `version`. |
| `version` | `BIGINT` | NO | — | Mirror of `migration_state.version` — the integer prefix of the file. Indexed because every operator query starts "show me history for version N." `BIGINT` matches what golang-migrate uses internally. |
| `name` | `TEXT` | NO | — | Filename without extension or version prefix, e.g. `add_account_id_to_accounts`. Derivation is fixed by `name = strings.TrimSuffix(strings.TrimPrefix(identifier, fmt.Sprintf("%03d_", version)), ".up.sql")` (or `.down.sql` for downs). The `%03d` zero-pad is load-bearing — version `0` → `000`, version `24` → `024`. |
| `direction` | `TEXT` | NO | — | One of `up`, `down`, `force`. CHECK constraint enforces the enum. `force` rows are written exclusively by the `axiaopsctl migrate force` subcommand (see §Operator UX); the wrapper itself never produces a `force` row because `migrate.Force(N)` does not go through `Steps()` — it only calls `SetVersion(N, false)` against `migration_state`. |
| `status` | `TEXT` | NO | — | One of `started`, `succeeded`, `failed`, `backfilled`. `started` is inserted *before* the migration runs so a crashed wrapper still leaves a forensic trail; the wrapper updates it to `succeeded`/`failed` on completion. `backfilled` is reserved for the one-shot rows inserted on first deploy of this feature for migrations applied before the table existed. |
| `started_at` | `TIMESTAMPTZ` | NO | `now()` | When the wrapper inserts the `started` row. UTC by convention. |
| `finished_at` | `TIMESTAMPTZ` | YES | NULL | NULL while `status='started'`. Set on the wrapper's UPDATE when the migration returns. Stays NULL forever if the wrapper crashes mid-run — that's the forensic signal "this row never finished." |
| `duration_ms` | `BIGINT` | YES | NULL | Computed Go-side as `(finishedAt - startedAt).Milliseconds()` and stored. NULL while in-flight, NULL forever for crashed-mid-run rows, NULL for backfilled rows. Includes the wrapper's pre-step COMMIT and post-step UPDATE wall-clock — for a sub-second migration the bookkeeping overhead is detectable. Treat values as ±100 ms accurate when comparing across builds. |
| `error_message` | `TEXT` | YES | NULL | The error string from the migration driver, truncated Go-side to a hard cap of `maxErrorMessageBytes = 4096`. `CHECK (length(error_message) <= 4096)` enforces it at the DB. NULL on success. Captured even on `dirty` outcomes so the operator can read what went wrong without grepping container logs. |
| `file_sha256` | `TEXT` | YES | NULL | Lowercase hex SHA-256 of the migration file's bytes, validated by a regex CHECK. The hash is taken of the *raw bytes as embedded in `migrationsFS` at build time* — no normalization, no whitespace trimming, no line-ending fixup. Linux/macOS dev and Linux CI build identical hashes; a Windows developer running with `core.autocrlf=true` would produce a different hash, which is out of scope for this codebase (we do not support Windows builds). NULL for `force` (no file executed) and for `backfilled` rows where we baseline against the live file. |
| `applied_by_actor` | `TEXT` | YES | NULL | "Who/what applied this." Container hostname (App Runner / docker compose container ID prefix) by default; override via `MIGRATION_ACTOR_LABEL` env var when running `axiaopsctl` from a bastion. Free-form text on purpose — operators want to grep, not enforce a schema. |
| `applied_by_image` | `TEXT` | YES | NULL | The build identity of the binary that ran the wrapper: `${APP_VERSION}@${APP_COMMIT_SHA}` from the existing observability env vars (see `services/shared/CLAUDE.md` → Logging). Falls back to `unknown@unknown` if either is empty — the wrapper logs a one-shot startup WARN (`migration_history: applied_by_image will be 'unknown@unknown'; drift forensics degraded`) when this fallback fires. CI and deploy pipelines must set both. |
| `migration_state_dirty_after` | `BOOLEAN` | YES | NULL | Snapshot of `axiaops.migration_state.dirty` read by a separate `SELECT` on the history-writer connection *after* `m.Steps(±1)` returns, just before the wrapper UPDATE. **Always populated on the post-step UPDATE path** — success or failure — so readers don't special-case NULL on succeeded rows. NULL means *only* one of: row is still in-flight (`status='started'`), wrapper crashed before UPDATE, or row is `backfilled`/`force` (no UPDATE happens for those). On the success path it is redundant with `status='succeeded'`; the column earns its keep on the post-failure dirty state and as the forensic signal "this row's UPDATE never ran." |

### Columns explicitly considered and dropped

- **`environment` (`dev` / `staging` / `production`)**. The DB already knows
  which env it is; recording it in every row is redundant and lies after a
  cross-env restore. `applied_by_actor` and `applied_by_image` carry enough.
- **`source_path` / `source_filename`**. `version` + `name` + `direction`
  reconstruct the filename uniquely.
- **`session_user` / `current_user` (DB role)**. Always `axiaops_owner` by
  policy.
- **`postgres_version`**. Once-per-cluster, not once-per-migration.
- **`hostname` separate from `applied_by_actor`**. One field, free-form,
  good enough.

## Population mechanism

### Options considered

**(a) DB trigger on `axiaops.migration_state`** — `AFTER INSERT/UPDATE/DELETE`
fires a function that appends to `migration_history`. Rejected: the trigger
sees only `version` and `dirty`. It cannot compute `file_sha256` (file bytes
never enter Postgres as data, only as parsed-and-applied DDL), cannot record
the actor or the image, and has no robust way to distinguish "this dirty=true
update is the start" from "this dirty=false update is the end" without
stateful book-keeping. Without checksums the table is just a slower
`migration_state`, and checksums are precisely the column that would have
caught the 010.down / 024 drift bug.

**(b) Go-side stepwise wrapper** — replace `m.Up()` with a loop over
`m.Steps(1)` that records around each step. We control the boundary so we can
read the file bytes (already in `migrationsFS`), set actor + image from env,
and time the operation. Down/up symmetry is straightforward. The cost is that
a CLI-run migration via the upstream `migrate` binary from a bastion (formerly
documented as a supported path) produces no history row.

**(c) Hybrid trigger + wrapper**. Considered and rejected: two seams to keep in
lockstep, plus the same brittle "is this UPDATE a start or an end" trigger
logic option (a) had.

### Recommendation: **(b) Go-side wrapper**, plus a CLI subcommand

We pick option (b) and close the bastion-CLI gap by shipping the wrapper as a
subcommand of our own binary. Operators run `axiaopsctl migrate up` instead of
`migrate up`; same Go binary that the api/ingestion startup paths call into,
just with a different entrypoint. See §Operator UX for the subcommand layout.
This keeps the seam single.

### Connection model

The wrapper opens **two distinct database connections**, both against
`MIGRATION_DATABASE_URL` (owner role). Calling `Migrate()` with `DATABASE_URL`
(app role) is a configuration error and will fail with permission errors on
the very first history INSERT — `migration_history` is created by `Bootstrap()`
*before* the `ALTER DEFAULT PRIVILEGES` in `000_init.up.sql` runs, so the app
role does **not** inherit DML on it (see §Bootstrap, M8 grant).

1. **Driver connection** — the existing `*sql.DB` opened in
   `postgres.Migrate()` and handed to `migratepg.WithInstance`. Owns
   `migration_state` and the migration DDL. **Owned by golang-migrate** for
   the lifetime of the call. The wrapper does not run side queries on this
   connection.
2. **History-writer connection** — a separate `*sql.DB` opened by the wrapper
   on the same `MIGRATION_DATABASE_URL`. Used for: the wrapper-level advisory
   lock (see below), every `INSERT INTO migration_history`, every UPDATE, the
   `SELECT dirty FROM migration_state` snapshot, and the orphan-recovery
   queries. Closed in `defer` when `Migrate()` returns.

### Wrapper-level advisory lock

`golang-migrate/migrate/v4@v4.19.1`'s `Steps(n)` acquires an advisory lock via
`m.lock()` on entry and releases it via `m.unlockErr()` on return — i.e. it
takes and drops the lock once per call. Because our wrapper invokes
`Steps(1)` in a loop with the history-row INSERT/UPDATE bracketing each call,
the library lock is **released between iterations**. A second replica
starting at the same time can interleave its own `Steps(1)` between our
INSERT and our UPDATE, and the resulting history rows would be incoherent.

The wrapper therefore takes its own session-level advisory lock on the
history-writer connection, held for the entire duration of the loop:

```go
const migrationHistoryLockID int64 = 0x417869614F70734D // ASCII "AxiaOpsM" — full brand "AxiaOps"
                                                        // (7 bytes) + "M" for Migration. Exactly
                                                        // 8 bytes to fit int64. Distinct from any
                                                        // golang-migrate-internal lock ID to
                                                        // avoid self-deadlock. Sibling-lock
                                                        // convention: replace the trailing letter
                                                        // (e.g. "AxiaOpsB" for a future bootstrap
                                                        // lock, "AxiaOpsR" for replication).

if _, err := historyConn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationHistoryLockID); err != nil {
    return fmt.Errorf("migrate: acquire history lock: %w", err)
}
defer func() {
    _, _ = historyConn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationHistoryLockID)
}()
```

Two replicas calling `Migrate()` concurrently now serialise: replica B blocks
on `pg_advisory_lock` until replica A has finished its entire loop and
released it.

### The loop

The wrapper must know **V** (the version it's about to apply) *before*
`Steps(1)` so it can write the `started` row pre-step. `golang-migrate`'s own
source driver (`migrate.Migrate.sourceDrv`) is unexported, so we cannot ask
it. The wrapper indexes `migrationsFS` itself once at startup — walking the
embed.FS yields the full set of `(version, name, hasUp, hasDown)` tuples — and
computes the next pending version as
`min(version | version > current_version AND hasUp)`. This is V; we
sanity-check it against `m.Version()` after `Steps(1)` returns.

```go
// indexEmbeddedMigrations walks migrationsFS once at startup and returns a
// map[version] -> (name, hasUp, hasDown). Implementation: fs.WalkDir on
// "migrations/", regex-match "(\d+)_(\w+)\.(up|down)\.sql".
fsIndex := indexEmbeddedMigrations(migrationsFS)

for {
    // 1. Determine the next pending version *before* Steps(1).
    current, _, vErr := m.Version()
    if vErr != nil && !errors.Is(vErr, migrate.ErrNilVersion) {
        return fmt.Errorf("migrate: read pre-step version: %w", vErr)
    }
    V, name, ok := fsIndex.nextPendingUp(current) // smallest V > current with an up file
    if !ok {
        break // No more pending up migrations.
    }
    identifier := fmt.Sprintf("%03d_%s.up.sql", V, name)
    fileBytes, err := migrationsFS.ReadFile("migrations/" + identifier)
    if err != nil {
        return fmt.Errorf("migrate: read embedded file %s: %w", identifier, err)
    }
    sha := fmt.Sprintf("%x", sha256.Sum256(fileBytes))

    // 2. INSERT-and-COMMIT the started row on the history-writer connection.
    //    Durable on disk before Steps(1) runs.
    historyID, err := insertStartedRow(historyConn, V, name, "up", sha)
    if err != nil {
        return fmt.Errorf("migrate: insert started row for V=%d: %w", V, err)
    }

    // 3. Run one step.
    stepErr := m.Steps(1)

    // 4. ErrNoChange / ErrShortLimit here means golang-migrate disagreed with
    //    our index — possible if the embed.FS was rebuilt against a different
    //    binary than the DB has seen. Resolve the started row and exit.
    var shortLimit migrate.ErrShortLimit
    if errors.Is(stepErr, migrate.ErrNoChange) || errors.As(stepErr, &shortLimit) {
        completeRowAsNeverStarted(historyConn, historyID,
            "library returned no-change despite indexed pending version")
        break
    }

    // 5. Read post-step version + dirty flag; sanity-check.
    postV, dirty, _ := m.Version()
    if stepErr == nil && postV != V {
        slog.Warn("post-step version mismatch", "expected", V, "got", postV)
    }

    // 6. Complete the row.
    if stepErr != nil {
        completeRowFailed(historyConn, historyID, dirty, stepErr)
        return fmt.Errorf("migrate: step %d: %w", V, stepErr)
    }
    completeRowSucceeded(historyConn, historyID, dirty)
}
```

The loop terminates on either of two sentinels emitted by `m.Steps(1)` —
`migrate.ErrNoChange` (returned when no pending version is known to the
library, typically the first call against an up-to-date DB) or
`migrate.ErrShortLimit{Short: 1}` (returned from subsequent calls once the
pending queue has drained). In practice with the index-driven approach
above, we exit via the `nextPendingUp` `!ok` path before either sentinel
fires; the sentinel handler is defensive against index/library disagreement.

### Pre-step row vs post-step UPDATE

The `started` row must be visible to a future orphan-recovery pass even if the
wrapper process is killed during `m.Steps(1)`. Therefore:

1. **Before** `m.Steps(1)`, the wrapper opens a short-lived `*sql.Tx` on the
   history-writer connection, executes the `INSERT ... RETURNING id`, and
   calls `tx.Commit()`. The row is durable on disk before the migration
   step starts.
2. **After** `m.Steps(1)` returns, the wrapper executes a single `UPDATE` on
   the history-writer connection (auto-commit) that sets `status`,
   `finished_at`, `duration_ms`, `error_message`, and
   `migration_state_dirty_after = (SELECT dirty FROM axiaops.migration_state)`.

The two are deliberately on **different transactions on a different connection
from the migration driver**. That separation is the property the orphan
detector relies on: a crashed wrapper leaves the `started` row durably
committed even though the migration step rolled back, and the next boot can
distinguish "migration applied but UPDATE never ran" from "migration never
started."

### Down direction

Same wrapper structure with `Steps(-1)`. The asymmetry: for an up step, V is
the *post-step* version; for a down step, V is the *pre-step* version (the
version we're rolling back FROM). The wrapper captures V before the call,
hashes the `.down.sql` for V, writes the started row keyed by V, runs
`Steps(-1)`, then expects `m.Version()` to report `V-1`.

```go
// Loop iteration for a down step.
current, _, vErr := m.Version()
if vErr != nil {
    if errors.Is(vErr, migrate.ErrNilVersion) {
        break // Nothing to roll back.
    }
    return fmt.Errorf("migrate down: read pre-step version: %w", vErr)
}
V := current // V is the version we're rolling back FROM.
name, hasDown := fsIndex.lookupDown(V)
if !hasDown {
    return fmt.Errorf("migrate down: no .down.sql in fsIndex for V=%d", V)
}
identifier := fmt.Sprintf("%03d_%s.down.sql", V, name)
fileBytes, err := migrationsFS.ReadFile("migrations/" + identifier)
if err != nil {
    return fmt.Errorf("migrate down: read embedded file %s: %w", identifier, err)
}
sha := fmt.Sprintf("%x", sha256.Sum256(fileBytes))

historyID, err := insertStartedRow(historyConn, V, name, "down", sha)
if err != nil {
    return fmt.Errorf("migrate down: insert started row for V=%d: %w", V, err)
}

stepErr := m.Steps(-1)

postV, dirty, _ := m.Version()
if stepErr == nil && postV != V-1 {
    slog.Warn("post-down version mismatch", "expected", V-1, "got", postV)
}

if stepErr != nil {
    completeRowFailed(historyConn, historyID, dirty, stepErr)
    return fmt.Errorf("migrate down: step %d→%d: %w", V, V-1, stepErr)
}
completeRowSucceeded(historyConn, historyID, dirty)
```

Advisory lock, two-connection model, pre-step commit / post-step UPDATE, and
orphan-recovery handling all apply unchanged. The orphan-recovery truth
table (§Failure modes) branches on direction — see there for how a crashed
down step is distinguished from a crashed up step.

### Force direction

`force` is **not** the up/down wrapper's responsibility. `axiaopsctl migrate
force N` issues `m.Force(N)` directly and writes a single history row with
`direction='force'`, `status='succeeded'`, `file_sha256=NULL` (no file was
applied). This is intentional: `Force(N)` does no DDL — it only writes
`(N, false)` into `migration_state`. Because the row is born terminal,
there is no `started`→`succeeded` lifecycle and no orphan-recovery case for
force.

The force subcommand acquires the same wrapper-level advisory lock
(`migrationHistoryLockID`) as the up/down loop before calling `m.Force(N)`
and writing the history row. Without the lock, a concurrent `axiaopsctl
migrate up` from another replica could observe `migration_state` mid-force
and the orphan detector could race the force-row INSERT.

## Bootstrap

The history table itself has to exist before we can record into it. Two
options were considered:

**(i) Create it in `postgres.Bootstrap()`** alongside the schema and user
setup — `CREATE TABLE IF NOT EXISTS axiaops.migration_history (...)`.

**(ii) Make it a numbered migration** (e.g. `025_migration_history.up.sql`)
and special-case it in the wrapper.

### Recommendation: **(i) `Bootstrap()`**

The whole point of `Bootstrap()`
(`services/shared/storage/postgres/migrate.go:30`) is "infrastructure that
has to exist before golang-migrate runs." Numbering the table as a migration
creates a self-referential mess: 025 would have to record its own application
into a table that 025 just created, and the wrapper would need to special-case
"skip recording for the migration whose version equals the
migration_history-creation migration."

### Privileges (load-bearing)

The `ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops GRANT ... ON TABLES TO axiaops`
in `000_init.up.sql` only applies to tables created **after** that statement
runs. `migration_history` is created in `Bootstrap()`, which runs *before*
golang-migrate runs `000_init`. Therefore, default privileges do not grant the
app user anything on `migration_history`, and we must grant explicitly.
`Bootstrap()` must execute, after the `CREATE TABLE IF NOT EXISTS`:

```sql
GRANT SELECT ON axiaops.migration_history TO axiaops;
-- INSERT/UPDATE/DELETE are NOT granted: DML is owner-only.
```

This is a hard requirement, not a footnote. Without it the app user has no
read access to the table and `make migration-history` from a non-owner
operator account silently shows "permission denied for table
migration_history."

### Schema evolution

The table's schema lives in `Bootstrap()` as a SQL string constant — not as a
migration file. Schema *changes* to `migration_history` (e.g. "add a column")
go in `Bootstrap()` itself as additional `ALTER TABLE ... ADD COLUMN IF NOT
EXISTS` statements, idempotent and gated. Same posture as schema/user
creation.

**CHECK-constraint enum changes** (e.g. adding a new `status` value beyond
`started/succeeded/failed/backfilled`) are *not* idempotent via a simple
`ADD CONSTRAINT IF NOT EXISTS` — Postgres has no such form for CHECK, and
re-issuing `ALTER TABLE ... ADD CONSTRAINT` against an existing constraint
name fails. The pattern when this becomes necessary: name the constraint
explicitly (`status_check`), `DROP CONSTRAINT IF EXISTS status_check`, then
`ADD CONSTRAINT status_check CHECK (...)`. Until that need arises, prefer
adding new orthogonal columns over extending existing enums.

The convenience view `migration_history_v` (see §Operator UX) was *originally*
designed to live in Bootstrap. **Removed during implementation** (2026-05-10):
the view was a `SELECT *` + `LEFT(file_sha256, 8)` wrapper with no joins, no
WHERE, no aggregate, and the schema-evolution friction it created (every
column rename needed a `DROP VIEW` + `CREATE` dance because Postgres'
`CREATE OR REPLACE VIEW` can't rename output columns) wasn't worth the
saved 3 lines in the CLI's printer. The 8-char short hash is now computed
Go-side. Bootstrap idempotently drops any leftover view from earlier
deploys.

## Drift detection

The wrapper records `file_sha256` on every successful step. On every
subsequent boot, before any new work, the wrapper reads back the most recent
non-NULL checksum per `version` (excluding `backfilled` rows) and compares to
the live file in `migrationsFS`.

```sql
SELECT file_sha256
FROM axiaops.migration_history
WHERE version = $1
  AND direction = 'up'
  AND status = 'succeeded'
  AND file_sha256 IS NOT NULL
ORDER BY id DESC
LIMIT 1;
```

`ORDER BY id DESC` (not `started_at DESC`) — same-millisecond rows must
order deterministically, and `id` is a `BIGSERIAL` so insertion order is
guaranteed.

### Posture

**warn-and-continue** in v1: log a structured warning, keep starting the
service. Customer self-hosted installs can have legitimate reasons for drift
(operator hand-edited a migration, restored a backup from before a column
existed) and refusing to start would turn drift into a deploy outage on
installs we don't operate.

A `MIGRATION_HISTORY_STRICT=true` env var flips the posture to
refuse-to-start; we default it on in CI and our own staging/dev envs and off
elsewhere. The escalation path to defaulting it on for production is a
quarter of real data on how often legitimate drift occurs.

#### No Prometheus counter (revised from earlier draft)

The original draft called for an `axiaops_migration_history_drift_total`
Prometheus counter alongside the slog.Warn. Dropped during implementation
once we noticed the wiring doesn't reach a scraper:

- `Migrate()` is only called by the dedicated migrate Docker image and
  `axiaopsctl` — both short-lived, no `/metrics` endpoint, exit within
  ~500 ms.
- `services/api` and `services/ingestion` do **not** call `postgres.Migrate`;
  they assume the migrate container has already run. So the long-lived
  binaries with `/metrics` never run drift detection and never increment any
  counter.

Net effect of a counter would be: incremented in-memory in a process that
exits before any Prometheus scrape can reach it, then garbage-collected.
Pure dead code, plus a transitive dep that bloats the slim migrate Docker
image.

The slog.Warn flows through the same observability pipeline (Loki / journald
/ stdout) that already handles every other warn-level event in this project,
so a log-based alert
`count_over_time({job=~"axiaops-migrate"} |= "file checksum drift detected" [1h]) > 0`
is workable. Active inspection lives in `bin/axiaopsctl migrate drift`,
which queries the database directly without touching the registry.

If we ever wire `postgres.Migrate` (or a thin `postgres.CheckDrift` helper)
into api/ingestion startup — long-lived binaries that DO serve `/metrics` —
the counter becomes useful and we'd reintroduce it. Today it would just be
noise.

### Backfill

Existing envs (dev-1, dev-2, staging) already have 24+ migrations applied and
zero history rows. The backfill block runs **iff `migration_history` is empty
AND `migration_state` has at least one row**:

1. `Bootstrap()` creates the table and grants.
2. Right after, before `Migrate()` runs new migrations, the wrapper checks the
   condition. On match: insert one row per applied version with
   `status='backfilled'`, `direction='up'`, NULL timing, NULL
   `error_message`, **observed (live-file) `file_sha256`**,
   `applied_by_actor='backfill'`, `applied_by_image=<current image>`.
3. Drift detection runs *after* backfill. Strictly: drift detection skips
   `backfilled` rows when reading the "expected" checksum, so the first
   invocation never reports drift — we baseline whatever is on disk as ground
   truth.

If an operator `TRUNCATE`s `migration_history` post-rollout and restarts, the
next boot **does** re-baseline (empty history + non-empty `migration_state`
trips the condition again). This is acceptable: the table is forensic, not
evidentiary (see §Open questions). Operators who truncate the audit trail
have the audit trail they deserve.

**Partial deletion** (e.g. `DELETE FROM migration_history WHERE id < N`)
leaves the table non-empty and does *not* trigger backfill. Drift detection
then runs against a hole-y history and may report drift for any version
whose checksum row was deleted. This is by design: the backfill condition
is unambiguous (empty / non-empty) rather than per-version-coverage
checking, at the cost that gaps caused by partial deletion are not
auto-repaired. Operators who want gaps backfilled should `TRUNCATE` rather
than `DELETE`.

## Failure modes

### Wrapper crashes mid-recording — orphan recovery

The orphan detector runs once at the top of every `Migrate()` call, **after**
the wrapper-level advisory lock is acquired and **before** the `Steps(±1)`
loop. Holding the lock during recovery prevents two replicas from racing on
the same orphan rows: replica B blocks on `pg_advisory_lock` while replica A
resolves orphans, then sees a clean state. Concretely: after acquiring
`migrationHistoryLockID`, the wrapper selects every row with
`status='started' AND finished_at IS NULL`.

For each orphan row `(history_id, version=V, direction=D)`:

1. Read `(current_version, dirty)` from `axiaops.migration_state`.
2. Decide. Each direction has a *target* version the migration should land
   at on success — for `up V` the target is `V`; for `down V` the target is
   `V-1`. The orphan resolver compares `current_version` against the target,
   not against V directly:

   | `direction` | `current_version` | `dirty` | Verdict | UPDATE |
   |---|---|---|---|---|
   | `up` | `== V` (target) | `false` | Up succeeded; only the post-step UPDATE was lost. | `status='succeeded'`, `finished_at=now()`, `duration_ms=NULL`, `error_message='timing lost; recovered post-crash'`, `migration_state_dirty_after=false`. |
   | `up` | `== V` | `true` | Up failed (golang-migrate left dirty=true); wrapper crashed before the UPDATE. | `status='failed'`, `finished_at=now()`, `error_message='migration failed; details lost in wrapper crash'`, `migration_state_dirty_after=true`. |
   | `up` | `== V-1` (pre-state) | `false` | Up step never started — wrapper crashed between INSERT and `Steps(1)`. Next loop iteration retries V. | `status='failed'`, `finished_at=now()`, `error_message='wrapper killed before migration step'`, `migration_state_dirty_after=NULL`. |
   | `down` | `== V-1` (target) | `false` | Down succeeded; only the post-step UPDATE was lost. | as for up-success above. |
   | `down` | `== V` | `true` | Down failed (dirty); wrapper crashed before the UPDATE. | as for up-failure above. |
   | `down` | `== V` (pre-state) | `false` | Down step never started — wrapper crashed between INSERT and `Steps(-1)`. Next loop iteration retries. | as for up-never-started above. |
   | any | none of the above | any | Should not happen. Log a structured warning with `(direction, V, current_version, dirty)` and treat as the lost-UPDATE case. | as for up-success above, plus a warning. |

Crucially: the algorithm is **direction-aware and version-exact**. The two
"never started" cases (`up` at `V-1`, `down` at `V`) are indistinguishable
without `direction` — both mean "migration_state is still at the row's
pre-state" — and they *must* be distinguished from "succeeded" because the
SQL/dirty state for a successful down (`V-1, false`) is identical to the
pre-state for a never-started up of `V` (`V-1, false`). The resolver only
declares success when `current_version` matches the *direction's target*
AND `dirty=false`.

`force` rows are never orphans — they are inserted directly at terminal
status (§Force direction) and have no started phase.

After the orphan UPDATE, the loop proceeds to `Steps(±1)` and either
re-runs the version (in the never-started rows above) or moves past it.

### Migration succeeds in postgres but the wrapper's UPDATE fails

Same path as orphan recovery row 1: a future boot will see
`current_version == V`, `dirty=false`, and complete the row.

### Idempotency requirements

- **No row is updated twice.** The `started` row is updated exactly once,
  by either the wrapper-on-success path or the orphan-recovery path.
- **`(version, direction)` is not unique.** A migration can legitimately run
  many times (up → down → up); the unique key is `id`. We do not add
  `UNIQUE (version, direction)`.
- **Concurrent wrappers serialise** on `migrationHistoryLockID` (the
  wrapper-level advisory lock).
- **Backfill is single-shot per truncate** — empty + non-empty is the only
  condition that triggers it.

## Operator UX

### `axiaopsctl` migrate subcommands

There is no new binary. `axiaopsctl` is the existing migrate Go binary at
`services/shared/cmd/migrate/main.go` (the same image the api/ingestion
deployments invoke as a one-off pre-deploy task). We extend that `main` with
flag-based subcommand dispatch so operators have a single entrypoint:

```text
axiaopsctl migrate up                # Bootstrap + Migrate (current default)
axiaopsctl migrate down N            # Steps(-N) with history recording
axiaopsctl migrate force N           # Force(N) + write a force history row
axiaopsctl migrate drift             # Print drift table (Go-side hash + DB compare)
axiaopsctl migrate history [V]       # Pretty-print migration_history rows
```

Canonical implementation path is `services/shared/cmd/migrate/main.go` (Go
module `axiaops.io/shared/cmd/migrate/`). The legacy entrypoint at
`services/migrate/main.go` is kept as a temporary backward-compat shim that
forwards to `axiaopsctl migrate up`, so existing Dockerfiles, CI scripts,
and the `migrate-image` Make target keep working unchanged. The shim is
deleted in a follow-up MR once `grep -r "services/migrate" .gitlab-ci.yml
deploy/ services/*/Dockerfile` returns no hits.

The make target `migrate-image` (existing) builds the binary; a new make
target invokes it locally:

```make
axiaopsctl: ## Build the migrate/operator CLI
    go build -o bin/axiaopsctl ./services/shared/cmd/migrate/

migration-history: axiaopsctl
    @MIGRATION_DATABASE_URL=$(MIGRATION_DATABASE_URL) bin/axiaopsctl migrate history $(V)

migration-history-drift: axiaopsctl
    @MIGRATION_DATABASE_URL=$(MIGRATION_DATABASE_URL) bin/axiaopsctl migrate drift
```

### SQL view (removed)

The original draft proposed a convenience view `axiaops.migration_history_v`
that exposed `LEFT(file_sha256, 8) AS file_sha256_short` alongside every
column of the base table. **Dropped during implementation 2026-05-10**: with
no joins, no WHERE, no aggregate, the view did nothing the base table didn't,
and the `DROP VIEW` + `CREATE VIEW` dance required for any column rename
(Postgres' `CREATE OR REPLACE VIEW` can't rename output columns) outweighed
the saved 3 lines in the CLI's printer. The 8-char short hash is now
computed Go-side. Bootstrap idempotently drops any leftover view from
earlier deploys.

Operator queries should `SELECT … FROM axiaops.migration_history` directly,
adding their own `ORDER BY started_at, id` — `id` (a `BIGSERIAL`) breaks
same-millisecond ties deterministically.

`LEFT(file_sha256, 8)` (8 hex chars = 32 bits) inline is **for at-a-glance
display only** — collision probability is ~10⁻⁵ over a few thousand rows,
fine for visual reading but never use it for drift comparison. The full
`file_sha256` column is the diff target.

### Drift query

`axiaopsctl migrate drift` prints a table of `(version, name, expected_sha,
observed_sha)` for every version where the most-recent successful row's
checksum disagrees with the live file. Implementation: read all
`(version, file_sha256)` from the view, hash the corresponding file from
`migrationsFS`, diff in Go (the SQL view alone cannot compute on-disk SHAs).

## Out of scope (explicit)

The following are intentionally not in v1:

- **Per-migration row counts (`rows_affected`).** Postgres does not expose
  this generically across DDL + DML.
- **Before/after schema diffs.** Useful but expensive.
- **Integration with `audit_log`.** Migration history is not org-scoped.
- **Web UI / dashboard panel.** Three operators using `psql` is fine.
- **Multi-env aggregation.** Requires a central store that does not exist.
- **Backfill of timing/actor for already-applied envs.** Impossible.
- **Trigger-based fallback.** Rejected (option (a) and (c) in §Population).
- **Tamper-evident records.** See §Open questions.

## Open questions

1. **How aggressive should drift posture be in CI?** CI builds run their
   own migrations on disposable Postgres containers, so drift is impossible
   there by construction. "Set `MIGRATION_HISTORY_STRICT=true` in CI envs"
   is a reflex worth pinning before the first developer trips on it.
2. **Truncation of `error_message` at 4 KB.** The cap is now enforced by a
   Go constant + a CHECK constraint. The number itself (4 KB) is a guess;
   pin a final value once we have one quarter of real `pq` error strings.
3. **Connection-context details.** Some bastion-run migrations connect with
   one `sslmode`, App Runner forces another. Adding `applied_by_connection`
   is doable but noisy and not on the critical path.

### Settled, not open

**`make start-staging` migration step.** Cosmetic, not architectural. Once
`axiaopsctl` ships, `make start-staging` invokes `axiaopsctl migrate up`
instead of the bare migrate binary; same image, same `Bootstrap()`, just a
longer argv. No design impact; tracked as a follow-up MR docs/Makefile
delta.

**Tamper-evidence.** `migration_history` is forensic, not legal-evidentiary.
If an operator with owner credentials drops or truncates it, the next boot
re-baselines from disk and re-records the active set as `backfilled` — the
audit trail for prior events is gone. This is the same posture as
`audit_log`, which is itself cascade-deleted by `DELETE /organizations/me`
(see api `CLAUDE.md`). Operators who need un-tamperable history ship rows
to S3 / a SIEM out-of-band; that is the agreed disposition and it does not
need a separate write-only WAL inside Postgres.
