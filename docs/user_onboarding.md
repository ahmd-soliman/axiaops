# User onboarding (current state)

> **Status:** describes the implementation as of Phase 2. The
> next-phase rewrite — app-owned organisations, self-serve
> onboarding, and email invitations — is designed in
> `docs/onboarding-and-app-owned-orgs.md` and tracked in
> `Tasks.md` Phase 3 #14.

This doc was previously aspirational. It now reflects what actually
ships. For where we're going, see the plan doc above.

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
- Reads `DEV_ORGANIZATION_ID` env var (default
  `dev-organization-axiaops`).
- Reads `DEV_USER_ID` env var (default `dev-user-axiaops`).
- On startup, calls `EnsureDevOrganization`, `EnsureDevUser`, and
  `EnsureDevMembership(role='owner')` to guarantee a known-id
  triple exists.
- The auth middleware is replaced with `DevBypass`, which injects
  the dev IDs into every request context — no JWT required.

This is why the local dashboard "just works" on `make start-dev`
and why the CI integration stack uses `DEV_ORGANIZATION_ID:
"ci-tenant"` (an opaque fixture ID).

## What the next phase changes

`docs/onboarding-and-app-owned-orgs.md` — Phase 3 #14 — replaces:

- The manual Kinde-dashboard step with `POST /v1/organizations`.
- The chicken-and-egg invite flow with token-based magic-link emails
  (Resend) and `POST /v1/invitations/accept`.
- The `EnsureFirstMembership` auto-promotion with explicit owner
  assignment at org-creation time.
- The Kinde `org_code` JWT coupling with an `X-Organization-ID`
  header validated against `memberships`.

That refactor unblocks paid self-serve and the self-managed-license
GTM path. Read the plan doc for sequencing, AC checklist, and
risks.
