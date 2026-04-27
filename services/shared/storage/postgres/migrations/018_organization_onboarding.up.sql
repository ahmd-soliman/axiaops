-- 018_organization_onboarding.up.sql
-- Adds the onboarding_completed_at flag used by the post-signup wizard.
-- See docs/onboarding-wizard.md §4.
--
-- Design notes:
--   * One-way ratchet: NULL means "wizard pending", non-NULL means "completed".
--     The wizard never re-triggers, even if accounts/teammates are later
--     deleted. The dashboard's WhatsNextPanel surfaces follow-up state via
--     derived signals (no accounts → show "Connect AWS" tile).
--   * Backfill: existing organizations have already completed setup; mark
--     them done so they never see the wizard.
--   * Rides the existing organizations RLS policy — no new policy needed.

SET search_path TO axiaops;

ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS onboarding_completed_at TIMESTAMPTZ NULL;

UPDATE organizations
   SET onboarding_completed_at = NOW()
 WHERE onboarding_completed_at IS NULL;
