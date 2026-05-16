-- 000_init.up.sql
-- One-time infrastructure setup: grants on the axiaops schema.
-- postgres.Bootstrap() runs before this migration and is responsible for:
--   - creating the axiaops user and syncing its password
--   - creating the axiaops schema (so schema_migrations can live inside it)
-- Runs as axiaops_owner (superuser) via MIGRATION_DATABASE_URL.

-- Grant database connection permission (user already exists via Bootstrap).
GRANT CONNECT ON DATABASE axiaops TO axiaops;

-- ── Schema ────────────────────────────────────────────────────────────────────

GRANT USAGE ON SCHEMA axiaops TO axiaops;

-- Tables and sequences created by migrations (as axiaops_owner) are automatically
-- accessible to the app user via these default privilege grants.
ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO axiaops;

ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT USAGE, SELECT ON SEQUENCES TO axiaops;

-- Grant permissions on existing tables (if any)
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA axiaops TO axiaops;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA axiaops TO axiaops;
