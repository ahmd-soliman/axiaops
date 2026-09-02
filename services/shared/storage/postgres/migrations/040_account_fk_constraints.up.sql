-- Add missing foreign key constraints to prevent orphaned records when an account is deleted.
-- We must scrub existing orphans before applying the constraints.

SET search_path TO axiaops;

DELETE FROM cost_records WHERE internal_account_id IS NOT NULL AND internal_account_id NOT IN (SELECT id FROM accounts);
ALTER TABLE cost_records ADD CONSTRAINT cost_records_internal_account_id_fkey FOREIGN KEY (internal_account_id) REFERENCES accounts(id) ON DELETE CASCADE;

DELETE FROM resource_records WHERE internal_account_id IS NOT NULL AND internal_account_id NOT IN (SELECT id FROM accounts);
ALTER TABLE resource_records ADD CONSTRAINT resource_records_internal_account_id_fkey FOREIGN KEY (internal_account_id) REFERENCES accounts(id) ON DELETE CASCADE;

DELETE FROM zombie_records WHERE internal_account_id IS NOT NULL AND internal_account_id NOT IN (SELECT id FROM accounts);
ALTER TABLE zombie_records ADD CONSTRAINT zombie_records_internal_account_id_fkey FOREIGN KEY (internal_account_id) REFERENCES accounts(id) ON DELETE CASCADE;

DELETE FROM zombie_snapshots WHERE account_id IS NOT NULL AND account_id NOT IN (SELECT id FROM accounts);
ALTER TABLE zombie_snapshots ADD CONSTRAINT zombie_snapshots_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

DELETE FROM dismissed_zombies WHERE account_id IS NOT NULL AND account_id NOT IN (SELECT id FROM accounts);
ALTER TABLE dismissed_zombies ADD CONSTRAINT dismissed_zombies_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
