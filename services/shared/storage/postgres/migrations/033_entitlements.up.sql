-- 033_entitlements.up.sql
-- SaaS per-tenant entitlement — the billing-driven analogue of the self-hosted
-- license claim. See docs/saas-platform-admin-design.md §7.2 (recommended
-- model) and §8 (data-model deltas).
--
-- DORMANT SCAFFOLD (Phase 2A): this table exists and is read/written by the
-- `entitlement` package + cmd/entitlement-seed, but NO scan-gate consults it
-- yet — that wiring (the *-saashosted composition roots that call
-- license.SetEnforcementBypass()) is Phase 2B, deliberately deferred until
-- ADR-0002 is accepted and the self-serve activation gate proves out
-- (design §7.1 / §11.1 decision 3). Until then this table changes no running
-- deployment's behaviour.
--
-- SYSTEM-scoped, NOT per-org-RLS data — and this is load-bearing:
--   * Written ONLY by AxiaOps (billing webhooks / admin / seed), never by the
--     tenant — so there is no app-pool write path to protect with RLS.
--   * Read CROSS-ORG and PRE-AUTH: the ingestion scheduler enumerates every
--     org with no app.organization_id set, and the worker reads per-job before
--     any org-context middleware runs. An RLS-scoped table would be unreadable
--     by exactly those call sites. So entitlement is keyed by an explicit
--     `WHERE organization_id = $1` on the axiaops_runtime (admin) pool, the
--     same posture as staff_users (migration 032) and the §8 summary.
--   * No RLS, GRANT to axiaops_runtime only — deliberately NOT to the `axiaops`
--     app role. Withholding the app grant is defence-in-depth: with no RLS, an
--     accidental future tenant-handler read would be unprotected; denying the
--     grant turns that into a hard permission error instead. The 029
--     _runtime_bypass policy loop only touches RLS-enabled tables and correctly
--     skips this one (TestRuntimeAdmin_PolicyCoversAllRLSTables asserts only
--     over RLS tables, so its absence is not flagged).
--
-- Schema notes:
--   * id is TEXT (gen_random_uuid()::text), matching every other table.
--   * organization_id is a plain UNIQUE FK (one entitlement per org), NOT an
--     RLS discriminator. ON DELETE CASCADE so a GDPR org purge takes it.
--   * The past_due grace window is NOT stored — it is derived at read time as
--     current_period_end + ENTITLEMENT_GRACE_DAYS, mirroring how license grace
--     is `exp + grace_period_days` rather than a stored column (design §7.2's
--     "+ a grace window on past_due, exactly like the license in_grace
--     philosophy"). Keeps billing the single source of truth for the period.
--   * billing_*_ref are opaque provider handles (Stripe customer/subscription
--     ids) the future webhook seam populates; nullable, no provider coupling
--     in the schema.

SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS entitlements (
    id                       TEXT        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    organization_id          TEXT        NOT NULL UNIQUE
                                         REFERENCES organizations(id) ON DELETE CASCADE,
    plan                     TEXT        NOT NULL DEFAULT 'free'
                                         CHECK (plan IN ('free', 'pro', 'enterprise')),
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

-- No explicit index on organization_id: the UNIQUE constraint above already
-- backs it with a unique index, which serves the WHERE organization_id = $1
-- lookup hot path. (Unlike staff_role_grants, whose staff_user_id index is on
-- a non-unique column, there is nothing to add here.)

GRANT SELECT, INSERT, UPDATE, DELETE ON entitlements TO axiaops_runtime;
