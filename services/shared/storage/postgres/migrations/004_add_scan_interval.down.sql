-- 004_add_scan_interval.down.sql
-- Removes scan_interval_hours column from accounts table.

SET search_path TO axiaops;

ALTER TABLE accounts
    DROP COLUMN scan_interval_hours;
