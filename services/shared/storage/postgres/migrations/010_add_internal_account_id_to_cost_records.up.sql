-- Add internal_account_id to cost_records for consistency with ghost_records pattern
ALTER TABLE cost_records ADD COLUMN internal_account_id TEXT;

-- Create index for filtering
CREATE INDEX idx_cost_records_internal_account_id ON cost_records(tenant_id, internal_account_id);
