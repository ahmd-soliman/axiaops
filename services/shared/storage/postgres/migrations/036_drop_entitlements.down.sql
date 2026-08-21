-- 039_drop_entitlements.down.sql — reverse of 039 up.
-- Recreates entitlements + processed_stripe_events in their final pre-039
-- shape (033 base + 034's widened plan CHECK + 038's price_id column and
-- indexes). Does NOT restore data — only schema. A real rollback that needs
-- the data back must restore from a pre-039 backup.

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
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    price_id                 TEXT
);

CREATE INDEX IF NOT EXISTS entitlements_billing_customer_ref_idx
    ON entitlements (billing_customer_ref) WHERE billing_customer_ref IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON entitlements TO axiaops_runtime;

CREATE TABLE IF NOT EXISTS processed_stripe_events (
    event_id     TEXT        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS processed_stripe_events_received_idx
    ON processed_stripe_events (received_at);

GRANT SELECT, INSERT, DELETE ON processed_stripe_events TO axiaops_runtime;
