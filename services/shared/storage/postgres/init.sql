-- init.sql — idempotent infrastructure setup, run explicitly via scripts/db_init.sh.
-- Creates the application user, schema, and default grants.
-- All table DDL is managed by versioned migrations (001_initial.up.sql etc).

-- ── Application user ──────────────────────────────────────────────────────────

DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'axiaops') THEN
    CREATE USER axiaops WITH PASSWORD 'axiaops';
  END IF;
END
$$;

-- ── Schema ────────────────────────────────────────────────────────────────────

CREATE SCHEMA IF NOT EXISTS axiaops;

GRANT USAGE ON SCHEMA axiaops TO axiaops;

-- Tables and sequences created by migrations (as axiaops_owner) are automatically
-- accessible to the app user via these default privilege grants.
ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO axiaops;

ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT USAGE, SELECT ON SEQUENCES TO axiaops;
