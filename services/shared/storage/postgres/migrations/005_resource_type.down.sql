-- 005_resource_type_add_columns.down.sql
-- Rollback: drop the columns and index added in migration 005.
-- Data cleanup happens in migration 006's rollback.

SET search_path TO axiaops;

DROP INDEX IF EXISTS idx_snapshot_services_tenant_service_rt;

ALTER TABLE ghost_snapshot_services DROP COLUMN IF EXISTS resource_type;
ALTER TABLE ghost_records           DROP COLUMN IF EXISTS resource_type;
ALTER TABLE resource_records        DROP COLUMN IF EXISTS resource_type;
