-- 029_runtime_admin_role.up.sql
-- Least-privilege runtime RLS-bypass role: axiaops_runtime.
--
-- See docs/runtime-admin-db-role.md for the full rationale. In short: the
-- api + ingestion services need an RLS-bypassing connection at runtime —
-- native login reads memberships/users with no app.organization_id set, the
-- scheduled-scan loop enumerates accounts across every organization, and the
-- GDPR org-cascade purge spans all per-org tables. They previously reused the
-- schema-OWNER connection (MIGRATION_DATABASE_URL / axiaops_owner), which also
-- carries DDL/DROP/ownership. axiaops_runtime gets DML + cross-org visibility
-- but NO DDL and NO ownership, so an RCE in a long-lived service container
-- cannot reshape the schema. The owner connection is now reserved for the
-- one-off migrate task.
--
-- RDS NOTE: we deliberately do NOT use the BYPASSRLS role attribute — setting
-- it requires a true superuser, which AWS RDS does not expose (rds_superuser
-- cannot grant BYPASSRLS). Instead we add a permissive RLS policy per
-- RLS-enabled table that grants axiaops_runtime unconditional access.
-- Permissive policies are OR'd, and these are role-scoped to axiaops_runtime,
-- so the app role's org-isolation policy is unaffected. CREATE POLICY only
-- needs table ownership (the migrate connection), so this is RDS-safe.
--
-- Role lifecycle: Bootstrap() (services/shared/storage/postgres/migrate.go)
-- sets LOGIN + a password synced from RUNTIME_ADMIN_DATABASE_URL when that env
-- var is configured. This migration is the authoritative source of the role's
-- PRIVILEGES and also creates the role (NOLOGIN, password-less) if Bootstrap
-- hasn't, so it is self-sufficient on envs with no runtime URL. Runs as
-- axiaops_owner.

SET search_path TO axiaops;

-- Create the role if Bootstrap hasn't already (idempotent — mirrors the
-- axiaops app-user guard in migrate.go).
DO $$ BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'axiaops_runtime') THEN
        CREATE ROLE axiaops_runtime NOLOGIN;
    END IF;
END $$;

-- Explicitly no privilege escalation. NO BYPASSRLS (see RDS note above);
-- cross-org reads come from the per-table policies below.
ALTER ROLE axiaops_runtime NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;

-- Connect + schema usage.
GRANT CONNECT ON DATABASE axiaops TO axiaops_runtime;
GRANT USAGE ON SCHEMA axiaops TO axiaops_runtime;

-- DML on all current + future tables/sequences (mirrors 000_init.up.sql for
-- the app role). NO CREATE on the schema and NO ownership — the privilege
-- drop that makes this role safe for a long-lived runtime.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA axiaops TO axiaops_runtime;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA axiaops TO axiaops_runtime;

ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO axiaops_runtime;
ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops
    GRANT USAGE, SELECT ON SEQUENCES TO axiaops_runtime;

-- Migration bookkeeping stays owner-only (mirrors 025_migration_history_revoke_dml):
-- the runtime role must never tamper with migrate state.
REVOKE INSERT, UPDATE, DELETE ON axiaops.migration_history FROM axiaops_runtime;
REVOKE INSERT, UPDATE, DELETE ON axiaops.migration_state   FROM axiaops_runtime;

-- Cross-org visibility: a permissive bypass policy on every RLS-enabled table.
-- Role-scoped to axiaops_runtime so the app role (axiaops) keeps its
-- org-isolation policy; combined with OR so axiaops_runtime sees all rows.
-- Dynamic so it can't drift from a hand-maintained list; a CI invariant test
-- (runtime_admin_test.go) fails if a future RLS table is missing its policy.
DO $$
DECLARE t text;
BEGIN
    FOR t IN
        SELECT c.relname
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'axiaops' AND c.relkind = 'r' AND c.relrowsecurity
    LOOP
        EXECUTE format('DROP POLICY IF EXISTS %I ON axiaops.%I', t || '_runtime_bypass', t);
        EXECUTE format(
            'CREATE POLICY %I ON axiaops.%I AS PERMISSIVE FOR ALL TO axiaops_runtime USING (true) WITH CHECK (true)',
            t || '_runtime_bypass', t);
    END LOOP;
END $$;

-- audit_log note: the broad grant above gives axiaops_runtime DELETE on
-- audit_log, which the GDPR org-cascade purge (DELETE /v1/organizations/me, run
-- on the bypass pool) needs. This is NOT a new exposure — the app role
-- (axiaops) already holds DELETE on audit_log via the 000_init ALTER DEFAULT
-- PRIVILEGES grant (it fires when audit_log is created in 014; nothing revokes
-- it, so 014's "no DELETE" comment was never actually enforced). The runtime
-- role still cannot DDL or own objects. See docs/audit_trail_plan.md §7.
