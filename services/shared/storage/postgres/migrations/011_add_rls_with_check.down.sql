-- Revert to USING-only RLS policies
-- Remove WITH CHECK clauses (going back to original policies that only have USING)

ALTER POLICY accounts_tenant_isolation ON axiaops.accounts
  USING (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY cost_tenant_isolation ON axiaops.cost_records
  USING (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY ghost_tenant_isolation ON axiaops.ghost_records
  USING (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY ghost_snapshots_tenant_isolation ON axiaops.ghost_snapshots
  USING (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY snapshot_services_tenant_isolation ON axiaops.ghost_snapshot_services
  USING (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY resource_tenant_isolation ON axiaops.resource_records
  USING (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY dismissed_ghosts_tenant_isolation ON axiaops.dismissed_ghosts
  USING (tenant_id = current_setting('app.tenant_id', true));
