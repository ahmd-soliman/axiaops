-- 034_entitlement_internal_plan.down.sql
-- Reverse 034: drop the auto-granted 'internal' rows, then restore the original
-- migration-033 plan CHECK (without 'internal'). The DELETE must run BEFORE the
-- CHECK is narrowed, otherwise the surviving 'internal' rows would violate it.

SET search_path TO axiaops;

DELETE FROM entitlements WHERE plan = 'internal';

ALTER TABLE entitlements
    DROP CONSTRAINT entitlements_plan_check,
    ADD CONSTRAINT entitlements_plan_check
        CHECK (plan IN ('free', 'pro', 'enterprise'));
