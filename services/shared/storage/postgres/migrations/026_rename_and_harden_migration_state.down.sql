-- 026_rename_and_harden_migration_state.down.sql
--
-- Reverses 026_rename_and_harden_migration_state.up.sql by re-granting the DML
-- 000_init originally landed on the metadata table. Rolling forward through
-- this migration re-creates the security gap the .up.sql closed; only
-- meaningful in migration-reversibility tests.

GRANT INSERT, UPDATE, DELETE ON axiaops.migration_state TO axiaops;
