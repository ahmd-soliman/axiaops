-- 016_rename_tenant_to_organization.down.sql
-- Reverse 016 in mirror order: data migration → indexes → policy names →
-- policy predicates → columns → table.

SET search_path TO axiaops;

-- ── Audit action revert ──────────────────────────────────────────────────────

UPDATE audit_log SET action = 'tenant_deleted' WHERE action = 'organization_deleted';

-- ── Index renames ────────────────────────────────────────────────────────────

ALTER TABLE memberships RENAME CONSTRAINT memberships_organization_id_user_id_key
    TO memberships_tenant_id_user_id_key;

ALTER INDEX idx_cost_records_organization_period_end RENAME TO idx_cost_records_tenant_period_end;
ALTER INDEX audit_log_organization_created_idx       RENAME TO audit_log_tenant_created_idx;
ALTER INDEX memberships_one_owner_per_organization   RENAME TO memberships_one_owner_per_tenant;

-- ── RLS policy renames ───────────────────────────────────────────────────────

ALTER POLICY cost_organization_isolation                     ON cost_records              RENAME TO cost_tenant_isolation;
ALTER POLICY zombie_organization_isolation                   ON zombie_records            RENAME TO zombie_tenant_isolation;
ALTER POLICY resource_organization_isolation                 ON resource_records          RENAME TO resource_tenant_isolation;
ALTER POLICY accounts_organization_isolation                 ON accounts                  RENAME TO accounts_tenant_isolation;
ALTER POLICY zombie_snapshots_organization_isolation         ON zombie_snapshots          RENAME TO zombie_snapshots_tenant_isolation;
ALTER POLICY zombie_snapshot_services_organization_isolation ON zombie_snapshot_services  RENAME TO zombie_snapshot_services_tenant_isolation;
ALTER POLICY dismissed_zombies_organization_isolation        ON dismissed_zombies         RENAME TO dismissed_zombies_tenant_isolation;
ALTER POLICY audit_log_organization_isolation                ON audit_log                 RENAME TO audit_log_tenant_isolation;
ALTER POLICY memberships_organization_isolation              ON memberships               RENAME TO memberships_tenant_isolation;

-- ── RLS policy predicates: switch GUC back ──────────────────────────────────

ALTER POLICY cost_tenant_isolation ON cost_records
  USING (organization_id = current_setting('app.tenant_id', true))
  WITH CHECK (organization_id = current_setting('app.tenant_id', true));

ALTER POLICY zombie_tenant_isolation ON zombie_records
  USING (organization_id = current_setting('app.tenant_id', true))
  WITH CHECK (organization_id = current_setting('app.tenant_id', true));

ALTER POLICY resource_tenant_isolation ON resource_records
  USING (organization_id = current_setting('app.tenant_id', true))
  WITH CHECK (organization_id = current_setting('app.tenant_id', true));

ALTER POLICY accounts_tenant_isolation ON accounts
  USING (organization_id = current_setting('app.tenant_id', true))
  WITH CHECK (organization_id = current_setting('app.tenant_id', true));

ALTER POLICY zombie_snapshots_tenant_isolation ON zombie_snapshots
  USING (organization_id = current_setting('app.tenant_id', true))
  WITH CHECK (organization_id = current_setting('app.tenant_id', true));

ALTER POLICY zombie_snapshot_services_tenant_isolation ON zombie_snapshot_services
  USING (organization_id = current_setting('app.tenant_id', true))
  WITH CHECK (organization_id = current_setting('app.tenant_id', true));

ALTER POLICY dismissed_zombies_tenant_isolation ON dismissed_zombies
  USING (organization_id = current_setting('app.tenant_id', true))
  WITH CHECK (organization_id = current_setting('app.tenant_id', true));

ALTER POLICY audit_log_tenant_isolation ON audit_log
  USING (organization_id = current_setting('app.tenant_id', true))
  WITH CHECK (organization_id = current_setting('app.tenant_id', true));

ALTER POLICY memberships_tenant_isolation ON memberships
  USING (organization_id = current_setting('app.tenant_id', true))
  WITH CHECK (organization_id = current_setting('app.tenant_id', true));

-- ── Column renames back ──────────────────────────────────────────────────────

ALTER TABLE memberships               RENAME COLUMN organization_id TO tenant_id;
ALTER TABLE audit_log                 RENAME COLUMN organization_id TO tenant_id;
ALTER TABLE dismissed_zombies         RENAME COLUMN organization_id TO tenant_id;
ALTER TABLE zombie_snapshot_services  RENAME COLUMN organization_id TO tenant_id;
ALTER TABLE zombie_snapshots          RENAME COLUMN organization_id TO tenant_id;
ALTER TABLE accounts                  RENAME COLUMN organization_id TO tenant_id;
ALTER TABLE resource_records          RENAME COLUMN organization_id TO tenant_id;
ALTER TABLE zombie_records            RENAME COLUMN organization_id TO tenant_id;
ALTER TABLE cost_records              RENAME COLUMN organization_id TO tenant_id;
ALTER TABLE users                     RENAME COLUMN organization_id TO tenant_id;

-- ── Table rename back ────────────────────────────────────────────────────────

ALTER TABLE organizations RENAME TO tenants;
