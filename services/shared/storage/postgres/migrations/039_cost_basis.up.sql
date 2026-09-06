SET search_path TO axiaops;

ALTER TABLE cost_records
  ADD COLUMN cost_basis TEXT NOT NULL DEFAULT 'billed';

ALTER TABLE zombie_records
  ADD COLUMN cost_basis TEXT;

ALTER TABLE cost_records
  ADD CONSTRAINT cost_records_cost_basis_check
  CHECK (cost_basis IN ('billed','list_price'));
ALTER TABLE zombie_records
  ADD CONSTRAINT zombie_records_cost_basis_check
  CHECK (cost_basis IS NULL OR cost_basis IN ('billed','list_price'));
