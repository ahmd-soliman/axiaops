-- 001_initial.down.sql
-- Drops all tables in reverse dependency order (FK constraints respected).

SET search_path TO axiaops;

DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS resource_records;
DROP TABLE IF EXISTS ghost_records;
DROP TABLE IF EXISTS cost_records;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
