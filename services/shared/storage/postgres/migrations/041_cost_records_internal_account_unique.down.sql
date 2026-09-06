-- 041_cost_records_internal_account_unique.down.sql
-- Revert: drop the internal_account_id-aware unique constraint and NOT NULL,
-- restoring the prior (organization_id, provider, account_id, service,
-- region, resource_id, period_start, period_end) constraint.
--
-- Note: if two accounts sharing the same AWS account_id have both been
-- scanned since this migration ran, restoring the old constraint will fail
-- because rows now legitimately differing only by internal_account_id would
-- violate it. Dedupe by hand (or delete one account's cost_records) before
-- running this migration in that case.

SET search_path TO axiaops;

ALTER TABLE cost_records DROP CONSTRAINT IF EXISTS cost_records_org_resource_unique;

ALTER TABLE cost_records ALTER COLUMN internal_account_id DROP NOT NULL;

ALTER TABLE cost_records
    ADD CONSTRAINT cost_records_org_resource_unique
    UNIQUE (organization_id, provider, account_id, service, region, resource_id, period_start, period_end);
