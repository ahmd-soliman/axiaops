-- 015_memberships.up.sql
-- RBAC Phase 1: per-(user, tenant) role membership.
--
-- Design notes (see docs/rbac-design.md §4 for full rationale):
--   * Role is a property of (user, tenant), not of the user. A single Kinde user
--     could belong to multiple AxiaOps tenants with different roles.
--   * Partial unique index enforces at-most-one-owner per tenant at the DB level,
--     backstopping the application-level transfer-ownership flow against races.
--   * Backfill: every existing user becomes 'admin'; earliest-created user per
--     tenant is promoted to 'owner'. Migration fails loudly if any tenant ends
--     up ownerless (e.g. tenant rows with zero users).
--   * gen_random_uuid() is a core function in PostgreSQL 13+ — no pgcrypto needed.

SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS memberships (
    id         TEXT        PRIMARY KEY,
    tenant_id  TEXT        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    role       TEXT        NOT NULL CHECK (role IN ('owner','admin','member','viewer')),
    invited_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, user_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS memberships_one_owner_per_tenant
    ON memberships (tenant_id) WHERE role = 'owner';

GRANT SELECT, INSERT, UPDATE, DELETE ON memberships TO axiaops;

ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY memberships_tenant_isolation ON memberships
        USING (tenant_id = current_setting('app.tenant_id', true))
        WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ── Backfill ─────────────────────────────────────────────────────────────────

-- Every existing user becomes 'admin' in their tenant.
INSERT INTO memberships (id, tenant_id, user_id, role, created_at, updated_at)
SELECT gen_random_uuid()::text, u.tenant_id, u.id, 'admin', NOW(), NOW()
FROM users u
ON CONFLICT (tenant_id, user_id) DO NOTHING;

-- Promote the earliest-created user per tenant to 'owner'.
WITH first_user AS (
    SELECT DISTINCT ON (tenant_id) tenant_id, id AS user_id
    FROM users
    ORDER BY tenant_id, created_at ASC, id ASC
)
UPDATE memberships m
SET role = 'owner', updated_at = NOW()
FROM first_user f
WHERE m.tenant_id = f.tenant_id AND m.user_id = f.user_id;

-- Safety check: every tenant must end with at least one owner.
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM tenants t
        WHERE NOT EXISTS (
            SELECT 1 FROM memberships m
            WHERE m.tenant_id = t.id AND m.role = 'owner'
        )
    ) THEN
        RAISE EXCEPTION 'migration 015: tenant(s) without an owner — refusing to proceed';
    END IF;
END $$;
