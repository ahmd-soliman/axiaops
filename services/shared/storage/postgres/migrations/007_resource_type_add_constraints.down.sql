-- 007_resource_type_add_constraints.down.sql
-- Rollback: remove NOT NULL constraints from resource_type columns.

SET search_path TO axiaops;

ALTER TABLE ghost_snapshot_services ALTER COLUMN resource_type DROP NOT NULL;
ALTER TABLE ghost_records           ALTER COLUMN resource_type DROP NOT NULL;
ALTER TABLE resource_records        ALTER COLUMN resource_type DROP NOT NULL;
