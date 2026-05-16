-- Rollback: remove internal_account_id from cost_records
SET search_path TO axiaops;

DROP INDEX IF EXISTS idx_cost_records_internal_account_id;
ALTER TABLE cost_records DROP COLUMN internal_account_id;
