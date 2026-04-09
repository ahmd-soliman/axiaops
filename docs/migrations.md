# Database Migrations — AxiaOps

## Tool

[golang-migrate/migrate v4](https://github.com/golang-migrate/migrate) — a lightweight Go library that tracks and applies versioned SQL migration files. No JVM, no config format, just plain SQL.

---

## How It Works

On every startup, both the `api` and `ingestion` services call `postgres.Migrate(dbURL)` before opening the connection pool. golang-migrate:

1. Connects to PostgreSQL
2. Acquires an advisory lock (safe if both services start simultaneously)
3. Checks `axiaops.schema_migrations` — a table it owns and manages
4. Runs any `.up.sql` files not yet recorded there, in version order
5. Records each applied migration and releases the lock

If no new migrations are pending, it exits immediately with no side effects.

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

ALTER TABLE ghost_records ADD COLUMN status TEXT NOT NULL DEFAULT 'open';
```

3. Write the reverse in `.down.sql`:

```sql
SET search_path TO axiaops;

ALTER TABLE ghost_records DROP COLUMN status;
```

4. Deploy — the app applies it on next startup automatically.

**Rules:**
- Never edit an already-applied migration file. Add a new one instead.
- Always include `SET search_path TO axiaops;` at the top of each file.
- Number sequentially: `001`, `002`, `003` — no gaps.
- Both `.up.sql` and `.down.sql` are required for every version.

---

## Schema Migrations Table

golang-migrate records applied migrations in `axiaops.schema_migrations`:

```
 version | dirty
---------+-------
       1 | false
```

- `version` — the number prefix of the migration file (`001` → `1`)
- `dirty` — `true` if the last migration failed mid-run; requires manual intervention before any new migrations can run

---

## Running Migrations Manually

Migrations run automatically on startup. To run them manually (useful for production database management without restarting the app), install the CLI:

```bash
brew install golang-migrate
```

Then:

```bash
# Apply all pending migrations
migrate -path services/shared/storage/postgres/migrations \
        -database "postgres://axiaops_owner:axiaops_owner@localhost:5432/axiaops?sslmode=disable&search_path=axiaops" \
        up

# Roll back the last migration
migrate -path services/shared/storage/postgres/migrations \
        -database "postgres://axiaops_owner:axiaops_owner@localhost:5432/axiaops?sslmode=disable&search_path=axiaops" \
        down 1

# Check current version
migrate -path services/shared/storage/postgres/migrations \
        -database "..." \
        version
```

---

## Dirty State

If a migration fails halfway, the `dirty` flag is set to `true` and all future migration attempts are blocked. To recover:

1. Manually fix or reverse the partial change in the database
2. Force the version back:

```bash
migrate ... force 1
```

This marks version 1 as clean without re-running it. Then either fix the migration file or roll back and retry.
