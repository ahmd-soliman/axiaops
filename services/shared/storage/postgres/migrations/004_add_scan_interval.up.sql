-- 004_add_scan_interval.up.sql
-- Add scan_interval_hours field to accounts table for scheduled auto-scanning.

SET search_path TO axiaops;

ALTER TABLE accounts
    ADD COLUMN scan_interval_hours INTEGER NOT NULL DEFAULT 24;

-- Ensure the app user has grants on the updated table.
GRANT SELECT, INSERT, UPDATE, DELETE ON accounts TO axiaops;
