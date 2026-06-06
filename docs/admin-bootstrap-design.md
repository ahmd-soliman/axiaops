# Admin-Plane Web Bootstrap — Design

**Status:** Proposed | **Last Updated:** June 2026 | **Owner:** platform-admin
**Related:** [`admin-portal-plan.md`](admin-portal-plan.md) · [`saas-platform-admin-design.md`](saas-platform-admin-design.md) · [`native-auth-bootstrap.md`](native-auth-bootstrap.md) (the tenant ceremony this clones)

## TL;DR

Today the admin plane (`cmd/api-admin`) is bootstrapped **only** by the
`seed-staff` CLI, which writes the first superadmin **directly to the DB**
(`store.CreateStaffUser`) — no HTTP, no UI. That's prod-safe but host-shell-only.

This adds a **token-gated web bootstrap** for the admin UI by **faithfully
cloning the tenant's install-token → `POST /auth/bootstrap` flow**, scoped to the
staff plane. **It is a clone, not a new lighter design** — the tenant flow is
already audit-hardened and solves multi-replica, restart-durability, atomic
single-use, and ECS-ephemeral-FS, so the admin reuses those exact patterns rather
than re-deriving (worse) versions of them.

`seed-staff` **stays** as the headless / CI / recovery path. Both paths converge
on `store.CreateStaffUser`, so they can't drift.

## Why clone, not slim down

A prior draft proposed a lighter "no-table, zero-superadmin seal." Review against
the tenant code showed that shortcut **re-introduces three problems the tenant
already solved**:

1. **No persisted token hash** → the token would live in process memory →
   **breaks under multi-replica and under container restart-before-bootstrap**.
   The tenant persists the hash in PG and treats that row as the cluster-shared
   source of truth (`install_token.go:92-109`, the `won`-race flag).
2. **Non-transactional seal** → `count(superadmin)==0` then `CreateStaffUser` are
   two statements → two concurrent distinct-email POSTs both win → **two
   superadmins**. The tenant's `ConsumeBootstrapState` does verify+create+seal in
   **one tx** (`handler.go:325`).
3. **ECS ephemeral FS** → an in-memory token + a token file on a read-only root
   is a dead end. The tenant supports `TOKEN_FILE_PATH=""` to disable the file and
   fall back to an operator-supplied env token (`install_token.go:113-122`).

So the design below is "clone the tenant flow, here is the staff-scoped diff."

## Goals / Non-goals

**Goals**
- `POST /admin/auth/bootstrap` mints the **first superadmin** from
  `{token, email, name, password}` via a **transactional consume**, gated by a
  single-use install token whose hash is **persisted in PG**.
- `GET /admin/auth/bootstrap/state` → `{available}` so the admin SPA auto-redirects
  to a Bootstrap screen on a fresh install.
- A dashboard-admin **Bootstrap screen** mirroring the tenant's.
- Keep `seed-staff` as the headless/recovery path, unchanged.

**Non-goals**
- No organization creation (staff are **org-less**).
- No public exposure — the private-ingress requirement stands (see Security).
- Not SSO bootstrap (the corporate-IdP `staff.Provider` end-state is separate).

## Design — a staff-scoped clone of the tenant flow

### 1. Seal: a persisted `staff_bootstrap_state` singleton (mirror `bootstrap_state`)

One migration adds `staff_bootstrap_state` mirroring the tenant `bootstrap_state`:
a singleton row holding the **install-token hash** + host name, written at first
boot, **consumed (deleted) inside the bootstrap tx**. This is what gives us:
- a **durable token hash** (multi-replica + restart safe),
- an **atomic single-use** seal,
- a clean `available` predicate (`row exists && not yet consumed`).

> The earlier "zero-superadmin" seal is **rejected** as the primary mechanism — it
> forces the in-memory-token + race problems above. The **recovery** property it
> was chosen for already lives in `seed-staff` (which is seal-independent — see §5),
> so the web seal is the *strong*, permanent one.

### 2. Install token (mirror `MaybeGenerateInstallToken`, copied — not reused)

The tenant's `MaybeGenerateInstallToken` is **welded to `bootstrap_state` +
`CountOrganizations`** (`install_token.go:74-92`) and is **not** reusable as-is.
What we reuse vs. copy:
- **Reuse (pure helpers):** `HashToken`, `writeTokenFile` (mode `0600`),
  `removeInstallTokenFile`, `clearInstallTokenEnv`, `printInstallBanner`. Extract
  the pure ones to a shared spot if cheap; otherwise copy (~40 lines).
- **Copy + adapt:** a `MaybeGenerateAdminInstallToken` that gates on
  `staff_bootstrap_state` (not orgs), persists the hash via a new
  `CreateStaffBootstrapState`, and honours the same `won`-race / file / banner
  logic — including `TOKEN_FILE_PATH=""` to disable the file on ECS, the
  operator-supplied env override, and the **default-secure banner that never logs
  the token** (`install_token.go:132-147`).

Admin-scoped env (never shared with the tenant token):
`ADMIN_BOOTSTRAP_INSTALL_TOKEN`, `ADMIN_BOOTSTRAP_TOKEN_FILE_PATH`
(default `/var/run/axiaops/admin_initial_setup_token`), `ADMIN_BOOTSTRAP_PRINT_BANNER`.

**Where it runs:** the boot-time token mint goes in **`cmd/api-admin/main.go`**
(next to `openStore`), **not** in `ComposeAdminServer` — `build_admin.go` is
intentionally side-effect-free (mirrors the tenant, whose write is in
`cmd/main.go:205`, not `ComposeServer`).

### 3. Endpoints (registered in `ComposeAdminServer`, added to `publicAdminPath`)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/admin/auth/bootstrap/state` | none | `{available: bool}` — true iff `staff_bootstrap_state` exists & unconsumed. Drives the SPA redirect to `/bootstrap`. |
| POST | `/admin/auth/bootstrap` | none | `{token, email, name, password}` → **`ConsumeStaffBootstrap`** (one tx): verify token hash, create superadmin (`store.CreateStaffUser`, argon2id via `auth.Hash`), mint staff session, **delete the singleton**. Sets `axiaops_staff_session`. |

Both must be added to `publicAdminPath` (`staff/middleware.go:15`) or `WrapStaff`
401s them — there are no staff yet to authenticate.

### 4. `ConsumeStaffBootstrap` (mirror `ConsumeBootstrapState`)

A new store method doing, in **one tx**: compare `HashToken(token)` to the
singleton's hash; if absent → `ErrStaffBootstrapAlreadyDone`; if mismatch →
`ErrStaffBootstrapTokenMismatch`; insert the staff user (unique-email index is the
race backstop, returns `ErrStaffEmailExists`); insert the staff session; delete
the singleton. Returns the staff user + session.

### 5. Error taxonomy + metrics (mirror the tenant exactly)

| Store error | HTTP | code | `AdminBootstrapAttemptsTotal{outcome}` |
|---|---|---|---|
| `ErrStaffBootstrapAlreadyDone` | 409 | `bootstrap_already_done` | `sealed` |
| `ErrStaffBootstrapTokenMismatch` | 401 | `invalid_token` | `invalid_token` |
| `ErrStaffEmailExists` | 409 | `email_taken` | `email_taken` |
| success | 200 | — | `success` |

`email_taken` "should not happen on a fresh install but fail loudly" — surface it
distinctly (matches `handler.go:336-343`). Apply the same input validation the
tenant does: `model.ValidateInvitableEmail`, `validUserName` (1–100, no control
chars, not email-like), `CheckPolicy(password)`.

### 6. Post-consume cleanup + session pre-warm (mirror the tenant)

After a successful consume: `removeAdminInstallTokenFile()` +
`clearAdminInstallTokenEnv()` (shrinks the `/proc/$pid/environ` window —
`handler.go:358-364`); pre-warm the staff session cache so the first authed
request skips the cache-miss path (`handler.go:369-371`); `SetSession` the staff
cookie.

### 7. Auditability (the tenant's biggest pattern the admin must NOT drop)

The tenant writes `AuditActionBootstrapCompleted` + records the session **IP + UA
hash** (`handler.go:321-322, 373`). The admin plane is the *higher*-privilege
surface, so "who minted the first superadmin, from what IP, when" matters more.
The staff plane has **no audit table** yet, so at minimum:
- a structured `slog.Info("admin: bootstrap completed", "staff_user_id", …, "ip", …)`,
- capture the staff session's IP + UA hash (the session row already carries them),
- a Prometheus counter (`AdminBootstrapAttemptsTotal`, above).

### 8. Dashboard-admin UI

A Bootstrap screen mirroring the tenant's (email / name / password → POST), plus
the mount-time `state` probe / `/login → /bootstrap` redirect. Handle the distinct
error shapes (`email_taken`, `already_bootstrapped`, `invalid_token`). This React
work is the **long pole** of the estimate.

## `seed-staff` — retained, and intentionally seal-independent

`seed-staff` stays unchanged as the **headless / CI / recovery** path:
- e2e admin tests mint a superadmin with no browser (`STAFF_SEED_PASSWORD` env);
- unattended installs; shell recovery if the web flow is unusable.

**Correction vs. an earlier draft:** `seed-staff` does **not** consult the seal —
it will create a superadmin even after web-bootstrap has sealed (different email).
That's deliberate: it's the break-glass tool. Only the web `POST` honours the
singleton. Both still converge on `store.CreateStaffUser`.

## Security model

Safe **only under the standing admin-ingress requirement**: the admin plane is
**never internet-facing** (internal ALB + SSM port-forward / Tailscale /
SG-restricted — see the admin-portal plan's ingress fork). But because that's an
ops/infra invariant **not enforced in code**, pair it with in-code controls:

- **Token required unconditionally** — no DEV bypass (the admin plane has none; keep it that way).
- **Rate-limit the POST** — reuse the existing admin login limiter
  (`build_admin.go:26 newAdminLoginRateLimiter`). **This is a requirement, not an
  open question.**
- **Single-use** via the transactional singleton consume.
- **Token never logged** (default-secure banner) + file `0600` + deleted on consume.
- *(Optional)* bind bootstrap to loopback when `ADMIN_BOOTSTRAP_INSTALL_TOKEN` is unset.

So the web bootstrap reproduces the tenant's token+seal hardening on a surface
that is *additionally* network-private. Private-ingress is the **primary** control;
the in-code controls are defence-in-depth in case it ever fails.

## Token lifecycle & durability (ECS / restart / multi-replica)

- **Multi-replica:** the persisted hash + `won`-race flag make any replica able to
  validate an incoming bootstrap against the shared PG row (mirrors
  `install_token.go:102-109`). No in-memory divergence.
- **Restart before bootstrap:** the hash is in PG, so a task restart does **not**
  invalidate the operator's token (the no-table draft would have).
- **ECS ephemeral / read-only root:** prefer an **operator-supplied
  `ADMIN_BOOTSTRAP_INSTALL_TOKEN`** (durable, no file) over the file path; or set
  `ADMIN_BOOTSTRAP_TOKEN_FILE_PATH=""` to disable the file. On ECS specifically,
  **`seed-staff` remains the recommended path**; the web flow primarily targets the
  self-hosted / docker-compose self-host shape (which has a writable `/var/run`).

## Differences from the tenant bootstrap

| | Tenant `/auth/bootstrap` | Admin `/admin/auth/bootstrap` |
|---|---|---|
| Creates | first **owner** + **organization** | first **superadmin** (org-less) |
| Seal | `bootstrap_state` singleton | `staff_bootstrap_state` singleton (same shape) |
| Reachability | public internet | private ingress only |
| Token env/file | `BOOTSTRAP_*`, `…/initial_setup_token` | `ADMIN_BOOTSTRAP_*`, `…/admin_initial_setup_token` |
| Session | user session | staff session (`axiaops_staff_session`) |
| Audit | `audit_log` table | slog + metric (no staff audit table yet) |
| Programmatic path | `BOOTSTRAP_INSTALL_TOKEN` | `seed-staff` **and** `ADMIN_BOOTSTRAP_INSTALL_TOKEN` |
| License | n/a | n/a (admin plane is license-free) |

## Lessons from the tenant bootstrap (each adopted above)

| Tenant pattern | Evidence | Adopted as |
|---|---|---|
| PG-persisted token hash + `won`-race | `install_token.go:92-109` | §1/§2 — durable, multi-replica-safe |
| Transactional verify+create+seal | `handler.go:325` | §4 `ConsumeStaffBootstrap` |
| Distinct error codes + per-outcome metric | `handler.go:327-350` | §5 |
| Post-consume file+env cleanup | `handler.go:358-364` | §6 |
| Audit + session IP/UA capture | `handler.go:321-322, 373` | §7 |
| Input validation (email/name/password policy) | `handler.go:278-293` | §5 |
| Session mint + cache pre-warm | `handler.go:302-371` | §6 |
| File-disable + env token + default-secure banner (ECS) | `install_token.go:113-147` | §2 / Token lifecycle |

## Implementation plan

1. **Migration** — `NNN_staff_bootstrap_state.{up,down}.sql` mirroring `bootstrap_state`.
2. **`services/shared/storage`** — `CreateStaffBootstrapState`, `ConsumeStaffBootstrap`, `StaffBootstrapState()` + the `ErrStaffBootstrap*` errors; impl in `postgres/staff.go`.
3. **`services/api/internal/auth` (or `internal/staff`)** — `MaybeGenerateAdminInstallToken` (copy/adapt), reusing the pure file/banner helpers.
4. **`cmd/api-admin/main.go`** — call the token mint at boot (next to `openStore`).
5. **`serverbuild/build_admin.go`** — register the two routes; add them to `publicAdminPath`; wire the rate limiter onto the POST.
6. **`internal/staff`** — the bootstrap handler (validation → `ConsumeStaffBootstrap` → cleanup → audit/metric → session cookie).
7. **`services/dashboard-admin`** — Bootstrap screen + mount-time probe/redirect + error states.
8. **Tests** — `bootstrap→200+sealed(409)`, `bad token→401`, `email_taken→409`, `state flips`, concurrent-POST → exactly one superadmin. Keep the `seed-staff` tests.
9. **Docs** — update `services/api/CLAUDE.md` (endpoints + `ADMIN_BOOTSTRAP_*` env).

**Effort: ~2 days** end-to-end — backend ~1 day (migration + store + handler + token + tests), the **dashboard-admin screen ~1 day** (screen + probe + redirect + error states).

## Open questions

1. **Token TTL** — tenant install token is long-lived until redeemed; keep the same (file-deleted on use), or add `ADMIN_BOOTSTRAP_TTL`? Default: long-lived.
2. **Shared install-token primitive** — extract the pure file/banner helpers into a small shared package, or copy? Decide based on churn cost (lean: extract the ~4 pure funcs, keep the gating logic per-plane).
3. **Loopback-bind fallback** — worth binding bootstrap to loopback when no operator token is set, or is rate-limit + private-ingress enough? (Defence-in-depth, low effort.)
