-- 031_notification_channels.up.sql
-- Outbound notification channels (email + Slack v1; Teams/Jira pre-provisioned).
-- See docs/notifications-plan.md.
--
-- Two RLS-scoped tables mirroring the `accounts` table shape:
--   * notification_channels   — one row per configured channel (transport +
--     trigger rule + encrypted transport config blob).
--   * notification_dispatches — one row per send attempt, the audit/visibility
--     trail surfaced in the dashboard's "Recent deliveries" drawer.
--
-- Schema notes:
--   * IDs are TEXT (gen_random_uuid()::text), matching every other table in
--     this schema — the codebase has no UUID-typed columns. The plan's DDL
--     showed UUID; adapted to TEXT so the FKs to organizations(id) /
--     accounts(id) / zombie_snapshots(id) (all TEXT) typecheck.
--   * `kind` is a CHECK enum spanning all four planned transports even though
--     only email + slack ship in v1 — widening a CHECK later would force a
--     deploy-time gap, so pre-provisioning lets #113/#114 ship transport-only.
--   * `config_ciphertext` is AES-256-GCM (hex-encoded, nonce-prepended — the
--     crypto.Encrypt output and the accounts.secret_encrypted precedent). TEXT,
--     not BYTEA: the codec returns a hex string.
--   * `external_ticket_id` + its partial unique index ship in v1 (unused by
--     email/slack) so the deferred Jira transport (#113) needs no migration —
--     it's the dedup key for "update the open ticket, don't create a dup".
--   * No denormalised last_dispatched_at/last_error on channels — read the
--     newest notification_dispatches row via the (channel_id, created_at DESC)
--     index instead.
--   * Each new RLS table carries a <table>_runtime_bypass permissive policy for
--     axiaops_runtime — migration 029 created these in a one-time loop, so
--     tables added later must declare their own or TestRuntimeAdmin_
--     PolicyCoversAllRLSTables fails. The org-cascade GDPR purge runs on the
--     bypass pool and needs to reach these rows.

SET search_path TO axiaops;

-- ── notification_channels ─────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS notification_channels (
    id                TEXT        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    organization_id   TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind              TEXT        NOT NULL CHECK (kind IN ('email','slack','teams','jira')),
    label             TEXT        NOT NULL,
    enabled           BOOLEAN     NOT NULL DEFAULT TRUE,
    trigger_rule      JSONB       NOT NULL
                                  DEFAULT '{"min_monthly_savings_usd":25,"digest_top_n":10,"on":["new_zombies"]}'::jsonb,
    config_ciphertext TEXT        NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Dispatcher hot path: ListEnabledChannels filters by (org, enabled); kind is
-- the transport discriminator. Mirrors the index posture described in the plan.
CREATE INDEX IF NOT EXISTS notification_channels_org_kind_enabled_idx
    ON notification_channels (organization_id, kind, enabled);

GRANT SELECT, INSERT, UPDATE, DELETE ON notification_channels TO axiaops;

ALTER TABLE notification_channels ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY notification_channels_organization_isolation ON notification_channels
        USING (organization_id = current_setting('app.organization_id', true))
        WITH CHECK (organization_id = current_setting('app.organization_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY notification_channels_runtime_bypass ON notification_channels
        AS PERMISSIVE FOR ALL TO axiaops_runtime
        USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ── notification_dispatches ───────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS notification_dispatches (
    id                    TEXT        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    organization_id       TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    channel_id            TEXT        NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    snapshot_id           TEXT        REFERENCES zombie_snapshots(id) ON DELETE SET NULL,
    account_id            TEXT        REFERENCES accounts(id) ON DELETE SET NULL,
    -- What triggered this dispatch: a completed scan, or an admin's /test send.
    -- Explicit discriminator (not inferred from snapshot_id being NULL) so the
    -- deliveries drawer can label test rows unambiguously and a future
    -- scheduled-vs-on-demand split widens the CHECK rather than backfilling a
    -- column onto a populated table (a human-gated axiaops_owner migration).
    source                TEXT        NOT NULL DEFAULT 'scan' CHECK (source IN ('scan','test')),
    status                TEXT        NOT NULL CHECK (status IN ('queued','sent','failed','skipped_threshold')),
    zombie_count          INT,
    monthly_savings_cents BIGINT,
    attempts              INT         NOT NULL DEFAULT 0,
    external_ticket_id    TEXT,
    error                 TEXT,
    dispatched_at         TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Newest-dispatch-per-channel lookup (replaces denormalised columns on channels).
CREATE INDEX IF NOT EXISTS notification_dispatches_channel_created_idx
    ON notification_dispatches (channel_id, created_at DESC);

-- One open row per (channel, external resource) — Jira dedup (#113). Partial so
-- the email/slack rows (NULL external_ticket_id) don't collide. Deliberately NOT
-- keyed on organization_id: channel_id already implies the org (channels are
-- org-scoped via FK), so adding organization_id here would be redundant and
-- would silently widen the dedup key. Do not "fix" it by adding the column.
CREATE UNIQUE INDEX IF NOT EXISTS notification_dispatches_channel_external_idx
    ON notification_dispatches (channel_id, external_ticket_id)
    WHERE external_ticket_id IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON notification_dispatches TO axiaops;

ALTER TABLE notification_dispatches ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY notification_dispatches_organization_isolation ON notification_dispatches
        USING (organization_id = current_setting('app.organization_id', true))
        WITH CHECK (organization_id = current_setting('app.organization_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE POLICY notification_dispatches_runtime_bypass ON notification_dispatches
        AS PERMISSIVE FOR ALL TO axiaops_runtime
        USING (true) WITH CHECK (true);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
