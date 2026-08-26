-- 032_staff_identity.up.sql
-- Platform admin plane — staff identity + RBAC.
--
-- These are SYSTEM-scoped tables, NOT per-org data:
--   * staff_users        — AxiaOps employee identities (the admin/control
--     plane principal; structurally separate from tenant `users` — §3 "a
--     principal never spans planes").
--   * staff_role_grants  — staff principal → staff role (support/ops/billing/
--     superadmin), the orthogonal-to-tenant RBAC of §4.2.
--
-- Schema notes:
--   * No RLS. Like `users` / `sessions` / `organizations`, these are not
--     org-scoped — a staff member belongs to no organization. They are read
--     ONLY by the admin plane on the axiaops_runtime pool, never by tenant
--     handlers on the RLS-enforced `axiaops` app pool — so we grant to
--     axiaops_runtime explicitly and deliberately do NOT grant to `axiaops`.
--     Withholding the app-role grant is defence-in-depth: since these tables
--     have no RLS, an accidental future tenant-handler call would be
--     unprotected — denying the grant turns that into a hard permission error
--     instead. (Migration 029's ALTER DEFAULT PRIVILEGES would also cover
--     axiaops_runtime here; the explicit grant makes the intent legible and
--     independent of grantor-ordering subtleties.) The dynamic _runtime_bypass
--     policy loop in 029 only touches RLS-enabled tables and correctly skips
--     these; TestRuntimeAdmin_PolicyCoversAllRLSTables asserts only over RLS
--     tables, so it will not flag their absence.
--   * IDs are TEXT (gen_random_uuid()::text), matching every other table.
--   * password_hash is the argon2id PHC string (auth.Hash output), nullable so
--     a later corporate-IdP staff.Provider impl can mint IdP-only staff rows
--     with no local secret.
--   * `role` CHECK is the baseline taxonomy. An auditor/engineering tier is
--     purely additive — a future migration widens the CHECK; do not
--     pre-provision it (no shipping code consumes it yet, unlike the
--     notification_channels kind-enum which had transport stubs).

SET search_path TO axiaops;

-- ── staff_users ───────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS staff_users (
    id            TEXT        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    email         TEXT        NOT NULL,
    name          TEXT        NOT NULL DEFAULT '',
    -- argon2id PHC string; NULL once a corporate-IdP impl owns auth for the row.
    password_hash TEXT,
    status        TEXT        NOT NULL DEFAULT 'active'
                              CHECK (status IN ('active', 'suspended')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen     TIMESTAMPTZ
);

-- Case-insensitive uniqueness, mirroring users_email_lower_unique (migration 021).
CREATE UNIQUE INDEX IF NOT EXISTS staff_users_email_lower_unique
    ON staff_users (lower(email));

GRANT SELECT, INSERT, UPDATE, DELETE ON staff_users TO axiaops_runtime;

-- ── staff_role_grants ─────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS staff_role_grants (
    id            TEXT        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    staff_user_id TEXT        NOT NULL REFERENCES staff_users(id) ON DELETE CASCADE,
    role          TEXT        NOT NULL
                              CHECK (role IN ('support', 'ops', 'billing', 'superadmin')),
    -- NULL for the bootstrap superadmin (no prior staff member granted it).
    granted_by    TEXT        REFERENCES staff_users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (staff_user_id, role)
);

-- Hot path: resolve a staff principal's roles by staff_user_id on every request.
CREATE INDEX IF NOT EXISTS staff_role_grants_staff_user_idx
    ON staff_role_grants (staff_user_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON staff_role_grants TO axiaops_runtime;
