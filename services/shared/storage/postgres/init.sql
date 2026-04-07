-- init.sql — runs once on first PostgreSQL startup as axiaops_owner.
-- Creates the application user, schema, and default grants.
-- All table DDL is managed by versioned migrations (001_initial.up.sql etc).

-- ── Application user ──────────────────────────────────────────────────────────

CREATE USER axiaops WITH PASSWORD 'axiaops';

-- ── Schema ────────────────────────────────────────────────────────────────────

CREATE SCHEMA IF NOT EXISTS axiaops;

GRANT USAGE ON SCHEMA axiaops TO axiaops;

-- Tables and sequences created by migrations (as axiaops_owner) are automatically
-- accessible to the app user via these default privilege grants.
ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO axiaops;

ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT USAGE, SELECT ON SEQUENCES TO axiaops;
