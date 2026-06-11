-- 034_entitlement_internal_plan.up.sql
-- Auto-entitle every org at creation time so the default (SaaS) build's
-- fail-closed scan-gate (entitlement.IsScanAllowedForOrg — missing row → deny)
-- works without billing wired up. See docs/saas-platform-admin-design.md §7.2.
--
-- Two parts:
--   1. Widen the `plan` CHECK to admit a new 'internal' value. Auto-granted
--      pre-billing entitlements carry plan='internal' so they are trivially
--      distinguishable from a real billing-set row (free/pro/enterprise) — the
--      down migration keys its cleanup off exactly this marker, and a future
--      billing integration can leave 'internal' rows untouched or upgrade them.
--   2. Backfill: every pre-existing org gets an 'internal'/'active' row with a
--      high max_accounts so historical orgs (created before this migration) are
--      not gated by the now-live scan-gate. ON CONFLICT DO NOTHING preserves any
--      org that already has a billing-set entitlement.
--
-- The CHECK is an inline CHECK in migration 033, so Postgres auto-named the
-- constraint `entitlements_plan_check`. We DROP + re-ADD it under the same name
-- to keep the schema legible (a named constraint matching the column).

SET search_path TO axiaops;

ALTER TABLE entitlements
    DROP CONSTRAINT entitlements_plan_check,
    ADD CONSTRAINT entitlements_plan_check
        CHECK (plan IN ('free', 'pro', 'enterprise', 'internal'));

-- Backfill pre-existing orgs. 'internal'/'active' is the pre-billing full grant:
-- status='active' is what the fail-closed gate treats as scan-allowed, and a
-- high max_accounts avoids artificially capping onboarding before billing lands.
INSERT INTO entitlements (organization_id, plan, status, max_accounts)
SELECT id, 'internal', 'active', 1000
FROM organizations
ON CONFLICT (organization_id) DO NOTHING;
