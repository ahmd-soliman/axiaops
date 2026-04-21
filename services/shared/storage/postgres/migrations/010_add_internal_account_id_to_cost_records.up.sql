-- Add internal_account_id to cost_records for consistency with ghost_records pattern
SET search_path TO axiaops;

ALTER TABLE cost_records ADD COLUMN IF NOT EXISTS internal_account_id TEXT;

-- Create index for filtering
CREATE INDEX IF NOT EXISTS idx_cost_records_internal_account_id ON cost_records(tenant_id, internal_account_id);
