SET search_path TO axiaops;

ALTER TABLE zombie_records
  DROP CONSTRAINT IF EXISTS zombie_records_cost_basis_check,
  DROP COLUMN IF EXISTS cost_basis;

ALTER TABLE cost_records
  DROP CONSTRAINT IF EXISTS cost_records_cost_basis_check,
  DROP COLUMN IF EXISTS cost_basis;
