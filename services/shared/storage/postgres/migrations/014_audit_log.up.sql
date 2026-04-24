-- 014_audit_log.up.sql
-- Audit trail for user-initiated mutations: dismiss, snooze, revoke, on-demand
-- scan, account CRUD, and (later) remediation views. Scheduled/automated scans
-- are logged to scan_runs, not audit_log — audit rows always have a human actor.
--
-- Design notes (see docs/audit_trail_plan.md §3 for full rationale):
--   * actor_email is denormalised on write so that GDPR anonymisation (setting
--     user_id = NULL) preserves the human-readable attribution that was true
--     at event time.
--   * metadata is JSONB so new action types can be added without schema churn.
--   * INSERT-mostly table. UPDATE is granted only so the GDPR anonymiser can
--     null out user_id; no DELETE grant — tenant purges run as axiaops_owner.
--   * Composite index on (tenant_id, created_at DESC) serves both the tenant
--     timeline view and any future time-range filtered queries.

SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS audit_log (
    id            BIGSERIAL   PRIMARY KEY,
    tenant_id     TEXT        NOT NULL REFERENCES tenants(id),
    user_id       TEXT,                              -- NULL after GDPR anonymisation
    actor_email   TEXT        NOT NULL DEFAULT '',   -- captured at event time
    action        TEXT        NOT NULL,              -- enum validated in Go, not DB
    resource_type TEXT        NOT NULL DEFAULT '',   -- "zombie" | "dismissal" | "account" | "scan"
    resource_id   TEXT        NOT NULL DEFAULT '',
    reason        TEXT        NOT NULL DEFAULT '',
    metadata      JSONB       NOT NULL DEFAULT '{}'::JSONB,
    request_id    TEXT        NOT NULL DEFAULT '',
    ip_address    INET,
    user_agent    TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS audit_log_tenant_created_idx
    ON audit_log (tenant_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS audit_log_resource_idx
    ON audit_log (tenant_id, resource_type, resource_id);

CREATE INDEX IF NOT EXISTS audit_log_user_idx
    ON audit_log (tenant_id, user_id)
    WHERE user_id IS NOT NULL;

GRANT SELECT, INSERT, UPDATE ON audit_log TO axiaops;
GRANT USAGE, SELECT ON SEQUENCE audit_log_id_seq TO axiaops;

ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY audit_log_tenant_isolation ON audit_log
        USING (tenant_id = current_setting('app.tenant_id', true))
        WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
