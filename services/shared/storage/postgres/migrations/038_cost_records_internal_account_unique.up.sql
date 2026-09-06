-- 038_cost_records_internal_account_unique.up.sql
-- Include internal_account_id in the cost_records unique key.
--
-- Without it, two different `accounts` rows connected to the same AWS
-- account_id (e.g. running CE and CUR ingestion side-by-side for migration
-- comparison) collide on the same conflict key and silently overwrite each
-- other's cost_records on every scan — whichever account scanned last "wins"
-- and the other's rows get reassigned to it, making the other account look
-- like it has zero cost data even though it was scanned successfully.
--
-- Same NULL-distinctness concern as migration 020's resource_id fix: Postgres
-- treats two NULLs as DISTINCT in a unique constraint, so we tighten
-- internal_account_id to NOT NULL DEFAULT '' rather than leaving it nullable
-- — otherwise every row lacking it (any future code path that never sets it)
-- would never conflict with itself between re-fetches. In practice, both
-- ingestion call sites that produce cost_records already always set this
-- field before calling Save(); the nullable-with-COALESCE handling that
-- existed in Store.Save was only ever needed for rows written before this
-- column existed (added nullable in migration 010).

SET search_path TO axiaops;

UPDATE cost_records SET internal_account_id = '' WHERE internal_account_id IS NULL;
ALTER TABLE cost_records ALTER COLUMN internal_account_id SET DEFAULT '';
ALTER TABLE cost_records ALTER COLUMN internal_account_id SET NOT NULL;

ALTER TABLE cost_records DROP CONSTRAINT IF EXISTS cost_records_org_resource_unique;

ALTER TABLE cost_records
    ADD CONSTRAINT cost_records_org_resource_unique
    UNIQUE (organization_id, provider, account_id, service, region, resource_id, period_start, period_end, internal_account_id);
