-- 026_schema_migrations_revoke_dml.down.sql
--
-- Reverses 026_schema_migrations_revoke_dml.up.sql by re-granting the DML
-- 000_init originally landed on schema_migrations. Rolling forward through
-- this migration re-creates the security gap the .up.sql closed; only
-- meaningful in migration-reversibility tests.

GRANT INSERT, UPDATE, DELETE ON axiaops.schema_migrations TO axiaops;
