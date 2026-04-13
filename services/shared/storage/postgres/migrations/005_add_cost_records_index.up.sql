-- 005_add_cost_records_index.up.sql
-- Add index on (tenant_id, period_end) for efficient retention range deletes.

SET search_path TO axiaops;

CREATE INDEX IF NOT EXISTS idx_cost_records_tenant_period_end
    ON cost_records (tenant_id, period_end);
