-- 036_drop_entitlements.up.sql
-- Removes the per-tenant entitlement subsystem entirely — per-tenant tiers
-- are dropped, no plan/status/limits concept anywhere. Drops the table
-- introduced for it:
--   - entitlements (033, widened 034)
--
-- Deploy ordering: deploy the code that no longer reads/writes this table
-- FIRST, migrate AFTER — a rolling old-code task still SELECTing a dropped
-- table would 500 mid-rollout.

SET search_path TO axiaops;

DROP TABLE IF EXISTS entitlements;
