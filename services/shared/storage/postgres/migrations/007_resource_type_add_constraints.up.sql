-- 007_resource_type_add_constraints.up.sql
-- Step 3: Add NOT NULL constraints on resource_type columns.
-- Safe to do after migration 006 ensures all rows have resource_type populated.

SET search_path TO axiaops;

ALTER TABLE ghost_snapshot_services ALTER COLUMN resource_type SET NOT NULL;
ALTER TABLE ghost_records           ALTER COLUMN resource_type SET NOT NULL;
ALTER TABLE resource_records        ALTER COLUMN resource_type SET NOT NULL;
