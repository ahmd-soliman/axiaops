# Platform Admin Portal — Phase 2 Implementation Plan & Status

Status: **Slices 1–7 implemented** (backend + staff management). Slice 8 (admin
UI) deferred — the JSON surface is curl-demoable behind restricted ingress.

Implements §10 step 2 of [`saas-platform-admin-design.md`](saas-platform-admin-design.md)
— *staff identity + read-only admin console*, the foundation only. Sits under
ADR-0002 (SaaS-first), which is
**Proposed, not Accepted** — this foundation is self-hosted-safe (a separate
binary self-hosted simply never deploys), so it does not force the ADR.

---

## Reconciliation findings (design doc vs. actual tree)

- **R0 — ADR-0002 is not accepted.** Foundation is built self-hosted-safe.
- **R1 — `cmd/api-selfhosted` / `cmd/api-saashosted` do NOT exist.** The design
  doc's "four composition roots" is aspirational; the tree has exactly one
  (`services/api/cmd/main.go`). `cmd/api-admin` is therefore the **first real
  second composition root**, not a "fourth sibling."
- **R2 — `serverbuild.ComposeServer` is real;** `ComposeAdminServer` is the new
  sibling.
- **R3 — `auth.Provider` is a genuine single-method seam,** but `WrapNative`
  copies an OrganizationID onto context and the tenant chain assumes a bound
  org. A staff principal has none — hence a **staff-flavoured** auth chain
  (`staff.WrapStaff` + `staff.Provider`), not reuse of the tenant `WrapNative`.
  This is the decisive reason the admin console is a **separate binary**, not a
  handler-group bolted into `ComposeServer`.
- **R4 — Migration number:** this work took **032** (highest was 031). If
  the self-signup email-verification migration lands, it rebases to 033.
- **R5 — RLS-bypass grants:** the staff tables have **no RLS** (system tables
  like `users`/`sessions`). Migration 029's blanket `GRANT ... ON ALL TABLES` +
  `ALTER DEFAULT PRIVILEGES` already covers them on the `axiaops_runtime` admin
  pool; the dynamic `_runtime_bypass` policy loop only touches RLS tables and
  correctly skips these. `TestRuntimeAdmin_PolicyCoversAllRLSTables` stays green.
- **R6 — Staff auth for the beta: native credentials,** reusing the argon2id
  `auth.Hash`/`auth.Verify` machinery. The design's end-state corporate IdP is a
  future `staff.Provider` impl behind the same seam — table + RBAC + console
  don't change.
- **R7 — "Entitlement summary" ≠ the `entitlements` table** (deferred, design
  §11.1 decision 3). Summary = org metadata + account count + last-scan
  aggregates from existing tables. The console surfaces `entitlement: null` to
  document the absence.

### Binary-vs-handler-group decision

A **second binary, `cmd/api-admin`**, calling a minimal `ComposeAdminServer`.
Reasoning: the tenant middleware chain is structurally org-bound (R3); a staff
principal has no org; bolting staff routes into `ComposeServer` either runs them
outside auth (fragile) or teaches the tenant chain a cross-plane concept the
design explicitly separates (§3). A second binary satisfies §4.3's
"separate blast radius / not internet-facing" cheaply (one extra task on a
restricted listener, same RDS + image), and the CI cost is the shape ADR-0002
already accepts.

---

## Implemented slices

| Slice | What | Key files |
|---|---|---|
| 1 | Migration 032: `staff_users` + `staff_role_grants` (system-scoped, no RLS) | `services/shared/storage/postgres/migrations/032_staff_identity.{up,down}.sql` |
| 2 | Staff storage methods + model | `services/shared/storage/storage_staff.go`, `services/shared/storage/postgres/staff.go`, `services/shared/model/staff.go` |
| 3 | `staff.Provider` + native staff login + cache-backed staff session | `services/api/internal/staff/{staff,session,provider,middleware,http,handler}.go` |
| 4 | `ComposeAdminServer` + `cmd/api-admin` binary | `services/api/internal/serverbuild/build_admin.go`, `services/api/cmd/api-admin/main.go` |
| 5 | Cross-org reads: `ListAllOrganizations` + `StaffTenantSummary` | `services/shared/storage/postgres/staff.go` |
| 6 | Read-only console endpoints | `services/api/internal/staff/tenants_handler.go` |
| 7 | Staff management (superadmin) + `seed-staff` bootstrap | `services/api/internal/staff/admin_handler.go`, `services/api/cmd/api-admin/seed.go` |

### Staff session: cache-backed (beta trade)

Staff sessions live in the shared cache (`staff:sess:<tokenHash>` → staff_user_id),
not a PG table — the admin plane is low-volume + internal, so a cache flush
logging staff out is acceptable and avoids a new table. The cache is the
in-memory impl when no Redis is wired, so dev works without a backend. A future
multi-replica admin plane swaps to a durable `staff_sessions` table behind the
same `staff.SessionManager`. Roles + status are re-read from the DB on every
request, so a revoked role / suspended account takes effect on the next request.

### Admin endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | /admin/auth/login | No (rate-limited) | Native staff login → `axiaops_staff_session` cookie. 401 `invalid_credentials` (collapsed). |
| POST | /admin/auth/logout | staff cookie | Revoke session + clear cookie. 204. |
| GET | /admin/me | staff | Current staff principal `{staff_user_id, email, name, roles}`. |
| GET | /admin/tenants | staff (any role) | List orgs — metadata only, NOT FinOps data (§7.5). |
| GET | /admin/tenants/{id} | staff (any role) | One org summary + `entitlement: null` placeholder. No zombie/cost detail (break-glass, deferred §5). |
| GET | /admin/staff | superadmin | List staff + grants. |
| POST | /admin/staff | superadmin | Create staff `{email,name,password,roles[]}`. 409 `staff_email_taken`. |
| POST | /admin/staff/{id}/roles | superadmin | Grant a role. |
| DELETE | /admin/staff/{id}/roles/{role} | superadmin | Revoke a role (last-superadmin guard → 409 `last_superadmin`). |

### Admin env vars (`cmd/api-admin`)

| Variable | Required | Default | Notes |
|---|---|---|---|
| DATABASE_URL | Yes | — | RLS app pool (same as tenant API). |
| RUNTIME_ADMIN_DATABASE_URL | Yes | — | RLS-bypass pool — mandatory (no DEV_MODE collapse); the admin plane reads system + cross-org tables on it. |
| ADMIN_API_ADDR | No | :8090 | Admin HTTP listen address. |
| STAFF_SESSION_TTL_HOURS | No | 8 | Staff session lifetime. |
| ADMIN_CORS_ORIGIN | No | — | Reflected (credentialed) origin for a separate admin-UI dev origin. Empty → same-origin only. |
| REDIS_URL | No | — | Cache backend for staff sessions + login rate-limit. Empty → in-memory (single-replica only). |

Bootstrap the first superadmin:

```
api-admin seed-staff --email you@axiaops.io --name "You" --password '…'
# or: STAFF_SEED_PASSWORD=… api-admin seed-staff --email … --name …
```

### Local dev

`make start-dev` starts the admin plane alongside the tenant stack (host-mode,
`scripts/start.sh`) on **:8090**. It boots with zero staff; mint the first
superadmin with **`make seed-staff`** (idempotent — re-running is a no-op;
override `STAFF_EMAIL` / `STAFF_NAME` / `STAFF_PASSWORD`). The admin plane needs
`RUNTIME_ADMIN_DATABASE_URL` (no DEV_MODE collapse) — `start.sh` exports the
local `axiaops_runtime` creds already. Then `POST /admin/auth/login` → cookie →
`GET /admin/tenants`.

---

## Deliberately deferred (later phases / open decisions)

- **`entitlements` table + per-tenant plan/status/limits** — design §11.1
  decision 3 (no billing plumbing before PLG validates). Summary uses existing
  metadata; the console shows `entitlement: null`.
- **Break-glass cross-tenant grants + impersonation** (`staff_access_grants`,
  `staff_access_log`, `WithStaffGrant`) — §5 write/act paths. This slice is
  read-only metadata, which §7.5 says is NOT a break-glass tenant-data read.
- **Tenant FinOps-data reads in the console** (zombie/cost detail) — gated
  behind break-glass, deferred with it.
- **Internal-ops notifications** (`system_notification_channels`) — §6 / §10
  step 4.
- **Corporate staff IdP (OIDC)** — deferred behind the `staff.Provider` seam.
- **Admin UI** (Slice 8) — JSON surface is curl-demoable behind restricted
  ingress for the internal beta.
- **Separate AWS account/VPC for the admin plane** — §11.2 #8 open; beta = same
  VPC/RDS, separate binary + restricted ingress.
- **Auditor/engineering staff tier** — §11.2 #5 additive; baseline
  support/ops/billing/superadmin is CHECK-enforced.
