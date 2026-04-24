# RBAC Design — AxiaOps

Status: proposed, not implemented.
Supersedes the role sketch in `docs/user_onboarding.md`.

---

## 1. Goals & Non-Goals

### Goals (v1)

- Authorization within a tenant. Every current endpoint gets a required permission. Unauthorized users receive `403 Forbidden`.
- Four roles with clear, non-overlapping semantics: `owner`, `admin`, `member`, `viewer`.
- Role is a **property of (user, tenant)**, not a property of the user. A single Kinde user could in theory belong to multiple AxiaOps tenants with different roles. (Kinde supports multi-org per user; we preserve that.)
- Enforcement at the HTTP handler layer via a decorator. No change to the `storage.Store` interface. No change to RLS.
- Admin UX to invite, promote, demote, and remove users.
- Safe rollout: all existing users become `admin` on the v1 ship (no regression in capabilities).

### Non-goals (v1)

- **Per-cloud-account scoping.** ("This user can only see the `dev` account.") Real requirement for FinOps but deferred to v2. Schema is designed to extend without a painful migration.
- **Custom roles.** No `CREATE ROLE ... GRANT permission`. The four roles are hardcoded.
- **API keys / service accounts** as first-class principals with their own roles. Deferred to v2. The ingestion service talks to the API/DB as trusted infrastructure, not as a "user."
- **SSO-driven role provisioning.** Kinde org-roles are not mapped to AxiaOps roles. Admins assign roles manually in v1.
- **Audit log.** Deferred to v2. `dismissed_zombies.dismissed_by` already captures the most sensitive action; broader audit can come with the security track.
- **Resource-level permissions** (per-zombie dismiss permissions, per-snapshot export, etc.). Dismissals are tenant-wide.
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
| `owner` | Controls the tenant itself: billing, delete org, transfer ownership. Exactly one (or a small number — see §8). | Founder, primary account holder. |
| `admin` | Full operational control inside the tenant. Manages users, cloud accounts, dismissals. | Engineering lead, platform owner. |
| `member` | Daily FinOps work: connect/update cloud accounts, trigger scans, dismiss/snooze zombies. | Platform engineer, SRE. |
| `viewer` | Read-only. Sees zombies, summary, trends, costs. Cannot mutate anything. | Finance analyst, contractor observer, exec. |

**Capability matrix — current endpoints**

Columns: endpoints as registered in `services/api/internal/api/handler.go:40-58`. Rows: roles.

| Endpoint | `viewer` | `member` | `admin` | `owner` |
|---|---|---|---|---|
| `GET /health` | public | public | public | public |
| `GET /metrics` | internal | internal | internal | internal |
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
| `DELETE /v1/memberships/{id}` — new | — | — | admin* | yes |
| `POST /v1/tenants/transfer-ownership` — new | — | — | — | yes |

*\* admin can promote/demote `member`↔`viewer`, and invite at member/viewer level. Only `owner` can promote to or demote from `admin`. This prevents an `admin` from creating another `admin` and escalating permanently.*

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

tenant:transfer        # transfer ownership
tenant:delete          # future
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
        "members:manage_admin", "tenant:transfer",
    ),
}
```

Where `perms()` is a helper that unions a role's list with all lower roles. The inheritance happens at package-init time so runtime lookups are `O(1)`.

### Extensibility

When a new endpoint is added, add the permission string and slot it into the right role. No migration. That's the whole point.

---

## 4. Data Model Changes

### New table: `memberships`

```
memberships
├── id          TEXT  PRIMARY KEY         -- UUID
├── tenant_id   TEXT  NOT NULL REFERENCES tenants(id)  ON DELETE CASCADE
├── user_id     TEXT  NOT NULL REFERENCES users(id)    ON DELETE CASCADE
├── role        TEXT  NOT NULL CHECK (role IN ('owner','admin','member','viewer'))
├── invited_by  TEXT                                    -- users.id, nullable for the first owner
├── created_at  TIMESTAMPTZ NOT NULL
├── updated_at  TIMESTAMPTZ NOT NULL
└── UNIQUE (tenant_id, user_id)
```

- `UNIQUE(tenant_id, user_id)` — a user has at most one role per tenant.
- Not `users.role` because: (a) a user can belong to multiple tenants with different roles (Kinde supports this), (b) lets us delete membership without touching the user row.
- RLS: `tenant_id = current_setting('app.tenant_id', true)` USING + WITH CHECK (same pattern as every other table — see `migrations/011_add_rls_with_check.up.sql`).

### No changes to `users` or `tenants`

The existing `users` row is still populated on every authenticated request by `auth.go:152-164`. Membership is a pure join. Auth middleware gains one extra query: "given this tenant and user, what role?"

### Migration shape

`services/shared/storage/postgres/migrations/013_memberships.up.sql`:

1. `CREATE TABLE memberships (...)`
2. `ALTER TABLE memberships ENABLE ROW LEVEL SECURITY`
3. `CREATE POLICY memberships_tenant_isolation ON memberships USING (...) WITH CHECK (...)`
4. `GRANT SELECT, INSERT, UPDATE, DELETE ON memberships TO axiaops`
5. **Backfill:** `INSERT INTO memberships (id, tenant_id, user_id, role, created_at, updated_at) SELECT gen_random_uuid(), tenant_id, id, 'admin', NOW(), NOW() FROM users;` — every existing user becomes `admin`.
6. **Promote one user per tenant to `owner`:** for each tenant, the user with the earliest `created_at` gets `UPDATE memberships SET role='owner' WHERE ...`. Single SQL with a CTE; not a long-running migration given current tenant count.

`services/shared/storage/postgres/migrations/013_memberships.down.sql` drops the table.

---

## 5. Kinde Integration Strategy

**Recommendation: manage roles in our own DB. Kinde is authn only.**

### The fork in the road

Kinde supports org-scoped roles and permissions natively — you can define roles in the Kinde dashboard and they come back in the JWT as `roles` / `permissions` claims. Tempting to use.

### Why we don't

1. **Vendor independence is a stated goal.** `docs/auth.md` explicitly calls out "Never put Kinde-specific claims in your business logic — only extract `tenant_id` in the middleware layer and pass it down as a plain string." Pulling roles from Kinde violates that.
2. **No round-trip.** Role changes from the admin UI would have to call the Kinde Management API, wait, and then users would need to re-login for their JWT to refresh. With local roles: one `UPDATE memberships` and the next request sees the new role.
3. **We already have `users` and `tenants`.** Adding a `memberships` table is natural. Maintaining role state in two places (Kinde + our DB) is the worst option.
4. **Testing.** Local roles are testable with `httptest` and a `Store` mock, which is the established pattern (see `handler_test.go`). Kinde-sourced roles would require a mock JWT per role or a Kinde API mock.
5. **The permission vocabulary is ours.** `zombies:dismiss` is not a Kinde concept. Storing roles in Kinde but permissions in our code still means we'd fetch the role from the JWT and translate. If we're translating anyway, the source of truth might as well be our DB.

### What Kinde still does

- Authentication (identity + JWT issuance). No change.
- Org-switching UX (Kinde's built-in dashboard). No change.
- SSO provisioning (eventually). A SAML claim could seed a default role — but that's a v2 concern. v1: every invited user starts as `member` (or whatever the inviter specifies).

### Alternative: Kinde-native roles (the road not taken)

This section exists so the decision is visible. If priorities change, this is the variant you'd build instead.

**Shape of the implementation**

1. Define four roles in the Kinde dashboard: `owner`, `admin`, `member`, `viewer`. Kinde scopes them per-organization, which matches our per-tenant model.
2. Attach them as custom claims in the Kinde token (Kinde supports this via the "Token customization" settings — they appear as a `roles` array claim on the JWT).
3. In `auth.go`, after verifying the JWT, extract `claims.Roles[0]` and stash it on the request context next to `tenant_id`.
4. `middleware.Require(perm)` reads the role from context (not from a DB query) and checks against the same `rolePermissions` map described in §3. The permission vocabulary still lives in Go.
5. User management UI: a "Manage users" button in the dashboard deep-links to Kinde's hosted org-management page. No in-app user admin screens are built.
6. Role changes happen in Kinde. To take effect, the affected user must refresh their JWT (re-login, or wait for token expiry + silent refresh).
7. No `memberships` table. No migration 013. No new endpoints for member management.

**What you gain**

- **~2–3 days of implementation** saved. No table, no migration, no CRUD endpoints, no dashboard user-management screen.
- **SSO role provisioning comes free on day one.** Enterprise customer configures their SAML IdP to pass `role=admin` → Kinde maps it → it's in the JWT → it works. The DB-backed variant requires a custom mapping layer (§ Phase 2).
- **One fewer table to keep in sync with users/tenants.** Simpler data model.
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
        tid := TenantID(r.Context())
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

1. **No cache.** `SELECT role FROM memberships WHERE tenant_id = $1 AND user_id = $2` — single indexed lookup, sub-millisecond. Redis already caches JWKS; we're not running 10k RPS.
2. **Cache in the existing Redis `cache.Cache` with a short TTL** (e.g. 30s) under `role:{tenant_id}:{user_id}`. Invalidate on role change in the mutation handler. Defer until metrics show it matters.

**Recommendation: option 1 for v1.** Premature.

### How it composes with RLS

RLS is still the last line of defence for tenant isolation. RBAC filters **what a user in tenant X can do to tenant X's data**. RLS filters **which tenant's data you touch at all**. They're orthogonal. The `middleware.Require` check runs **after** `Auth.Wrap` has set `tenant_id` and **before** the handler runs `storage.WithTenantID(...)`. Nothing changes in the Store.

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
| `GET /health` | *(none)* | public, see `auth.go:115` |
| `GET /metrics` | *(none)* | Prometheus scrape, internal |
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
| `POST /v1/dismissals` | `zombies:dismiss` | `DismissedBy` should become `user.email` not `tenant_id` — see §8 |
| `DELETE /v1/dismissals/{id}` | `zombies:dismiss` | |
| `GET /v1/dismissals` | `zombies:read` | read-only listing |
| `GET /v1/memberships` | `members:read` | new |
| `POST /v1/memberships` | `members:invite` | new, admin+ |
| `PATCH /v1/memberships/{id}/role` | `members:manage_basic` or `members:manage_admin` | new; handler picks based on target role |
| `DELETE /v1/memberships/{id}` | `members:manage_basic` | new, admin+; last-admin guard in handler |
| `POST /v1/tenants/transfer-ownership` | `tenant:transfer` | new, owner only |

---

## 8. Edge Cases & Considerations

### Last-admin protection

- **Rule:** a tenant must always have at least one `owner`. A `DELETE /v1/memberships/{id}` or role-demotion that would leave zero owners returns `409 Conflict`.
- **Self-demotion:** allowed only if another owner exists.
- **Owner deletion:** to remove the only owner, the owner must first `POST /v1/tenants/transfer-ownership` to another admin/member, which in one transaction demotes current user to `admin` and promotes target to `owner`.

### `DismissedBy` is currently `tenant_id` — fix it

In `handler.go:616`, `DismissedBy: middleware.TenantID(r.Context())`. With RBAC we also have a user identity. Change to `middleware.UserEmail(r.Context())` or `user_id`. Not strictly an RBAC change, but it's the first place "who did this" becomes visible.

### `DEV_MODE`

`DEV_MODE=true` bypasses auth (`auth.go:185` → `DevBypass`). The dev-mode user has no JWT, no `kinde_sub`, no role row. Handling:

- **At startup in `main.go`:** after `store.EnsureTenant(ctx, devTenantID, ...)`, also ensure a single dev user and a single membership with `role=owner`. New helper: `store.EnsureDevMembership(ctx, tenantID, userID, role)`.
- **In `DevBypass`:** set both `tenant_id` **and** `user_id` on the context.
- **In the `Require` middleware:** no branching on dev mode — it just looks up the role, finds `owner`, and allows everything. Keeps prod and dev code paths identical.

### Pending invitations

v1 flow:

1. Admin calls `POST /v1/memberships` with `{email, role}`.
2. We check if a user with that email has ever logged in (`users.email`). If yes, membership row created immediately against that `user_id`.
3. If no, we create a **pending** row — same table, `user_id = NULL`, plus an `invited_email TEXT NULL` column.
4. Next time someone logs in with that email, `UpsertUser` (in `auth.go:160`) attaches the `user_id` to the pending membership.

Adds one nullable column and one index. No separate `invitations` table. Keeps the "what roles does this user have" query to a single table.

*(This is a small v1 scope creep. Alternative: hard-require the invitee to have logged in at least once, making invite a two-step admin workflow. Either is fine — recommend the pending-row approach because invite-first is the UX every competitor ships.)*

### Self-service user management

- **Admins invite `member`/`viewer`.** Admins cannot create `admin`.
- **Only owners invite or promote to `admin`.**
- **Owners transfer ownership explicitly** (not by inviting a second owner — there's at most one owner at a time). *(Alternate: allow multiple owners; simpler but loses the "one throat to choke" semantic. Recommend: single owner for v1, revisit if customers push back.)*

### Audit logging

Deferred to v2. For v1, the existing `slog.Info` calls on mutating handlers are sufficient (they already log `user_id` through request context once §7 is applied). When a proper audit log ships, it will subscribe to the same slog stream or tap into a DB trigger.

### API keys / service accounts

Deferred to v2. Current architecture: the ingestion service writes zombies with `ListAllAccounts` (`storage.go:82`) — explicitly documented as "trusted internal code." That's the only non-human principal today and it doesn't need a role. The v2 API-key story will be "issue a token bound to a membership with a specific role" — the memberships table already supports this shape (`user_id` points to a user record of type `service_account`).

### New tenant bootstrap

Migration 013 handles existing tenants by promoting the earliest-created user to `owner`. But when a **new** Kinde org signs up after v1 ships, the first authenticating user has no membership row at all.

**Rule:** on first login to a tenant with zero memberships, the authenticating user is auto-promoted to `owner`. Implemented in `auth.go` after `UpsertUser`:

1. `SELECT COUNT(*) FROM memberships WHERE tenant_id = $1`.
2. If zero → INSERT membership with `role='owner'` for the current user.
3. If non-zero and no membership exists for this user → they've joined a Kinde org they haven't been invited to in AxiaOps. Return 403 with "contact your organization admin".

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

Rule: if **any** rows exist for a membership, the user is restricted to those accounts. Zero rows = tenant-wide (default for everyone in v1). This lets v2 ship without migrating existing memberships — they just have no scope rows.

Filtering applies in the handler (add `WHERE account_id IN (scoped_ids)` to list queries) not in RLS (RLS stays purely tenant-scoped). Keeps RLS simple.

---

## 9. Migration & Rollout

### Ship sequence

1. **Migration 013:** create `memberships`, enable RLS, backfill all existing users as `admin`, elevate earliest-created user per tenant to `owner`.
2. **Extend `Store` interface** with `RoleOf`, `ListMemberships`, `SaveMembership`, `DeleteMembership`, `EnsureDevMembership`. Postgres impl.
3. **Add `authz` package** in `services/shared/authz/` — roles, permissions, `Allows(role, perm)`.
4. **Add `middleware.Require`** in `services/api/internal/middleware/authz.go`.
5. **Extend `auth.go`** to set `user_id` on the request context (currently only `tenant_id` is set).
6. **Wire `Require` into `Handler.Register`** — all current endpoints get a permission.
7. **Add membership endpoints** (invite, list, promote, demote, remove, transfer ownership).
8. **Dashboard:** add a `/settings/users` screen (owner/admin only). Hide `Connect AWS` nav from `viewer`.

### Feature flag

Not recommended. Once the migration runs, every existing user has role `admin`, which is equivalent to the pre-RBAC "everyone can do everything" behaviour. There is no behaviour gap during rollout — turn it on directly.

### Post-ship verification

- Integration test: tenant A `member` cannot DELETE `/v1/accounts/...` even for their own tenant.
- Integration test: tenant A `admin` cannot see tenant B data (RLS still works under RBAC).
- Integration test: last-owner demotion returns 409.

### Rolling back

Drop migration 013. Remove `Require` wrappers. Since the backfill defaults everyone to `admin`, the pre-RBAC world is fully restored. No data loss.

---

## 10. Phased Implementation Plan

### Phase 1 (MVP RBAC — this doc)

- `memberships` table + migration 013
- `authz` package (roles, permissions, `Allows`)
- `middleware.Require`
- User context propagation (auth.go adds `user_id` to ctx)
- All current endpoints mapped to permissions
- Membership endpoints: invite, list, promote/demote, remove, transfer-ownership
- Last-admin/last-owner guards
- Dashboard user-management screen
- `DEV_MODE` seeds an owner membership

**Estimated effort:** ~3–5 days for a single developer. Biggest risk is the membership endpoint handlers and their edge-case tests (last-owner, self-demotion, pending invitations), not the decorator plumbing.

### Phase 2 (post-MVP)

- Audit log (new `audit_events` table, written from mutation handlers)
- API keys / service-account memberships
- Per-cloud-account scoping (`membership_account_scopes` table + handler filtering)
- SSO-driven default-role mapping (Kinde org-role → AxiaOps role on first login)
- Email-based invitations (v1 creates pending rows; v2 sends the actual email via Kinde or SES)
- Billing admin role (ships alongside the subscription/billing feature)
- Internal / staff roles — see §11 (triggered by second hire, first paying customer, or billing system shipping)

### Phase 3 (speculative)

- Custom roles
- IP allow-listing per membership
- Per-feature permission overrides (explicit grants/denies)

Do not build any of phase 3 without a named customer.

---

## 11. Internal / Staff Roles (out of v1 scope)

**Scope:** This section documents the *future* shape of AxiaOps-employee access. None of it is v1. It exists so that when the trigger conditions arrive, the pattern is decided and you're not building it ad-hoc under pressure.

### Why staff can't share the tenant RBAC system

Staff (AxiaOps employees: support, engineering, billing ops) are not customers and don't belong to any tenant. Three hard requirements make them incompatible with the `memberships` table:

1. **Cross-tenant access.** Support reads tenant X to answer a ticket. RLS as designed forbids this. Staff need a documented RLS bypass.
2. **Mandatory audit.** Every staff touch on customer data must be logged. The tenant-side audit deferred in §8 is optional; this is not (SOC2, GDPR).
3. **Impersonation.** Engineers debug from the customer's perspective. The session must carry *both* the real staff ID and the acted-as tenant — never losing the real identity.

### The four staff roles

| Role | Intent | Access | Notable restriction |
|---|---|---|---|
| `staff_support` | Answer support tickets | Read-only across all tenants | Cannot modify customer data |
| `staff_engineer` | Debug issues, reproduce bugs | Read-write across all tenants, impersonation | Every action audited with ticket reference |
| `staff_billing` | Subscription + billing operations | Read/write on tenant subscription state only | Explicitly **no access** to cost/zombie data |
| `staff_admin` | Onboarding, tenant suspension, emergency intervention | Full | Time-boxed, break-glass flow |

`staff_billing` segregation matters under GDPR: the billing person has no business reason to see specific resource names or cost breakdowns. This is a real audit finding in SOC2.

### Architecture: separate `staff_users` table, separate auth flow

- **New table:** `staff_users(id, email, role, created_at, ...)`. Independent of `users`.
- **Separate Kinde org:** "AxiaOps Internal". Hard boundary — customer tokens cannot authenticate as staff.
- **Separate middleware chain:** `/internal/*` routes go through `StaffAuth` which validates against the internal Kinde org and populates `staff_id` on the context. Customer `/v1/*` routes unchanged.
- **RLS bypass:** staff queries use a dedicated connection pool running as `axiaops_staff` (a role between `axiaops` and `axiaops_owner`) with RLS bypass for SELECT. Writes go through explicit `SET app.tenant_id = 'xyz'` so they still land in the right tenant.

Alternatives rejected: `is_staff bool` on `users` (simpler, messier isolation; RLS becomes conditional) and `memberships` with `tenant_id = NULL` (clever, confusing semantics).

### Impersonation flow

1. `staff_engineer` visits `/internal/tenants/{id}/impersonate` with a ticket reference.
2. Server writes an `audit_event` with `{staff_id, tenant_id, action: 'impersonate_start', ticket_ref}`.
3. Response sets a short-lived session cookie with `acting_as_tenant_id`.
4. Subsequent `/v1/*` requests bind tenant context to the acted-as tenant **but** audit-log entries retain the real `staff_id`.
5. Dashboard displays a persistent banner ("Impersonating customer X — [Exit]").
6. Session auto-expires after 1 hour. Exit button writes `action: 'impersonate_end'`.

The existing tenant-scoped dashboard UI works unchanged — it just renders against a tenant it doesn't really own.

### Break-glass for `staff_admin`

Nobody holds `staff_admin` permanently. Grant via a separate flow:

- Slack command with justification (`/break-glass incident-456 "investigating data corruption"`).
- Second staff member approves (two-person rule).
- Grant time-boxed to 1 hour.
- Every action while elevated logged + Slack notification to `#security`.
- Auto-revokes on expiry.

For a solo-founder company this is overkill — you'd grant yourself permanent `staff_admin`. The pattern exists for when the team grows past 3 people.

### DEV_MODE and staff roles

`DEV_MODE` in §8 covers tenant roles by seeding an `owner` membership. Staff routes need a parallel treatment:

- **Dev mode is tenant-scoped, not staff-scoped.** `DEV_MODE=true` bypasses customer auth — the developer is acting as a tenant user. It does **not** grant staff access. `/internal/*` routes should stay reachable only by explicit staff auth even in dev.
- **For local staff-route development:** a separate `DEV_STAFF_MODE=true` flag (or reuse `DEV_MODE` with a second env var `DEV_STAFF_ROLE=staff_engineer`) bypasses the staff auth chain and sets `staff_id=dev-staff` + `staff_role=<configured>` on the context. Defaults to off so staff routes aren't accidentally exposed in dev builds that hit real customer data.
- **Impersonation in dev mode:** skip. The dev user already has owner access to `dev-tenant-axiaops`. There's nothing to impersonate into.
- **Mutual exclusion:** a request is either authenticated as a tenant user *or* as staff — never both. Middleware should fail fast if both context keys are set.

### What this looks like today (pre-implementation)

Direct DB access via `psql` using `MIGRATION_DATABASE_URL`. No auth, no audit, no impersonation. Fine for zero customers and one developer. Stops being fine at the first trigger condition.

### Triggers that force implementation

- Second full-time person joins AxiaOps (needs scoped access, not the owner DB URL).
- First paying customer (staff access now crosses a trust boundary).
- Support volume exceeds "I can SSH into prod and figure it out".
- Billing system ships (`staff_billing` is a prerequisite, not a follow-up).

### Estimated effort when the time comes

~2 weeks for one developer: 3–4 days for `staff_users` + auth + separate Kinde org, 3–4 days for impersonation + dual-identity audit, 2–3 days for break-glass + Slack integration, 2–3 days for staff dashboard.

---

## Appendix A — Files that change

- `services/shared/storage/postgres/migrations/013_memberships.up.sql` (new)
- `services/shared/storage/postgres/migrations/013_memberships.down.sql` (new)
- `services/shared/model/membership.go` (new)
- `services/shared/storage/storage.go` (extend `Store` interface)
- `services/shared/storage/postgres/postgres.go` (implement new methods)
- `services/shared/authz/roles.go` (new)
- `services/shared/authz/roles_test.go` (new)
- `services/api/internal/middleware/auth.go` (add `UserID` context key; set it after `UpsertUser`)
- `services/api/internal/middleware/authz.go` (new — `Require` decorator)
- `services/api/internal/api/handler.go` (wire `Require` into `Register`, fix `DismissedBy` to use user email)
- `services/api/internal/api/memberships.go` (new — membership handlers)
- `services/api/cmd/main.go` (seed dev owner membership under `DEV_MODE`)
- `services/dashboard/src/pages/Users.jsx` (new — admin-only user management)
- `services/dashboard/src/components/AppShell.jsx` (role-aware nav gating)
- `docs/rbac-design.md` (this doc)
- `docs/user_onboarding.md` (mark stale sections; reference this doc)
