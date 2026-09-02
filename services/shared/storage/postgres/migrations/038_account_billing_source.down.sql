ALTER TABLE accounts
  DROP CONSTRAINT IF EXISTS accounts_cur_fields_present,
  DROP CONSTRAINT IF EXISTS accounts_billing_source_valid,
  DROP COLUMN IF EXISTS billing_source,
  DROP COLUMN IF EXISTS cur_database,
  DROP COLUMN IF EXISTS cur_table,
  DROP COLUMN IF EXISTS cur_workgroup,
  DROP COLUMN IF EXISTS cur_results_s3,
  DROP COLUMN IF EXISTS cur_region;
