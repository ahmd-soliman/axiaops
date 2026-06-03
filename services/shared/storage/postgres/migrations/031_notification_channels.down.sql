-- 031_notification_channels.down.sql

SET search_path TO axiaops;

-- Dispatches first (FK → channels); CASCADE also drops the RLS policies.
DROP TABLE IF EXISTS notification_dispatches CASCADE;
DROP TABLE IF EXISTS notification_channels CASCADE;
