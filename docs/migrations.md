# Database Migrations — AxiaOps

## Tool

[golang-migrate/migrate v4](https://github.com/golang-migrate/migrate) — a lightweight Go library that tracks and applies versioned SQL migration files. No JVM, no config format, just plain SQL.

---

## How It Works

On every startup, both the `api` and `ingestion` services call `postgres.Migrate(dbURL)` before opening the connection pool. The wrapper:

1. Opens a pinned `*sql.Conn` against `MIGRATION_DATABASE_URL` and acquires the wrapper-level session advisory lock (`AxiaOpsM`).
2. Runs **orphan recovery** — finalises any `axiaops.migration_history` row whose post-step UPDATE was lost to a wrapper crash. See [§Migration history](#migration-history) and `docs/migration-history-table-design.md` §Failure modes.
3. Runs **backfill** if `migration_history` is empty but `migration_state` is non-empty (first deploy of the history table on an existing env). Inserts one `status='backfilled'` row per applied version with the live-file SHA-256.
4. Runs **drift detection** — compares the SHA-256 of every embedded `.up.sql` against the most recent succeeded recorded checksum. Mismatch → `slog.Warn` (the only signal — see [§Drift detection](#drift-detection) for why there's no Prometheus counter). With `MIGRATION_HISTORY_STRICT=true` the wrapper refuses to start.
5. Drives golang-migrate one step at a time (`Steps(1)` in a loop). For each step it INSERT-and-COMMITs a `status='started'` row **before** the step, then UPDATEs it to `succeeded`/`failed` **after** the step on a separate transaction.
6. Releases the advisory lock and closes the pinned conn.

If no new migrations are pending, the loop exits immediately and only the boot-time bookkeeping (orphan recovery + drift detection) runs.

`axiaops.migration_state` is still owned by golang-migrate as before — `migration_history` layers on top, it does not replace it.

---

## File Layout

```
services/shared/storage/postgres/
  migrate.go                        ← Migrate() function called at startup
  migrations/
    000_init.up.sql                 ← creates app user + schema + default grants (runs as owner/admin)
    000_init.down.sql
    001_initial.up.sql              ← creates all tables + RLS policies
    001_initial.down.sql            ← drops all tables (reverse FK order)
    002_your_next_change.up.sql     ← future migrations go here
    002_your_next_change.down.sql
```

### 000_init vs schema migrations

`000_init.up.sql` runs as the owner/admin connection (`MIGRATION_DATABASE_URL`). Its only job is infrastructure:

- Create the `axiaops` application user
- Create the `axiaops` schema
- Set default privileges so future tables are accessible to the app user

**All table DDL lives in migrations**, not in `000_init`. This means:

- Fresh installs: the app runs `000_init` (user + schema), then runs `001_initial` (tables)
- Existing installs: the app runs any new migrations

---

## Adding a New Migration

1. Create two files in `services/shared/storage/postgres/migrations/`:

```
002_add_dismissed_status.up.sql
002_add_dismissed_status.down.sql
```

2. Write the forward change in `.up.sql`:

```sql
SET search_path TO axiaops;

ALTER TABLE zombie_records ADD COLUMN status TEXT NOT NULL DEFAULT 'open';
```

3. Write the reverse in `.down.sql`:

```sql
SET search_path TO axiaops;

ALTER TABLE zombie_records DROP COLUMN status;
```

4. Deploy — the app applies it on next startup automatically.

**Rules:**
- Never edit an already-applied migration file. Add a new one instead.
- Always include `SET search_path TO axiaops;` at the top of each file.
- Number sequentially: `001`, `002`, `003` — no gaps.
- Both `.up.sql` and `.down.sql` are required for every version.

---

## Schema Migrations Table

> **Renamed 2026-05-10**: `axiaops.schema_migrations` → `axiaops.migration_state`. The old name was golang-migrate's compiled-in default but it was a misnomer (plural name, single-row table). Bootstrap's atomic guard `ALTER TABLE`-renames existing DBs idempotently; nothing on disk references the old name except in historical breadcrumbs like this one. If you grep for `schema_migrations` in an old runbook, the new name is `migration_state`.

golang-migrate records applied migrations in `axiaops.migration_state`:

```
 version | dirty
---------+-------
       1 | false
```

- `version` — the number prefix of the migration file (`001` → `1`)
- `dirty` — `true` if the last migration failed mid-run; requires manual intervention before any new migrations can run

`migration_state` carries the *current* state. For *what was applied, when, by which build, against which file bytes*, see `migration_history` below.

---

## Migration history

`axiaops.migration_history` is a per-event audit table written by our wrapper on every up / down / force. It exists to answer questions `migration_state` cannot:

- *When* was migration N applied to this env, and how long did it take?
- *Which build* (`APP_VERSION@APP_COMMIT_SHA`) ran it?
- *Which file bytes* were applied — i.e. is the file on disk today the same SHA-256 that ran two months ago?
- Did the version ever roll back, then re-apply, on this database?

The table lives in the `axiaops` schema, owned by `axiaops_owner`. The app user (`axiaops`) has `SELECT` only — DML is owner-only by policy. The schema is created in `Bootstrap()` (not as a numbered migration) because it must exist *before* `000_init.up.sql` runs; full rationale lives in `docs/migration-history-table-design.md` §Bootstrap.

### Useful queries

Inspect the table directly:

```sql
SELECT id, version, name, direction, status, started_at, duration_ms,
       migration_state_dirty_after, LEFT(file_sha256, 8) AS sha8, applied_by_image
FROM axiaops.migration_history
ORDER BY started_at, id;
```

`LEFT(file_sha256, 8)` is for at-a-glance display only — collision probability is ~10⁻⁵ over a few thousand rows, fine for visual reading but never use it for drift comparison. The full `file_sha256` column is the diff target.

(There used to be a `migration_history_v` convenience view that pre-computed this short hash; dropped 2026-05-10 — the view did `SELECT *` and one `LEFT()` and didn't earn its schema-evolution friction.)

Latest succeeded checksum for one version:

```sql
SELECT file_sha256
FROM axiaops.migration_history
WHERE version = 24 AND direction = 'up' AND status = 'succeeded'
  AND file_sha256 IS NOT NULL
ORDER BY id DESC LIMIT 1;
```

In-flight or crashed-mid-run rows (none expected during normal operation):

```sql
SELECT id, version, direction, started_at
FROM axiaops.migration_history
WHERE status = 'started' AND finished_at IS NULL;
```

### Drift detection

On every boot the wrapper compares the on-disk SHA-256 of every embedded `.up.sql` to the most recent succeeded recorded SHA. Mismatch → `slog.Warn`, **service still starts**. Set `MIGRATION_HISTORY_STRICT=true` to flip the posture to refuse-to-start (default-on for CI / staging / dev; default-off for customer self-hosted installs).

The signal is log-only by design. `Migrate()` runs in short-lived contexts — the dedicated migrate Docker image and `axiaopsctl` — neither of which serves a `/metrics` endpoint, so a Prometheus counter would be incremented in-memory and lost on process exit before any scraper could reach it. Logs flow through the same observability pipeline (Loki / journald / stdout) on every deploy, where `count_over_time({job=~"axiaops-migrate"} |= "file checksum drift detected" [1h]) > 0` makes a workable alert. The on-demand `bin/axiaopsctl migrate drift` command (which queries the database directly, not the metric) covers active inspection.

`bin/axiaopsctl migrate drift` (or `make migration-history-drift`) prints the drift table on demand:

```
VERSION  NAME                   EXPECTED   OBSERVED
24       drop_kinde_residue     a1b2c3d4   ff0011ee
```

### Operator escape hatches

- **Truncating `migration_history` is allowed** but acts as a re-baseline: the next boot sees an empty history with a non-empty `migration_state` and inserts a `backfilled` row per applied version, taking the live-file SHA as ground truth. Anything you wanted to forensically catch is gone. The table is forensic, not legal-evidentiary; for un-tamperable history, ship rows to S3 / a SIEM out-of-band.
- **`bin/axiaopsctl migrate force N` writes a force history row** with `direction='force'`, `status='succeeded'`, `file_sha256=NULL` (no file is applied — `Force` only rewrites `migration_state`). Prefer this over the upstream `migrate force` so the event is on the record.
- **A bastion `migrate` install bypasses the history table.** The legitimate channel is `axiaopsctl`. There is no `migrate-binary-on-bastion` workflow we support post-history-table.

For deeper architecture (advisory-lock layout, two-connection model, orphan-recovery truth table, force semantics) see `docs/migration-history-table-design.md`.

---

## Running Migrations Manually

Migrations run automatically on startup. For ad-hoc operator work — applying or rolling back without restarting the app, inspecting history, hunting drift — use **`axiaopsctl`**, the project's own migrate CLI:

```bash
make axiaopsctl                    # builds bin/axiaopsctl

# Apply all pending migrations (also: argv-less call lands here)
bin/axiaopsctl migrate up

# Roll back N migrations — each step records a 'down' history row
bin/axiaopsctl migrate down 1

# Force migration_state to version N without running DDL.
# Writes a single 'force' history row for forensics. Use only when fixing dirty=true.
bin/axiaopsctl migrate force 24

# Print every history row (optionally filter by version)
bin/axiaopsctl migrate history
bin/axiaopsctl migrate history 24

# Print versions whose on-disk SHA differs from the recorded SHA
bin/axiaopsctl migrate drift
```

`axiaopsctl` is the same Go binary the api/ingestion services run on startup (`services/shared/cmd/migrate/`), just with a different entrypoint. That's load-bearing: a bare `migrate` install from Homebrew bypasses the wrapper, so the operation lands in the database without leaving a `migration_history` row. Use `axiaopsctl` always.

Make wrappers exist for the inspection subcommands:

```bash
make migration-history V=24        # bin/axiaopsctl migrate history 24
make migration-history-drift       # bin/axiaopsctl migrate drift
```

Both pass `MIGRATION_DATABASE_URL` through.

---

## Production Migrations

Production runs on RDS inside a VPC. The CI runner cannot reach it directly, so
migrations are **not run automatically** by the `deploy:production` pipeline job.
The `axiaops-migrate` image is built and pushed to ECR on every deploy — you must
run it manually before the App Runner services pick up the new image.

**The order matters: always migrate before deploying.** App Runner rolls out the
new image gradually; if the new code reaches a schema it doesn't recognise, the
service will error until migrations are applied.

### Option A — ECS one-off task (recommended)

Once the Terraform ECS task definition is in place, trigger a one-off run from
any machine with AWS credentials:

```bash
aws ecs run-task \
  --cluster axiaops \
  --task-definition axiaops-migrate \
  --launch-type FARGATE \
  --network-configuration "awsvpcConfiguration={
    subnets=[subnet-xxxxxxxx],
    securityGroups=[sg-xxxxxxxx],
    assignPublicIp=DISABLED
  }" \
  --overrides '{
    "containerOverrides": [{
      "name": "migrate",
      "environment": [
        {"name": "MIGRATION_DATABASE_URL", "value": "postgres://axiaops_owner:<password>@<rds-host>:5432/axiaops"},
        {"name": "DATABASE_URL",           "value": "postgres://axiaops:<password>@<rds-host>:5432/axiaops"}
      ]
    }]
  }'
```

Wait for the task to reach `STOPPED` and confirm its exit code is `0` before
proceeding with the App Runner deployment.

### Option B — bastion / VPN jump host

If you have a host inside the VPC (or an active VPN tunnel to the RDS subnet),
pull and run the migrate image directly:

```bash
# Pull from ECR (authenticate first)
aws ecr get-login-password --region eu-central-1 | \
  docker login --username AWS --password-stdin <account_id>.dkr.ecr.eu-central-1.amazonaws.com

docker pull <account_id>.dkr.ecr.eu-central-1.amazonaws.com/axiaops-migrate:<sha>

docker run --rm \
  -e MIGRATION_DATABASE_URL="postgres://axiaops_owner:<password>@<rds-host>:5432/axiaops?sslmode=require" \
  -e DATABASE_URL="postgres://axiaops:<password>@<rds-host>:5432/axiaops?sslmode=require" \
  <account_id>.dkr.ecr.eu-central-1.amazonaws.com/axiaops-migrate:<sha>
```

### Verifying the migration ran

Connect to RDS and check the migrations table:

```sql
SELECT version, dirty FROM axiaops.migration_state ORDER BY version DESC LIMIT 5;
```

`dirty = false` on the latest version means the migration completed cleanly. If
`dirty = true`, see the Dirty State section below before redeploying.

---

## Dirty State

If a migration fails halfway, the `dirty` flag is set to `true` and all future migration attempts are blocked. To recover:

1. Manually fix or reverse the partial change in the database
2. Force the version back:

```bash
migrate ... force 1
```

This marks version 1 as clean without re-running it. Then either fix the migration file or roll back and retry.
