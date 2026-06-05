# dashboard-admin — AxiaOps platform admin UI

The **staff-only** admin console for the platform admin plane. Deliberately a
**separate app** from the tenant dashboard (`services/dashboard`) so no staff
code ever ships in the tenant bundle — the plane-separation rule of
[`docs/saas-platform-admin-design.md`](../../docs/saas-platform-admin-design.md) §3.

It talks to the admin-plane backend, `cmd/api-admin` (`:8090`), over `/admin/*`.
See [`docs/admin-portal-plan.md`](../../docs/admin-portal-plan.md).

## What it does

- **Login** (`/login`) — native staff credentials → `axiaops_staff_session` cookie.
- **Tenants** (`/tenants`, `/tenants/:id`) — read-only org list + per-org summary
  (metadata, account count, last-scan aggregates). **No tenant FinOps detail** —
  that is the audited break-glass surface (deferred). The per-tenant
  `entitlement` is always `null` for now (entitlements table deferred).
- **Staff** (`/staff`, superadmin only) — create staff, grant/revoke roles, with
  the last-superadmin guard surfaced inline.

## Run it locally

The backend must be running on `:8090` (it ships in `make start-dev`; mint a
superadmin with `make seed-staff`). Then:

```bash
cd services/dashboard-admin
npm install
npm run dev          # http://localhost:5174
```

In dev, Vite proxies `/admin/*` → `:8090` with no path rewrite, so the browser
stays same-origin and the staff session cookie round-trips with no CORS. For a
true cross-origin deployment, set `VITE_ADMIN_API_URL` to the absolute admin-API
origin and set `ADMIN_CORS_ORIGIN` on the backend to match.

```bash
npm run test:run     # Vitest (mocked fetch — no real network)
npm run build        # production bundle → dist/
```

## Deferred

- **Deployment wiring** (Dockerfile / nginx / docker-compose / CI) — lands with
  the broader "deploy the admin plane" work, alongside the `cmd/api-admin`
  service definition and its restricted ingress. Not in this slice.
- **Break-glass tenant-data views, entitlement management, internal-ops
  notifications** — follow the backend phases (design §5–7).
