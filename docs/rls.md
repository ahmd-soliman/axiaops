# Row-Level Security — AxiaOps Multi-Tenancy

## Overview

AxiaOps uses PostgreSQL Row-Level Security (RLS) to isolate data between organizations. Every customer's cost records and zombie resources are stored in the same tables but are invisible to other organizations — enforced at the database level, not just in application code.

---

## How It Works

### 1. Organization Identity

Every organization has an internal UUID in the `organizations` table:

```sql
organizations
├── id         TEXT  PRIMARY KEY   -- internal UUID (e.g. "a1b2c3...")
├── org_code   TEXT  UNIQUE        -- Kinde org identifier (e.g. "org_acme")
└── name       TEXT                -- display name (e.g. "Acme Corp")
```

Every row in `zombie_records` and `cost_records` has a `organization_id` column that references `organizations.id`.

---

### 2. RLS Policies

RLS is enabled on both tables:

```sql
ALTER TABLE zombie_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE cost_records   ENABLE ROW LEVEL SECURITY;
```

Each table has a policy that compares the row's `organization_id` to the current session variable `app.organization_id`:

```sql
CREATE POLICY zombie_organization_isolation ON zombie_records
    USING (organization_id = current_setting('app.organization_id', true));

CREATE POLICY cost_organization_isolation ON cost_records
    USING (organization_id = current_setting('app.organization_id', true));
```

If `app.organization_id` is not set, `current_setting(..., true)` returns `NULL` — which matches nothing, so no rows are returned.

---

### 3. Setting the Organization Context

The application sets `app.organization_id` inside a transaction before any query:

```go
// In postgres store — called before every SELECT/INSERT/DELETE
func setOrganization(ctx context.Context, tx pgx.Tx) error {
    organizationID := storage.OrganizationIDFromCtx(ctx)
    _, err := tx.Exec(ctx, `SELECT set_config('app.organization_id', $1, true)`, organizationID)
    return err
}
```

`set_config('app.organization_id', value, true)` — the third argument `true` means the setting is **transaction-local**: it resets automatically when the transaction ends. This prevents organization context from leaking across requests.

---

### 4. How Organization ID Flows Through the System

#### API requests (read path)

```
Browser → JWT (contains org_code)
  ↓
Auth middleware
  → UpsertOrganization(org_code) → returns organization UUID
  → stores organization UUID in request context
  ↓
Handler (GET /zombies)
  → storage.WithOrganizationID(ctx, organizationID)
  ↓
postgres.LoadZombies(ctx)
  → BEGIN transaction
  → set_config('app.organization_id', organizationID, true)
  → SELECT * FROM zombie_records   ← RLS filters automatically
  → COMMIT
```

#### Ingestion job (write path)

```
Scheduler (EventBridge) → passes ORGANIZATION_ID env var
  ↓
ingestion/cmd/main.go
  → storage.WithOrganizationID(ctx, organizationID)
  ↓
postgres.SaveZombies(ctx, zombies)
  → BEGIN transaction
  → set_config('app.organization_id', organizationID, true)
  → DELETE FROM zombie_records     ← RLS: only deletes this organization's rows
  → INSERT INTO zombie_records     ← organization_id column set explicitly
  → COMMIT
```

---

### 5. Verification

The seed script (`scripts/seed_test_data.sh`) creates two organizations, runs ingestion for each, then verifies isolation:

```sql
-- As organization A — only sees own rows
SET app.organization_id = '<organization_a_uuid>';
SELECT COUNT(*) FROM zombie_records;  -- returns organization A count

-- As organization A — cannot see organization B rows
SELECT COUNT(*) FROM zombie_records
WHERE organization_id = '<organization_b_uuid>';  -- returns 0 (RLS filters them out)
```

---

## Schema Location

All tables live in the `axiaops` schema (not `public`). The connection pool sets `search_path = axiaops` on every new connection via `AfterConnect`:

```go
cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
    _, err := conn.Exec(ctx, "SET search_path TO axiaops")
    return err
}
```

This means queries never need to qualify table names with the schema prefix.

---

## Important Notes

- **Table owner bypasses RLS** — by default PostgreSQL skips RLS for the table owner. AxiaOps applies `FORCE ROW LEVEL SECURITY` on all organization tables so the owning user is also subject to RLS. In production, use a separate application user without ownership for an additional layer of safety.
- **Superuser bypasses RLS** — never connect as a superuser from the application.
- **`SET LOCAL` vs `SET`** — the store uses `set_config(..., true)` which is transaction-local (equivalent to `SET LOCAL`). This is intentional — the setting cannot leak outside the transaction.
- **Missing organization_id** — if `organization_id` is not in the context, `setOrganization()` returns an error before any query runs. No data is ever read or written without an organization context.

---

## Production Checklist

- [ ] Create a dedicated application DB user (not the owner)
- [ ] `GRANT USAGE ON SCHEMA axiaops TO app_user`
- [ ] `GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA axiaops TO app_user`
- [ ] Connect from the application as `app_user`, not as the schema owner
- [ ] RDS: disable superuser login for the application connection string
