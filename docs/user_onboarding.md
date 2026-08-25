# User Onboarding (current state)

> **Status:** describes the native-auth flow as shipped (2026-05 onward).
> An earlier Kinde-brokered flow (manual org creation in Kinde's dashboard,
> `org_code` JWT, Kinde Management API invitations) has been fully replaced —
> auth is native (email/password + native SSO) throughout.

## The current flow (self-hosted, native auth)

### First install (bootstrap)

1. Operator starts the API for the first time. The server generates a one-time
   install token and writes it to `/var/run/axiaops/initial_setup_token` (mode 0600).
2. Operator visits `/bootstrap` on the dashboard, supplies the token plus their
   email, name, password, and organization name via `POST /v1/auth/bootstrap`.
3. The API creates the organization, the owner user, and an owner membership row.
   The bootstrap endpoint returns 409 on any subsequent call — it is single-use.
4. Operator logs in at `/login` via `POST /v1/auth/login` → `axiaops_session` cookie set.

See [`docs/native-auth-bootstrap.md`](./native-auth-bootstrap.md) for the full install
walkthrough.

### Inviting teammates

The owner invites teammates by email from Settings → Team:

1. Owner posts `POST /v1/invitations {email, role}`.
2. API writes a `pending_memberships` row (token_hash stored; plaintext in response).
3. Owner copies the `redemption_url` from the response and shares it OOB (Slack, email, etc.).
4. Invitee opens the URL → AcceptInviteScreen:
   - `POST /v1/auth/invitations/preview {token}` — previews email/role.
   - `POST /v1/auth/invitations/redeem {token, password[, name]}` — creates account + membership + mints session.

### SSO-based onboarding

When an org has an active OIDC connection, users can authenticate via SSO:

1. User visits `/login` → provides email → server responds with SSO redirect if domain is verified.
2. Browser redirected to IdP → IdP authenticates → callback at `/v1/sso/oidc/callback`.
3. API JIT-provisions a user row + membership (role=`member` unless a mapping overrides it)
   and mints a native session with `auth_mode='sso'`.

## Roles & permissions

See `docs/rbac-design.md` for the role model — `owner`, `admin`, `member`, `viewer`.
Roles live in the `memberships` table (one row per (user, organization)), not on the
`users` row.

## The DEV_MODE bootstrap

In `DEV_MODE=true`, the auth dance is bypassed. The API:
- Reads `DEV_ORGANIZATION_ID` env var (no default — startup dies if unset).
- Reads `DEV_USER_ID` env var (default `dev-user-axiaops`).
- On startup, calls `EnsureDevOrganization`, `EnsureDevUser`, and
  `EnsureDevMembership(role='owner')` to guarantee a known-id triple exists.
- `DevBypass` middleware injects the dev IDs into every request context — no cookie required.

## The shipped endpoint surface

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/v1/auth/bootstrap` | No | First-owner install |
| `POST` | `/v1/auth/login` | No | Email + password login |
| `POST` | `/v1/auth/logout` | Cookie | Revoke session |
| `POST` | `/v1/auth/invitations/preview` | No | Peek at invite token |
| `POST` | `/v1/auth/invitations/redeem` | No | Accept invite + create account |
| `POST` | `/v1/auth/password-reset/redeem` | No | Set new password from admin-issued token |
| `GET` | `/v1/me` | Yes | Current user, role, permission set |
| `GET` | `/v1/memberships` | Yes | List memberships of the current org |
| `POST` | `/v1/memberships` | Yes | Add an existing user (by `user_id`) with a role |
| `PATCH` | `/v1/memberships/{id}/role` | Yes | Promote / demote |
| `DELETE` | `/v1/memberships/{id}` | Yes | Remove (self-leave bypasses perm) |
| `POST` | `/v1/invitations` | Yes | Invite by email |
| `GET` | `/v1/invitations` | Yes | List pending invitations |
| `DELETE` | `/v1/invitations/{id}` | Yes | Revoke an invitation |
| `POST` | `/v1/organizations/transfer-ownership` | Yes | Owner handover |
| `DELETE` | `/v1/organizations/me` | Yes | GDPR right-to-erasure |
