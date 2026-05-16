-- 002_dismiss_snooze.up.sql
-- Adds the dismissed_ghosts table for Track C (Dismiss / Snooze with Reason Codes).
-- Because ghost_records are replaced wholesale on every scan, dismissal state lives
-- in a separate table keyed by a stable resource fingerprint.

SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS dismissed_ghosts (
    id              BIGSERIAL   PRIMARY KEY,
    tenant_id       TEXT        NOT NULL REFERENCES tenants(id),
    account_id      TEXT        NOT NULL,                        -- internal account UUID
    provider        TEXT        NOT NULL,
    service         TEXT        NOT NULL,
    region          TEXT        NOT NULL,
    resource_id     TEXT        NOT NULL,
    action          TEXT        NOT NULL CHECK (action IN ('dismiss', 'snooze')),
    reason          TEXT        NOT NULL,                        -- see ValidDismissReasons
    note            TEXT        NOT NULL DEFAULT '',             -- required when reason='other'
    snoozed_until   TIMESTAMPTZ,                                 -- NULL when action='dismiss'
    dismissed_by    TEXT        NOT NULL DEFAULT '',             -- email / user identifier
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at      TIMESTAMPTZ,                                 -- NULL = active
    revoked_by      TEXT        NOT NULL DEFAULT ''
);

-- Only one active (non-revoked) dismissal per resource per tenant.
CREATE UNIQUE INDEX IF NOT EXISTS dismissed_ghosts_active_fingerprint
    ON dismissed_ghosts (tenant_id, account_id, provider, service, region, resource_id)
    WHERE revoked_at IS NULL;

-- Index for the snooze expiry worker (finds expired snoozes efficiently).
CREATE INDEX IF NOT EXISTS dismissed_ghosts_expiry
    ON dismissed_ghosts (snoozed_until)
    WHERE action = 'snooze' AND revoked_at IS NULL;

-- Grant the app user full access.
GRANT SELECT, INSERT, UPDATE, DELETE ON dismissed_ghosts TO axiaops;
GRANT USAGE, SELECT ON SEQUENCE dismissed_ghosts_id_seq TO axiaops;

-- Row-Level Security: tenants can only see their own dismissals.
ALTER TABLE dismissed_ghosts ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY dismissed_ghosts_tenant_isolation ON dismissed_ghosts
        USING (tenant_id = current_setting('app.tenant_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
