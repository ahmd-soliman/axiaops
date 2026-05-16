-- 003_add_arn.up.sql
-- Adds ARN (Amazon Resource Name) column to ghost_records and resource_records.

SET search_path TO axiaops;

ALTER TABLE ghost_records ADD COLUMN IF NOT EXISTS arn TEXT NOT NULL DEFAULT '';
ALTER TABLE resource_records ADD COLUMN IF NOT EXISTS arn TEXT NOT NULL DEFAULT '';
