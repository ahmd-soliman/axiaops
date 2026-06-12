-- 035_users_rls.down.sql

SET search_path TO axiaops;

DROP POLICY IF EXISTS users_runtime_bypass ON users;
DROP POLICY IF EXISTS users_organization_isolation ON users;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;
DROP INDEX IF EXISTS users_organization_id_idx;
