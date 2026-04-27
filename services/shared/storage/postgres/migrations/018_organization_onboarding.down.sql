-- 018_organization_onboarding.down.sql

SET search_path TO axiaops;

ALTER TABLE organizations DROP COLUMN IF EXISTS onboarding_completed_at;
