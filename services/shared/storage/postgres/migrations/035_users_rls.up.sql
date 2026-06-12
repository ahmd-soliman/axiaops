-- 035_users_rls.up.sql
-- Security finding H-1 (docs/security-audit-2026-05-09.md §H-1): the `users`
-- table was the one multi-tenant table with no Row-Level Security policy. Every
-- other data table (cost_records/…/memberships/notification_channels) carries
-- org-isolation RLS; `users` relied on explicit WHERE clauses + adminPool usage
-- alone. That is a defence-in-depth gap: any future handler that opens an
-- app-pool tx and runs `SELECT … FROM users` without an explicit org filter
-- would see every user across every org (sensitive columns: password_hash,
-- email, sso_external_id). This migration closes it so a future regression to
-- the app pool fails closed.
--
-- Pattern mirrors 031_notification_channels: org-isolation policy for the app
-- role (axiaops) + a permissive runtime-bypass policy for axiaops_runtime. The
-- runtime role must read/write users with NO org context across orgs (pre-auth
-- native login, /v1/me for cross-org members, SSO upsert, GDPR purge); the
-- dynamic per-table loop in 029 only covered tables that had RLS at that time,
-- so a newly-RLS table must add its own bypass policy. The CI invariant
-- TestRuntimeAdmin_PolicyCoversAllRLSTables (runtime_admin_test.go) fails if it
-- is missing.

SET search_path TO axiaops;

-- RLS USING/WITH-CHECK predicate and the explicit-WHERE callsites both filter on
-- organization_id; there is no index on it today (only PK + email/external_id
-- uniques). Add one so the policy predicate stays cheap.
CREATE INDEX IF NOT EXISTS users_organization_id_idx ON users (organization_id);

ALTER TABLE users ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY users_organization_isolation ON users
        USING (organization_id = current_setting('app.organization_id', true))
        WITH CHECK (organization_id = current_setting('app.organization_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY users_runtime_bypass ON users
        AS PERMISSIVE FOR ALL TO axiaops_runtime
        USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
