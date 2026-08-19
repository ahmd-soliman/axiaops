# Onboarding + app-owned organisations (auth pattern B)

> **This plan was not implemented.** See [`docs/native-auth-bootstrap.md`](./native-auth-bootstrap.md)
> for what shipped.
>
> This plan describes a refactor that **we decided not to do**. It is preserved as the
> historical record of an alternative we evaluated and rejected, not as a forward-looking
> roadmap item.
>
> **What shipped instead:** native cookie sessions (argon2id) — org creation via
> `POST /v1/auth/bootstrap` (first install) or SSO JIT provisioning; team invitations
> via OOB token links (`POST /v1/invitations`). Kinde was removed in 2026-05.
> See [`docs/native-auth-bootstrap.md`](./native-auth-bootstrap.md) and
> [`docs/decisions/0001-deployment-model.md`](./decisions/0001-deployment-model.md).
>
> **Why we changed direction:**
>
> - The original premise — that org-creation must be in-app and that the chicken-and-egg invite flow is unfixable — turned out to be wrong. Kinde's `register()` SDK call plus the "Create org on sign up" toggle solves self-serve org creation in zero application code. Kinde's Management API solves email-based invitations without a `pending_invitations` magic-link flow, an SMTP/Resend dependency, an `OrgContext` middleware, or a state machine.
> - The Kinde-org-per-AxiaOps-org 1:1 mapping is fine for our model. The "split between Kinde and AxiaOps is the worst of both worlds" framing assumed multi-org-per-user as a near-term requirement; we've explicitly deferred that.
> - Cost: the original plan was ~10 commits / ~2 weeks plus an SMTP provider + DNS work. The Kinde-Mgmt-API path is ~7.75 days with no infra additions. See `docs/invitation-flow.md §11`.
> - The "self-managed-license / BYO-OIDC" GTM path is real but speculative; we'll cross that bridge if a customer asks for it. Pattern B was over-engineering for a problem we don't yet have.
>
> **What stays unchanged:** the `memberships` table, the role/permission model, RLS, and the §3 stance ("AxiaOps owns authorization, ignores Kinde's role claims") all remain correct. None of the RBAC story depended on app-owned orgs.
>
> **What this means for cross-references in other docs:**
>
> - `docs/rbac-design.md` — the Phase 3 #14 callout previously at the top of that doc has been removed.
> - `docs/user_onboarding.md` — the "next-phase rewrite" status block and the "What the next phase changes" section have been removed; that doc now describes the actual current + near-term flow.
> - The Phase 3 #14 tracking entry and related "Invite-by-email deferred to Phase 3 #14" notes have been redirected to `docs/invitation-flow.md`.
> - `docs/refactor-tenant-to-organization.md` — historical "unblocks Phase 3 #14, #15, #16" reference is left untouched (it's an immutable shipped-work record).
>
> The original plan is preserved below for reference. Do not implement from this doc.

---

## Why (original rationale — historical, not active)

Today's auth model couples organisation identity to Kinde:

- Every authenticated request reads `claims["org_code"]` and calls
  `UpsertOrganization(orgCode, orgName)` to mirror the Kinde org locally.
- `EnsureFirstMembership` auto-promotes the first authenticator into a
  brand-new Kinde org to `owner`. Subsequent users in the same org get
  no membership row and bounce off `403` until the owner explicitly
  invites them by `user_id`.
- Org *creation* must happen in the Kinde dashboard, not in AxiaOps.
  There is no `POST /v1/organizations` endpoint — the closest the docs
  describe is aspirational (`docs/user_onboarding.md` predates this plan).
- Inviting a colleague requires that colleague to first sign in (so a
  `users` row exists), bounce off the 403, and *then* be invited by
  `user_id`. Chicken-and-egg.

This setup does not scale to paid self-serve and it actively blocks
the self-managed-license GTM path (Model B),
where the customer's IT team is not going to provision a Kinde org
for our SaaS. The "vetting checkpoint" value of the current manual
step is real but unrelated to *which auth pattern we're on* — closed
beta gates `POST /v1/organizations` behind an approval queue or
email-domain allow-list (see "Closed-beta gate" below). The auth
choice is independent of the GTM choice.

The architectural decision in `docs/rbac-design.md §3` is already half
of pattern B: we deliberately ignore Kinde's role claims and own
authorization in `memberships.role`. Splitting the *org* primitive
across Kinde and AxiaOps while owning the *role* in AxiaOps is the
worst of both worlds — they're conceptually one model. This plan
finishes the job.

## What pattern B looks like

- **Kinde becomes pure OIDC.** It proves "this human is `john@acme.com`,
  email-verified, MFA-passed." We stop reading `org_code` /
  `org_name` from the JWT.
- **Org lifecycle is 100% app-side.** Orgs are created via
  `POST /v1/organizations` from the dashboard. They live in
  `organizations` and are joined to users only via `memberships`.
  `users.organization_id` stops being a FK pointing at "primary org" —
  it's deprecated and eventually dropped.
- **Org context is per-request.** The dashboard sends an
  `X-Organization-ID` header on every API call. Auth middleware
  validates: "does this JWT-authenticated user have a membership in
  that org?" → if yes, set `app.organization_id`; if no, 403.
- **Invitations are first-class.** `POST /v1/invitations` writes a
  `pending_invitations` row, sends a magic-link email via Resend.
  Recipient clicks → signs in via Kinde → `POST /v1/invitations/accept`
  reads the token, materialises membership with the role from the row.
  No more chicken-and-egg.
- **Multi-org per user works correctly.** A user's `GET /v1/me`
  response includes their full membership list. Dashboard renders an
  org switcher when the list has > 1 entry.

## Today vs target — the diff in a table

| Concern | Today (pattern A) | Pattern B |
|---|---|---|
| Org primitive | Kinde's `org_code` claim, mirrored to `organizations.org_code` | App's `organizations.id` UUID. `org_code` becomes a nullable external-reference column |
| Org creation | Manual: AxiaOps admin creates Kinde org | `POST /v1/organizations` from dashboard `/onboarding` |
| Owner assignment | `EnsureFirstMembership` on first JWT in a new Kinde org | Explicit at org-creation time inside `POST /v1/organizations` |
| Subsequent member onboarding | User signs in → 403 → owner invites by `user_id` | Owner sends email invitation → recipient clicks magic link → signs in → invitation auto-accepted with pre-assigned role |
| Org context | JWT `org_code` claim | `X-Organization-ID` header (or path param) |
| `auth.go` responsibilities | JWT verify + UpsertOrganization + UpsertUser + EnsureFirstMembership + sets `organizationIDKey` | JWT verify + UpsertUser + sets `userIDKey` only |
| New middleware | (none) | `OrgContext` — reads `X-Organization-ID`, validates membership, sets `organizationIDKey` |
| Multi-org switching | Kinde's hosted "switch organization" UI | Dashboard navbar org switcher |
| `users.organization_id` | Required FK | Deprecated → dropped |
| `users.kinde_sub` | Per-org primary identity | Per-user primary identity (must be unique across the platform, not per-org) |

The big shape change in the data model: `users` becomes a global
identity table; `memberships` is the *only* place a user is associated
with an org. Today `users.organization_id` carries a redundant "primary
org" pointer that becomes meaningless in a true multi-org world.

## Sequencing — proposed commit shape

Mirrors the `refactor-tenant-to-organization.md` shape. `make test`
must be green at every commit boundary. ~10 commits.

| # | Commit | Why this slot |
|---|--------|---------------|
| 1 | `docs(refactor): plan app-owned organisations` | This document. Zero code. |
| 2 | `feat(storage): pending_invitations table (migration 017)` | New table, RLS policy, FK to `organizations(id)` with `ON DELETE CASCADE` (so an org delete sweeps its pending invitations). `invited_by` is plain TEXT (no FK to `users`) so an admin's self-deletion doesn't orphan invitations they sent. No behaviour yet. Cheap, isolated. |
| 3 | `feat(model): Invitation type + Store interface methods` | `model.Invitation`, `Store.CreateInvitation`, `ListInvitations`, `GetInvitationByToken`, `AcceptInvitation`, `ExpireInvitations`. Postgres impl. Tests. Token generator is an injectable interface (mockable in tests) — never returned in API responses. |
| 4 | `feat(api,email): invitations end-to-end` (collapses former 4+5+6) | Three handlers (`POST/GET/DELETE /v1/invitations`, `POST /v1/invitations/accept`) **plus** the `services/shared/email/` package (Resend client + `stdout` fallback, `EMAIL_PROVIDER` env, shared with Phase 2 #5 weekly digest) **plus** the invitation HTML+text template. Shipping together avoids a half-built feature in main; tests use `EMAIL_PROVIDER=stdout` and the mock token generator from commit 3. Audit-log writes for `invitation_sent`/`accepted`/`revoked`/`expired`. Email-side rate limit: N invites per IP per hour, M per recipient email per 24h. |
| 5 | `feat(api): POST /v1/organizations` | App-side org creation. Auth-only (no permission required — any authenticated user can create their own org), gated by the closed-beta mechanism (see "Closed-beta gate" below). Inside the same transaction: insert org, insert membership with `role='owner'`, audit-log `organization_created`. Returns the new org. Rate limited via the existing `RateLimiter` at 1 / user / 24h **and** 3 / IP / 24h. |
| 6 | `refactor(middleware): introduce OrgContext middleware` | New middleware reads `X-Organization-ID` header, validates membership, sets `organizationIDKey`. **Three explicit branches** (see "OrgContext state machine" below): valid header + membership → 200 with `app.organization_id` set; valid header + no membership → 403; missing header + has memberships but didn't pick → 409 `{needs:"pick_org", memberships:[…]}`; missing header + zero memberships → 200 with empty body and `{needs:"onboarding"}` semantics on whitelisted endpoints (`/v1/me`, `/v1/organizations`, `/v1/invitations/accept`). **Existing `auth.go` keeps doing `UpsertOrganization` for back-compat in this commit** — header wins when present. Includes 60-second Redis cache for the `(user_id, org_id) → role` lookup; falls back to direct DB hit when Redis is unavailable. |
| 7 | `refactor(api): GET /v1/me returns full memberships list` | `{user, memberships: [{organization, role, joined_at}]}`. Dashboard's source of truth for "what orgs am I in?". Replaces single-org assumption. |
| 8 | `feat(dashboard): React OrgContext provider + localStorage persistence` | The data layer for org switching. On app load, calls `/me` first; populates `currentOrg = memberships[0]` if localStorage is empty; sends `X-Organization-ID` on every subsequent API call via `client.js`. Handles "user removed from previously-selected org" by falling back to `memberships[0]` or routing to `/onboarding`. |
| 9 | `feat(dashboard): /onboarding page + accept-invitation route + navbar org switcher UI` | The visible UX layer on top of commit 8. Onboarding handles zero-membership and `?invitation=...` deep links; accept-invitation route handles `/accept?token=...` (preserves token through Kinde sign-in if user is signed out). Navbar dropdown visible only when `memberships.length >= 2`. |
| 10 | `refactor(middleware): drop UpsertOrganization + EnsureFirstMembership from auth.go` | The cleanup. **Pre-condition: Kinde dashboard org-creation has been disabled in the Kinde tenant** so a brand-new Kinde org during the cutover can't sign in with no path to ownership. Auth middleware becomes "verify JWT + `UpsertUser` + set `userIDKey`." Deletes ~40 LoC and the `Store.EnsureFirstMembership` interface method + impl + tests. Migration 018 makes `organizations.org_code` nullable. |
| 11 | `chore: scrub users.organization_id from Go + seed scripts` | `UpsertUser`, `EnsureUser`, `model.User.OrganizationID`, `cmd/main.go` dev bootstrap, `scripts/seed_test_data.sh:195` all stop writing the column. **Without this, commit 12's migration fails CI**: the column is in `INSERT ... ON CONFLICT DO UPDATE SET organization_id = EXCLUDED.organization_id` (`postgres.go:306-322`) and `seed_test_data.sh` hard-codes it. |
| 12 | `chore(storage): drop users.organization_id (migration 019)` | After commit 11's scrub, the column is unused. Migration drops it. |

`postgres.go`'s `setOrganization(ctx, tx)` and the RLS predicate on
`app.organization_id` are unchanged throughout — only *who sets the
context* shifts.

## Cross-cutting design decisions

These apply across multiple commits — calling them out here so they
don't get lost in a single commit's diff.

### OrgContext state machine

Three states the middleware must distinguish, with concrete response
contracts (the previous version of this plan implicitly 403'd two of
these and would wedge the dashboard):

| State | Header | Memberships | Response |
|---|---|---|---|
| Happy path | `X-Organization-ID: <id>` | user has membership in `<id>` | 200, `app.organization_id` set, request proceeds |
| Forged / stale header | `X-Organization-ID: <id>` | user has no membership in `<id>` | 403, dashboard reloads `/me` and falls back to `memberships[0]` or `/onboarding` |
| Switcher needed | (absent) | user has ≥1 membership but didn't pick | 409 `{needs:"pick_org", memberships:[…]}` — dashboard routes to switcher |
| Onboarding needed | (absent) | user has 0 memberships | Whitelisted endpoints (`GET /v1/me`, `POST /v1/organizations`, `POST /v1/invitations/accept`) → 200; everything else → 409 `{needs:"onboarding"}` |

The whitelist is small and documented in the middleware. Anything
else returns 409 with the same `needs` payload so the dashboard has
one routing rule.

### Membership cache

`OrgContext` reads `(user_id, org_id) → role` on every request — a
DB hit on the hot path. Cache in Redis with key
`mem:{user_id}:{org_id}` and TTL 60 seconds. Falls back to direct DB
hit when Redis is unavailable (same pattern as the existing
`RateLimiter`). Invalidation: explicit DEL on
`PATCH /v1/memberships/{id}/role` and `DELETE /v1/memberships/{id}`,
otherwise the 60-second window is the bound on staleness.

### Audit events

New `AuditAction*` constants in `services/shared/model/audit.go`:

- `AuditActionOrganizationCreated` — emitted by `POST /v1/organizations`
- `AuditActionInvitationSent` — `POST /v1/invitations`
- `AuditActionInvitationAccepted` — `POST /v1/invitations/accept`
- `AuditActionInvitationRevoked` — `DELETE /v1/invitations/{id}`
- `AuditActionInvitationExpired` — emitted by the expiry sweeper (best-effort; not all expirations need an event, but bulk sweeps should log a single aggregated row)
- `AuditActionMemberJoinedViaInvitation` — emitted by accept handler alongside `InvitationAccepted` so the team-membership timeline is complete

All entries land via the existing `audit.Record(...)` helper. RLS
policy on `audit_log` already enforces org isolation.

### Invitation token storage

256-bit cryptographically-random token. Store SHA-256 hash, never
the raw token. SHA-256 not bcrypt: bcrypt's slowness defends against
brute-forcing low-entropy passwords; a 256-bit random has no entropy
floor to defend, and the index lookup needs to be fast. Indexed on
`token_hash`. Email contains the raw token only in the magic-link
URL — never in subject lines, never logged, never returned in API
responses.

Idempotency rule: **delete the pending row on first redemption**.
Second click on the same magic link → 404. This is simpler and
safer than a "row stays until expiry" rule: it makes the token
single-use by construction, and a user who's already a member
clicking the same link gets a clear "this invitation has already
been used" message instead of an opaque 200.

### Dashboard initialisation order

When the dashboard boots:

1. Read `currentOrg` from localStorage (may be null).
2. Call `GET /v1/me` (no `X-Organization-ID` header — whitelisted endpoint).
3. If `me.memberships` is empty → route to `/onboarding`.
4. If `currentOrg` is set and present in `me.memberships` → use it.
5. Otherwise → set `currentOrg = me.memberships[0]`, persist to localStorage.
6. From this point on, every API call sends `X-Organization-ID: <currentOrg>`.

If a request later returns 403 because the user was removed from
that org while the page was loaded → reload `/me`, fall back to
`memberships[0]` or `/onboarding`. The same 60-second membership
cache means the worst-case window for a stale role is ~1 minute.

### DEV_MODE bootstrap (unchanged)

`services/api/cmd/main.go` keeps calling `EnsureDevOrganization` +
`EnsureDevUser` + `EnsureDevMembership` at startup, fed by
`DEV_ORGANIZATION_ID` / `DEV_USER_ID`. The `DevBypass` middleware
still injects those IDs into the request context and skips the
JWT path entirely. Pattern B doesn't touch this — `make start-dev`
keeps working as-is.

### Closed-beta gate

`POST /v1/organizations` defaults to **manual approval queue** in
closed beta:

- Endpoint accepts the request, writes a `pending_organizations`
  row (or stamps `organizations.status = 'pending_approval'`),
  emits `OrganizationCreated` audit, returns 202 with a "we'll
  email you when approved" body.
- AxiaOps admin reviews via a CLI / admin endpoint, flips status
  to `approved`, the org becomes usable.
- Flip a feature flag (`SELF_SERVE_ORG_CREATION=true`) and the
  endpoint creates org + owner directly, skipping the queue.

Email-domain allow-list is an alternative (auto-approve any
@example.com signup) but adds policy complexity for marginal value;
prefer the manual queue + flag flip.

### Per-account scoping (free design win for v2)

`X-Organization-ID` per request makes per-account scoping (a v2
feature in `rbac-design.md`) trivial: add `X-Account-ID` alongside,
the middleware validates that the user's role for that account
permits the operation, sets `app.account_id` GUC. Same shape as
the org-context layer. Worth a note here so the future ticket has
a clear path.

## Questions to answer before sign-off

These were raised in code review and need explicit answers in the
PR description (or this doc, if the answer is durable):

- [ ] Where does the JWT live in the browser — localStorage,
      sessionStorage, or HttpOnly cookie? Affects the CSRF defence
      story for `X-Organization-ID`. (Recommended: HttpOnly cookie
      via Kinde's session helper; if localStorage, the membership
      check on the server side is the load-bearing defence.)
- [ ] What HTTP status + body shape does `OrgContext` return for
      "valid JWT, no header, zero memberships"? Documented in the
      state machine above as 200 with `{needs:"onboarding"}` on
      whitelisted endpoints, 409 elsewhere — confirm before
      implementing.
- [ ] Closed-beta gate: manual approval queue (recommended),
      email-domain allow-list, or open + rate-limited only?
- [ ] Does `POST /v1/invitations/accept` require the recipient's
      Kinde JWT to carry `email_verified=true`? Recommended: yes,
      reject unverified — magic link goes to email, so accepting
      without verification means an attacker who phished the link
      can claim the invitation.
- [ ] After commit 10 ships, an explicit grep + smoke check
      confirming **no code path** calls `EnsureOrganization` or
      `EnsureFirstMembership` outside the dev-bootstrap helpers.
- [ ] Email-side rate limits: N (invites per IP per hour) and M
      (per recipient email per 24h) — pick concrete numbers. Default
      proposal: N=20, M=3.

## Acceptance criteria

- [ ] `POST /v1/organizations { name }` creates an org and an owner membership atomically; returns the new org row.
- [ ] `POST /v1/invitations { email, role }` creates a `pending_invitations` row and sends a Resend email with a magic link. `EMAIL_PROVIDER=stdout` in dev mode.
- [ ] `POST /v1/invitations/accept { token }` materialises a membership with the role from the pending row; deletes the pending row; idempotent on retry.
- [ ] `DELETE /v1/invitations/{id}` revokes a pending invitation. Permission: `members:invite`.
- [ ] Tokens expire after 7 days. Background ticker (or cron) deletes expired rows. Acceptance returns 410 if expired.
- [ ] `GET /v1/me` returns `{user, memberships: [{organization, role}]}` — full list, not single-org.
- [ ] Dashboard `/onboarding` route: signed-in user with zero memberships sees "create org or accept invitation" CTAs.
- [ ] Dashboard `/accept?token=...` route: handles invitation acceptance, redirects to dashboard with the new org as context.
- [ ] Dashboard navbar org switcher: visible only when current user has ≥2 memberships, persists choice to localStorage.
- [ ] All API client calls send `X-Organization-ID` header derived from current dashboard org context.
- [ ] `OrgContext` middleware validates the header against `memberships`; missing or unauthorised → 403.
- [ ] `auth.go` no longer calls `UpsertOrganization` or `EnsureFirstMembership`.
- [ ] `users.organization_id` column dropped (migration 019).
- [ ] `organizations.org_code` column nullable (migration 018) — keeps it as an optional Kinde external-reference field for any users who came through pattern A.
- [ ] All existing endpoints continue to work for the legacy Kinde-coupled flow until commit 11; afterwards they require `X-Organization-ID`.
- [ ] `make test` green at every commit boundary; `make test-storage` green at every migration boundary.
- [ ] Self-hosted Model B story unblocked: a deployment with no Kinde org_codes still works, users sign in with any OIDC provider that emits `sub`+`email`, create their own org via `/onboarding`.

## Allow-list — what we deliberately keep

- **Kinde as the identity provider.** The OIDC dance, JWT signature
  verification, JWKS rotation — all unchanged. Kinde is responsible
  for "who is this human?" and only that.
- **Kinde JWT claims `sub` and `email`.** These are the auth-boundary
  contract. We will *also* keep accepting `org_code` if present but
  treat it as informational only — never as the source of truth for
  org membership.
- **MFA, password policy, social login.** All Kinde-side. Unchanged.
- **The `multi-tenant` SaaS architecture term** in marketing copy.
- **`memberships` table shape, RLS, and `app.organization_id` GUC.**
  Pattern B doesn't touch any of the per-org data model — it only
  changes who is *responsible* for setting the GUC and how the
  membership row got there.

## Migration of existing customers

For closed-beta customers who came through pattern A:

1. Their `organizations` row already has a non-null `org_code`.
2. Their `users` row has `kinde_sub` set.
3. Their `memberships` row exists with the correct role.
4. After commit 11, their next sign-in skips `UpsertOrganization` —
   the org row is already there.
5. The dashboard's `/me` call returns their existing membership; the
   navbar shows the org name; everything works.

No data migration script needed. The Kinde-org → AxiaOps-org link
remains via `organizations.org_code` (nullable post-018).

If we ever offboard from Kinde entirely, that nullable column drops
along with the auth migration.

## Risks

- **Active Kinde sessions during the cutover.** Same shape as the
  GUC rename in migration 016. After commit 11, any Kinde-session
  JWTs in flight will still validate fine, but old dashboard bundles
  not yet refreshed will hit endpoints without `X-Organization-ID`
  → 403. Mitigation: on commit 8 the OrgContext middleware should
  *fall back* to the deprecated `org_code` claim if the header is
  absent, log a warning, and the deprecation hard-line lands in
  commit 11 only after the dashboard bundle has propagated.
- **Email deliverability.** Invitations through Resend depend on
  SPF/DKIM/DMARC on `axiaops.io`. Set those up before ship — Resend
  has a 1-click setup but it requires DNS access. Otherwise
  invitation emails go to spam.
- **Token leakage.** A `pending_invitations` token grants org
  membership when redeemed. Treat it like a password: 256-bit random,
  store hashed in the DB, never log the raw token, expire in 7 days,
  delete on first redemption. Audit the email template for the
  token appearing in plain text in subject lines (it shouldn't —
  link only).
- **Self-serve abuse.** `POST /v1/organizations` without rate-limiting
  is spam-bait. Apply the existing rate limiter at 1 org / user / 24h.
- **MSP-style nested orgs.** A future feature (Model A). Pattern B makes this *easier*
  (the `organizations` table can grow a `parent_organization_id`
  nullable FK without touching auth) but is out of scope for this
  ticket.

## Out of scope (separate tickets)

- **Resend / DKIM setup at the domain level** — ops, not engineering.
- **Kinde org-roles claim mapping** — explicitly rejected in `rbac-design.md §3`. Stay rejected.
- **Soft-delete with grace window for orgs** — roadmap item 3.10, separate.
- **MSP-style nested orgs / parent-child organisations** — Phase 4+.
- **Domain-based auto-join** ("anyone with @acme.com can join Acme org") — nice-to-have, separate.
- **Customer-supplied OIDC (BYO IdP)** — unblocked by this work but
  not delivered as part of it. Once `auth.go` reads only generic
  OIDC claims, swapping Kinde for any OIDC provider is configuration,
  not code. Document and ship in a follow-up.

## Done criteria

- A fresh user can sign up via Kinde, create their own org via the
  dashboard, invite a colleague by email, and the colleague can
  accept the invitation and land on the dashboard with the assigned
  role — without any AxiaOps admin manually touching Kinde or the
  database.
- The grep guard `scripts/check-tenant-terminology.sh` still passes.
- `docs/user_onboarding.md` is rewritten as the *current state*
  description with a forward-link to this plan; this plan doc is
  archived under the historical-changelog allow-list once shipped.
