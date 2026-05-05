-- 023_sso_force_reauth.down.sql

SET search_path TO axiaops;

ALTER TABLE sso_connections DROP COLUMN IF EXISTS force_reauth;
