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
  → API also emails redemption_url to the invitee (best-effort) via the org's
    first enabled email notification channel; result reported as email_delivery

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

## Invite-email delivery

On `POST /v1/invitations` the API also tries to email the redemption URL to the
invitee, so the admin doesn't have to copy-paste it. This is **best-effort and
never fatal** — the URL is always in the response, so OOB sharing stays the
durable fallback. The response carries an `email_delivery` field:

| `email_delivery` | Meaning |
|---|---|
| `sent` | Mailed (via a per-org channel or the global SMTP config) |
| `failed` | A transport was found but the SMTP send errored (logged, scrubbed) |
| `skipped_no_transport` | No enabled email channel **and** no global SMTP config |
| `skipped_no_public_host` | `PUBLIC_HOST` unset → no absolute link to mail |
| `error` | Transient internal failure resolving the transport (e.g. DB read errored) — distinct from `skipped_no_transport` |

### Transport resolution (channel-first, global fallback)

Delivery is routed through the **`InviteMailer` seam** (`api.InviteMailer`,
wired in `serverbuild.ComposeServer`, swappable by a future SaaS composition
root for a platform mailer like Resend). The default impl
(`api.NewInviteMailer`, `invite_mailer.go`) resolves the SMTP config in order:

1. **Per-org email channel** — the org's first *enabled* email
   `notification_channel` (`notification_channels`, kind=`email`), decrypted via
   `notifications.DecodeEmailConfig`. So an org that runs its own relay sends
   invites from it.
2. **Global SMTP config** — the `SMTP_*` env/SSM settings (see below), used when
   the org has no usable email channel (none configured, or the only one is
   disabled — which is why disabling the digest channel no longer silently kills
   invites).
3. Neither → `skipped_no_transport`.

Only the recipient + message body differ from a scan digest; the SMTP send reuses
the same timeout-bounded, secret-scrubbing path
(`EmailTransport.SendInvite` / `deliver` in `services/shared/notifications/`).
The SMTP send runs on a **detached context** so a client disconnect can't
mislabel a delivered message as failed.

### Global transactional SMTP config (env / SSM)

A system-wide SMTP relay — sourced from env vars, injected from **SSM in prod**
exactly like `DATABASE_URL` — backs the fallback. The intended target is a
**Gmail SMTP relay** (`smtp-relay.gmail.com:587`, STARTTLS + PLAIN auth), but any
SES/Postfix relay works.

| Variable | Default | Notes |
|---|---|---|
| `SMTP_HOST` | — | Relay host. **Empty ⇒ no global mailer** (invites then depend on a per-org channel) |
| `SMTP_PORT` | `587` | STARTTLS submission port |
| `SMTP_USER` | — | Relay auth user (Gmail relay: a real mailbox with 2SV + App Password) |
| `SMTP_PASS` | — | Relay auth password / App Password |
| `SMTP_FROM` | — | Envelope sender + `From:` address (required when `SMTP_HOST` is set). Use a **generic transactional address** (`noreply@<domain>`), not `invitation@…` — the same global mailer also sends password resets (#126); per-message context lives in the subject/body/display-name. **Gmail-relay constraint:** `smtp-relay.gmail.com` accepts any user/alias in the Workspace domain, so `noreply@` need only exist as an **alias**; `smtp.gmail.com` (App-Password auth) forces `From` to the authenticated mailbox **unless** `noreply@` is a verified "Send mail as" alias — otherwise Gmail silently rewrites it to `SMTP_USER`. |
| `SMTP_FROM_NAME` | `AxiaOps` | `From:` display name. Recipients see e.g. `AxiaOps <noreply@axiaops.io>`. |

### Observability

Each attempt increments `axiaops_auth_invite_email_total{outcome, source}`
(`source` = `channel` \| `global` \| `none`) and is recorded in the invitation's
audit-log metadata (`email_delivery`). It is **not** written to
`notification_dispatches` — that table is scan-digest-shaped. (A durable
per-invite delivery log is a follow-up, tracked alongside the SaaS platform
mailer.)

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `INVITATION_TTL_DAYS` | 14 | How long a `pending_memberships` row stays redeemable |
| `PUBLIC_HOST` | — | Used to build the OOB `redemption_url`. Empty → relative URL, and invite-email delivery is skipped (`skipped_no_public_host`) |
