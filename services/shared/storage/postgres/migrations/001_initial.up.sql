-- 001_initial.up.sql
-- Baseline schema: all tables and RLS policies.
-- User, schema, and default grants are set up by 000_init.up.sql.

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
    id                  BIGSERIAL   PRIMARY KEY,
    tenant_id           TEXT        NOT NULL REFERENCES tenants(id),
    provider            TEXT        NOT NULL,
    account_id          TEXT        NOT NULL,
    internal_account_id TEXT,
    service             TEXT        NOT NULL,
    region              TEXT        NOT NULL,
    resource_id         TEXT        NOT NULL,
    tags                JSONB,
    monthly_cost        NUMERIC     NOT NULL,
    currency            TEXT        NOT NULL,
    period_start        TIMESTAMPTZ NOT NULL,
    period_end          TIMESTAMPTZ NOT NULL,
    usage_metric        TEXT        NOT NULL,
    usage_avg           NUMERIC     NOT NULL,
    usage_unit          TEXT        NOT NULL,
    reason              TEXT        NOT NULL,
    owner               TEXT        NOT NULL,
    detected_at         TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS resource_records (
    id                  BIGSERIAL   PRIMARY KEY,
    tenant_id           TEXT        NOT NULL REFERENCES tenants(id),
    provider            TEXT        NOT NULL,
    account_id          TEXT        NOT NULL,
    internal_account_id TEXT,
    service             TEXT        NOT NULL,
    region              TEXT        NOT NULL,
    resource_id         TEXT        NOT NULL,
    tags                JSONB,
    monthly_cost        NUMERIC     NOT NULL,
    currency            TEXT        NOT NULL,
    period_start        TIMESTAMPTZ NOT NULL,
    period_end          TIMESTAMPTZ NOT NULL,
    usage_metric        TEXT        NOT NULL DEFAULT '',
    usage_avg           NUMERIC     NOT NULL DEFAULT 0,
    usage_unit          TEXT        NOT NULL DEFAULT '',
    is_ghost            BOOLEAN     NOT NULL DEFAULT false,
    reason              TEXT        NOT NULL DEFAULT '',
    owner               TEXT        NOT NULL DEFAULT '',
    detected_at         TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS accounts (
    id                TEXT        PRIMARY KEY,
    tenant_id         TEXT        NOT NULL REFERENCES tenants(id),
    provider          TEXT        NOT NULL DEFAULT 'aws',
    label             TEXT        NOT NULL DEFAULT '',
    access_key_id     TEXT        NOT NULL DEFAULT '',
    secret_encrypted  TEXT        NOT NULL DEFAULT '',
    region            TEXT        NOT NULL DEFAULT 'us-east-1',
    status            TEXT        NOT NULL DEFAULT 'connected',
    last_scanned_at   TIMESTAMPTZ,
    scan_interval_hours INT       NOT NULL DEFAULT 24,
    created_at        TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS ghost_snapshots (
    id                  TEXT        PRIMARY KEY,
    tenant_id           TEXT        NOT NULL REFERENCES tenants(id),
    account_id          TEXT        NOT NULL,
    snapshot_at         TIMESTAMPTZ NOT NULL,
    ghost_count         INTEGER     NOT NULL DEFAULT 0,
    total_monthly_cost  NUMERIC     NOT NULL DEFAULT 0,
    currency            TEXT        NOT NULL DEFAULT 'USD'
);

-- Ensure the app user can access all tables created by this migration.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA axiaops TO axiaops;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA axiaops TO axiaops;

-- ── Row-Level Security ────────────────────────────────────────────────────────

ALTER TABLE cost_records     ENABLE ROW LEVEL SECURITY;
ALTER TABLE ghost_records    ENABLE ROW LEVEL SECURITY;
ALTER TABLE resource_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE accounts         ENABLE ROW LEVEL SECURITY;
ALTER TABLE ghost_snapshots  ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY cost_tenant_isolation ON cost_records
        USING (tenant_id = current_setting('app.tenant_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY ghost_tenant_isolation ON ghost_records
        USING (tenant_id = current_setting('app.tenant_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY resource_tenant_isolation ON resource_records
        USING (tenant_id = current_setting('app.tenant_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY accounts_tenant_isolation ON accounts
        USING (tenant_id = current_setting('app.tenant_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY ghost_snapshots_tenant_isolation ON ghost_snapshots
        USING (tenant_id = current_setting('app.tenant_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Add index on (tenant_id, period_end) for efficient retention range deletes.
CREATE INDEX IF NOT EXISTS idx_cost_records_tenant_period_end
    ON cost_records (tenant_id, period_end);
