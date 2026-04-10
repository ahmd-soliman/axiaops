-- 002_ghost_snapshots.up.sql
-- Adds ghost_snapshots table for savings history / trend (Phase 2.5).
-- One row is written per ingestion scan, preserving historical ghost counts and spend.

SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS ghost_snapshots (
    id                  TEXT        PRIMARY KEY,
    tenant_id           TEXT        NOT NULL REFERENCES tenants(id),
    account_id          TEXT        NOT NULL,
    snapshot_at         TIMESTAMPTZ NOT NULL,
    ghost_count         INTEGER     NOT NULL DEFAULT 0,
    total_monthly_cost  NUMERIC     NOT NULL DEFAULT 0,
    currency            TEXT        NOT NULL DEFAULT 'USD'
);

GRANT SELECT, INSERT ON ghost_snapshots TO axiaops;

ALTER TABLE ghost_snapshots ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY ghost_snapshots_tenant_isolation ON ghost_snapshots
        USING (tenant_id = current_setting('app.tenant_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
