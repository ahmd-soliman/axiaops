-- 003_add_arn.down.sql
-- Removes ARN column from ghost_records and resource_records.

SET search_path TO axiaops;

ALTER TABLE ghost_records DROP COLUMN IF EXISTS arn;
ALTER TABLE resource_records DROP COLUMN IF EXISTS arn;
