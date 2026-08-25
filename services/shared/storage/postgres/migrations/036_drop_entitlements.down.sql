-- 036_drop_entitlements.down.sql — reverse of 036 up.
-- Recreates entitlements in its pre-036 shape (033 base + 034's widened plan
-- CHECK). Does NOT restore data — only schema. A real rollback that needs the
-- data back must restore from a pre-036 backup.

SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS entitlements (
    id                       TEXT        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    organization_id          TEXT        NOT NULL UNIQUE
                                         REFERENCES organizations(id) ON DELETE CASCADE,
    plan                     TEXT        NOT NULL DEFAULT 'free'
                                         CHECK (plan IN ('free', 'pro', 'enterprise', 'internal')),
    status                   TEXT        NOT NULL DEFAULT 'trialing'
                                         CHECK (status IN ('trialing', 'active', 'past_due', 'canceled', 'suspended')),
    max_accounts             INT         NOT NULL DEFAULT 1,
    features                 TEXT[]      NOT NULL DEFAULT '{}',
    trial_ends_at            TIMESTAMPTZ,
    current_period_end       TIMESTAMPTZ,
    billing_customer_ref     TEXT,
    billing_subscription_ref TEXT,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

GRANT SELECT, INSERT, UPDATE, DELETE ON entitlements TO axiaops_runtime;
