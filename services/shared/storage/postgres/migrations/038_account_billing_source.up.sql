ALTER TABLE accounts
  ADD COLUMN billing_source   TEXT NOT NULL DEFAULT 'cost_explorer',
  ADD COLUMN cur_database     TEXT,
  ADD COLUMN cur_table        TEXT,
  ADD COLUMN cur_workgroup    TEXT,
  ADD COLUMN cur_results_s3   TEXT,
  ADD COLUMN cur_region       TEXT,
  ADD CONSTRAINT accounts_billing_source_valid
    CHECK (billing_source IN ('cost_explorer','cur_athena')),
  ADD CONSTRAINT accounts_cur_fields_present CHECK (
    billing_source <> 'cur_athena' OR (
      cur_database IS NOT NULL AND cur_table IS NOT NULL AND
      cur_workgroup IS NOT NULL AND cur_results_s3 IS NOT NULL AND
      cur_region IS NOT NULL
    )
  );
