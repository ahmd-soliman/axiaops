# Email-Based Team Invitation Flow

> **HISTORICAL — Kinde-Mgmt-API design.** This was the original design for
> Kinde-mediated invitations. The shipped flow is **native end-to-end**:
> token + hash in `pending_memberships`, redemption URL returned in the
> `POST /v1/invitations` response body for OOB sharing, redeemed at
> `POST /v1/auth/invitations/redeem`. Kinde was removed in MR
> `chore/remove-kinde-auth` (2026-05-06).
>
> **What to read instead:**
> - `services/api/internal/api/invitations.go` — handler for `POST /v1/invitations`,
>   `GET /v1/invitations`, `DELETE /v1/invitations/{id}`
> - `services/api/CLAUDE.md` → Endpoints table — `POST /v1/invitations` and
>   `POST /v1/auth/invitations/preview` / `POST /v1/auth/invitations/redeem`
> - `docs/native-auth-bootstrap.md` — first-run flow; invitations extend the same session model
>
> **What survived intact from this design:** the `pending_memberships` table schema
> (§3 below), the audit action constants, and the `INVITATION_TTL_DAYS` env var.
> The Kinde API call layer (§4–§6, §8.3, §12.2) no longer exists; disregard it.

---

## Shipped `pending_memberships` table

Migration `017_pending_memberships.up.sql` in `services/shared/storage/postgres/migrations/`.

```sql
SET search_path TO axiaops;

CREATE TABLE IF NOT EXISTS pending_memberships (
    id                    TEXT        PRIMARY KEY,
    organization_id       TEXT        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email                 TEXT        NOT NULL,
    role                  TEXT        NOT NULL CHECK (role IN ('admin','member','viewer')),
    invited_by_user_id    TEXT        REFERENCES users(id) ON DELETE SET NULL,
    invited_by_email      TEXT        NOT NULL DEFAULT '',
    status                TEXT        NOT NULL DEFAULT 'pending'
                                      CHECK (status IN ('pending','expired','revoked')),
    token_hash            TEXT        NOT NULL DEFAULT '',  -- argon2id hash of the OOB redemption token
    expires_at            TIMESTAMPTZ NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS pending_memberships_org_email_idx
    ON pending_memberships (organization_id, lower(email))
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS pending_memberships_lookup_idx
    ON pending_memberships (organization_id, lower(email), status);

CREATE INDEX IF NOT EXISTS pending_memberships_expires_idx
    ON pending_memberships (expires_at)
    WHERE status = 'pending';

GRANT SELECT, INSERT, UPDATE, DELETE ON pending_memberships TO axiaops;
ALTER TABLE pending_memberships ENABLE ROW LEVEL SECURITY;
```

Note: the columns `kinde_invitation_id` and `kinde_user_id` (present in the original
design) were dropped in migration `024_drop_kinde_residue` (shipped with
`chore/remove-kinde-auth`). The token is now an argon2id hash in `token_hash`; the
plaintext token is returned once in the `POST /v1/invitations` response and not stored.

## Redemption flow (current)

```
Admin: POST /v1/invitations {email, role}
  → API writes pending_memberships row (token_hash stored, plaintext in response)
  → API returns {redemption_url} — admin shares OOB (Slack / email / password manager)

Invitee: clicks redemption_url → dashboard AcceptInviteScreen
  → POST /v1/auth/invitations/preview {token}  — peek: returns {email, role, existing_user}
  → POST /v1/auth/invitations/redeem {token, password[, name]}
      • new user:      hash + create users row + insert memberships + mint session
      • existing user: verify password + insert memberships + mint session
```

## Audit events

| When | Action constant |
|------|----------------|
| Invitation created | `AuditActionMemberInvited` |
| Invitation redeemed | `AuditActionMemberInvited` (metadata `redeemed: true`) |
| Invitation revoked | `AuditActionMemberRemoved` (metadata `phase: "invitation_revoked"`) |

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `INVITATION_TTL_DAYS` | 14 | How long a `pending_memberships` row stays redeemable |
| `PUBLIC_HOST` | — | Used to build the OOB `redemption_url`. Empty → relative URL |
