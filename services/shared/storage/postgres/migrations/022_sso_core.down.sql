-- 022_sso_core.down.sql
-- Reverse 022_sso_core.up.sql. Drop in reverse FK order; columns last.

SET search_path TO axiaops;

-- memberships.provisioned_via
ALTER TABLE memberships DROP COLUMN IF EXISTS provisioned_via;

-- users.sso_external_id, users.sso_connection_id
DROP INDEX IF EXISTS users_sso_external_idx;
ALTER TABLE users DROP COLUMN IF EXISTS sso_connection_id;
ALTER TABLE users DROP COLUMN IF EXISTS sso_external_id;

-- sso_assertion_replay
DROP INDEX IF EXISTS sso_assertion_replay_expires_idx;
DROP TABLE IF EXISTS sso_assertion_replay;

-- sso_group_mappings
DROP TABLE IF EXISTS sso_group_mappings;

-- sso_domains
DROP INDEX IF EXISTS sso_domains_expiry_idx;
DROP INDEX IF EXISTS sso_domains_lookup_idx;
DROP INDEX IF EXISTS sso_domains_one_active_claim_per_domain;
DROP TABLE IF EXISTS sso_domains;

-- sso_connections — last; FK target for the others above.
DROP INDEX IF EXISTS sso_connections_org_status_idx;
DROP TABLE IF EXISTS sso_connections;
