-- Rollback: Remove AWS account ID from accounts table
DROP INDEX IF EXISTS axiaops.idx_accounts_account_id;
ALTER TABLE axiaops.accounts DROP COLUMN account_id;
