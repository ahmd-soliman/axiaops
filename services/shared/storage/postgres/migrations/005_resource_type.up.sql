-- 005_resource_type_add_columns.up.sql
-- Step 1: Add resource_type columns for two-tier service filtering.
-- Canonical AWS service names stay in `service`; sub-classification goes in `resource_type`.
-- Columns are added with defaults but without NOT NULL — data population happens in migration 006.

SET search_path TO axiaops;

ALTER TABLE ghost_snapshot_services ADD COLUMN IF NOT EXISTS resource_type TEXT DEFAULT '';
ALTER TABLE ghost_records           ADD COLUMN IF NOT EXISTS resource_type TEXT DEFAULT '';
ALTER TABLE resource_records        ADD COLUMN IF NOT EXISTS resource_type TEXT DEFAULT '';

-- Composite index for filtered trend queries (created here for early availability)
CREATE INDEX IF NOT EXISTS idx_snapshot_services_tenant_service_rt
    ON ghost_snapshot_services (tenant_id, service, resource_type);
