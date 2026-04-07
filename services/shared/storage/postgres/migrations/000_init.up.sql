-- 000_init.up.sql
-- One-time infrastructure setup: app user, schema, and default grants.
-- Runs as axiaops_owner (superuser) via MIGRATION_DATABASE_URL.

-- ── Application user ──────────────────────────────────────────────────────────

DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'axiaops') THEN
    CREATE USER axiaops WITH PASSWORD 'axiaops';
  END IF;
END
$$;

-- ── Schema ────────────────────────────────────────────────────────────────────

-- Revoke app user access to public schema (schema_migrations lives there).
REVOKE ALL ON SCHEMA public FROM axiaops;

CREATE SCHEMA IF NOT EXISTS axiaops;

GRANT USAGE ON SCHEMA axiaops TO axiaops;

-- Tables and sequences created by migrations (as axiaops_owner) are automatically
-- accessible to the app user via these default privilege grants.
ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO axiaops;

ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT USAGE, SELECT ON SEQUENCES TO axiaops;
