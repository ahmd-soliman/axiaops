-- 028_audit_actor_name.up.sql
-- Denormalise the actor's display name onto audit_log, mirroring actor_email.
-- See services/shared/model/audit.go for the contract: name is captured at
-- request time (resolved via LookupMembership against the live users.name on
-- every authenticated request, same posture as actor_email) and the resulting
-- audit row is immutable — a later rename does not rewrite history. Cleared
-- by AnonymiseUser on GDPR erasure alongside actor_email.
--
-- Existing rows backfill to '' — we don't have their then-current name in
-- history, and reconstructing it from users.name today would produce the
-- *current* name rather than the name-at-event-time, which is the property
-- the column exists to preserve. The frontend already falls back to
-- actor_email when actor_name is empty.

SET search_path TO axiaops;

ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS actor_name TEXT NOT NULL DEFAULT '';
