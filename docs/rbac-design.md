# RBAC Design — AxiaOps

Status: implemented (Phase 1 — branch feat/rbac-phase1).
Supersedes the role sketch in `docs/user_onboarding.md`.

> **Post-ADR-0001 note.** The §3 stance ("AxiaOps owns authorization") is fully implemented. Organizations are now owned by the `organizations` table — the `org_code` JWT claim and Kinde's Management API are gone (removed 2026-05). Org creation is via `POST /v1/auth/bootstrap` (first install) or SSO JIT provisioning; email-based invitations are native OOB token links. The four-role matrix, permission model, RLS, and promote/demote rules are unchanged — nothing in this doc needs revision except this note.

---

## 1. Goals & Non-Goals

### Goals (v1)

- Authorization within an organization. Every current endpoint gets a required permission. Unauthorized users receive `403 Forbidden`.
- Four roles with clear, non-overlapping semantics: `owner`, `admin`, `member`, `viewer`.
- Role is a **property of (user, organization)**, not a property of the user. A single user can belong to multiple AxiaOps organizations with different roles (B1.5 multi-org support).
- Enforcement at the HTTP handler layer via a decorator. No change to the `storage.Store` interface. No change to RLS.
- Admin UX to invite, promote, demote, and remove users.
- Safe rollout: all existing users become `admin` on the v1 ship (no regression in capabilities).

### Non-goals (v1)

- **Per-cloud-account scoping.** ("This user can only see the `dev` account.") Real requirement for FinOps but deferred to v2. Schema is designed to extend without a painful migration.
- **Custom roles.** No `CREATE ROLE ... GRANT permission`. The four roles are hardcoded.
- **API keys / service accounts** as first-class principals with their own roles. Deferred to v2. The ingestion service talks to the API/DB as trusted infrastructure, not as a "user."
- **SSO-driven role provisioning.** IdP role claims are not mapped to AxiaOps roles. Admins assign roles manually in v1; JIT-provisioned SSO users start as `member`.
- **Audit log.** Deferred to v2. `dismissed_zombies.dismissed_by` already captures the most sensitive action; broader audit can come with the security track.
- **Resource-level permissions** (per-zombie dismiss permissions, per-snapshot export, etc.). Dismissals are organization-wide.
- **Billing admin** as a separate role. `owner` handles billing until a subscription system exists.
- **Internal / staff roles** for AxiaOps employees (support, engineering, billing ops). These are a fundamentally different principal type — documented in §11 as a future scope, not implemented in v1.

---

## 2. Role Hierarchy & Capability Matrix

Four roles, strictly hierarchical. Higher roles inherit all capabilities of lower ones.

```
owner > admin > member > viewer
```

**Roles**

| Role | Intent | Typical user |
|---|---|---|
| `owner` | Controls the organization itself: billing, delete org, transfer ownership. Exactly one (or a small number — see §8). | Founder, primary account holder. |
| `admin` | Full operational control inside the organization. Manages users, cloud accounts, dismissals. | Engineering lead, platform owner. |
| `member` | Daily FinOps work: connect/update cloud accounts, trigger scans, dismiss/snooze zombies. | Platform engineer, SRE. |
| `viewer` | Read-only. Sees zombies, summary, trends, costs. Cannot mutate anything. | Finance analyst, contractor observer, exec. |

**Capability matrix — current endpoints**

Columns: endpoints registered in `services/api/internal/api/handler.go:40-58` and `services/api/cmd/main.go:120-163` (`/health`, `/metrics`). Rows: roles.

| Endpoint | `viewer` | `member` | `admin` | `owner` |
|---|---|---|---|---|
| `GET /health` | public | public | public | public |
| `GET /metrics` | see note† | see note† | see note† | see note† |
| `GET /v1/me` — new | yes | yes | yes | yes |
| `GET /v1/zombies` | yes | yes | yes | yes |
| `GET /v1/summary` | yes | yes | yes | yes |
| `GET /v1/trend` | yes | yes | yes | yes |
| `GET /v1/trend/services` | yes | yes | yes | yes |
| `GET /v1/trend/resource-types` | yes | yes | yes | yes |
| `GET /v1/costs` | yes | yes | yes | yes |
| `GET /v1/resources` | yes | yes | yes | yes |
| `GET /v1/accounts` | yes | yes | yes | yes |
| `GET /v1/accounts/{id}` | yes | yes | yes | yes |
| `POST /v1/accounts` | — | yes | yes | yes |
| `PATCH /v1/accounts/{id}` | — | yes | yes | yes |
| `DELETE /v1/accounts/{id}` | — | — | yes | yes |
| `POST /v1/accounts/{id}/scan` | — | yes | yes | yes |
| `POST /v1/dismissals` | — | yes | yes | yes |
| `DELETE /v1/dismissals/{id}` | — | yes | yes | yes |
| `GET /v1/dismissals` | yes | yes | yes | yes |
| `POST /v1/memberships` (invite) — new | — | — | yes | yes |
| `GET /v1/memberships` — new | yes | yes | yes | yes |
| `PATCH /v1/memberships/{id}/role` — new | — | — | admin* | yes |
| `DELETE /v1/memberships/{id}` — new | self** | self** | admin* | yes |
| `POST /v1/organizations/transfer-ownership` — new | — | — | — | yes |

*\* admin can promote/demote `member`↔`viewer`, and invite at member/viewer level. Only `owner` can promote to or demote from `admin`. This prevents an `admin` from creating another `admin` and escalating permanently.*

*† `/metrics` is in the `publicPath()` bypass list (`middleware/auth.go`) and exposed via `observability.MetricsHandler()` outside the auth chain in `serverbuild/build.go`. No session required for Prometheus scraping.*

*\*\* Any user can remove themselves (leave the organization), subject to the last-owner guard in §8. Admins can remove any member/viewer but not another admin — see §7 for the permission split and the two-perm check (`members:manage_basic` for member/viewer targets, `members:manage_admin` for admin targets).*

**Design rationale for `DELETE /v1/accounts/{id}` being admin-only:** deleting an account drops scan history and breaks dashboards. `member` can disconnect effectively by rotating keys or setting `scan_interval_hours=0` via `PATCH`, but the destructive act is admin-gated. This matches Datadog/New Relic patterns.

**Design rationale for giving `member` dismiss/snooze:** dismissals directly affect the savings number on the dashboard. But in practice the engineer who triages a zombie is the one who should dismiss it; forcing admin approval creates a bottleneck and kills the product loop. `admin` retains the audit trail (phase 2) and revoke capability.

---

## 3. Permission Model

**Recommendation: coarse, hardcoded.** Role → `[]permission` is a module-level `map[Role][]Permission` in Go. Not stored in the DB.

### Why not fine-grained, DB-stored permissions?

One developer, MVP stage. Fine-grained RBAC (role → permissions table, custom roles, UI to edit them) takes weeks to build, is hard to test, and we have no customer asking for it. The four-role model is what CloudZero, Vantage Lite, and Datadog's team plan ship with.

### Permission vocabulary (v1)

```
accounts:read
accounts:write         # create, update
accounts:delete
accounts:scan

zombies:read
zombies:dismiss        # create + revoke dismissal
snapshots:read
costs:read
resources:read

members:read
members:invite
members:manage_basic   # promote/demote member↔viewer, remove member/viewer
members:manage_admin   # promote/demote to/from admin

organization:transfer        # transfer ownership
organization:delete          # future
```

### Role → permissions table

```go
// services/shared/authz/roles.go
package authz

type Role string
type Permission string

const (
    RoleOwner   Role = "owner"
    RoleAdmin   Role = "admin"
    RoleMember  Role = "member"
    RoleViewer  Role = "viewer"
)

var rolePermissions = map[Role]map[Permission]bool{
    RoleViewer: perms(
        "accounts:read", "zombies:read", "snapshots:read",
        "costs:read", "resources:read", "members:read",
    ),
    RoleMember: perms( /* viewer + */
        "accounts:write", "accounts:scan", "zombies:dismiss",
    ),
    RoleAdmin: perms( /* member + */
        "accounts:delete", "members:invite", "members:manage_basic",
    ),
    RoleOwner: perms( /* admin + */
        "members:manage_admin", "organization:transfer",
    ),
}
```

Where `perms()` is a helper that unions a role's list with all lower roles. The inheritance happens at package-init time so runtime lookups are `O(1)`.

### Limitation: pure inheritance, no deny

A higher role always has every permission of every lower role. There is no way to give `viewer` a permission that `member` doesn't have. If such a case ever arises (hypothetical: "viewers can read the audit log but members cannot"), the model must be extended to explicit per-role sets without inheritance. For v1 the hierarchy matches reality — do not build the extended model preemptively.

### Extensibility

When a new endpoint is added, add the permission string and slot it into the right role. No migration. That's the whole point.

---

## 4. Data Model Changes

### New table: `memberships`

```
memberships
├── id          TEXT  PRIMARY KEY         -- UUID
├── organization_id   TEXT  NOT NULL REFERENCES organizations(id)  ON DELETE CASCADE
├── user_id     TEXT  NOT NULL REFERENCES users(id)    ON DELETE CASCADE
├── role        TEXT  NOT NULL CHECK (role IN ('owner','admin','member','viewer'))
├── invited_by  TEXT                                    -- users.id, nullable for the first owner
├── created_at  TIMESTAMPTZ NOT NULL
├── updated_at  TIMESTAMPTZ NOT NULL
└── UNIQUE (organization_id, user_id)

-- Partial unique index enforces at-most-one-owner per organization at the DB level.
-- Backstops the application-level "transfer ownership" flow against the
-- first-login race condition described in §8.
CREATE UNIQUE INDEX memberships_one_owner_per_organization
    ON memberships (organization_id) WHERE role = 'owner';
```

- `UNIQUE(organization_id, user_id)` — a user has at most one role per organization. Also provides the index used by `RoleOf` lookups on the hot path (no separate `CREATE INDEX` needed).
- Not `users.role` because: (a) a user can belong to multiple organizations with different roles (B1.5 multi-org), (b) lets us delete membership without touching the user row.
- RLS: `organization_id = current_setting('app.organization_id', true)` USING + WITH CHECK (same pattern as every other table — see `migrations/011_add_rls_with_check.up.sql`). **Bootstrap caveat:** the `Require` middleware's `RoleOf` call happens before any handler sets `app.organization_id`. The `RoleOf` implementation must therefore open its own transaction, run `SET LOCAL app.organization_id = $1` inside it, then SELECT — identical pattern to `postgres.setOrganization` at `postgres.go:72-81`. Do not use `adminPool` here; we want RLS to enforce the organization scope even during the auth check.

### No changes to `users` or `organizations`

The `users` row is populated at login / JIT-provisioning time. Membership is a pure join. The `WrapNative` middleware resolves the role via `MembershipLookup` for every authenticated request.

### Migration shape

`services/shared/storage/postgres/migrations/015_memberships.up.sql`:

1. `CREATE TABLE memberships (...)`
2. `ALTER TABLE memberships ENABLE ROW LEVEL SECURITY`
3. `CREATE POLICY memberships_organization_isolation ON memberships USING (...) WITH CHECK (...)`
4. `GRANT SELECT, INSERT, UPDATE, DELETE ON memberships TO axiaops`
5. **Backfill:** `INSERT INTO memberships (id, organization_id, user_id, role, created_at, updated_at) SELECT gen_random_uuid(), organization_id, id, 'admin', NOW(), NOW() FROM users;` — every existing user becomes `admin`.
6. **Promote one user per organization to `owner`:** for each organization, the user with the earliest `created_at` gets `UPDATE memberships SET role='owner' WHERE ...`. Single SQL with a CTE.
7. **Safety check:** `DO $$ BEGIN IF EXISTS (SELECT 1 FROM organizations t WHERE NOT EXISTS (SELECT 1 FROM memberships m WHERE m.organization_id = t.id AND m.role = 'owner')) THEN RAISE EXCEPTION 'migration 015: organization(s) without an owner — refusing to proceed'; END IF; END $$;` — fails the migration if any organization ended up ownerless (happens when an organization row exists with zero users — the "earliest user" CTE produces no row for it). Surfaces the orphan organization loudly rather than leaving the invariant broken.

`services/shared/storage/postgres/migrations/015_memberships.down.sql` drops the table.

---

## 5. Auth Provider Role Strategy

> **Post-ADR-0001:** Kinde is removed. This section's recommendation — "manage roles in our
> own DB; the auth provider is authn only" — was correct and is now fully implemented. Roles
> live in the `memberships` table; no JWT role claim is used. The "What Kinde still does"
> subsection below is historical artefact. The "Alternative: Kinde-native roles" subsection
> is preserved as the design rationale for the decision we did NOT take.

**Recommendation: manage roles in our own DB. The auth provider is authn only.**

### The fork in the road

Kinde (the original provider — since removed) supported org-scoped roles natively. They
came back in the JWT as `roles` / `permissions` claims. Tempting to use.

### Why we don't

1. **Vendor independence is a stated goal.** `docs/auth.md` explicitly calls out "Never put Kinde-specific claims in your business logic — only extract `organization_id` in the middleware layer and pass it down as a plain string." Pulling roles from Kinde violates that.
2. **No round-trip.** Role changes from the admin UI would have to call the Kinde Management API, wait, and then users would need to re-login for their JWT to refresh. With local roles: one `UPDATE memberships` and the next request sees the new role.
3. **We already have `users` and `organizations`.** Adding a `memberships` table is natural. Maintaining role state in two places (Kinde + our DB) is the worst option.
4. **Testing.** Local roles are testable with `httptest` and a `Store` mock, which is the established pattern (see `handler_test.go`). Kinde-sourced roles would require a mock JWT per role or a Kinde API mock.
5. **The permission vocabulary is ours.** `zombies:dismiss` is not a Kinde concept. Storing roles in Kinde but permissions in our code still means we'd fetch the role from the JWT and translate. If we're translating anyway, the source of truth might as well be our DB.

### What the auth provider does (post-ADR-0001)

- Authentication (identity verification + session issuance) — native argon2id for password logins, OIDC RP for SSO logins.
- Org-switching is native (B1.5 org-picker flow).
- SSO JIT provisioning: a SAML/OIDC claim can seed a default role — every JIT-provisioned user starts as `member`.

### Alternative: Kinde-native roles (the road not taken)

This section exists so the decision is visible. If priorities change, this is the variant you'd build instead.

**Shape of the implementation**

1. Define four roles in the Kinde dashboard: `owner`, `admin`, `member`, `viewer`. Kinde scopes them per-organization, which matches our per-organization model.
2. Attach them as custom claims in the Kinde token (Kinde supports this via the "Token customization" settings — they appear as a `roles` array claim on the JWT).
3. In `auth.go`, after verifying the JWT, extract `claims.Roles[0]` and stash it on the request context next to `organization_id`.
4. `middleware.Require(perm)` reads the role from context (not from a DB query) and checks against the same `rolePermissions` map described in §3. The permission vocabulary still lives in Go.
5. User management UI: a "Manage users" button in the dashboard deep-links to Kinde's hosted org-management page. No in-app user admin screens are built.
6. Role changes happen in Kinde. To take effect, the affected user must refresh their JWT (re-login, or wait for token expiry + silent refresh).
7. No `memberships` table. No migration 015. No new endpoints for member management.

**What you gain**

- **~2–3 days of implementation** saved. No table, no migration, no CRUD endpoints, no dashboard user-management screen.
- **SSO role provisioning comes free on day one.** Enterprise customer configures their SAML IdP to pass `role=admin` → Kinde maps it → it's in the JWT → it works. The DB-backed variant requires a custom mapping layer (§ Phase 2).
- **One fewer table to keep in sync with users/organizations.** Simpler data model.
- **Kinde's user-management UI is already polished.** Invitations, email delivery, resend, revoke — all built, all free.

**What you lose**

- **Instant role changes.** A demoted user keeps elevated access until their JWT expires (typically 1h). For "revoke admin rights" this is a real security gap. Mitigations exist (short token TTL + silent refresh, or calling Kinde's Management API to force logout) but they add complexity back.
- **Vendor independence.** `docs/auth.md` explicitly codifies "never put Kinde-specific claims in business logic." This variant violates that rule — swapping auth providers later requires rewriting the authorization layer.
- **Split source of truth when features grow.** Per-cloud-account scoping (§ Phase 2) lives in *our* DB. So does audit logging, API keys, pending-invitations-with-business-logic. Over time you'd rebuild a `memberships`-shaped table anyway to hang those features off — at which point you have Kinde roles *and* a local table and must reconcile them.
- **Testability.** Every handler test needs a mock JWT with the right role claims, rather than a Store mock returning a role. The existing test pattern in `handler_test.go` doesn't stretch to this cleanly.
- **Dashboard UX friction.** "Manage users" becoming a deep-link to Kinde is a jarring context switch and exposes Kinde branding to customers.

**When this variant is the right call**

- You have one developer and a two-week runway to ship *some* authorization.
- You're confident AxiaOps will stay on Kinde for the foreseeable future (2+ years).
- Your customers are small teams where JWT-staleness on role change is acceptable.
- You don't plan to build per-account scoping or audit logging for 6+ months.

**When to abandon it and migrate to the DB-backed variant**

- First enterprise customer asks for per-cloud-account scoping.
- First security review flags the JWT-staleness gap.
- You decide to offer a non-Kinde auth path (self-hosted, SAML-direct, Auth0 for larger customers).

The migration path is straightforward: create `memberships`, backfill from current Kinde roles via the Management API, flip `middleware.Require` to query the table instead of the context. One week of work, but it's throw-away work — the Kinde-native code you wrote gets deleted.

**Recommendation stands: DB-backed variant.** The vendor-independence rule in `docs/auth.md` is the deciding factor. If that rule didn't exist, this alternative would be a defensible choice.

---

## 6. Authorization Enforcement

### Where: HTTP handler layer, via a decorator

```go
// services/api/internal/middleware/authz.go — new

func Require(perm authz.Permission, store authz.RoleStore, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        tid := OrganizationID(r.Context())
        uid := UserID(r.Context())  // new context key — set by auth.go after UpsertUser
        role, err := store.RoleOf(r.Context(), tid, uid)
        if err != nil || !authz.Allows(role, perm) {
            http.Error(w, "forbidden", http.StatusForbidden)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

### Caching

The role lookup runs on every request. Two options:

1. **No cache.** `SELECT role FROM memberships WHERE organization_id = $1 AND user_id = $2` — hits the index provided by the `UNIQUE (organization_id, user_id)` constraint (see §4). Sub-millisecond on any realistic load. Redis already caches JWKS; we're not running 10k RPS.
2. **Cache in the existing Redis `cache.Cache` with a short TTL** (e.g. 30s) under `role:{organization_id}:{user_id}`. Invalidate on role change in the mutation handler. Defer until metrics show it matters.

**Recommendation: option 1 for v1.** Premature.

### How it composes with RLS

RLS is still the last line of defence for organization isolation. RBAC filters **what a user in organization X can do to organization X's data**. RLS filters **which organization's data you touch at all**. They're orthogonal.

**Ordering inside the middleware chain:**

1. `Auth.Wrap` validates the JWT, upserts organization + user, sets `organization_id` and `user_id` on the request context. No DB query against `memberships` yet.
2. `Require(perm)` calls `store.RoleOf(ctx, organizationID, userID)`. The `RoleOf` implementation opens a short transaction, executes `SET LOCAL app.organization_id = $1`, then SELECTs — same `setOrganization` pattern handlers use. RLS is active for this query: the row the middleware reads must be in the authenticated organization, which is exactly what we want.
3. Handler runs. It calls `storage.WithOrganizationID(ctx, organizationID)` as it does today — no change.

The Store interface gains one method (`RoleOf`). No other surface moves.

### Where the decorator wires in

Inside `Handler.Register(mux)` (`handler.go:40`). Each route gets wrapped. Proposed shape:

```go
mux.Handle("GET /v1/zombies",           require("zombies:read",      h.listZombies))
mux.Handle("POST /v1/accounts",         require("accounts:write",    h.createAccount))
mux.Handle("DELETE /v1/accounts/{id}",  require("accounts:delete",   h.deleteAccount))
// ...
```

Where `require` is a closure over the membership-role lookup store.

**Alternative considered: per-handler inline check.** Rejected — 20+ handlers, duplication, easy to forget on a new endpoint. Route-level registration fails closed if a developer forgets (the route simply isn't wrapped, but then they also forgot to register it — it's harder to accidentally expose an endpoint).

---

## 7. Endpoint → Permission Mapping

Authoritative table. See §2 capability matrix for which role gets each permission.

| Method & Path | Permission | Notes |
|---|---|---|
| `GET /health` | *(none)* | public, see `auth.go:115` (`Auth.Wrap`) and `auth.go:187` (`DevBypass` — both paths must agree) |
| `GET /metrics` | *(none)* | Prometheus scrape, internal — currently broken behind auth (see §2 note †) |
| `GET /v1/me` | *(none beyond authn)* | Returns current user's role + permission set. Used by dashboard to refresh after a 403 (see §8 "Role-change propagation"). |
| `GET /v1/zombies` | `zombies:read` | |
| `GET /v1/summary` | `zombies:read` | derived from zombies |
| `GET /v1/trend` | `snapshots:read` | |
| `GET /v1/trend/services` | `snapshots:read` | |
| `GET /v1/trend/resource-types` | `snapshots:read` | |
| `GET /v1/costs` | `costs:read` | |
| `GET /v1/resources` | `resources:read` | |
| `GET /v1/accounts` | `accounts:read` | |
| `GET /v1/accounts/{id}` | `accounts:read` | |
| `POST /v1/accounts` | `accounts:write` | |
| `PATCH /v1/accounts/{id}` | `accounts:write` | |
| `DELETE /v1/accounts/{id}` | `accounts:delete` | admin+ |
| `POST /v1/accounts/{id}/scan` | `accounts:scan` | |
| `POST /v1/dismissals` | `zombies:dismiss` | `DismissedBy` holds `user_id` (stable UUID) via `dismissActor()` — prefers `UserID`, falls back to email, then organization-id |
| `DELETE /v1/dismissals/{id}` | `zombies:dismiss` | |
| `GET /v1/dismissals` | `zombies:read` | read-only listing |
| `GET /v1/memberships` | `members:read` | new |
| `POST /v1/memberships` | `members:invite` | new, admin+ |
| `PATCH /v1/memberships/{id}/role` | `members:manage_basic` if target is/becomes member/viewer, `members:manage_admin` if target is/becomes admin | new; handler inspects both current and proposed roles and picks the stricter permission |
| `DELETE /v1/memberships/{id}` | `members:manage_basic` if target is member/viewer, `members:manage_admin` if target is admin; **bypass permission check entirely if user is deleting their own membership** (self-leave, still subject to last-owner guard) | new |
| `POST /v1/organizations/transfer-ownership` | `organization:transfer` | new, owner only |

---

## 8. Edge Cases & Considerations

### Last-admin protection

- **Rule:** an organization must always have at least one `owner`. A `DELETE /v1/memberships/{id}` or role-demotion that would leave zero owners returns `409 Conflict`.
- **Self-demotion:** allowed only if another owner exists.
- **Owner deletion:** to remove the only owner, the owner must first `POST /v1/organizations/transfer-ownership` to another admin/member, which in one transaction demotes current user to `admin` and promotes target to `owner`.

### `DismissedBy` stores the stable user UUID

`DismissedBy` is set by `dismissActor()` (`handler.go`), which prefers `middleware.UserID(ctx)` (the immutable UUID), falls back to `middleware.UserEmail(ctx)` when user ID is unavailable (e.g. test contexts with `store=nil`), and finally falls back to the organization ID so rows are never written with an empty string. Emails can change (rename, re-marry, new company domain); UUIDs are immutable. This was implemented as part of RBAC Phase 1.

### `DEV_MODE`

`DEV_MODE=true` bypasses auth (`auth.go:185` → `DevBypass`). The dev-mode user has no JWT, no `kinde_sub`, no role row. Handling:

- **At startup in `main.go`:** after `store.EnsureOrganization(ctx, devOrganizationID, ...)`, also call `store.EnsureDevUser(ctx, devOrganizationID, devUserID, devEmail)` followed by `store.EnsureDevMembership(ctx, devOrganizationID, devUserID, authz.RoleOwner)`. Two helpers — the membership depends on a user row existing, and neither exists today.
- **New env var:** `DEV_USER_ID` (default: `dev-user-axiaops`). `DEV_ORGANIZATION_ID` already exists.
- **In `DevBypass`:** set both `organization_id` **and** `user_id` on the context. Today it only sets organization (see `auth.go:185-194`).
- **In the `Require` middleware:** no branching on dev mode — it just looks up the role, finds `owner`, and allows everything. Keeps prod and dev code paths identical. If you branched here, the permission-check path would only run in prod and bugs there wouldn't surface until staging.

### Self-service user management

- **Admins invite `member`/`viewer`.** Admins cannot create `admin`.
- **Only owners invite or promote to `admin`.**
- **Owners transfer ownership explicitly** (not by inviting a second owner — there's at most one owner at a time, enforced by the partial unique index in §4). *(Alternate: allow multiple owners; simpler but loses the "one throat to choke" semantic. Recommend: single owner for v1, revisit if customers push back.)*

### Self-leave

A user can remove themselves from any organization they're a member of — `DELETE /v1/memberships/{id}` where the target is the current user. Permission check bypassed (you don't need `members:manage_*` to leave), but the last-owner guard still applies: a sole owner must transfer first.

Common UX; many B2B apps shipped without it and regretted it (support tickets: "stop mailing me, I don't work there anymore").

### Invitations (current scope)

Two endpoints, two primitives:

- **`POST /v1/memberships {user_id, role}`** — promote an **existing** AxiaOps user to a role. The invitee must already have a `users` row (i.e. authenticated via Kinde at least once). Useful when promoting someone who churned through the app previously, or — post multi-org support — adding a known user to a second organization.
- **`POST /v1/invitations {email, role}`** — invite **by email**, including users who do not yet have a Kinde or AxiaOps account. AxiaOps writes a `pending_memberships` row, calls Kinde's Management API to send an org-scoped invitation, and redeems the pending row into a real `memberships` row when the invitee first authenticates (see `auth.go` redemption hook after `EnsureFirstMembership`).

The `pending_memberships` table:

| Column | Notes |
|---|---|
| `id` | UUID |
| `organization_id` | FK to `organizations`, RLS-scoped |
| `email` | stored verbatim, indexed by `lower(email)` |
| `role` | `admin` / `member` / `viewer` (no `owner` — transferred via dedicated endpoint) |
| `invited_by_user_id`, `invited_by_email` | the inviter, for audit |
| `status` | `pending` / `expired` / `revoked` (no `accepted` — redemption deletes the row) |
| `kinde_invitation_id`, `kinde_user_id` | from the Kinde Mgmt API response, used for revocation |
| `expires_at` | default `NOW() + 14 days` |

Partial unique index on `(organization_id, lower(email)) WHERE status='pending'` makes re-invites idempotent (upsert refreshes `expires_at` + `role`, re-issues the Kinde email).

Permission tiers mirror `POST /v1/memberships`: `members:invite` for `member`/`viewer` targets, `members:manage_admin` for `admin` targets. Revocation (`DELETE /v1/invitations/{id}`) calls Kinde's `DELETE .../organizations/{org_code}/users/{kinde_user_id}` first, then flips the local row to `revoked`.

### Deleted user with valid JWT

A user whose membership row was deleted still has their JWT until it expires. Next request:

1. `Auth.Wrap` validates the JWT → upserts organization + user (the user row survived the membership deletion — that's intentional, §4).
2. `Require` → `RoleOf` returns "no row." Middleware returns 403.
3. Dashboard sees 403, calls `/v1/me`, also gets 403 (no permission beyond authn, but there's no row to return). UI logs the user out.

Side effect: `UpsertUser` fires on every rejected request until the token expires. Log noise but no data issue. If this becomes a problem, short-circuit by checking `RoleOf` returned zero rows before calling `UpsertUser` — **do not** build this unless logs actually show it's a problem.

### Inter-service auth (ingestion `/scan`, scheduled scans)

The ingestion service runs its own HTTP server on `:8081` with its own `/scan` endpoint. RBAC as described applies to `/v1/*` on the API service **only**. The ingestion service remains network-trusted infrastructure in v1:

- **API → ingestion call** (`POST /accounts/{id}/scan` handler POSTs to `:8081/scan`): internal, network-level trust. No Kinde JWT, no `Require` wrapping. This is the "trusted internal code" referenced in §1 non-goals.
- **Scheduled scans** (background goroutine on the API side, triggered by the interval ticker): no user context. Same code path as the on-demand scan but without a user — `user_id` is null on the downstream call. Any DB writes happen against the system-owned connection (`adminPool`) since there's no authenticated user to attribute them to.

If/when the ingestion service becomes externally reachable or runs in a different trust zone, this story changes — service-to-service auth (mTLS or a signed service token) becomes a v2 concern alongside API keys.

### Audit logging

Deferred to v2. For v1, the existing `slog.Info` calls on mutating handlers are sufficient (they already log `user_id` through request context once §7 is applied). When a proper audit log ships, it will subscribe to the same slog stream or tap into a DB trigger.

### API keys / service accounts

Deferred to v2. Current architecture: the ingestion service writes zombies with `ListAllAccounts` (`storage.go:82`) — explicitly documented as "trusted internal code." That's the only non-human principal today and it doesn't need a role. The v2 API-key story will be "issue a token bound to a membership with a specific role" — the memberships table already supports this shape (`user_id` points to a user record of type `service_account`).

### New organization bootstrap

Migration 015 handles existing organizations by promoting the earliest-created user to `owner`. But when a **new** Kinde org signs up after v1 ships, the first authenticating user has no membership row at all.

**Rule:** on first login to an organization with zero memberships, the authenticating user is auto-promoted to `owner`. Implemented in `auth.go` after `UpsertUser`:

1. Open a transaction.
2. `INSERT INTO memberships (organization_id, user_id, role, ...) VALUES ($1, $2, 'owner', ...) ON CONFLICT DO NOTHING`.
3. If no row was inserted **and** no membership row exists for this user → they've joined a Kinde org they haven't been invited to in AxiaOps. Return 403 with "contact your organization admin".
4. Commit.

**Race protection:** the partial unique index `memberships_one_owner_per_organization` in §4 guarantees at most one `owner` per organization at the DB level. If two users from the same brand-new Kinde org authenticate simultaneously, one INSERT succeeds, the other hits the partial index conflict — the losing request falls through to the "no membership" 403 branch and the user is told to contact their organization admin (which is now the other user). Slightly awkward but safe; never produces two owners.

This is the **only** code-level auto-promotion. All subsequent users go through explicit invitation.

### Role-change propagation

Admin demotes user X from `admin` to `viewer` at time T. X's dashboard was loaded at T-5min and still shows admin UI. X clicks "Delete account".

**Behavior:**

- Server returns 403 — `Require(accounts:delete)` fails on the fresh DB lookup.
- Dashboard intercepts 403, calls `GET /v1/me` (new endpoint returning the current role), re-renders with updated capabilities.
- User sees an initially-confusing-but-immediately-corrected state, not a silent security hole.

No forced logout, no JWT invalidation, no session-revocation machinery. The server is always the source of truth; the client is eventually consistent. For more aggressive invalidation — v2.

### Per-cloud-account scoping

Deferred to v2. When needed:

```
membership_account_scopes
├── membership_id  TEXT  REFERENCES memberships(id) ON DELETE CASCADE
├── account_id     TEXT  REFERENCES accounts(id)    ON DELETE CASCADE
└── PRIMARY KEY (membership_id, account_id)
```

Rule: if **any** rows exist for a membership, the user is restricted to those accounts. Zero rows = organization-wide (default for everyone in v1). This lets v2 ship without migrating existing memberships — they just have no scope rows.

Filtering applies in the handler (add `WHERE account_id IN (scoped_ids)` to list queries) not in RLS (RLS stays purely organization-scoped). Keeps RLS simple.

---

## 9. Migration & Rollout

### Ship sequence

1. **Migration 015:** create `memberships` (with partial unique index for owner), enable RLS, backfill all existing users as `admin`, elevate earliest-created user per organization to `owner`, run the safety check that fails the migration if any organization is ownerless.
2. **Extend `Store` interface** with `RoleOf`, `ListMemberships`, `SaveMembership`, `DeleteMembership`, `EnsureDevUser`, `EnsureDevMembership`. Postgres impl. `RoleOf` opens its own tx and runs `SET LOCAL app.organization_id` — it must not bypass RLS.
3. **Add `authz` package** in `services/shared/authz/` — roles, permissions, `Allows(role, perm)`.
4. **Add `middleware.Require`** in `services/api/internal/middleware/authz.go`. Also add `UserID(ctx)` accessor alongside the existing `OrganizationID(ctx)`.
5. **Extend `auth.go`**:
   - Set `user_id` on the request context in both `Auth.Wrap` (after `UpsertUser`) and `DevBypass`.
   - Add the new-organization bootstrap logic (§8) in `Auth.Wrap` after upsert.
   - Add `/metrics` to the public-path bypass list (pre-existing bug — see §2 note †).
6. **Wire `Require` into `Handler.Register`** — all current endpoints get a permission. Change `mux.HandleFunc` call sites that need a permission to `mux.Handle(method_path, Require(perm, store, http.HandlerFunc(handler)))`.
7. **Add `/v1/me` endpoint** — returns `{user_id, organization_id, role, permissions: [...]}`. No `Require` wrapping (any authenticated user can fetch their own role).
8. **Add membership endpoints** (invite, list, promote, demote, remove, transfer ownership). Implement last-owner guard and self-leave branch (§8).
9. **Seed dev identity on startup:** in `main.go`, after `EnsureOrganization`, call `EnsureDevUser` then `EnsureDevMembership` when `DEV_MODE=true`.
10. **Dashboard:** add a `/settings/users` screen (owner/admin only). Hide `Connect AWS` nav from `viewer`. Implement 403 → `/v1/me` refresh flow; if `/v1/me` itself returns 403, redirect to the login/removed-user screen.

### Feature flag

Not recommended. Once the migration runs, every existing user has role `admin`, which is equivalent to the pre-RBAC "everyone can do everything" behaviour. There is no behaviour gap during rollout — turn it on directly.

### Post-ship verification

- Integration test: organization A `member` cannot DELETE `/v1/accounts/...` even for their own organization.
- Integration test: organization A `admin` cannot see organization B data (RLS still works under RBAC).
- Integration test: last-owner demotion returns 409.

### Rolling back

Drop migration 015. Remove `Require` wrappers. Since the backfill defaults everyone to `admin`, the pre-RBAC world is fully restored. No data loss.

---

## 10. Phased Implementation Plan

### Phase 1 (MVP RBAC — shipped on feat/rbac-phase1)

- `memberships` table + migration 015 (with partial unique index + ownerless-organization safety check)
- `authz` package (roles, permissions, `Allows`)
- `middleware.Require` + `UserID(ctx)` accessor
- `auth.go` changes: set `user_id` on ctx, new-organization bootstrap via `EnsureFirstMembership`, `/metrics` auth bypass
- All current endpoints mapped to permissions
- `GET /v1/me` endpoint
- Membership endpoints: invite-by-user-id, list, promote/demote, remove, transfer-ownership, self-leave
- Last-admin/last-owner guards (app-level) + partial unique index (DB-level)
- Dashboard user-management screen + 403→`/v1/me` refresh flow
- Dev identity seeding (`EnsureDevMembership` on startup in `cmd/main.go`)
- `DismissedBy` uses `user_id` via `dismissActor()` (shipped — `handler.go` uses `middleware.UserID` with email and organization-id fallbacks)

### Phase 2 (post-MVP)

- ~~Audit log (new `audit_log` table, written from mutation handlers)~~ — **shipped**. Migration 014, `Store.AuditLogWrite/List/AnonymiseUser`, `services/api/internal/audit` helper, `GET /v1/audit` with cursor pagination, dashboard `AuditScreen.jsx`.
- **GDPR — right to erasure** (`DELETE /v1/users/me`, `DELETE /v1/organizations/me`) — owner-only `organization:delete` permission, `Store.DeleteUser`/`DeleteOrganizationCascade` (admin-pool, FK-safe order), audit_log purge baked into the organization cascade. Sole-owner guard on user delete. Prometheus counters `axiaops_user_deletions_total` / `axiaops_organization_deletions_total` are the durable ops trail (the audit row gets purged with the rest). Out of scope here: `GET /v1/export` (data portability), Stripe cancellation hook, dashboard UI for organization deletion.
- API keys / service-account memberships
- Per-cloud-account scoping (`membership_account_scopes` table + handler filtering)
- SSO-driven default-role mapping (Kinde org-role → AxiaOps role on first login)
- ~~Invite-by-email (before first login)~~ — **shipped**. `POST /v1/invitations`, `pending_memberships` table, middleware redemption hook. See §8 "Invitations (current scope)".
- Billing admin role (ships alongside the subscription/billing feature)
- Internal / staff roles — see §11 (triggered by second hire, first paying customer, or billing system shipping)

### Phase 3 (speculative)

- Custom roles
- IP allow-listing per membership
- Per-feature permission overrides (explicit grants/denies)

Do not build any of phase 3 without a named customer.

---

## 12. Kinde-Native RBAC Evaluation

We evaluated whether to replace AxiaOps' internal RBAC (the `memberships` table + Go permission decorators above) with Kinde's built-in roles and permissions. This section documents the evaluation, the decision, and the upgrade path.

### What Kinde offers

| Capability | Kinde behaviour |
|---|---|
| Native roles | **Yes**, scoped per-organization. The same user can hold different roles in different orgs. |
| Native permissions | **Yes**, arbitrary strings (e.g. `members:invite`) bundled into roles. |
| JWT delivery | **Opt-in.** Roles ship as a `roles` array claim, permissions as a `permissions` string array. Toggled in *Settings > Applications > [App] > Tokens > Token Customization*. Reflects the active `org_code`. No per-request callback to Kinde needed. |
| Custom definitions | **Fully custom** name, description, key, bundled permissions. |
| Multi-org users | JWT carries the role/permission set for the **active `org_code` only**. To switch orgs the user re-authorizes. Aligns with our `SET app.organization_id` model. |
| Migration cost | **Moderate.** Role/permission keys live in Kinde, referenced by string in JWT. Mitigated by mirroring `(user_id, org_id, role_key)` into our DB on each login (which we already do via `memberships`). |

Sources (verified during evaluation): [Kinde Pricing](https://kinde.com/pricing/), [Manage user roles](https://docs.kinde.com/manage-users/roles-and-permissions/user-roles/), [Apply roles and permissions](https://docs.kinde.com/manage-users/roles-and-permissions/apply-roles-and-permissions-to-users/), [Token customization](https://docs.kinde.com/build/tokens/token-customization/).

### The blocker: free-tier caps

The Kinde free tier caps custom RBAC at **2 roles and 10 permissions** (per business). AxiaOps already uses:

- **4 roles** (`owner`, `admin`, `member`, `viewer`)
- **~14 permissions** (`accounts:read`, `accounts:write`, `accounts:delete`, `accounts:scan`, `zombies:read`, `zombies:dismiss`, `snapshots:read`, `costs:read`, `resources:read`, `members:read`, `members:invite`, `members:manage_basic`, `members:manage_admin`, `organization:transfer`, plus `organization:delete` already shipped)

Both numbers are over the free-tier cap. Adopting Kinde RBAC natively forces us onto **Pro at $25/mo day one**, which contradicts the target infra cost envelope.

### Decision: stay internal for now

Keep the current model:

- `memberships` table is the source of truth for `(user, organization, role)`.
- Permissions are evaluated by Go decorators (`middleware.Require`) using the `rolePermissions` map in `services/shared/authz/roles.go`.
- Kinde's JWT carries identity (`sub`, `email`, `org_code`) only. No `roles` or `permissions` claim is read or required.

This matches the existing §3 stance ("AxiaOps owns authorization, ignores Kinde's role claims") and the Phase 3 #14 callout at the top of this doc — both already point in this direction.

### Migration path if/when we move to Pro

Should AxiaOps move to Kinde Pro for unrelated reasons (custom domain, advanced branding, more MAU), the **hybrid model** becomes attractive: let Kinde own role *assignment* (the painful UI part) while AxiaOps keeps the rich permission matrix in code.

The migration is additive, not destructive:

1. Define the 4 roles in Kinde with the same key strings used in `authz` (`owner`, `admin`, `member`, `viewer`). Define a single permission per role at first (or none — we don't need Kinde to model permissions if we keep the matrix in Go).
2. Enable the `roles` JWT claim in Kinde token customization.
3. Extend `auth.go` after `EnsureFirstMembership` / `RedeemPendingInvitation`: read `roles` from the JWT, call a new `Store.SyncMembershipRole(ctx, userID, orgID, roleKey)` that updates the `memberships.role` column to match the Kinde claim. The `memberships` table becomes a **derived projection** of Kinde state.
4. Membership-management endpoints (`POST /v1/memberships`, `PATCH /v1/memberships/{id}/role`, `DELETE /v1/memberships/{id}`, `POST /v1/invitations`) call Kinde Mgmt API instead of writing the local row directly. The local row is updated when Kinde fires a webhook or on the user's next login.
5. Permission decorators (`Require(perm, ...)`) are unchanged — they still consult the local `memberships.role` and the `rolePermissions` map.

This gives us:

- A single source of truth (Kinde) for role assignment, with all the UI Kinde provides.
- Local cache (`memberships`) for fast permission checks without per-request Kinde calls.
- Provider portability: if we ever leave Kinde, the local cache is already populated and we just stop syncing.

Estimated effort when triggered: **~3 days** (Kinde role/permission setup, sync hook in middleware, Mgmt API client extensions for membership writes, integration tests). Not on any roadmap; trigger is "we're paying for Kinde Pro anyway."

### Open questions for Kinde (deferred — not blocking)

These would need confirmation before executing the hybrid migration:

1. **Role-key stability:** if a role is renamed in Kinde's UI, does the `key` (used in JWT) stay stable, or get regenerated? Critical for our string-keyed `rolePermissions` map.
2. **Token size:** with multi-org users, does the JWT carry only the active-org permissions, or all orgs' permissions? (Docs imply active-org only.)
3. **Free-tier cap scope:** is the "2 roles / 10 permissions" cap per business or per organization? If per-org and AxiaOps customers are modelled as Kinde orgs, the cap effectively never bites for *internal* roles.
4. **Webhook on role change:** is there a `role.assigned` / `role.revoked` webhook so the local mirror stays consistent without waiting for the user's next login?

### Decision summary

| When | What we do |
|---|---|
| Today (Free tier) | Internal RBAC. Kinde JWT carries identity only. **No change.** |
| If we move to Kinde Pro | Hybrid: Kinde owns role assignment, `memberships` is a synced projection, Go decorators unchanged. ~3 days of work. |
| If we leave Kinde entirely | Internal RBAC continues to work — `memberships` is already self-contained. The provider swap is a JWT-validation concern, not an RBAC concern. |

---

## Appendix A — Files that change

**Backend**

- `services/shared/storage/postgres/migrations/015_memberships.up.sql` (new — table, RLS policy, partial unique index, backfill, safety check)
- `services/shared/storage/postgres/migrations/015_memberships.down.sql` (new)
- `services/shared/model/membership.go` (new)
- `services/shared/storage/storage.go` (extend `Store`: `RoleOf`, `ListMemberships`, `SaveMembership`, `DeleteMembership`, `EnsureDevUser`, `EnsureDevMembership`)
- `services/shared/storage/postgres/postgres.go` (implement new methods; `RoleOf` uses own tx with `SET LOCAL app.organization_id`)
- `services/shared/authz/roles.go` (new — `Role`, `Permission`, `rolePermissions`, `Allows`)
- `services/shared/authz/roles_test.go` (new)
- `services/api/internal/middleware/auth.go` (add `UserID` context key + accessor; set `user_id` on ctx in both `Auth.Wrap` and `DevBypass`; add new-organization-bootstrap logic; add `/metrics` to public-path bypass — fixes the pre-existing bug flagged in §2 note †)
- `services/api/internal/middleware/authz.go` (new — `Require` decorator)
- `services/api/internal/api/handler.go` (convert relevant `mux.HandleFunc` to `mux.Handle(path, Require(perm, store, http.HandlerFunc(handler)))`; `DismissedBy` now uses `dismissActor()` which calls `middleware.UserID`)
- `services/api/internal/api/me.go` (new — `GET /v1/me` handler)
- `services/api/internal/api/memberships.go` (new — invite, list, patch-role, delete with self-leave branch, transfer-ownership)
- `services/api/cmd/main.go` (seed dev user + dev owner membership under `DEV_MODE`; register `/v1/me` route)

**Frontend**

- `services/dashboard/src/pages/Users.jsx` (new — admin-only user management)
- `services/dashboard/src/pages/Me.jsx` or equivalent (new — thin wrapper around `/v1/me` for context provider)
- `services/dashboard/src/lib/api.ts` or equivalent (intercept 403 → call `/v1/me` → re-render; if `/v1/me` returns 403, redirect to removed-user screen)
- `services/dashboard/src/components/AppShell.jsx` (role-aware nav gating — hide `Connect AWS` from viewer; show `Users` only to owner/admin)

**Docs**

- `docs/rbac-design.md` (this doc)
- `docs/user_onboarding.md` (**delete lines 140–199** — the stale `admin`/`member`/`viewer` sketch using a non-existent `users.role` column — and replace with a short "see docs/rbac-design.md" pointer)
- `docs/auth.md` (add a one-line pointer from the auth flow to this doc; the vendor-independence rule at `docs/auth.md:233` underwrites §5's Kinde decision and should cross-link)
