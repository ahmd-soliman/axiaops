-- 004_snapshot_services.up.sql
-- Per-service breakdown for each ghost snapshot, enabling service-filtered trend views.

SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS ghost_snapshot_services (
    id              TEXT        PRIMARY KEY,
    snapshot_id     TEXT        NOT NULL REFERENCES ghost_snapshots(id) ON DELETE CASCADE,
    tenant_id       TEXT        NOT NULL REFERENCES tenants(id),
    service         TEXT        NOT NULL,
    ghost_count     INTEGER     NOT NULL DEFAULT 0,
    monthly_cost    NUMERIC     NOT NULL DEFAULT 0,
    currency        TEXT        NOT NULL DEFAULT 'USD'
);

CREATE INDEX IF NOT EXISTS idx_snapshot_services_snapshot
    ON ghost_snapshot_services (snapshot_id);

CREATE INDEX IF NOT EXISTS idx_snapshot_services_tenant_service
    ON ghost_snapshot_services (tenant_id, service);

GRANT SELECT, INSERT, UPDATE, DELETE ON ghost_snapshot_services TO axiaops;

ALTER TABLE ghost_snapshot_services ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY snapshot_services_tenant_isolation ON ghost_snapshot_services
        USING (tenant_id = current_setting('app.tenant_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
