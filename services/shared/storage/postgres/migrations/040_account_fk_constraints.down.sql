-- Rollback: remove account foreign key constraints

SET search_path TO axiaops;

ALTER TABLE cost_records DROP CONSTRAINT IF EXISTS cost_records_internal_account_id_fkey;
ALTER TABLE resource_records DROP CONSTRAINT IF EXISTS resource_records_internal_account_id_fkey;
ALTER TABLE zombie_records DROP CONSTRAINT IF EXISTS zombie_records_internal_account_id_fkey;
ALTER TABLE zombie_snapshots DROP CONSTRAINT IF EXISTS zombie_snapshots_account_id_fkey;
ALTER TABLE dismissed_zombies DROP CONSTRAINT IF EXISTS dismissed_zombies_account_id_fkey;
