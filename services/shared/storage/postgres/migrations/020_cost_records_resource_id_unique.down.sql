-- 020_cost_records_resource_id_unique.down.sql
-- Revert: drop the resource_id-aware unique constraint and restore the
-- original (organization_id, provider, account_id, service, region,
-- period_start, period_end) constraint with the legacy auto-generated name.
--
-- Note: if data containing multiple rows per (service, region, period) with
-- different resource_id values has been written, restoring the old constraint
-- will fail because the duplicates would violate it. In that case, dedupe
-- the table by hand before running this migration.

SET search_path TO axiaops;

ALTER TABLE cost_records DROP CONSTRAINT IF EXISTS cost_records_org_resource_unique;

ALTER TABLE cost_records ALTER COLUMN resource_id DROP NOT NULL;
ALTER TABLE cost_records ALTER COLUMN resource_id DROP DEFAULT;

ALTER TABLE cost_records
    ADD CONSTRAINT cost_records_tenant_id_provider_account_id_service_region_p_key
    UNIQUE (organization_id, provider, account_id, service, region, period_start, period_end);
