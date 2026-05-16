-- 025_migration_history_revoke_dml.down.sql
--
-- Reverses 025_migration_history_revoke_dml.up.sql by re-granting the DML
-- 000_init originally landed on migration_history. Rolling forward through
-- this migration re-creates the security gap the .up.sql closed; only
-- meaningful in migration-reversibility tests.

GRANT INSERT, UPDATE, DELETE ON axiaops.migration_history TO axiaops;
