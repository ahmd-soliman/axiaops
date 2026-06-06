# Admin-Plane Web Bootstrap — Design

**Status:** Proposed | **Last Updated:** June 2026 | **Owner:** platform-admin
**Related:** [`admin-portal-plan.md`](admin-portal-plan.md) · [`saas-platform-admin-design.md`](saas-platform-admin-design.md) · [`native-auth-bootstrap.md`](native-auth-bootstrap.md) (the tenant ceremony this mirrors)

## TL;DR

Today the admin plane (`cmd/api-admin`) is bootstrapped **only** by the
`seed-staff` CLI, which writes the first superadmin **directly to the DB**
(`store.CreateStaffUser`) — no HTTP, no UI. That's correct and prod-safe, but
it's a host-shell-only flow with no operator UX.

This adds a **token-gated web bootstrap ceremony** for the admin UI, mirroring
the tenant's install-token → `POST /auth/bootstrap` flow. **`seed-staff` stays**
as the headless / CI / automation path. Both mint the first superadmin through
the same `store.CreateStaffUser` — they're two front-ends to one operation.

It is deliberately small: ~one handler, a token seam (reused from the tenant), a
state probe, and one dashboard-admin screen.

## Why add it (the bootstrap and access paths are decoupled)

- **Operator UX parity** with the tenant: a fresh admin install presents a
  "create the first superadmin" screen instead of requiring `docker exec … api-admin seed-staff`.
- **It does not weaken the security posture** *because the admin ingress is
  private* (see [admin ingress decision](#preconditions--security-model)). The
  bootstrap endpoint is reachable only where the admin plane is — and the admin
  plane is never on the public internet. So this is **not** a new public surface;
  it's a private, token-gated, single-use endpoint.
- The earlier "CLI-only" rationale (admin is private + ops-bootstrapped → no
  public endpoint needed) still holds for the *threat model*; the web flow is
  **additive convenience on top of the same private boundary**, hardened the same
  way the tenant bootstrap is (token + sealing).

## Goals / Non-goals

**Goals**
- A `POST /admin/auth/bootstrap` endpoint that mints the **first superadmin** from
  `{token, email, name, password}`, gated by a single-use install token.
- A `GET /admin/auth/bootstrap/state` probe so the admin SPA auto-redirects to a
  bootstrap screen on a fresh install.
- A dashboard-admin **Bootstrap screen** mirroring the tenant's.
- Keep `seed-staff` as the headless/testing path, unchanged in behaviour.

**Non-goals**
- No organization creation (staff are **org-less** — unlike the tenant owner).
- No change to the auth model (still native argon2id staff creds).
- No public exposure of the admin plane — the private-ingress requirement stands.
- Not SSO bootstrap — the corporate-IdP `staff.Provider` end-state is separate.

## Design

Mirror the tenant ceremony (`services/api/internal/auth` + `serverbuild`), scoped
to the staff plane and composed in `ComposeAdminServer` (`build_admin.go`).

### 1. Install token

Identical mechanism to the tenant, with admin-scoped names so the two planes
never share a token:

- On first boot, `api-admin` writes a random install token to
  `ADMIN_BOOTSTRAP_TOKEN_FILE_PATH` (default `/var/run/axiaops/admin_initial_setup_token`,
  mode `0600`), **unless** already sealed (a superadmin exists) — same as the
  tenant's file write.
- `ADMIN_BOOTSTRAP_INSTALL_TOKEN` env overrides the generated token (unattended
  installs / CI). When set, the file/banner is suppressed.
- `ADMIN_BOOTSTRAP_PRINT_BANNER` (default `false`, default-secure) — when `true`,
  print the token to stdout for ephemeral local dev; otherwise file-only.
- Token is **deleted** from the file on first successful bootstrap.

### 2. Endpoints (composed only in `ComposeAdminServer`)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/admin/auth/bootstrap/state` | No | `{available: bool}` — true iff bootstrap would succeed (no superadmin yet). Drives the SPA's mount-time redirect to `/bootstrap`. |
| POST | `/admin/auth/bootstrap` | No | Body `{token, email, name, password}`. Verifies the install token, mints the first superadmin via `store.CreateStaffUser` (`role=superadmin`), mints a staff session + sets `axiaops_staff_session`. **Sealed** afterward. |

Both are **public-relative-to-the-admin-plane** (no staff session required —
there are none yet) but, like everything in the admin plane, only reachable
through its private ingress.

### 3. Sealing — "zero superadmins" is the gate

The tenant uses a `bootstrap_state` singleton row. The admin plane has a more
natural seal: **bootstrap is available iff no superadmin staff exists.**

- `state.available = (count of superadmin staff == 0)`.
- `POST /admin/auth/bootstrap`: require `token valid` **AND** `zero superadmins`;
  on success the new superadmin exists → subsequent calls `409 already_bootstrapped`.
- This needs **no new table** — the `staff_users` table is the seal.

**Trade-off vs. the tenant's permanent seal:** "zero superadmins" means that if
every superadmin is ever removed, bootstrap re-opens (still token-gated). The
admin plane already has a **last-superadmin guard** (`409 last_superadmin` on
delete/demote), so zero-superadmins is only reachable via direct DB surgery —
making this a deliberate **break-glass recovery path**, not an accidental
re-open. If a permanent one-time seal is preferred instead, add a
`staff_bootstrap_state` singleton mirroring the tenant (one extra migration). The
recommendation is the zero-superadmin seal for the recovery property.

### 4. Dashboard-admin UI

- A **Bootstrap screen** mirroring the tenant's (`set email / name / password`),
  posting to `/admin/auth/bootstrap`.
- Mount-time: the SPA calls `GET /admin/auth/bootstrap/state`; if `available`,
  redirect `/login → /bootstrap` (the tenant already does this — Tasks.md 2.7.16).
- After success: session cookie is set → land on the staff console.

### 5. `seed-staff` — retained for headless / testing

`seed-staff` is **kept, unchanged**. It is the right tool when there is no
browser / operator in the loop:

- **CI / integration tests** — the e2e admin tests need a superadmin without a UI;
  `seed-staff` (or `STAFF_SEED_PASSWORD` env) mints one deterministically.
- **Unattended installs** — same role as `ADMIN_BOOTSTRAP_INSTALL_TOKEN` but
  fully scriptable (no token-exchange round trip).
- **Recovery** — re-mint a superadmin from a shell when the web flow isn't usable.

Both paths converge on `store.CreateStaffUser`, so they can't drift. The web flow
is the **operator default**; `seed-staff` is the **programmatic** path. The
`state.available` seal applies to both (seed after a superadmin exists → the same
`ErrStaffEmailExists` / sealed behaviour).

## Preconditions & security model

This design is **safe only under the standing admin-ingress requirement**: the
admin plane is **never internet-facing** — private ingress via internal ALB +
SSM port-forward, Tailscale, or SG-restricted ingress (see the admin-portal
plan + the ingress fork: *Express+edge-gate vs normal-ECS+internal-ALB*). Given
that:

- The bootstrap endpoint sits **behind the private boundary** — not a public
  surface.
- It is **token-gated** (install token, file `0600`, host access required) — even
  inside the private network, only the operator who deployed the box can bootstrap.
- It is **single-use / sealed** (zero-superadmin gate) — no replay.
- The token is **deleted on success** and never logged (default `PRINT_BANNER=false`).

So the web bootstrap reproduces the tenant's hardening (token + seal) on a surface
that is *additionally* network-private. If the admin plane were ever public, this
endpoint would still be token-gated + sealed (no worse than the tenant's public
bootstrap), but the private-ingress invariant is the primary control and must hold.

## Differences from the tenant bootstrap

| | Tenant `/auth/bootstrap` | Admin `/admin/auth/bootstrap` |
|---|---|---|
| Creates | first **owner** + **organization** | first **superadmin** (org-less) |
| Seal | `bootstrap_state` singleton (permanent) | zero-superadmin count (recovery-friendly) |
| Reachability | public internet | private ingress only |
| Token file | `/var/run/axiaops/initial_setup_token` | `/var/run/axiaops/admin_initial_setup_token` |
| Programmatic path | `BOOTSTRAP_INSTALL_TOKEN` | `seed-staff` CLI **and** `ADMIN_BOOTSTRAP_INSTALL_TOKEN` |
| License | n/a | n/a (admin plane is license-free) |

## Implementation plan (small)

1. **`services/api/internal/staff`** — add `BootstrapState()` + `Bootstrap(token, email, name, password)` on the staff service, reusing `internal/auth` argon2id + `store.CreateStaffUser(role=superadmin)`. Token verify mirrors the tenant's install-token helper (extract the shared bit if cheap, else copy ~30 lines).
2. **`serverbuild/build_admin.go`** — write the install token on boot (admin-scoped path/env), register `GET /admin/auth/bootstrap/state` + `POST /admin/auth/bootstrap`.
3. **`cmd/api-admin`** — env plumbing: `ADMIN_BOOTSTRAP_{INSTALL_TOKEN,TOKEN_FILE_PATH,PRINT_BANNER}`.
4. **`services/dashboard-admin`** — a Bootstrap screen + the mount-time `state` probe / redirect (mirror the tenant SPA).
5. **Tests** — `bootstrap → 200 + sealed (409 on replay)`, `bad token → 401`, `state flips available→false`. Keep the existing `seed-staff` tests.
6. **Docs** — update `services/api/CLAUDE.md` (Platform Admin Plane: add the two endpoints + env vars) and the admin-portal plan.

Estimated effort: **~1 focused day** — the tenant flow is the template, there's no
org creation, and `seed-staff` already provides the store seam.

## Open questions

1. **Seal semantics** — zero-superadmin (recovery-friendly, recommended) vs. a
   permanent `staff_bootstrap_state` singleton. Decision needed before build.
2. **Rate-limiting** — apply the tenant login limiter (10/min/IP) to
   `/admin/auth/bootstrap` too, even behind private ingress (defence in depth).
3. **Token TTL** — tenant install token is long-lived until redeemed; keep the
   same, or add an `ADMIN_BOOTSTRAP_TTL`? Default: long-lived, file-deleted on use.
