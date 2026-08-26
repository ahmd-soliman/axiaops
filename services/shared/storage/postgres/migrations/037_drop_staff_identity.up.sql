-- 037_drop_staff_identity.up.sql
-- Removes the platform admin plane's identity/RBAC tables entirely — the
-- admin plane is cut from the open-source release; it has no use in a
-- self-hosted single-org install. Drops the tables introduced for it:
--   - staff_role_grants (032) — drop first, it FKs into staff_users
--   - staff_users        (032)
--
-- Standing migration discipline: 032_staff_identity is left untouched in the
-- tree rather than edited or deleted in place, same precedent as 036 leaving
-- 033/034 alone.
--
-- Deploy ordering: deploy the code that no longer reads/writes these tables
-- FIRST, migrate AFTER — a rolling old-code task still calling into
-- api-admin's staff store against a dropped table would error mid-rollout.

SET search_path TO axiaops;

DROP TABLE IF EXISTS staff_role_grants CASCADE;
DROP TABLE IF EXISTS staff_users CASCADE;
