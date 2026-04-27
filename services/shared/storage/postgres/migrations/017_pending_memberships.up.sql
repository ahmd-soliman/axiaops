-- 017_pending_memberships.up.sql
-- Email-based team invitations (Phase 2). See docs/invitation-flow.md §3.
--
-- Design notes:
--   * One row per (organization, email) pending invitation. Redeemed rows are
--     deleted (not status-flipped) by the auth-middleware redemption hook so
--     the partial unique index below stays sparse and the hot-path SELECT is
--     index-only.
--   * Status is one of 'pending' | 'expired' | 'revoked'. There is no
--     'redeemed' — redemption deletes the row inside the same txn that
--     inserts the membership.
--   * Role excludes 'owner' deliberately (CHECK constraint). Owners come from
--     EnsureFirstMembership / TransferOwnership, never an invitation.
--   * Partial unique index on (organization_id, lower(email)) WHERE status='pending'
--     enforces idempotent re-invite — a second POST /v1/invitations for the
--     same email upserts the existing pending row instead of failing.
--   * Lookup index on (organization_id, lower(email), status) supports the
--     redemption SELECT (filtered by status='pending' AND expires_at > NOW()).
--   * Expiry index on (expires_at) WHERE status='pending' supports the future
--     ExpirePendingInvitations sweep without a sequential scan.

SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS pending_memberships (
    id                    TEXT        PRIMARY KEY,
    organization_id       TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email                 TEXT        NOT NULL,
    role                  TEXT        NOT NULL CHECK (role IN ('admin','member','viewer')),
    invited_by_user_id    TEXT        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invited_by_email      TEXT        NOT NULL,
    status                TEXT        NOT NULL DEFAULT 'pending'
                                      CHECK (status IN ('pending','expired','revoked')),
    kinde_invitation_id   TEXT        NOT NULL DEFAULT '',
    kinde_user_id         TEXT        NOT NULL DEFAULT '',
    expires_at            TIMESTAMPTZ NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Idempotent re-invite: only one pending row per (org, email).
CREATE UNIQUE INDEX IF NOT EXISTS pending_memberships_one_pending_per_email
    ON pending_memberships (organization_id, lower(email))
    WHERE status = 'pending';

-- Hot path: redemption SELECT in auth middleware.
CREATE INDEX IF NOT EXISTS pending_memberships_lookup_idx
    ON pending_memberships (organization_id, lower(email), status);

-- Background sweeper: ExpirePendingInvitations.
CREATE INDEX IF NOT EXISTS pending_memberships_expiry_idx
    ON pending_memberships (expires_at)
    WHERE status = 'pending';

GRANT SELECT, INSERT, UPDATE, DELETE ON pending_memberships TO axiaops;

ALTER TABLE pending_memberships ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
    CREATE POLICY pending_memberships_organization_isolation ON pending_memberships
        USING (organization_id = current_setting('app.organization_id', true))
        WITH CHECK (organization_id = current_setting('app.organization_id', true));
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
