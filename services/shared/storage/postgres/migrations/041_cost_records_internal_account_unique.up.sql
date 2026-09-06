-- 041_cost_records_internal_account_unique.up.sql
-- Include internal_account_id in the cost_records unique key.
--
-- Without it, two different `accounts` rows connected to the same AWS
-- account_id (e.g. running CE and CUR ingestion side-by-side for migration
-- comparison) collide on the same conflict key and silently overwrite each
-- other's cost_records on every scan — whichever account scanned last "wins"
-- and the other's rows get reassigned to it, making the other account look
-- like it has zero cost data even though it was scanned successfully.
--
-- A plain column-based UNIQUE constraint won't work here: Postgres treats
-- every NULL as distinct from every other NULL, so a record saved twice
-- without InternalAccountID set (nil) would insert a new row on every
-- re-fetch instead of updating in place -- a real regression this migration
-- was caught introducing against TestSave_SecondCallUpdatesExisting et al.
-- We also can't backfill NULL to '' the way migration 020 did for
-- resource_id: migration 040 on this branch added
-- cost_records_internal_account_id_fkey REFERENCING accounts(id), and ''
-- would need a real accounts row with that id to satisfy it.
--
-- Instead this is an expression-based UNIQUE INDEX keyed on
-- COALESCE(internal_account_id, ''). The stored column value stays exactly
-- what was written -- NULL stays NULL, satisfying the foreign key -- but
-- for the purposes of conflict detection two NULLs now collide with each
-- other (both normalize to ''), matching the old upsert-in-place behavior,
-- while two different real internal_account_id values still don't collide
-- with each other or with NULL.

-- IF NOT EXISTS on the index (and IF EXISTS on the drop) so this migration
-- tolerates being replayed by dirty-state recovery after a mid-step crash,
-- same requirement migration 031's DO-block pattern documents.

SET search_path TO axiaops;

ALTER TABLE cost_records DROP CONSTRAINT IF EXISTS cost_records_org_resource_unique;

CREATE UNIQUE INDEX IF NOT EXISTS cost_records_org_resource_unique ON cost_records (
    organization_id, provider, account_id, service, region, resource_id,
    period_start, period_end, COALESCE(internal_account_id, '')
);
