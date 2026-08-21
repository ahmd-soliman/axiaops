-- 039_drop_entitlements.up.sql
-- Removes the SaaS per-tenant entitlement/billing subsystem entirely
-- (docs/open-source-decision.md §0 — Stripe billing and per-tenant tiers are
-- being dropped; AxiaOps' hosted instance becomes unconditionally free, no
-- plan/status/limits concept anywhere). Drops both tables introduced for it:
--   - entitlements (033, widened 034, extended 038)
--   - processed_stripe_events (038, webhook replay dedupe)
--
-- Deploy ordering: deploy the code that no longer reads/writes these tables
-- FIRST, migrate AFTER — a rolling old-code task still SELECTing a dropped
-- table would 500 mid-rollout.

SET search_path TO axiaops;

DROP TABLE IF EXISTS entitlements;
DROP TABLE IF EXISTS processed_stripe_events;
