-- Add WITH CHECK to RLS policies for INSERT ... ON CONFLICT to work properly
-- The USING clause alone doesn't authorize INSERT operations; WITH CHECK is required
-- for new rows being inserted or updated via RLS-affected DML statements.

ALTER POLICY accounts_tenant_isolation ON axiaops.accounts
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY cost_tenant_isolation ON axiaops.cost_records
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY ghost_tenant_isolation ON axiaops.ghost_records
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY ghost_snapshots_tenant_isolation ON axiaops.ghost_snapshots
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY snapshot_services_tenant_isolation ON axiaops.ghost_snapshot_services
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY resource_tenant_isolation ON axiaops.resource_records
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

ALTER POLICY dismissed_ghosts_tenant_isolation ON axiaops.dismissed_ghosts
  USING (tenant_id = current_setting('app.tenant_id', true))
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
