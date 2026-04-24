# Row-Level Security — AxiaOps Multi-Tenancy

## Overview

AxiaOps uses PostgreSQL Row-Level Security (RLS) to isolate data between tenants. Every customer's cost records and zombie resources are stored in the same tables but are invisible to other tenants — enforced at the database level, not just in application code.

---

## How It Works

### 1. Tenant Identity

Every tenant has an internal UUID in the `tenants` table:

```sql
tenants
├── id         TEXT  PRIMARY KEY   -- internal UUID (e.g. "a1b2c3...")
├── org_code   TEXT  UNIQUE        -- Kinde org identifier (e.g. "org_acme")
└── name       TEXT                -- display name (e.g. "Acme Corp")
```

Every row in `zombie_records` and `cost_records` has a `tenant_id` column that references `tenants.id`.

---

### 2. RLS Policies

RLS is enabled on both tables:

```sql
ALTER TABLE zombie_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE cost_records   ENABLE ROW LEVEL SECURITY;
```

Each table has a policy that compares the row's `tenant_id` to the current session variable `app.tenant_id`:

```sql
CREATE POLICY zombie_tenant_isolation ON zombie_records
    USING (tenant_id = current_setting('app.tenant_id', true));

CREATE POLICY cost_tenant_isolation ON cost_records
    USING (tenant_id = current_setting('app.tenant_id', true));
```

If `app.tenant_id` is not set, `current_setting(..., true)` returns `NULL` — which matches nothing, so no rows are returned.

---

### 3. Setting the Tenant Context

The application sets `app.tenant_id` inside a transaction before any query:

```go
// In postgres store — called before every SELECT/INSERT/DELETE
func setTenant(ctx context.Context, tx pgx.Tx) error {
    tenantID := storage.TenantIDFromCtx(ctx)
    _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID)
    return err
}
```

`set_config('app.tenant_id', value, true)` — the third argument `true` means the setting is **transaction-local**: it resets automatically when the transaction ends. This prevents tenant context from leaking across requests.

---

### 4. How Tenant ID Flows Through the System

#### API requests (read path)

```
Browser → JWT (contains org_code)
  ↓
Auth middleware
  → UpsertTenant(org_code) → returns tenant UUID
  → stores tenant UUID in request context
  ↓
Handler (GET /zombies)
  → storage.WithTenantID(ctx, tenantID)
  ↓
postgres.LoadZombies(ctx)
  → BEGIN transaction
  → set_config('app.tenant_id', tenantID, true)
  → SELECT * FROM zombie_records   ← RLS filters automatically
  → COMMIT
```

#### Ingestion job (write path)

```
Scheduler (EventBridge) → passes TENANT_ID env var
  ↓
ingestion/cmd/main.go
  → storage.WithTenantID(ctx, tenantID)
  ↓
postgres.SaveZombies(ctx, zombies)
  → BEGIN transaction
  → set_config('app.tenant_id', tenantID, true)
  → DELETE FROM zombie_records     ← RLS: only deletes this tenant's rows
  → INSERT INTO zombie_records     ← tenant_id column set explicitly
  → COMMIT
```

---

### 5. Verification

The seed script (`scripts/seed_test_data.sh`) creates two tenants, runs ingestion for each, then verifies isolation:

```sql
-- As tenant A — only sees own rows
SET app.tenant_id = '<tenant_a_uuid>';
SELECT COUNT(*) FROM zombie_records;  -- returns tenant A count

-- As tenant A — cannot see tenant B rows
SELECT COUNT(*) FROM zombie_records
WHERE tenant_id = '<tenant_b_uuid>';  -- returns 0 (RLS filters them out)
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

- **Table owner bypasses RLS** — by default PostgreSQL skips RLS for the table owner. AxiaOps applies `FORCE ROW LEVEL SECURITY` on all tenant tables so the owning user is also subject to RLS. In production, use a separate application user without ownership for an additional layer of safety.
- **Superuser bypasses RLS** — never connect as a superuser from the application.
- **`SET LOCAL` vs `SET`** — the store uses `set_config(..., true)` which is transaction-local (equivalent to `SET LOCAL`). This is intentional — the setting cannot leak outside the transaction.
- **Missing tenant_id** — if `TENANT_ID` is not in the context, `setTenant()` returns an error before any query runs. No data is ever read or written without a tenant context.

---

## Production Checklist

- [ ] Create a dedicated application DB user (not the owner)
- [ ] `GRANT USAGE ON SCHEMA axiaops TO app_user`
- [ ] `GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA axiaops TO app_user`
- [ ] Connect from the application as `app_user`, not as the schema owner
- [ ] RDS: disable superuser login for the application connection string
