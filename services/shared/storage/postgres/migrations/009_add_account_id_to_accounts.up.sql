-- Add AWS account ID to accounts table for cost record linking
ALTER TABLE axiaops.accounts ADD COLUMN account_id TEXT DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_accounts_account_id ON axiaops.accounts(account_id);
