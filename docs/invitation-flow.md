# Email-Based Team Invitation Flow

Status: design — not yet implemented.
Owner: API service. Touches `services/shared` (storage, model), `services/api` (handler, middleware, new `kinde` package), and `services/dashboard` (Members screen).

This document is the implementation contract for `POST /v1/invitations` and the Kinde-mediated redemption hook. It assumes the prior decisions are settled (auto-create Kinde org on signup is on, Kinde-org per AxiaOps-org stays 1:1, single-org-per-user, Kinde free tier — Management API only).

---

## 1. Overview

The current `POST /v1/memberships` endpoint requires the invitee's `users.id` to already exist (`services/api/CLAUDE.md:33`, `services/api/internal/api/handler.go:110`). That works for promoting an existing teammate but not for inviting someone who has never logged in — there is no `users` row to reference. This document specifies the email-based path: an admin posts an email + role; AxiaOps records a `pending_memberships` row and asks Kinde to send the invitation; the invitee signs up landing in the right Kinde org; on their first authenticated request the auth middleware redeems the pending row into a real `memberships` row.

```
Owner / Admin             AxiaOps API              Kinde Mgmt API           Invitee inbox             Invitee browser            AxiaOps API
     │                         │                         │                         │                         │                         │
     │  POST /v1/invitations   │                         │                         │                         │                         │
     │  {email, role}          │                         │                         │                         │                         │
     ├────────────────────────►│                         │                         │                         │                         │
     │                         │  authz: members:invite  │                         │                         │                         │
     │                         │  (or manage_admin)      │                         │                         │                         │
     │                         │  insert pending row     │                         │                         │                         │
     │                         │  (txn open)             │                         │                         │                         │
     │                         │  POST .../organization/ │                         │                         │                         │
     │                         │     users (add user by  │                         │                         │                         │
     │                         │     email + send invite)│                         │                         │                         │
     │                         ├────────────────────────►│                         │                         │                         │
     │                         │  202 + invitation_id    │                         │                         │                         │
     │                         │◄────────────────────────┤                         │                         │                         │
     │                         │  commit txn             │  invitation email       │                         │                         │
     │                         │  audit: member_invited  ├────────────────────────►│                         │                         │
     │   201 {id, status:      │                         │                         │                         │                         │
     │   pending, expires_at}  │                         │                         │                         │                         │
     │◄────────────────────────┤                         │                         │                         │                         │
     │                         │                         │                         │  click "Accept"         │                         │
     │                         │                         │                         ├────────────────────────►│                         │
     │                         │                         │                         │  Kinde signup → JWT     │                         │
     │                         │                         │                         │  carries org_code = X   │                         │
     │                         │                         │                         │                         │  GET /v1/me + Bearer    │
     │                         │                         │                         │                         ├────────────────────────►│
     │                         │  Auth.Wrap:             │                         │                         │                         │
     │                         │   UpsertOrganization    │                         │                         │                         │
     │                         │   UpsertUser            │                         │                         │                         │
     │                         │   EnsureFirstMembership │                         │                         │                         │
     │                         │   RedeemPendingInvitation                         │                         │                         │
     │                         │   (txn: insert membership                         │                         │                         │
     │                         │    + delete pending row)                          │                         │                         │
     │                         │                         │                         │                         │  200 {role: member}     │
     │                         │                         │                         │                         │◄────────────────────────┤
```

---

## 2. End-to-End Flow

| Step | Where | What happens |
|---|---|---|
| 1 | Dashboard | Admin enters email + role on the Members screen, submits `POST /v1/invitations`. |
| 2 | API handler | Authz check: `members:invite` for member/viewer targets, `members:manage_admin` for admin targets (mirrors `POST /v1/memberships` — `services/api/internal/api/handler.go:110`, RBAC §7). |
| 3 | API handler | Validates email + role. Looks up `GetUserByEmail(ctx, email)` (`services/shared/storage/storage.go:264`). If a user with that email already has a membership in this organization → return 409. If they exist but have no membership → return 409 with hint to use `POST /v1/memberships` instead (cheap UX nudge; we have their `user_id`). |
| 4 | API handler | Opens a transaction, inserts a `pending_memberships` row with `status='pending'`, `expires_at = NOW() + INTERVAL '14 days'`. |
| 5 | API handler | Calls `kinde.InviteUserToOrganization(ctx, kindeOrgCode, email, name)` → `POST {KINDE_MGMT_API_URL}/api/v1/organizations/{org_code}/users` with body `{users: [{email, full_name, send_invite: true, roles: []}]}`. Kinde free tier supports this (Management API only — no Workflows / BYO code). |
| 6 | API handler | On Kinde 200/202 response, stores the `kinde_invitation_id` returned (or the `kinde_user_id` if Kinde returns one synchronously) on the `pending_memberships` row, commits the transaction. On Kinde failure (4xx/5xx after 1 retry) → rollback, return 502. |
| 7 | API handler | Writes `member_invited` audit event via `axiaops.io/api/internal/audit` helper (action constant already defined — `services/shared/model/audit.go:42`). |
| 8 | Email | Kinde sends the branded invitation email to the invitee with a link to the AxiaOps-Kinde org. |
| 9 | Invitee | Clicks link, completes Kinde signup (or login if they already have a Kinde account), lands in the AxiaOps dashboard. Their JWT carries `org_code` matching the inviting organization. |
| 10 | Auth middleware | On the invitee's first authenticated request: `UpsertOrganization` → no-op (org already exists), `UpsertUser` → inserts the new user, `EnsureFirstMembership` → no-op (organization already has memberships, partial unique index on `role='owner'` from migration 015 prevents a second owner), then **new step:** `RedeemPendingInvitation(ctx, organizationID, userID, email)` → finds the pending row by `(organization_id, lower(email))`, inserts a `memberships` row with the recorded role, deletes the pending row, all in one transaction. |
| 11 | Audit | Redemption writes a second `member_invited`-class event (`metadata.redeemed=true`) so the timeline shows both invite and acceptance. |
| 12 | Dashboard | Invitee sees the dashboard with their role applied; admin sees the row move from "Pending" to "Active" on the Members screen on next refresh. |

Kinde Management API endpoints used:

| Operation | Endpoint | Notes |
|---|---|---|
| OAuth2 token (M2M) | `POST {KINDE_ISSUER}/oauth2/token` body `grant_type=client_credentials&audience={KINDE_MGMT_API_URL}/api` | Token cached in-process, refreshed at 80% of TTL. |
| Add user to organization (sends invite email) | `POST {KINDE_MGMT_API_URL}/api/v1/organizations/{org_code}/users` | Body: `{users:[{email, full_name, send_invite:true, roles:[]}]}`. |
| Remove user from organization (revoke invite) | `DELETE {KINDE_MGMT_API_URL}/api/v1/organizations/{org_code}/users/{user_id}` | Used by `DELETE /v1/invitations/{id}` after revocation. |
| Lookup invitation status (optional) | `GET {KINDE_MGMT_API_URL}/api/v1/organizations/{org_code}/users` | We avoid polling Kinde — redemption is JWT-driven. |

---

## 3. Data Model

New table — migration `017_pending_memberships.up.sql` / `.down.sql` under `services/shared/storage/postgres/migrations/`.

```sql
-- 017_pending_memberships.up.sql
-- Email-based invitations. A row exists between "admin clicks invite" and
-- "invitee logs in for the first time and is redeemed into memberships".
--
-- Lookup is by (organization_id, lower(email)). We do not have a users.id at
-- creation time, so the linkage is purely by email. The auth middleware
-- redeems by matching the JWT email against pending rows for the user's
-- organization (see services/api/internal/middleware/auth.go after EnsureFirstMembership).
--
-- Status enum:
--   'pending'  — Kinde invite sent, awaiting first login
--   'expired'  — past expires_at, swept by background job (deferred)
--   'revoked'  — admin called DELETE /v1/invitations/{id}
--
-- Redemption deletes the row in the same transaction that inserts the
-- membership; we do not keep a 'accepted' state — the audit_log carries that
-- history. Status only exists for the listing UI to show in-flight invites.

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
    kinde_invitation_id   TEXT        NOT NULL DEFAULT '',
    kinde_user_id         TEXT        NOT NULL DEFAULT '',
    expires_at            TIMESTAMPTZ NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One pending invite per (org, email). Re-inviting upserts (see §4 POST behaviour).
CREATE UNIQUE INDEX IF NOT EXISTS pending_memberships_org_email_idx
    ON pending_memberships (organization_id, lower(email))
    WHERE status = 'pending';

-- Redemption hot path: middleware looks up by (organization_id, lower(email)).
CREATE INDEX IF NOT EXISTS pending_memberships_lookup_idx
    ON pending_memberships (organization_id, lower(email), status);

-- Background sweep (deferred) will scan by expires_at across all orgs.
CREATE INDEX IF NOT EXISTS pending_memberships_expires_idx
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
```

Down migration drops the table and policy.

**Why no `accepted` status:** redemption deletes the row inside the membership-insert transaction. The audit_log carries the "accepted at" record. Keeping an `accepted` state in this table would force a second sweep job to clean up.

**Why `role` excludes `owner`:** ownership is transferred via the existing `POST /v1/organizations/transfer-ownership` flow — never via invitation. Constraint enforces this at the DB level.

**Email storage:** stored verbatim, indexed by `lower(email)`. The unique index uses `lower()` so duplicate-invite detection is case-insensitive without losing the original casing.

---

## 4. API Surface

### `POST /v1/invitations`

Permission: `members:invite` (for `member`/`viewer` targets) or `members:manage_admin` (for `admin` targets). Same two-perm split as `POST /v1/memberships` — handler does the second check after parsing the body.

**Request**

```json
{
  "email": "newhire@example.com",
  "role": "member",
  "name": "New Hire"
}
```

`name` is optional; passed through to Kinde as `full_name` so the invitation email reads naturally.

**Response — 201 Created**

```json
{
  "id": "01HXY...",
  "organization_id": "...",
  "email": "newhire@example.com",
  "role": "member",
  "status": "pending",
  "invited_by": {
    "user_id": "...",
    "email": "admin@example.com"
  },
  "expires_at": "2026-05-10T12:00:00Z",
  "created_at": "2026-04-26T12:00:00Z"
}
```

**Status codes**

| Code | When |
|---|---|
| 201 | Invitation created, Kinde accepted the request. |
| 400 | Invalid email or role. |
| 401 | Missing/invalid JWT. |
| 403 | Caller lacks `members:invite` (or `members:manage_admin` when role=admin). |
| 409 | Email matches an existing user who is already a member of this organization. Body: `{"error": "already_a_member", "user_id": "..."}`. |
| 409 | Email matches an existing user without membership — caller should use `POST /v1/memberships` with the returned `user_id`. Body: `{"error": "user_exists_use_memberships", "user_id": "..."}`. |
| 502 | Kinde Management API call failed after retry. The pending row is rolled back. |

**Idempotent re-invite (within `pending_memberships_org_email_idx`):** if an active pending row exists for `(organization_id, lower(email))`, the handler updates `expires_at`, `role`, `kinde_invitation_id` and re-issues the Kinde invite (Kinde's own behaviour is to send a new email each time the user is re-added). Response is 200 with the updated row, not 201, to signal to clients that nothing was newly created.

### `GET /v1/invitations`

Permission: `members:read`.

Query params: `?status=pending|expired|revoked` (optional; default returns `pending` only — that's the only state the Members screen needs to render).

Response — 200 OK

```json
[
  {
    "id": "...",
    "email": "...",
    "role": "member",
    "status": "pending",
    "invited_by": {"user_id":"...","email":"..."},
    "expires_at": "...",
    "created_at": "..."
  }
]
```

Always returns `[]` (never `null`) on empty — same convention as `listZombies` (`services/api/internal/api/handler.go:184`).

### `DELETE /v1/invitations/{id}`

Permission: `members:invite` (the inverse of creating one). For invitations that targeted role=admin, also requires `members:manage_admin`.

**Effect:**

1. Load the row inside the org-scoped transaction.
2. If `status != 'pending'` → 410 Gone (already consumed/expired/revoked — idempotent revoke).
3. Call `kinde.RemoveUserFromOrganization(ctx, kindeOrgCode, kindeUserID)`. If Kinde returns 404 (user already removed/never created), proceed. If Kinde returns 5xx after retry → return 502, do not flip the local status.
4. Update local row: `status='revoked'`, `updated_at=NOW()`.
5. Audit: `member_removed` with `metadata={"phase": "invitation_revoked"}`.

**Status codes:** 204 on success, 404 if the invitation doesn't exist for this org, 410 if already consumed, 502 on Kinde failure.

### Status code summary across endpoints

| Edge case | Code |
|---|---|
| Email already a member of this org | 409 `already_a_member` |
| Email is a known user but not a member | 409 `user_exists_use_memberships` |
| Email already has a pending invite in this org | 200 (re-invite, treated as upsert) |
| Pending invite expired | redeemed only if `NOW() < expires_at`; otherwise middleware leaves the row (sweeper marks it `expired`) and the user lands without a membership — `Require` 403s them on protected routes |
| Revoke an already-redeemed/expired invitation | 410 |
| Kinde Management API down | 502 with retry-after |

---

## 5. Backend Implementation

### Migration

`services/shared/storage/postgres/migrations/017_pending_memberships.up.sql` (and `.down.sql`) — content as in §3. Latest migration is 016 (`016_rename_tenant_to_organization.up.sql`); 017 is the next slot.

### Domain model

New file `services/shared/model/invitation.go`:

```go
type PendingInvitation struct {
    ID                  string
    OrganizationID      string
    Email               string
    Role                string
    InvitedByUserID     string
    InvitedByEmail      string
    Status              string // "pending" | "expired" | "revoked"
    KindeInvitationID   string
    KindeUserID         string
    ExpiresAt           time.Time
    CreatedAt           time.Time
    UpdatedAt           time.Time
}

const (
    InvitationStatusPending = "pending"
    InvitationStatusExpired = "expired"
    InvitationStatusRevoked = "revoked"
)
```

### Store interface

Append to `services/shared/storage/storage.go` after the membership block:

```go
// ── Pending invitations (email-based invite flow) ───────────────────────────

CreatePendingInvitation(ctx context.Context, inv model.PendingInvitation) (model.PendingInvitation, bool, error)
ListPendingInvitations(ctx context.Context, status string) ([]model.PendingInvitation, error)
GetPendingInvitation(ctx context.Context, id string) (model.PendingInvitation, error)
RevokePendingInvitation(ctx context.Context, id string) error
RedeemPendingInvitation(ctx context.Context, organizationID, userID, email string) (bool, error)
ExpirePendingInvitations(ctx context.Context) (int64, error)
```

New sentinel errors at the top of `storage.go`:

```go
var ErrInvitationNotFound = errors.New("storage: invitation not found")
var ErrInvitationNotPending = errors.New("storage: invitation is not in pending state")
var ErrInvitationAlreadyMember = errors.New("storage: email already has a membership in this organization")
var ErrUserExistsNoMembership = errors.New("storage: email matches an existing user without membership — use POST /v1/memberships")
```

### Postgres implementation

In `services/shared/storage/postgres/postgres.go`. The non-trivial ones:

- **`CreatePendingInvitation`** — open txn, `SET LOCAL app.organization_id`, run a `JOIN` on `users + memberships` to pre-check the membership/user-exists cases (raise the sentinel errors before hitting the unique index), then `INSERT ... ON CONFLICT (organization_id, lower(email)) WHERE status='pending' DO UPDATE ... RETURNING ...`. The bool return distinguishes insert (true) from update (false).
- **`RedeemPendingInvitation`** — single transaction: `SET LOCAL app.organization_id` → `SELECT ... FOR UPDATE` filtered by `status='pending' AND expires_at > NOW()` → INSERT membership + DELETE pending row → commit. Index-only on the partial unique index, sub-millisecond hot path.

### Auth middleware hook

In `services/api/internal/middleware/auth.go`, insert a new step **after** `EnsureFirstMembership` (line 187) and **before** the `ctx = context.WithValue(ctx, organizationIDKey, organization.ID)` assignment (line 193):

```go
// Best-effort invitation redemption. If the user was invited by email,
// EnsureFirstMembership above will be a no-op (the org already has owners),
// and this step converts any matching pending row into a real membership.
if email != "" {
    redeemCtx := storage.WithOrganizationID(ctx, organization.ID)
    redeemed, err := a.store.RedeemPendingInvitation(redeemCtx, organization.ID, user.ID, email)
    if err != nil {
        slog.Error("auth: RedeemPendingInvitation failed", "error", err, "user_id", user.ID, "organization_id", organization.ID)
    } else if redeemed {
        slog.Info("auth: invitation redeemed", "user_id", user.ID, "organization_id", organization.ID, "email", email)
    }
}
```

The audit-from-middleware awkwardness is real — the audit helper today takes an `*http.Request`. The cleanest fix is `audit.WriteFromContext(ctx, store, event)` (small refactor, §11).

### API handler

New file `services/api/internal/api/invitations.go`:

```go
type invitationsHandler struct {
    store storage.Store
    kinde kinde.Client
}

func (h *invitationsHandler) Register(mux *http.ServeMux) {
    mux.Handle("POST   /v1/invitations",      middleware.Require(authz.PermMembersInvite, h.store, http.HandlerFunc(h.create)))
    mux.Handle("GET    /v1/invitations",      middleware.Require(authz.PermMembersRead,    h.store, http.HandlerFunc(h.list)))
    mux.Handle("DELETE /v1/invitations/{id}", middleware.Require(authz.PermMembersInvite, h.store, http.HandlerFunc(h.revoke)))
}
```

Two-phase commit pattern in `create`: insert pending row → call Kinde Mgmt API → on Kinde failure, compensating `RevokePendingInvitation` + return 502. We insert first (not Kinde-first) so the email never lands before the local row exists.

### Kinde Management API client

New module path: `services/api/internal/kinde/`:

- `client.go` — public interface, struct, constructor.
- `token.go` — M2M client_credentials flow with sync.Mutex-protected token cache (refresh at 80% of `expires_in`).
- `invitations.go` — `InviteUser`, `RemoveUser`.
- `client_test.go` — uses `httptest.NewServer` to mock Kinde.
- `stub.go` — in-memory stub for `DEV_MODE=true`.

```go
type Client interface {
    InviteUser(ctx context.Context, orgCode, email, fullName string) (kindeInvitationID string, kindeUserID string, err error)
    RemoveUser(ctx context.Context, orgCode, kindeUserID string) error
}
```

---

## 6. Configuration

| Variable | Required | Default | Notes |
|---|---|---|---|
| `KINDE_M2M_CLIENT_ID` | Prod | — | Client ID of the Kinde M2M application granted Mgmt API scopes (`read:users`, `create:users`, `update:user_properties`, `delete:users`, `read:organizations`, `update:organization_users`, `delete:organization_users`). |
| `KINDE_M2M_CLIENT_SECRET` | Prod | — | Secret for the M2M app. Loaded via env, never logged. |
| `KINDE_MGMT_API_URL` | Prod | derived from `KINDE_ISSUER` | Defaults to `KINDE_ISSUER`. Override only for non-standard tenants. |
| `INVITATION_TTL_DAYS` | No | `14` | Pending invitation expiry. |

```go
// services/api/cmd/main.go
var kindeClient kinde.Client
if os.Getenv("DEV_MODE") == "true" {
    kindeClient = kinde.NewStub()
} else {
    kindeClient = kinde.New(
        mustEnv("KINDE_ISSUER"),
        envOr("KINDE_MGMT_API_URL", os.Getenv("KINDE_ISSUER")),
        mustEnv("KINDE_M2M_CLIENT_ID"),
        mustEnv("KINDE_M2M_CLIENT_SECRET"),
    )
}
invH := api.NewInvitationsHandler(store, kindeClient)
invH.Register(mux)
```

In dev mode, redemption still works: invite a fake email, flip `DEV_USER_EMAIL` to that email, restart, middleware redeems on first auth.

---

## 7. Dashboard Changes

A new Members screen with:

1. **Active members** — existing `GET /v1/memberships` data, role dropdown for promote/demote, remove button.
2. **Pending invitations** — `GET /v1/invitations?status=pending`, with revoke button. Empty-state copy: "Invite a teammate by email — they'll get a link to join your organization."
3. **Invite member form** — email + role dropdown + optional name → `POST /v1/invitations`. Show 409 errors inline. On 502, show a retry button.

Defer to the `dashboard-screen` skill for the actual UI implementation — the data shape and error states are fully specified by §4.

---

## 8. Edge Cases & Decisions

### 8.1 Email mismatch

**Risk:** admin invites `alice@example.com`, but Alice has a Kinde account under `alice@personal.com` and clicks "Use existing account."

**Mitigation:**
1. Kinde's "Add user to organization" with `send_invite: true` emails a link tied to the specific email; Kinde forces confirmation if the user already has an account under a different email.
2. Belt-and-braces: redemption matches `lower(email)` only. Mismatch → no membership → user lands with no role and sees a "no access — ask your admin" empty state.
3. Admin re-invites under the correct email; original pending row stays until expiry.

### 8.2 Resending invitations

`POST /v1/invitations` for an `(org, lower(email))` with an active pending row → upsert: refresh `expires_at`, update `role` if changed, re-issue Kinde invite (Kinde sends a new email). Response 200 with `{"resent": true}`, not 201.

### 8.3 Revocation

`DELETE /v1/invitations/{id}` calls Kinde's `DELETE .../organizations/{org_code}/users/{kinde_user_id}` first (so the link in the email becomes useless), then flips local status. Kinde 404 = idempotent, proceed. Kinde 5xx after retry → 502, don't lie about the state.

### 8.4 Expiry

`expires_at = NOW() + INTERVAL '14 days'`. Two enforcement points:
- **Read-side:** `RedeemPendingInvitation` filters `WHERE expires_at > NOW()`.
- **Write-side cleanup:** `ExpirePendingInvitations` flips status to `expired`. **Documented gap:** no background sweeper in this iteration. Reuse the existing 5-minute stuck-scan ticker in a follow-up.

### 8.5 Inviting an existing member

→ **409 `already_a_member`**, not silent no-op. Carries the existing `user_id`.

### 8.6 Inviting a known user without membership

→ **409 `user_exists_use_memberships`** with the `user_id`. Dashboard converts into a one-click "use existing user" action calling `POST /v1/memberships`.

### 8.7 Role hierarchy

| Caller role | Can invite | Can revoke |
|---|---|---|
| `viewer` | — | — |
| `member` | — | — |
| `admin` | member, viewer | member, viewer invitations |
| `owner` | admin, member, viewer | any invitation |

Mirrors `POST /v1/memberships` (see `docs/rbac-design.md`).

### 8.8 Audit logging

| When | Action | Resource | Metadata |
|---|---|---|---|
| Invitation created | `member_invited` | `invitation`, `invitation.id` | `{email, role, kinde_invitation_id}` |
| Invitation re-sent | `member_invited` | same | `{email, role, resent: true}` |
| Invitation redeemed | `member_invited` | `membership`, `new_membership.id` | `{redeemed: true, invitation_id}` |
| Invitation revoked | `member_removed` | `invitation`, `invitation.id` | `{phase: "invitation_revoked"}` |
| Invitation expired | not audited (system action) | — | — |

`AuditActionMemberInvited` already exists (`services/shared/model/audit.go:42`). No new constants needed.

---

## 9. Testing Plan

### Handler unit tests (`services/api/internal/api/invitations_test.go`)

- 201 happy path (member, viewer roles).
- 403 when caller is `member` (lacks `members:invite`).
- 403 when caller is `admin` and `role=admin` in body (lacks `members:manage_admin`).
- 409 `already_a_member` / 409 `user_exists_use_memberships` from store sentinels.
- 502 on Kinde failure → verifies compensating `RevokePendingInvitation` call.
- 200 (not 201) on re-invite (existing pending row).
- `GET` empty list returns `[]` not `null`.
- `DELETE` calls Kinde's `RemoveUser` then flips local status; 410 on already-revoked.

### Store integration tests (`services/shared/storage/postgres/invitations_test.go`)

Run under `make test-storage`:

- `CreatePendingInvitation` happy path + upsert.
- Sentinel errors (`ErrInvitationAlreadyMember`, `ErrUserExistsNoMembership`).
- `RedeemPendingInvitation` insert + delete in one txn; returns `(false, nil)` for missing/expired/revoked rows.
- `RevokePendingInvitation` flip + idempotency.
- `ListPendingInvitations` filter behaviour.
- `ExpirePendingInvitations` ripeness + idempotency.
- RLS isolation: row in org A invisible from org B.

### Middleware end-to-end test

In `services/api/internal/middleware/auth_test.go`:

1. Seed org with existing owner.
2. Insert `pending_memberships` row for `email=invitee@example.com, role=member`.
3. Build JWT with `email=invitee@example.com`, signs with test RSA key, send through `Auth.Wrap`.
4. Assert: request succeeds, `memberships` row exists for new user, `pending_memberships` row deleted, `RoleOf` returns `member`.

This is the load-bearing test — it proves the middleware hook closes the loop.

### Smoke test

In `make test-smoke`, add a flow that calls `POST /v1/invitations` against the running stack with `kinde.NewStub`, then directly inserts a fake `users` row and asserts `GET /v1/me` returns role=member.

---

## 10. Rollout & Migration

**Keep both endpoints.** Different primitives:

- `POST /v1/memberships` — promote an **existing** user. Useful for the rare future case (post v1) of one Kinde user in multiple AxiaOps orgs.
- `POST /v1/invitations` — invite by email. The 99% "front door" for adding teammates.

The dashboard defaults the "Invite member" CTA to the email flow. The "promote existing user" flow is reachable from the 409 `user_exists_use_memberships` response.

**Migration strategy:** none. New endpoint is additive; existing memberships data is untouched.

**Rollback plan:** if the Kinde Mgmt API integration has problems, revert the dashboard CTA to a "paste user_id" form and ship a fix forward. The down migration drops the table cleanly; no data loss.

---

## 11. Effort Estimate

| Phase | Component | Effort |
|---|---|---|
| 1 | Migration `017_pending_memberships` | 0.25 d |
| 2 | `model.PendingInvitation` + sentinels | 0.25 d |
| 3 | Store interface methods | 0.25 d |
| 4 | Postgres implementations (especially `RedeemPendingInvitation`) | 1 d |
| 5 | Store integration tests | 0.5 d |
| 6 | `kinde` package (Client, M2M token, Invite/Remove, stub) | 1 d |
| 7 | `kinde` package tests | 0.25 d |
| 8 | Invitations handler + audit wiring | 1 d |
| 9 | Handler unit tests | 0.5 d |
| 10 | Auth middleware redemption hook | 0.5 d |
| 11 | Middleware end-to-end test | 0.25 d |
| 12 | Wire-up in `cmd/main.go` + env doc updates | 0.25 d |
| 13 | Dashboard Members screen | 1.5 d |
| 14 | Smoke test path | 0.25 d |
| **Total** | | **~7.75 days** |

**Out of scope / explicit follow-ups:**

- Background sweeper for `ExpirePendingInvitations` (~0.25 d).
- `audit.WriteFromContext` overload to let middleware emit audit events without `*http.Request` (~0.25 d).
- Multi-org-per-user — out of scope per prior decision.
- Kinde org membership sync (user removed from Kinde org out of band) — defer; needs Kinde webhooks (paid tier).

---

## 12. Appendix — Org Naming and Email Delivery

Two adjacent topics surface naturally during this design but are intentionally **out of scope of the invitations endpoint itself**. Captured here so the assumptions are explicit.

### 12.1 Org name (self-serve signup gap)

The invitation flow does **not** touch org names — invitees join an existing organization that already has a name. The naming gap is on the *self-serve signup* path:

| Signup path | Who sets `organizations.name`? |
|---|---|
| Invited (this doc) | Already set by the inviting org's creator. Nothing to do. ✅ |
| Self-serve | Kinde defaults to a generic placeholder (e.g. `Org-<random>` or the user's own name), surfaced via the JWT `org_name` claim and persisted via `UpsertOrganization(orgCode, orgName)` (`auth.go:166`). |

The self-serve path leaves the new owner staring at a meaningless org name with no in-app way to change it. Two options to close that gap (independent of this doc; track as a separate ticket):

1. **Post-signup rename in AxiaOps.** First-login onboarding step on the dashboard: detect a Kinde-default org name, prompt "What's your company called?", `PATCH /v1/organizations/me {name}` → also call Kinde Mgmt API to rename the upstream Kinde org so the two stay in sync. ~0.5 d. Recommended.
2. **Custom field on the Kinde signup form.** Kinde lets you add a custom "Organization name" field; the value becomes the new org's name on creation. Free-tier availability needs verification; couples our UX to Kinde's hosted form.

**Decision:** option 1 when prioritised. Document the gap; do not block invitation flow on it.

### 12.2 Email delivery (no AxiaOps SMTP)

**AxiaOps does not run SMTP for the invitation flow.** Kinde's Management API ships the email when we call `POST .../organizations/{org_code}/users` with `send_invite: true` — Kinde handles the SMTP, deliverability, bounce handling, and link routing. We never see the message body or hold credentials.

Capabilities by Kinde tier (verify before relying on the customisation column):

| Tier | Sender address | Template control |
|---|---|---|
| Free | `noreply@kinde.com` | Limited variables: logo, brand color, org name |
| Pro+ | Custom domain (e.g. `invitations@axiaops.io`, requires DKIM/SPF) | Full template editor |

When we **would** need our own SMTP (none apply to v1):

- AxiaOps-originated emails outside Kinde's scope — "your zombie scan finished," "your AWS account disconnected." Use SES or Postmark when needed (~$0–10/mo).
- Custom multi-step onboarding sequences Kinde can't model.
- Full template control before paying for Kinde Pro.

**Decision:** zero SMTP work in v1. Step 8 of the end-to-end flow (§2) reflects this — Kinde sends the email, AxiaOps does not.

### 12.3 Implications for this doc

Neither topic changes the API surface, data model, or middleware hook in this design. They are noted here so future readers don't:

- Wire an SMTP client into the invitations handler.
- Assume invitees can rename the org during the accept flow.
- Block invitations on a "rename org" feature.
