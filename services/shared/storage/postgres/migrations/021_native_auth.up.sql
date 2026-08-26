-- 021_native_auth.up.sql
-- Native email/password auth replacing Kinde.
--
-- Adds:
--   * users.password_hash, users.password_set_at, users.email_lower
--   * sessions       (server-side opaque-token session store; NO RLS — see below)
--   * password_resets (admin-mediated reset tokens; NO RLS)
--   * bootstrap_state (singleton row coordinating first-owner install token; NO RLS)
--   * pending_memberships.invite_token_hash (token-based redemption alongside Kinde)
--
-- RLS is intentionally OFF for sessions / password_resets / bootstrap_state.
-- The lookup primary key on each is a capability — possession of the
-- (cryptographically random) plaintext token IS the authorisation. Lookup also
-- happens BEFORE the request has any organization context, so RLS would block
-- the very query that establishes the org. See migration body for details.

SET search_path TO axiaops;

-- ── users: native-auth columns ──────────────────────────────────────────────
-- Self-hosted deploys one stack per customer, so global email uniqueness is
-- the desired property — a user with a given email exists in exactly one
-- AxiaOps installation.
ALTER TABLE users
    ADD COLUMN password_hash       TEXT        NOT NULL DEFAULT '',
    ADD COLUMN password_set_at     TIMESTAMPTZ,
    ADD COLUMN email_lower         TEXT        GENERATED ALWAYS AS (lower(email)) STORED;

-- Partial unique index: the empty string is a degenerate "no email" sentinel
-- used by older Kinde-issued users (whose email claim was absent) and by
-- some test fixtures. Real emails enforce uniqueness; sentinels do not. This
-- matches the design intent — "a user with a given email exists in exactly
-- one installation" — while staying compatible with the strangler window.
CREATE UNIQUE INDEX users_email_lower_unique
    ON users (email_lower)
    WHERE email_lower <> '';

-- ── sessions ────────────────────────────────────────────────────────────────
-- Server-side session store. Cookie carries an opaque session_id;
-- session_token_hash is the SHA-256 of the random token in the cookie.
--
-- IMPORTANT — RLS is intentionally NOT enabled on this table.
--   1. Session lookup happens BEFORE the request has any organization context
--      (it is the lookup that *establishes* the org context). RLS on this
--      table would block every authenticated request because
--      app.organization_id is unset at lookup time.
--   2. The session_token_hash is itself a capability — possession of the
--      plaintext token proves authorisation. RLS adds no security on top.
--   3. Cross-org leakage is impossible because the SELECT clause is
--      `WHERE session_token_hash = $1` — the caller must already know the
--      (cryptographically-random) token to retrieve the row.
-- Session middleware sets app.organization_id from sessions.organization_id
-- AFTER lookup, before any subsequent handler-level query runs.
CREATE TABLE IF NOT EXISTS sessions (
    id                  TEXT        PRIMARY KEY,
    user_id             TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id     TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    auth_mode           TEXT        NOT NULL CHECK (auth_mode IN ('password','sso','bootstrap')),
    session_token_hash  TEXT        NOT NULL UNIQUE,           -- UNIQUE creates the lookup index; no separate index needed
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMPTZ NOT NULL,
    revoked_at          TIMESTAMPTZ,
    last_seen_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip                  INET,
    user_agent_hash     TEXT
);
CREATE INDEX IF NOT EXISTS sessions_user_idx     ON sessions (user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_idx  ON sessions (expires_at) WHERE revoked_at IS NULL;
-- (no sessions_token_idx — the UNIQUE constraint on session_token_hash creates the index)

GRANT SELECT, INSERT, UPDATE, DELETE ON sessions TO axiaops;

-- ── password_resets ─────────────────────────────────────────────────────────
-- Single-use, time-bounded reset tokens. Admin-mediated in v1 (decision D4).
-- RLS NOT enabled — same capability-based reasoning as `sessions`. The
-- redeem endpoint looks up by token_hash before any org context exists.
CREATE TABLE IF NOT EXISTS password_resets (
    id                  TEXT        PRIMARY KEY,
    user_id             TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id     TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token_hash          TEXT        NOT NULL UNIQUE,           -- UNIQUE creates the index
    issued_by_user_id   TEXT        REFERENCES users(id) ON DELETE SET NULL,
    expires_at          TIMESTAMPTZ NOT NULL,
    redeemed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS password_resets_user_idx     ON password_resets (user_id);
CREATE INDEX IF NOT EXISTS password_resets_expires_idx  ON password_resets (expires_at) WHERE redeemed_at IS NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON password_resets TO axiaops;

-- ── bootstrap_state ─────────────────────────────────────────────────────────
-- Multi-replica-safe coordination for the first-owner bootstrap flow (D5).
-- A single row is inserted by exactly one replica (PG advisory lock + ON
-- CONFLICT DO NOTHING). The row holds the install-token hash and is deleted
-- on successful bootstrap. After deletion, organizations table has ≥1 row
-- and the bootstrap endpoint is sealed forever.
--
-- Why a table and not just a sentinel file: replicas don't share filesystems
-- in containerised deployments, and we need exactly-once token generation
-- across the cluster. PG is the only shared coordination point AxiaOps has.
CREATE TABLE IF NOT EXISTS bootstrap_state (
    id                  TEXT        PRIMARY KEY DEFAULT 'singleton'
                                    CHECK (id = 'singleton'),  -- enforces ≤1 row
    token_hash          TEXT        NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    minted_by_pod       TEXT                                    -- HOSTNAME of the replica that won the race; informational
);

GRANT SELECT, INSERT, DELETE ON bootstrap_state TO axiaops;

-- ── pending_memberships: token-based redemption ────────────────────────────
-- Existing table (017_pending_memberships) already enforces expires_at NOT NULL
-- and one pending row per (org, email). Add a column for the token-based
-- native-auth redemption flow alongside the Kinde-callback flow.
--
-- Migration is reversible per-environment (architect S2):
--   - invite_token_hash is NULLABLE during the strangler window.
--   - Existing Kinde-era rows keep invite_token_hash = NULL and continue
--     to redeem via the Kinde callback path.
--   - Native-mode rows MUST set invite_token_hash to a non-empty SHA-256.
--   - The NOT NULL tightening + Kinde-mode row deletion lands in a later
--     migration at the Kinde deprecation date (D2 — 2026-10-30).
ALTER TABLE pending_memberships
    ADD COLUMN invite_token_hash TEXT;

ALTER TABLE pending_memberships
    ADD CONSTRAINT pending_memberships_native_token_shape
        CHECK (
            -- Either a Kinde-mode row (no token, redeemed via Kinde callback)
            -- or a native-mode row (token present and non-empty)
            invite_token_hash IS NULL OR length(invite_token_hash) > 0
        );

CREATE UNIQUE INDEX pending_memberships_token_idx
    ON pending_memberships (invite_token_hash)
    WHERE invite_token_hash IS NOT NULL;
