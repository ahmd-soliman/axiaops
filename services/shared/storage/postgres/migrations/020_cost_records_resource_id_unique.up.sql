-- 020_cost_records_resource_id_unique.up.sql
-- Include resource_id in the cost_records unique key.
--
-- The original unique key was
--   (organization_id, provider, account_id, service, region, period_start, period_end)
-- which silently dropped per-resource rows from FetchResourceCosts because
-- their (service, region, period) keys collide with the FetchCosts aggregate
-- row for the same day. With this change, an aggregate row (resource_id = '')
-- and per-resource rows can coexist for the same service+region+period.
--
-- We also tighten resource_id to NOT NULL DEFAULT ''. Postgres treats two
-- NULL values as DISTINCT in a unique constraint by default; storing an
-- empty string keeps "no resource ID" rows mutually exclusive in the upsert
-- (so a duplicate aggregate row from a re-scan still conflicts as expected).
-- Existing data on dev/staging shows zero NULLs, only empty strings, so the
-- backfill is a no-op in practice — kept for safety on any installs that
-- never received an aggregate row.

SET search_path TO axiaops;

-- Backfill any nulls before tightening the column. ALTER ... SET NOT NULL
-- takes ACCESS EXCLUSIVE on cost_records for the duration of the table
-- scan that validates the constraint. Acceptable while cost_records is
-- small (pre-launch dev/staging, modest-sized production accounts). When
-- the table grows, switch to the NOT VALID + VALIDATE CONSTRAINT pattern
-- to avoid a long write-blocking lock.
UPDATE cost_records SET resource_id = '' WHERE resource_id IS NULL;
ALTER TABLE cost_records ALTER COLUMN resource_id SET DEFAULT '';
ALTER TABLE cost_records ALTER COLUMN resource_id SET NOT NULL;

-- The old constraint name is auto-generated from the original pre-rename
-- schema (the column was tenant_id before migration 016). Postgres does not
-- rename auto-generated constraints when columns rename, so the name still
-- reads "tenant_id" even though the column is now "organization_id".
-- IF EXISTS guards against partial-run reattempts.
ALTER TABLE cost_records
    DROP CONSTRAINT IF EXISTS cost_records_tenant_id_provider_account_id_service_region_p_key;

ALTER TABLE cost_records
    ADD CONSTRAINT cost_records_org_resource_unique
    UNIQUE (organization_id, provider, account_id, service, region, resource_id, period_start, period_end);
