# User onboarding (current state)

> **Status:** describes the implementation as of Phase 2 plus the email-invitation flow designed in [`docs/invitation-flow.md`](./invitation-flow.md) (pending implementation on `feat/team-invitations`) and the post-signup wizard designed in [`docs/onboarding-wizard.md`](./onboarding-wizard.md). The previously-planned "app-owned organisations" rewrite (`docs/onboarding-and-app-owned-orgs.md`, now marked superseded) was evaluated and **not pursued** — Kinde's `register()` plus its Management API solves both self-serve org creation and email invitations without an app-side org-primitive refactor.

This doc reflects what actually ships today, plus the invitation flow that is being added on top without changing the org primitive.

## The current flow (closed beta, pattern A)

AxiaOps couples organisation identity to Kinde. New customers come
on board through a manual handshake:

1. **AxiaOps admin creates the org in Kinde.** This is the manual
   step that gates onboarding today. Done in the Kinde dashboard,
   not via AxiaOps code. Sets the `org_code` slug and adds the
   inviting customer's email to that org.
2. **Customer signs in via Kinde** using the email Kinde sent them.
   Kinde returns a JWT carrying `org_code`, `sub`, `email`, `name`.
3. **AxiaOps auth middleware** (`services/api/internal/middleware/auth.go`)
   validates the JWT, then on every request:
   - `UpsertOrganization(orgCode, orgName)` — mirrors the Kinde org
     into `organizations` if missing, returns the internal UUID.
   - `UpsertUser(orgID, sub, email, name)` — mirrors the Kinde user
     into `users` if missing.
   - `EnsureFirstMembership(orgID, userID)` — inserts a membership
     row with `role='owner'` **only when no membership yet exists for
     that org**. The first authenticator into a brand-new Kinde org
     becomes the owner. Subsequent users get no membership and
     bounce off `403`.
4. **Subsequent team members** sign in via Kinde, hit a `403`,
   then the existing owner calls `POST /v1/memberships
   { user_id, role }` to create their membership row. Once
   created, the team member's next request succeeds.

## Why the manual Kinde step

It's a feature of the closed-beta GTM, not a bug. Each new
customer goes through a vetting checkpoint before getting access:
DPA signed, AWS IAM walked through, expectations set. See
`docs/gtm_assessment.md` for the GTM context.

## Why the chicken-and-egg invitation flow

Today the owner cannot invite by email — they have to invite by
`user_id`, which means the invitee must first sign in (creating
their `users` row) and bounce off `403`. This is a known limitation
called out in `Tasks.md` 3.9 and addressed by the plan doc.

## Roles & permissions

See `docs/rbac-design.md` for the role model — `owner`, `admin`,
`member`, `viewer`. Roles live in the `memberships` table (one row
per (user, organization)), not on the `users` row. AxiaOps
deliberately ignores Kinde org-roles claims (`rbac-design.md §3`).

## Invited User Flow (parallel onboarding path)

Self-serve signup is one of two onboarding paths. The other is **invitation-based**: an existing org admin invites a teammate by email and the invitee joins the inviter's organization rather than creating a new one.

```
Admin posts POST /v1/invitations {email, role}
  → AxiaOps writes pending_memberships row
  → AxiaOps calls Kinde Mgmt API → Kinde sends invitation email
  → Invitee clicks link → completes Kinde signup → JWT carries inviter's org_code
  → First authenticated request to AxiaOps:
      • UpsertOrganization (no-op — org exists)
      • UpsertUser (creates row)
      • EnsureFirstMembership (no-op — org has owners)
      • RedeemPendingInvitation (inserts membership with stored role, deletes pending row)
  → Invitee lands on dashboard with their role applied. No org-creation prompt.
```

The dashboard's onboarding form (above) is shown only when the user has zero memberships after redemption — i.e. they signed up self-serve. Invited users skip the org-name form entirely.

See [`docs/invitation-flow.md`](./invitation-flow.md) for the full design (data model, API surface, middleware hook, edge cases).

## The shipped endpoint surface

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/v1/me` | Yes | Returns current user, role, permission set |
| `GET` | `/v1/memberships` | Yes | List memberships of the current org |
| `POST` | `/v1/memberships` | `members:invite` | Add an existing user (by `user_id`) with a role |
| `PATCH` | `/v1/memberships/{id}/role` | tier-dependent | Promote / demote |
| `DELETE` | `/v1/memberships/{id}` | tier-dependent | Remove (self-leave bypasses perm) |
| `POST` | `/v1/organizations/transfer-ownership` | `organization:transfer` | Owner handover |
| `DELETE` | `/v1/organizations/me` | `organization:delete` | GDPR right-to-erasure |

There is **no** `POST /v1/organizations` today. Org creation
happens in the Kinde dashboard.

## The DEV_MODE bootstrap

In `DEV_MODE=true`, the Kinde dance is bypassed. The API instead:
- Reads `DEV_ORGANIZATION_ID` env var (no default — startup
  `die()`s if unset).
- Reads `DEV_USER_ID` env var (default `dev-user-axiaops`).
- On startup, calls `EnsureDevOrganization`, `EnsureDevUser`, and
  `EnsureDevMembership(role='owner')` to guarantee a known-id
  triple exists.
- The auth middleware is replaced with `DevBypass`, which injects
  the dev IDs into every request context — no JWT required.

This is why the local dashboard "just works" on `make start-dev`
and why the CI integration stack uses `DEV_ORGANIZATION_ID:
"ci-tenant"` (an opaque fixture ID).

## What changes next: email invitations (no org-primitive refactor)

The chicken-and-egg invite flow above is closed by the email-invitation design in [`docs/invitation-flow.md`](./invitation-flow.md):

- Admin posts `POST /v1/invitations { email, role }`.
- AxiaOps writes a `pending_memberships` row and calls Kinde's Management API → Kinde sends the org-scoped invitation email.
- Invitee clicks → Kinde signup → JWT carries the inviting org's `org_code`.
- Auth middleware redeems the pending row into a real `memberships` row on the invitee's first authenticated request (after `EnsureFirstMembership`, which is a no-op for already-populated orgs).

What stays the same: Kinde owns the org primitive (`org_code` in JWT → `organizations.id`); `EnsureFirstMembership` still auto-promotes the founder of a brand-new self-serve org; RLS, roles, and permissions are unchanged.

What the originally-planned [`onboarding-and-app-owned-orgs.md`](./onboarding-and-app-owned-orgs.md) rewrite would have added — `POST /v1/organizations`, `pending_invitations` magic-link flow via Resend, `X-Organization-ID` header, dropping `UpsertOrganization`/`EnsureFirstMembership` — is **not** being pursued. That doc is preserved as a historical record of the rejected alternative; do not implement from it.

## And: post-signup wizard

The empty-state landing for fresh organisations is replaced with a 3-step wizard (confirm org name → invite teammates → connect first AWS account) designed in [`docs/onboarding-wizard.md`](./onboarding-wizard.md). It introduces `PATCH /v1/organizations/me` for org rename, with two-phase commit to Kinde so invitation emails stay in sync.
