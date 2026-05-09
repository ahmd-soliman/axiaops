# Migration History Table — Design

**Status:** Design proposal | **Owner:** platform | **Companion to:** [`docs/migrations.md`](migrations.md)

## Goal

Keep a permanent, append-only record of **every migration event** applied to an
AxiaOps database — every up, every down, every `force`, including failures —
with enough metadata to answer the questions `axiaops.schema_migrations` cannot:

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

## Non-goals

The history table is **infra metadata**, not a domain audit. Things it
explicitly does *not* do:

- **No replacement for `schema_migrations`**. golang-migrate continues to own
  `axiaops.schema_migrations` (single row, version + dirty). We layer on top.
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
    schema_migrations_dirty_after BOOLEAN
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
| `version` | `BIGINT` | NO | — | Mirror of `schema_migrations.version` — the integer prefix of the file. Indexed because every operator query starts "show me history for version N." `BIGINT` matches what golang-migrate uses internally. |
| `name` | `TEXT` | NO | — | Filename without extension or version prefix, e.g. `add_account_id_to_accounts`. Derivation is fixed by `name = strings.TrimSuffix(strings.TrimPrefix(identifier, fmt.Sprintf("%03d_", version)), ".up.sql")` (or `.down.sql` for downs). The `%03d` zero-pad is load-bearing — version `0` → `000`, version `24` → `024`. |
| `direction` | `TEXT` | NO | — | One of `up`, `down`, `force`. CHECK constraint enforces the enum. `force` rows are written exclusively by the `axiaopsctl migrate force` subcommand (see §Operator UX); the wrapper itself never produces a `force` row because `migrate.Force(N)` does not go through `Steps()` — it only calls `SetVersion(N, false)` against `schema_migrations`. |
| `status` | `TEXT` | NO | — | One of `started`, `succeeded`, `failed`, `backfilled`. `started` is inserted *before* the migration runs so a crashed wrapper still leaves a forensic trail; the wrapper updates it to `succeeded`/`failed` on completion. `backfilled` is reserved for the one-shot rows inserted on first deploy of this feature for migrations applied before the table existed. |
| `started_at` | `TIMESTAMPTZ` | NO | `now()` | When the wrapper inserts the `started` row. UTC by convention. |
| `finished_at` | `TIMESTAMPTZ` | YES | NULL | NULL while `status='started'`. Set on the wrapper's UPDATE when the migration returns. Stays NULL forever if the wrapper crashes mid-run — that's the forensic signal "this row never finished." |
| `duration_ms` | `BIGINT` | YES | NULL | Computed Go-side as `(finishedAt - startedAt).Milliseconds()` and stored. NULL while in-flight, NULL forever for crashed-mid-run rows, NULL for backfilled rows. |
| `error_message` | `TEXT` | YES | NULL | The error string from the migration driver, truncated Go-side to a hard cap of `maxErrorMessageBytes = 4096`. `CHECK (length(error_message) <= 4096)` enforces it at the DB. NULL on success. Captured even on `dirty` outcomes so the operator can read what went wrong without grepping container logs. |
| `file_sha256` | `TEXT` | YES | NULL | Lowercase hex SHA-256 of the migration file's bytes, validated by a regex CHECK. The hash is taken of the *raw bytes as embedded in `migrationsFS` at build time* — no normalization, no whitespace trimming, no line-ending fixup. Linux/macOS dev and Linux CI build identical hashes; a Windows developer running with `core.autocrlf=true` would produce a different hash, which is out of scope for this codebase (we do not support Windows builds). NULL for `force` (no file executed) and for `backfilled` rows where we baseline against the live file. |
| `applied_by_actor` | `TEXT` | YES | NULL | "Who/what applied this." Container hostname (App Runner / docker compose container ID prefix) by default; override via `MIGRATION_ACTOR_LABEL` env var when running `axiaopsctl` from a bastion. Free-form text on purpose — operators want to grep, not enforce a schema. |
| `applied_by_image` | `TEXT` | YES | NULL | The build identity of the binary that ran the wrapper: `${APP_VERSION}@${APP_COMMIT_SHA}` from the existing observability env vars (see `services/shared/CLAUDE.md` → Logging). Falls back to `unknown@unknown` if both are empty. |
| `schema_migrations_dirty_after` | `BOOLEAN` | YES | NULL | Snapshot of `axiaops.schema_migrations.dirty` read by a separate `SELECT` on the history-writer connection *after* `m.Steps(1)` returns, just before the wrapper UPDATE. On the success path it is redundant with `status='succeeded'` — but it is the only way to record the post-failure dirty state, since on a wrapper crash by definition the UPDATE never runs and the column stays NULL forever (which is itself the correct forensic signal). |

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

**(a) DB trigger on `axiaops.schema_migrations`** — `AFTER INSERT/UPDATE/DELETE`
fires a function that appends to `migration_history`. Rejected: the trigger
sees only `version` and `dirty`. It cannot compute `file_sha256` (file bytes
never enter Postgres as data, only as parsed-and-applied DDL), cannot record
the actor or the image, and has no robust way to distinguish "this dirty=true
update is the start" from "this dirty=false update is the end" without
stateful book-keeping. Without checksums the table is just a slower
`schema_migrations`, and checksums are precisely the column that would have
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
   `schema_migrations` and the migration DDL. **Owned by golang-migrate** for
   the lifetime of the call. The wrapper does not run side queries on this
   connection.
2. **History-writer connection** — a separate `*sql.DB` opened by the wrapper
   on the same `MIGRATION_DATABASE_URL`. Used for: the wrapper-level advisory
   lock (see below), every `INSERT INTO migration_history`, every UPDATE, the
   `SELECT dirty FROM schema_migrations` snapshot, and the orphan-recovery
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
const migrationHistoryLockID int64 = 0x4178696F70734D48 // "AxiopsMH" - distinct from any
                                                        // golang-migrate-internal lock ID
                                                        // to avoid self-deadlock.

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

`migrate.Migrate.sourceDrv` is unexported, so the wrapper cannot directly ask
the source driver for "the next pending version." Instead we invoke
`Steps(1)`, then read the version that was just applied via `m.Version()`,
and use that to locate the file in `migrationsFS` for hashing. The loop
terminates on either of two sentinel errors:

- `migrate.ErrNoChange` — returned from `Steps(0)`, or from the very first
  call when there is nothing pending.
- `migrate.ErrShortLimit{Short: 1}` — returned from subsequent calls once the
  pending queue has drained mid-loop. **This is the normal-termination
  sentinel** for our loop, not `ErrNoChange`.

```go
for {
    // 1. Run one step.
    stepErr := m.Steps(1)

    // 2. Both sentinels mean "no more work" — clean exit.
    var shortLimit migrate.ErrShortLimit
    if errors.Is(stepErr, migrate.ErrNoChange) || errors.As(stepErr, &shortLimit) {
        break
    }

    // 3. m.Version() returns the version we just touched (success or dirty).
    version, dirty, vErr := m.Version()
    if vErr != nil {
        return fmt.Errorf("migrate: read post-step version: %w", vErr)
    }

    // 4. Resolve identifier (e.g. "024_drop_kinde_residue.up.sql"), hash bytes,
    //    derive name (see Schema row for derivation rule).
    identifier := fmt.Sprintf("%03d_%s.up.sql", version, lookupNameFromFS(version))
    fileBytes, err := migrationsFS.ReadFile("migrations/" + identifier)
    if err != nil {
        return fmt.Errorf("migrate: read embedded file %s: %w", identifier, err)
    }
    sha := fmt.Sprintf("%x", sha256.Sum256(fileBytes))
    name := strings.TrimSuffix(strings.TrimPrefix(identifier, fmt.Sprintf("%03d_", version)), ".up.sql")

    // 5. Write the history row(s) for this step. The 'started' row was
    //    INSERT-and-COMMITted *before* Steps(1) (see §"Pre-step row" below);
    //    here we only complete it.
    if stepErr != nil {
        completeRowFailed(historyConn, version, name, sha, dirty, stepErr)
        return fmt.Errorf("migrate: step %d: %w", version, stepErr)
    }
    completeRowSucceeded(historyConn, version, name, sha, dirty)
}
```

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
   `schema_migrations_dirty_after = (SELECT dirty FROM axiaops.schema_migrations)`.

The two are deliberately on **different transactions on a different connection
from the migration driver**. That separation is the property the orphan
detector relies on: a crashed wrapper leaves the `started` row durably
committed even though the migration step rolled back, and the next boot can
distinguish "migration applied but UPDATE never ran" from "migration never
started."

### Down direction

The same wrapper, with `Steps(-1)` instead of `Steps(1)`. The hashed file is
the `.down.sql` for that version. `m.Version()` post-step reports the version
the database is now at (so for a down from 24 → 23, it reports 23 — the
wrapper looks up the *down* file for the *just-rolled-back* version, which it
held in a local before the call, not the post-step version). Other than that,
all rules above (advisory lock, two-connection model, pre-step commit,
post-step UPDATE, termination sentinels) apply unchanged.

### Force direction

`force` is **not** the wrapper's responsibility. `axiaopsctl migrate force N`
issues `m.Force(N)` directly and writes a single history row with
`direction='force'`, `status='succeeded'`, `file_sha256=NULL` (no file was
applied). This is intentional: `Force(N)` does no DDL — it only writes
`(N, false)` into `schema_migrations`.

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

The convenience view `migration_history_v` (see §Operator UX) is also created
in `Bootstrap()` via `CREATE OR REPLACE VIEW` — idempotent, and the view's
shape can evolve without DDL conflicts.

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

**warn-and-continue** in v1: log a structured warning, emit a Prometheus
counter `axiaops_migration_history_drift_total{version=...}`, keep starting
the service. Customer self-hosted installs can have legitimate reasons for
drift (operator hand-edited a migration, restored a backup from before a
column existed) and refusing to start would turn drift into a deploy outage on
installs we don't operate.

A `MIGRATION_HISTORY_STRICT=true` env var flips the posture to
refuse-to-start; we default it on in CI and our own staging/dev envs and off
elsewhere. The escalation path to defaulting it on for production is a
quarter of real data on how often legitimate drift occurs.

### Backfill

Existing envs (dev-1, dev-2, staging) already have 24+ migrations applied and
zero history rows. The backfill block runs **iff `migration_history` is empty
AND `schema_migrations` has at least one row**:

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
next boot **does** re-baseline (empty history + non-empty `schema_migrations`
trips the condition again). This is acceptable: the table is forensic, not
evidentiary (see §Open questions). Operators who truncate the audit trail
have the audit trail they deserve.

## Failure modes

### Wrapper crashes mid-recording — orphan recovery

The orphan detector runs once at the top of every `Migrate()` call, before
the wrapper-level advisory lock is acquired (no — it runs *after* the lock,
so concurrent calls don't fight over the same orphans). Concretely: after
acquiring `migrationHistoryLockID`, before the `Steps(1)` loop, the wrapper
selects every row with `status='started' AND finished_at IS NULL`.

For each orphan row `(history_id, version=V, direction=D)`:

1. Read `(current_version, dirty)` from `axiaops.schema_migrations`.
2. Decide:

   | `current_version` vs `V` | `dirty` | Verdict | UPDATE |
   |---|---|---|---|
   | `current_version == V` | `false` | Migration succeeded; only the post-step UPDATE was lost. | `status='succeeded'`, `finished_at=now()`, `duration_ms=NULL`, `error_message='timing lost; recovered post-crash'`, `schema_migrations_dirty_after=false`. |
   | `current_version == V` | `true` | Migration failed (golang-migrate left dirty=true) and the wrapper crashed before the UPDATE. | `status='failed'`, `finished_at=now()`, `error_message='migration failed; details lost in wrapper crash'`, `schema_migrations_dirty_after=true`. |
   | `current_version < V` | any | The migration step never started — the wrapper crashed between INSERT and `Steps(1)`. The next loop iteration will retry V. | `status='failed'`, `finished_at=now()`, `error_message='wrapper killed before migration step'`, `schema_migrations_dirty_after=NULL`. |
   | `current_version > V` | any | Should not happen — V is the orphan but a newer version is current. Treat as the "succeeded but UPDATE lost" case (D=down would make this normal; for D=up, log a warning and apply the same UPDATE as row 1). | as row 1, plus a structured warning. |

Crucially: the algorithm is **version-exact**. We never write
`current_version >= V → succeeded` because that confuses "V succeeded before
crash" with "V is the next pending version." The orphan resolver only
declares success when `current_version == V AND dirty=false`.

After the orphan UPDATE, the loop proceeds to `Steps(1)` and either re-runs
V (in the third row above) or moves past it.

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
axiaopsctl migrate history [V]       # Pretty-print migration_history_v rows
```

Implementation lives in the same Go module
(`axiaops.io/shared/cmd/migrate/`); `services/migrate/main.go` is kept as a
thin shim that calls `up` for backward compatibility with existing
Dockerfiles. The make target `migrate-image` (existing) builds the binary; a
new make target invokes it locally:

```make
axiaopsctl: ## Build the migrate/operator CLI
    go build -o bin/axiaopsctl ./services/shared/cmd/migrate/

migration-history: axiaopsctl
    @MIGRATION_DATABASE_URL=$(MIGRATION_DATABASE_URL) bin/axiaopsctl migrate history $(V)

migration-history-drift: axiaopsctl
    @MIGRATION_DATABASE_URL=$(MIGRATION_DATABASE_URL) bin/axiaopsctl migrate drift
```

### SQL view

`Bootstrap()` creates a convenience view alongside the table:

```sql
CREATE OR REPLACE VIEW axiaops.migration_history_v AS
SELECT
    h.id,
    h.version,
    h.name,
    h.direction,
    h.status,
    h.started_at,
    h.finished_at,
    h.duration_ms,
    h.error_message,
    h.applied_by_image,
    h.applied_by_actor,
    h.schema_migrations_dirty_after,
    LEFT(h.file_sha256, 8) AS file_sha256_short,
    h.file_sha256
FROM axiaops.migration_history h
ORDER BY h.started_at, h.id;
```

`ORDER BY started_at, id` — `id` breaks same-millisecond ties deterministically.
`file_sha256_short` is for at-a-glance reading, `file_sha256` is the full
value for diffs.

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

1. **Should `axiaopsctl migrate up` replace the implicit migrate step in
   `make start-staging`?** Currently `make start-staging` runs the migrate
   image first, then `docker compose up`. Mechanical change once the
   subcommand lands; small docs/Makefile delta.
2. **How aggressive should drift posture be in CI?** CI builds run their
   own migrations on disposable Postgres containers, so drift is impossible
   there by construction. "Set `MIGRATION_HISTORY_STRICT=true` in CI envs"
   is a reflex worth pinning before the first developer trips on it.
3. **Truncation of `error_message` at 4 KB.** The cap is now enforced by a
   Go constant + a CHECK constraint. The number itself (4 KB) is a guess;
   pin a final value once we have one quarter of real `pq` error strings.
4. **Connection-context details.** Some bastion-run migrations connect with
   one `sslmode`, App Runner forces another. Adding `applied_by_connection`
   is doable but noisy and not on the critical path.

### Settled, not open

**Tamper-evidence.** `migration_history` is forensic, not legal-evidentiary.
If an operator with owner credentials drops or truncates it, the next boot
re-baselines from disk and re-records the active set as `backfilled` — the
audit trail for prior events is gone. This is the same posture as
`audit_log`, which is itself cascade-deleted by `DELETE /organizations/me`
(see api `CLAUDE.md`). Operators who need un-tamperable history ship rows
to S3 / a SIEM out-of-band; that is the agreed disposition and it does not
need a separate write-only WAL inside Postgres.
