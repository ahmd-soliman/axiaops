-- 016_rename_tenant_to_organization.up.sql
-- Tenant → organization: rename the `tenants` table, every `tenant_id` column,
-- every RLS policy name, the `app.tenant_id` GUC referenced by those policies,
-- and any indexes carrying the old name. Same shape as 012 (ghost→zombie) but
-- with two extra wrinkles:
--
--   1. The RLS policy USING / WITH CHECK predicates reference the GUC
--      `app.tenant_id` as a string literal — column renames do not propagate
--      into string literals, so each policy needs its predicates rewritten
--      to read `current_setting('app.organization_id', true)`.
--   2. Postgres carries column renames into existing policy expressions and
--      foreign-key constraints automatically, so we only restate predicates
--      where the GUC string changes (everywhere).
--
-- All callers (Go SQL strings, slog labels, observability metric labels) were
-- already updated by commits 1–4. The migration runs on app pool startup, so
-- the policy/GUC switch happens before any handler opens a transaction with
-- the old GUC name.

SET search_path TO axiaops;

-- ── Table rename ─────────────────────────────────────────────────────────────

ALTER TABLE tenants RENAME TO organizations;

-- ── Column renames ───────────────────────────────────────────────────────────
-- Postgres auto-updates UNIQUE constraints, FKs, and existing RLS predicates
-- that reference the column by name when the column is renamed.

ALTER TABLE users                     RENAME COLUMN tenant_id TO organization_id;
ALTER TABLE cost_records              RENAME COLUMN tenant_id TO organization_id;
ALTER TABLE zombie_records            RENAME COLUMN tenant_id TO organization_id;
ALTER TABLE resource_records          RENAME COLUMN tenant_id TO organization_id;
ALTER TABLE accounts                  RENAME COLUMN tenant_id TO organization_id;
ALTER TABLE zombie_snapshots          RENAME COLUMN tenant_id TO organization_id;
ALTER TABLE zombie_snapshot_services  RENAME COLUMN tenant_id TO organization_id;
ALTER TABLE dismissed_zombies         RENAME COLUMN tenant_id TO organization_id;
ALTER TABLE audit_log                 RENAME COLUMN tenant_id TO organization_id;
ALTER TABLE memberships               RENAME COLUMN tenant_id TO organization_id;

-- ── RLS policy predicates: switch GUC name ───────────────────────────────────
-- The column rename above already moved the predicate to read
-- `organization_id = current_setting('app.tenant_id', true)`. Rewrite each
-- policy's expressions so they read the new GUC. Adding/keeping WITH CHECK
-- preserves the exact behaviour established in 011 / 014 / 015.

ALTER POLICY cost_tenant_isolation ON cost_records
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

ALTER POLICY zombie_tenant_isolation ON zombie_records
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

ALTER POLICY resource_tenant_isolation ON resource_records
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

ALTER POLICY accounts_tenant_isolation ON accounts
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

ALTER POLICY zombie_snapshots_tenant_isolation ON zombie_snapshots
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

ALTER POLICY zombie_snapshot_services_tenant_isolation ON zombie_snapshot_services
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

ALTER POLICY dismissed_zombies_tenant_isolation ON dismissed_zombies
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

ALTER POLICY audit_log_tenant_isolation ON audit_log
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

ALTER POLICY memberships_tenant_isolation ON memberships
  USING (organization_id = current_setting('app.organization_id', true))
  WITH CHECK (organization_id = current_setting('app.organization_id', true));

-- ── RLS policy renames ───────────────────────────────────────────────────────

ALTER POLICY cost_tenant_isolation                     ON cost_records              RENAME TO cost_organization_isolation;
ALTER POLICY zombie_tenant_isolation                   ON zombie_records            RENAME TO zombie_organization_isolation;
ALTER POLICY resource_tenant_isolation                 ON resource_records          RENAME TO resource_organization_isolation;
ALTER POLICY accounts_tenant_isolation                 ON accounts                  RENAME TO accounts_organization_isolation;
ALTER POLICY zombie_snapshots_tenant_isolation         ON zombie_snapshots          RENAME TO zombie_snapshots_organization_isolation;
ALTER POLICY zombie_snapshot_services_tenant_isolation ON zombie_snapshot_services  RENAME TO zombie_snapshot_services_organization_isolation;
ALTER POLICY dismissed_zombies_tenant_isolation        ON dismissed_zombies         RENAME TO dismissed_zombies_organization_isolation;
ALTER POLICY audit_log_tenant_isolation                ON audit_log                 RENAME TO audit_log_organization_isolation;
ALTER POLICY memberships_tenant_isolation              ON memberships               RENAME TO memberships_organization_isolation;

-- ── Index renames ────────────────────────────────────────────────────────────
-- Auto-generated PK / FK constraint names that embed the old column name
-- (e.g. `users_tenant_id_fkey`, `cost_records_tenant_id_…_key`) keep their
-- existing names — they are DB-internal identifiers that no test or app code
-- depends on. The one exception below is `memberships_tenant_id_user_id_key`,
-- which `memberships_migration_test.go` asserts on directly.

ALTER INDEX idx_cost_records_tenant_period_end RENAME TO idx_cost_records_organization_period_end;
ALTER INDEX audit_log_tenant_created_idx       RENAME TO audit_log_organization_created_idx;
ALTER INDEX memberships_one_owner_per_tenant   RENAME TO memberships_one_owner_per_organization;

-- Postgres auto-renames the backing index when a UNIQUE constraint is
-- renamed, so a separate ALTER INDEX is not needed.
ALTER TABLE memberships RENAME CONSTRAINT memberships_tenant_id_user_id_key
    TO memberships_organization_id_user_id_key;

-- ── Audit action rename (forward-compatible with commit 7's Go constant) ─────
-- No prod rows yet (pre-launch), but staging may have written this action and
-- the validator in commit 7 only accepts the new value. Idempotent — affects
-- zero rows when no `tenant_deleted` events exist.

UPDATE audit_log SET action = 'organization_deleted' WHERE action = 'tenant_deleted';
