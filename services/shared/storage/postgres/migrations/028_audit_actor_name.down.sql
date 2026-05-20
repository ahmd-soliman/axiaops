-- 028_audit_actor_name.down.sql
-- Drop the denormalised actor_name column. Rolling this back loses every
-- captured name in audit history — actor_email survives so attribution
-- remains intact, just less friendly.

SET search_path TO axiaops;

ALTER TABLE audit_log
    DROP COLUMN IF EXISTS actor_name;
