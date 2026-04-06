-- migrate_accounts.sql — run once against an existing database to add the accounts table.
-- Safe to run multiple times (IF NOT EXISTS).

SET search_path TO axiaops;

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
    created_at        TIMESTAMPTZ NOT NULL
);

GRANT SELECT, INSERT, UPDATE, DELETE ON axiaops.accounts TO axiaops;

ALTER TABLE accounts ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE tablename = 'accounts' AND policyname = 'accounts_tenant_isolation'
    ) THEN
        CREATE POLICY accounts_tenant_isolation ON accounts
            USING (tenant_id = current_setting('app.tenant_id', true));
    END IF;
END$$;
