---
name: db-migration
description: "Create a new PostgreSQL database migration for AxiaOps. Use this skill when someone needs to add a table, column, index, or modify the database schema. Also trigger when the conversation mentions 'migration', 'ALTER TABLE', 'schema change', 'add column', 'new table', 'RLS policy', 'row-level security', or anything about the AxiaOps database structure. Covers versioned SQL migrations with RLS enforcement."
---

# Database Migration Skill

AxiaOps uses versioned SQL migrations with golang-migrate. Migrations live in `services/shared/storage/postgres/migrations/` and run automatically on service startup. The database enforces tenant isolation via PostgreSQL Row-Level Security (RLS).

## Before You Start

Read these to understand the existing schema and patterns:

- `services/shared/storage/postgres/migrations/` — all existing migrations
- `services/shared/storage/postgres/postgres.go` — how migrations run and how RLS is set
- `services/shared/storage/storage.go` — the Store interface (to update after schema changes)

## Migration File Convention

Each migration is a pair of files:

```
NNN_description.up.sql    — applies the change
NNN_description.down.sql  — reverts the change
```

Where `NNN` is a zero-padded sequence number. Check the highest existing number and increment by 1.

Current migrations:
- `000_init` — schema creation, roles, default grants
- `001_initial` — all base tables (tenants, users, cost_records, ghost_records, resource_records, accounts) + RLS policies
- `002_ghost_snapshots` — snapshot history table
- `004_add_scan_interval` — scan scheduling column

## Writing a Migration

### UP migration template

```sql
-- NNN_description.up.sql
-- Brief description of what this migration does.

SET search_path TO axiaops;

-- ── Your changes here ────────────────────────────────────────────────────────

-- Example: Add a new table
CREATE TABLE IF NOT EXISTS alerts (
    id           BIGSERIAL   PRIMARY KEY,
    tenant_id    TEXT        NOT NULL REFERENCES tenants(id),
    resource_id  TEXT        NOT NULL,
    severity     TEXT        NOT NULL DEFAULT 'info',
    message      TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL
);

-- Grant permissions to the app user
GRANT SELECT, INSERT, UPDATE, DELETE ON alerts TO axiaops;
GRANT USAGE, SELECT ON alerts_id_seq TO axiaops;

-- Enable RLS on any new table with tenant_id
ALTER TABLE alerts ENABLE ROW LEVEL SECURITY;

DO $ BEGIN
    CREATE POLICY alerts_tenant_isolation ON alerts
        USING (tenant_id = current_setting('app.tenant_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $;
```

### DOWN migration template

```sql
-- NNN_description.down.sql
SET search_path TO axiaops;

DROP TABLE IF EXISTS alerts;
```

## Critical Rules

### Every table with `tenant_id` MUST have RLS

This is non-negotiable. AxiaOps is multi-tenant and RLS is the security boundary. For any new table that contains a `tenant_id` column:

1. `ALTER TABLE new_table ENABLE ROW LEVEL SECURITY;`
2. Create a policy: `USING (tenant_id = current_setting('app.tenant_id', true))`
3. Wrap the policy creation in `DO $ BEGIN ... EXCEPTION WHEN duplicate_object THEN NULL; END $;` for idempotency

The RLS policy uses `current_setting('app.tenant_id', true)` — the Go code sets this via `SET app.tenant_id = $1` before each query (see `setTenant()` in `postgres.go`).

### Grant permissions to the `axiaops` app user

Migrations run as `axiaops_owner` (via `MIGRATION_DATABASE_URL`), but the app connects as `axiaops` (via `DATABASE_URL`). New tables, sequences, and functions need explicit grants:

```sql
GRANT SELECT, INSERT, UPDATE, DELETE ON new_table TO axiaops;
GRANT USAGE, SELECT ON new_table_id_seq TO axiaops;
```

### Always use `SET search_path TO axiaops;`

All AxiaOps tables live in the `axiaops` schema, not `public`.

### Make migrations idempotent where possible

Use `IF NOT EXISTS` for CREATE, and wrap policy creation in exception handlers. This prevents failures on re-runs.

## Adding a Column

```sql
-- up
SET search_path TO axiaops;
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS region TEXT NOT NULL DEFAULT 'us-east-1';

-- down
SET search_path TO axiaops;
ALTER TABLE accounts DROP COLUMN IF EXISTS region;
```

Use `DEFAULT` values so existing rows don't break. For NOT NULL columns without a sensible default, consider a two-step migration (add nullable, backfill, then set NOT NULL).

## After Creating the Migration

1. **Update the Go model** in `services/shared/model/` if you added/changed columns
2. **Update the Store interface** in `services/shared/storage/storage.go` if new queries are needed
3. **Update the PostgreSQL implementation** in `services/shared/storage/postgres/postgres.go`
4. **Test the migration** by running:

```bash
make test-storage
```

This runs the full migration suite and RLS tests against a real PostgreSQL instance.

## Two-Database-URL Pattern

AxiaOps uses two connection strings intentionally:

- `MIGRATION_DATABASE_URL` → connects as `axiaops_owner` (can ALTER, CREATE, DROP)
- `DATABASE_URL` → connects as `axiaops` (limited by RLS, only CRUD operations)

Migrations always run through the owner connection. App queries always run through the restricted connection. Never grant DDL privileges to the `axiaops` user.
