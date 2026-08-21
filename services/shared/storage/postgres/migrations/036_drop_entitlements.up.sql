-- 036_drop_entitlements.up.sql
-- Removes the SaaS per-tenant entitlement subsystem entirely
-- (docs/open-source-decision.md §0 — per-tenant tiers are being dropped;
-- AxiaOps' hosted instance becomes unconditionally free, no plan/status/limits
-- concept anywhere). Drops the table introduced for it:
--   - entitlements (033, widened 034)
--
-- Deploy ordering: deploy the code that no longer reads/writes this table
-- FIRST, migrate AFTER — a rolling old-code task still SELECTing a dropped
-- table would 500 mid-rollout.

SET search_path TO axiaops;

DROP TABLE IF EXISTS entitlements;
