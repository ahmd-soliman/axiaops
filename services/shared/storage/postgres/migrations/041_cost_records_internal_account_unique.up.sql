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
-- Tightened to NOT NULL rather than left nullable: both ingestion call
-- sites that produce cost_records already always set this field before
-- calling Save, no installation has any NULL rows today (verified against
-- prod and dev directly), and a cost record with no known owning account
-- isn't meaningful data to begin with. Same NULL-distinctness reasoning as
-- migration 020's resource_id fix applies (Postgres treats two NULLs as
-- distinct in a unique constraint), but here we close it by disallowing
-- NULL entirely rather than backfilling to a sentinel -- migration 040's
-- cost_records_internal_account_id_fkey means any stored value must
-- reference a real accounts row, and there is no sentinel account to
-- backfill orphaned rows to.
--
-- IF EXISTS/idempotent-safe so this tolerates being replayed by dirty-state
-- recovery after a mid-step crash, same requirement migration 031's
-- DO-block pattern documents.

SET search_path TO axiaops;

-- No-op today (verified zero NULL rows in prod and dev) -- kept as a guard
-- for any future install that somehow has orphaned rows; ON DELETE CASCADE
-- via the migration-040 FK means a NULL row can only exist if its owning
-- account was deleted without cascading, which shouldn't happen but this
-- makes the migration fail loudly (FK violation) rather than silently if
-- it ever does.
ALTER TABLE cost_records ALTER COLUMN internal_account_id SET NOT NULL;

ALTER TABLE cost_records DROP CONSTRAINT IF EXISTS cost_records_org_resource_unique;

ALTER TABLE cost_records
    ADD CONSTRAINT cost_records_org_resource_unique
    UNIQUE (organization_id, provider, account_id, service, region, resource_id, period_start, period_end, internal_account_id);
