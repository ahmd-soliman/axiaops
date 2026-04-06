-- init.sql — runs once on first PostgreSQL startup as axiaops_admin
-- Creates the application user, schema, tables, RLS policies, and grants.

-- ── Application user ──────────────────────────────────────────────────────────

CREATE USER axiaops WITH PASSWORD 'axiaops';

-- ── Schema ────────────────────────────────────────────────────────────────────

CREATE SCHEMA IF NOT EXISTS axiaops;

GRANT USAGE ON SCHEMA axiaops TO axiaops;

-- Future tables created in this schema are automatically accessible to axiaops
ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO axiaops;

SET search_path TO axiaops;

-- ── Tables ────────────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS tenants (
    id         TEXT        PRIMARY KEY,
    org_code   TEXT        NOT NULL UNIQUE,
    name       TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id         TEXT        PRIMARY KEY,
    tenant_id  TEXT        NOT NULL REFERENCES tenants(id),
    kinde_sub  TEXT        NOT NULL UNIQUE,
    email      TEXT        NOT NULL DEFAULT '',
    name       TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    last_seen  TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS cost_records (
    id           BIGSERIAL   PRIMARY KEY,
    tenant_id    TEXT        NOT NULL REFERENCES tenants(id),
    provider     TEXT        NOT NULL,
    account_id   TEXT        NOT NULL,
    service      TEXT        NOT NULL,
    region       TEXT        NOT NULL,
    resource_id  TEXT,
    amount       NUMERIC     NOT NULL,
    currency     TEXT        NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end   TIMESTAMPTZ NOT NULL,
    tags         JSONB,
    fetched_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, provider, account_id, service, region, period_start, period_end)
);

CREATE TABLE IF NOT EXISTS ghost_records (
    id           BIGSERIAL   PRIMARY KEY,
    tenant_id    TEXT        NOT NULL REFERENCES tenants(id),
    provider     TEXT        NOT NULL,
    account_id   TEXT        NOT NULL,
    service      TEXT        NOT NULL,
    region       TEXT        NOT NULL,
    resource_id  TEXT        NOT NULL,
    tags         JSONB,
    monthly_cost NUMERIC     NOT NULL,
    currency     TEXT        NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end   TIMESTAMPTZ NOT NULL,
    usage_metric TEXT        NOT NULL,
    usage_avg    NUMERIC     NOT NULL,
    usage_unit   TEXT        NOT NULL,
    reason       TEXT        NOT NULL,
    owner        TEXT        NOT NULL,
    detected_at  TIMESTAMPTZ NOT NULL
);

-- Grant access to existing tables (default privileges cover future ones)
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA axiaops TO axiaops;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA axiaops TO axiaops;
ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT USAGE, SELECT ON SEQUENCES TO axiaops;

-- ── Row-Level Security ────────────────────────────────────────────────────────
-- axiaops_app does not own the tables so FORCE ROW LEVEL SECURITY is not needed.
-- RLS applies naturally to non-owner users.

ALTER TABLE ghost_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE cost_records  ENABLE ROW LEVEL SECURITY;

CREATE POLICY ghost_tenant_isolation ON ghost_records
    USING (tenant_id = current_setting('app.tenant_id', true));

CREATE POLICY cost_tenant_isolation ON cost_records
    USING (tenant_id = current_setting('app.tenant_id', true));
