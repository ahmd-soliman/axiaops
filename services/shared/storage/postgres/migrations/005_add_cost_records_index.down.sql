-- 005_add_cost_records_index.down.sql

SET search_path TO axiaops;

DROP INDEX IF EXISTS idx_cost_records_tenant_period_end;
