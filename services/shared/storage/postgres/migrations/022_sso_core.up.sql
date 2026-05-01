-- 022_sso_core.up.sql
-- Phase B2 — Native OIDC RP foundation. See:
--   docs/sso-integration-design.md §4.1 (full schema rationale)
--   docs/sso-implementation-plan.md §5.1 (slice scope + acceptance)
--
-- Adds the per-organization SSO surface:
--   * sso_connections        — one row per (org, IdP); supports OIDC v1, SAML in Phase C
--   * sso_domains            — verified email domains for routing native logins to SSO
--   * sso_group_mappings     — IdP group identifier → AxiaOps role
--   * sso_assertion_replay   — SAML replay-protection cache (created here even though
--                              first used in Phase C; lets the migration not fork by phase)
--   * users.sso_external_id, users.sso_connection_id   — IdP subject identifier
--   * memberships.provisioned_via                      — manual / invitation / jit / scim
--
-- All four SSO tables enforce organisation isolation via RLS, mirroring memberships
-- and pending_memberships. Encrypted secrets (OIDC client secret, future SCIM token)
-- use the same crypto.Encrypt / ENCRYPTION_KEY as accounts.aws_secret_key_ciphertext.
-- Bumped from the design doc's `021_sso_core` to 022 because B1's native-auth took 021.

SET search_path TO axiaops;

-- ── sso_connections ─────────────────────────────────────────────────────────
-- One row per (organization, IdP). Multiple connections per org allowed (e.g.
-- staged Entra rollout while ADFS still serves a subset). Routing decision at
-- login is by email domain via sso_domains.
--
-- secrets stored in oidc_client_secret_ciphertext are AES-256-GCM via
-- crypto.Encrypt with the same ENCRYPTION_KEY as accounts.aws_secret_key_ciphertext.
-- saml_signing_cert and saml_previous_cert are PEM-encoded — public material,
-- not encrypted — these are the IdP's signing certs.
CREATE TABLE IF NOT EXISTS sso_connections (
    id                            TEXT        PRIMARY KEY,
    organization_id               TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    protocol                      TEXT        NOT NULL CHECK (protocol IN ('saml','oidc')),
    label                         TEXT        NOT NULL DEFAULT '',
    status                        TEXT        NOT NULL DEFAULT 'draft'
                                              CHECK (status IN ('draft','active','disabled')),
    enforcement                   TEXT        NOT NULL DEFAULT 'optional'
                                              CHECK (enforcement IN ('optional','preferred','required')),
    default_role                  TEXT        NOT NULL DEFAULT 'viewer'
                                              CHECK (default_role IN ('admin','member','viewer')),

    -- IdP metadata (both protocols)
    idp_issuer                    TEXT        NOT NULL DEFAULT '',
    idp_metadata_url              TEXT        NOT NULL DEFAULT '',
    idp_metadata_xml              TEXT        NOT NULL DEFAULT '',

    -- OIDC fields
    oidc_client_id                TEXT        NOT NULL DEFAULT '',
    oidc_client_secret_ciphertext BYTEA       NOT NULL DEFAULT '\x'::bytea,
    oidc_discovery_url            TEXT        NOT NULL DEFAULT '',
    oidc_tenant_id                TEXT        NOT NULL DEFAULT '',

    -- SAML fields (Phase C)
    saml_sso_url                  TEXT        NOT NULL DEFAULT '',
    saml_signing_cert             TEXT        NOT NULL DEFAULT '',
    saml_previous_cert            TEXT        NOT NULL DEFAULT '',
    saml_previous_cert_expires_at TIMESTAMPTZ,

    -- Pointer back to Kinde when Option A is the runtime. Empty under Option B
    -- (self-hosted v1 — handlers MUST reject non-empty values per design §4.2).
    kinde_connection_id           TEXT        NOT NULL DEFAULT '',

    -- SCIM forward-compat (filled later, never read in v1)
    scim_token_ciphertext         BYTEA       NOT NULL DEFAULT '\x'::bytea,
    scim_endpoint                 TEXT        NOT NULL DEFAULT '',

    created_by_user_id            TEXT        REFERENCES users(id) ON DELETE SET NULL,
    created_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                    TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- An active OIDC connection MUST carry a non-empty client secret. Without
    -- this DB-level constraint, an UPDATE that flips status='draft'→'active'
    -- bypasses the handler-level validation and leaves a half-configured
    -- connection silently active. SAML connections aren't subject to this —
    -- they're keyed by signing certs, not OIDC client secrets.
    CONSTRAINT sso_connections_oidc_active_secret_present CHECK (
        NOT (status = 'active' AND protocol = 'oidc')
        OR octet_length(oidc_client_secret_ciphertext) > 0
    )
);

CREATE INDEX IF NOT EXISTS sso_connections_org_status_idx
    ON sso_connections (organization_id, status);

GRANT SELECT, INSERT, UPDATE, DELETE ON sso_connections TO axiaops;
ALTER TABLE sso_connections ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY sso_connections_org_isolation ON sso_connections
        USING (organization_id = current_setting('app.organization_id', true))
        WITH CHECK (organization_id = current_setting('app.organization_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ── sso_domains ─────────────────────────────────────────────────────────────
-- Verified email domains. Login-page lookup: email → domain → (org, connection).
-- A domain can be claimed by exactly one organization globally (partial unique
-- index on status='verified').
--
-- Public-suffix-list rejection (gmail.com, outlook.com, ...) lives in handler
-- code — the PSL changes; keeping it in code lets us update without a migration.
CREATE TABLE IF NOT EXISTS sso_domains (
    id                  TEXT        PRIMARY KEY,
    organization_id     TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    sso_connection_id   TEXT        NOT NULL REFERENCES sso_connections(id) ON DELETE CASCADE,

    domain              TEXT        NOT NULL,
    status              TEXT        NOT NULL DEFAULT 'pending'
                                    CHECK (status IN ('pending','verified','stale','revoked')),
    -- UNIQUE so a leaked or guessed token can't match an attacker-controlled
    -- domain row instead of the legitimate one. Verification handlers should
    -- additionally scope by id, but the DB-level UNIQUE makes the security
    -- property self-enforcing under any future lookup shape.
    verification_token  TEXT        NOT NULL UNIQUE,

    verified_at         TIMESTAMPTZ,
    last_asserted_at    TIMESTAMPTZ,
    expires_at          TIMESTAMPTZ,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A domain can be verified by exactly one org globally.
CREATE UNIQUE INDEX IF NOT EXISTS sso_domains_one_verified_per_domain
    ON sso_domains (lower(domain)) WHERE status = 'verified';

-- Login-page hot path: email → (lower(domain), status='verified') → sso_connection_id.
-- sso_connection_id is included as a covering column so the index serves the
-- whole lookup without a heap fetch — the per-request frequency makes that
-- worthwhile.
CREATE INDEX IF NOT EXISTS sso_domains_lookup_idx
    ON sso_domains (lower(domain), status, sso_connection_id);

-- Cron sweep: stale domains.
CREATE INDEX IF NOT EXISTS sso_domains_expiry_idx
    ON sso_domains (expires_at) WHERE status = 'verified';

GRANT SELECT, INSERT, UPDATE, DELETE ON sso_domains TO axiaops;
ALTER TABLE sso_domains ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY sso_domains_org_isolation ON sso_domains
        USING (organization_id = current_setting('app.organization_id', true))
        WITH CHECK (organization_id = current_setting('app.organization_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ── sso_group_mappings ──────────────────────────────────────────────────────
-- IdP group identifier → AxiaOps role. group_external_id is whatever the IdP
-- sends — Entra GUID, Okta group name, generic SAML attribute value. We
-- compare with strings; case-sensitivity preserved (Entra is case-sensitive on
-- GUIDs, Okta is case-insensitive on names — admins stage their inputs).
-- Owner is deliberately excluded from the role check (sticky owner property).
CREATE TABLE IF NOT EXISTS sso_group_mappings (
    id                  TEXT        PRIMARY KEY,
    organization_id     TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    sso_connection_id   TEXT        NOT NULL REFERENCES sso_connections(id) ON DELETE CASCADE,

    group_external_id   TEXT        NOT NULL,
    group_display_name  TEXT        NOT NULL DEFAULT '',
    role                TEXT        NOT NULL CHECK (role IN ('admin','member','viewer')),

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (sso_connection_id, group_external_id)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON sso_group_mappings TO axiaops;
ALTER TABLE sso_group_mappings ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY sso_group_mappings_org_isolation ON sso_group_mappings
        USING (organization_id = current_setting('app.organization_id', true))
        WITH CHECK (organization_id = current_setting('app.organization_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ── sso_assertion_replay (Phase C SAML SP only — created empty here) ────────
-- Replay-protection cache for SAML assertion IDs. TTL = NotOnOrAfter + skew.
-- Created in this migration so Phase C doesn't need a forking migration; the
-- table is unused under Phase B2 (OIDC has nonce-based replay protection
-- handled by the OIDC RP, not a DB cache).
--
-- RLS via FK: unlike sessions/password_resets (which have no organization_id
-- handle at all), this table can derive org via sso_connections.organization_id.
-- Enforcing RLS by subquery means a missed predicate in any future Phase C
-- code path can't leak across orgs. SAML SP processing sets app.organization_id
-- from the connection_id BEFORE checking replay, so the policy doesn't block
-- the lookup.
CREATE TABLE IF NOT EXISTS sso_assertion_replay (
    assertion_id        TEXT        PRIMARY KEY,
    sso_connection_id   TEXT        NOT NULL REFERENCES sso_connections(id) ON DELETE CASCADE,
    seen_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sso_assertion_replay_expires_idx
    ON sso_assertion_replay (expires_at);
GRANT SELECT, INSERT, DELETE ON sso_assertion_replay TO axiaops;
ALTER TABLE sso_assertion_replay ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
    CREATE POLICY sso_assertion_replay_org_isolation ON sso_assertion_replay
        USING (sso_connection_id IN (
            SELECT id FROM sso_connections
            WHERE organization_id = current_setting('app.organization_id', true)
        ))
        WITH CHECK (sso_connection_id IN (
            SELECT id FROM sso_connections
            WHERE organization_id = current_setting('app.organization_id', true)
        ));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ── users + memberships additions ──────────────────────────────────────────
-- sso_external_id is the IdP's stable subject identifier (`sub` for OIDC,
-- NameID for SAML). Indexed with sso_connection_id so re-login finds the same
-- user even if email changes (hard requirement for SCIM later).
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS sso_external_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sso_connection_id TEXT REFERENCES sso_connections(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS users_sso_external_idx
    ON users (sso_connection_id, sso_external_id)
    WHERE sso_connection_id IS NOT NULL;

-- provisioned_via lets the UI distinguish "JIT-provisioned, role from group
-- claim" vs "manually invited" — important UX cue for admins reviewing teams.
ALTER TABLE memberships
    ADD COLUMN IF NOT EXISTS provisioned_via TEXT NOT NULL DEFAULT 'manual'
        CHECK (provisioned_via IN ('manual','invitation','jit','scim'));
