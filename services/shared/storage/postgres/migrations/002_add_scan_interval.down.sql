-- 002_add_scan_interval.down.sql
-- Remove scan_interval_hours column from accounts table.

SET search_path TO axiaops;

ALTER TABLE accounts
    DROP COLUMN IF EXISTS scan_interval_hours;
