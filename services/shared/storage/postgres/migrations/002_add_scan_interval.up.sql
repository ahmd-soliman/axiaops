-- 002_add_scan_interval.up.sql
-- Add scan_interval_hours column to accounts table.

SET search_path TO axiaops;

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS scan_interval_hours INT NOT NULL DEFAULT 24;
