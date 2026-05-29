-- 029_runtime_admin_role.down.sql
-- Reverses 029: drop the per-table bypass policies, revoke grants, drop the
-- axiaops_runtime role. Guarded + idempotent (only meaningful in
-- migration-reversibility tests; production never rolls this back).

SET search_path TO axiaops;

DO $$
DECLARE r record;
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'axiaops_runtime') THEN
        -- Drop every bypass policy created by the .up (matched by suffix).
        FOR r IN
            SELECT tablename, policyname
            FROM pg_policies
            WHERE schemaname = 'axiaops' AND policyname ~ '_runtime_bypass$'
        LOOP
            EXECUTE format('DROP POLICY IF EXISTS %I ON axiaops.%I', r.policyname, r.tablename);
        END LOOP;

        -- Reverse default privileges, revoke object/schema/db grants, drop role.
        EXECUTE 'ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM axiaops_runtime';
        EXECUTE 'ALTER DEFAULT PRIVILEGES IN SCHEMA axiaops REVOKE USAGE, SELECT ON SEQUENCES FROM axiaops_runtime';
        EXECUTE 'REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA axiaops FROM axiaops_runtime';
        EXECUTE 'REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA axiaops FROM axiaops_runtime';
        EXECUTE 'REVOKE ALL ON SCHEMA axiaops FROM axiaops_runtime';
        EXECUTE 'REVOKE ALL ON DATABASE axiaops FROM axiaops_runtime';
        EXECUTE 'DROP ROLE axiaops_runtime';
    END IF;
END $$;
