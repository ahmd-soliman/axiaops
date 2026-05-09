-- 025_migration_history_revoke_dml.up.sql
--
-- Revoke INSERT/UPDATE/DELETE on axiaops.migration_history (and the
-- companion view) from the app user. DML is owner-only by policy — see
-- docs/migration-history-table-design.md §Schema.
--
-- Why this migration exists at all: postgres.Bootstrap creates the table
-- before 000_init.up.sql runs and explicitly GRANTs only SELECT, but
-- 000_init's blanket
--
--     GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA axiaops TO axiaops;
--
-- fires on every table that exists at that moment — including
-- migration_history — silently undoing the SELECT-only posture. We can't
-- modify 000_init.up.sql (that would trip drift detection on every env
-- that already applied it), so we restore the design intent here.
--
-- Idempotent: REVOKE on a privilege the role doesn't hold is a no-op
-- with a NOTICE — fine on re-runs.
--
-- Sequences are unaffected: the BIGSERIAL sequence is granted USAGE/SELECT
-- via 000_init's blanket sequence grant, which is intentional (the app user
-- never inserts, but the sequence's existence is harmless to read).

REVOKE INSERT, UPDATE, DELETE ON axiaops.migration_history   FROM axiaops;
REVOKE INSERT, UPDATE, DELETE ON axiaops.migration_history_v FROM axiaops;
