# Auth Flow — Dashboard & API

## Overview

AxiaOps uses Kinde OAuth 2.0 with PKCE. The dashboard handles the browser-side
flow; the API validates JWTs on every request and auto-provisions tenants.

Two modes exist, controlled by `DEV_MODE`:

| Mode | Dashboard | API |
|------|-----------|-----|
| `DEV_MODE=true` | Fake token, no Kinde | `DevBypass` — fixed tenant injected |
| `DEV_MODE=false` | Full PKCE flow via Kinde | JWT validation + tenant upsert |

---

## Dev Mode (local, no auth)

**Dashboard (`App.js`):**
- Skips Kinde entirely
- Mints a fake token: `dev.<base64 {org_name}>.dev`
- Calls `setAuthToken(devToken)` and renders the app immediately

**API (`main.go`):**
- `DevBypass` middleware injects `DEV_TENANT_ID` from env into every request context
- No token parsing, no DB lookup

---

## Staging / Production Flow

### 1. Dashboard — Login

1. User sees `LoginScreen` → taps "Sign in"
2. `useKindeAuth()` (`src/auth/kinde.js`) calls `AuthSession.useAuthRequest` with:
   - `clientId` from `KINDE_CLIENT_ID`
   - `scopes: ['openid', 'profile', 'email']`
   - `usePKCE: true` — generates `code_verifier` / `code_challenge` locally
3. `promptAsync()` opens the Kinde hosted login page in the browser

### 2. Dashboard — Code Exchange

4. Kinde redirects back with `?code=...`
5. `App.js` calls `AuthSession.exchangeCodeAsync` with the `code` + `code_verifier`
6. Kinde returns an **access token** (RS256 JWT)
7. Token is saved to storage (`saveToken`) and set on the API client (`setAuthToken`)
8. On subsequent loads, `getToken()` restores the token — no re-login required

### 3. API — Request Authentication

Every API request (except `/health` and `OPTIONS`) goes through `auth.Wrap`:

1. Extracts `Bearer <token>` from the `Authorization` header → 401 if missing
2. Validates JWT signature using JWKS fetched from `<KINDE_ISSUER>/.well-known/jwks.json` at startup (keys cached and auto-refreshed)
3. Validates `iss` claim matches `KINDE_ISSUER` → 401 if wrong
4. Extracts `org_code` claim (Kinde's org identifier) → 401 if missing
5. Calls `store.UpsertTenant(org_code, org_name)` — creates tenant row on first login, idempotent thereafter
6. Calls `store.UpsertUser(tenant.ID, sub, email, name)` — same idempotent pattern
7. Injects `tenant_id`, `tenant_name`, `user_id` into the request context

Downstream handlers call `middleware.TenantID(ctx)` to get the tenant UUID, which
PostgreSQL RLS uses to isolate data between tenants.

---

## Config

| Variable | Where set | Purpose |
|----------|-----------|---------|
| `KINDE_ISSUER` | `services/api/.env` | JWT issuer + JWKS base URL (API) and OAuth discovery (dashboard) |
| `KINDE_CLIENT_ID` | `services/api/.env` | OAuth client ID — dashboard only, API does not read it |
| `DEV_MODE` | Makefile / env | Switches between dev bypass and real auth |
| `DEV_TENANT_ID` | Makefile (`start-dev`) | Fixed tenant for dev bypass |

The API only needs `KINDE_ISSUER` — it never reads `KINDE_CLIENT_ID` or any client
secret. The JWKS endpoint (`<issuer>/.well-known/jwks.json`) is public; no
credentials are required to fetch it.

`KINDE_CLIENT_ID` lives in `services/api/.env` purely as a convenience so `start.sh`
can source one file and pass it to the dashboard. The dashboard reads both vars via
`src/config.js`, which checks `window.__ENV__` first (nginx envsubst at runtime)
then falls back to `EXPO_PUBLIC_*` build-time vars.

### Staging deployment

Static values (`KINDE_ISSUER`, `KINDE_CLIENT_ID`) are hardcoded as defaults in
`deploy/staging.yml` — no CI variable needed for them.

Only these must be set as CI/CD secrets (GitLab → Settings → CI/CD → Variables):

| Variable | Why secret |
|----------|-----------|
| `ENCRYPTION_KEY` | AES-256 key for AWS secrets at rest |
| `DATABASE_URL` | Contains DB password |
| `MIGRATION_DATABASE_URL` | Contains DB owner password |
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` | AWS credentials for ingestion |

---

## Middleware Chain (API)

Outermost → innermost on every request:

```
CORS → RequestID → Metrics/Logger → RateLimit → Auth (or DevBypass) → Handler
```

Auth is applied after rate limiting so unauthenticated requests still count against
the IP-based rate limit.

---

## Tenant Auto-Provisioning

There is no manual tenant setup. The first authenticated request from a new Kinde
org automatically:

1. Creates a row in `tenants` (keyed on `org_code`)
2. Creates a row in `users` (keyed on Kinde `sub`)
3. Sets the tenant context for the rest of the request

All subsequent requests for the same org hit the existing rows (upsert is a no-op).

---

## Migration Away from Kinde

See [`docs/auth.md`](auth.md#migration-path-away-from-kinde) — the tenant model is
provider-agnostic. Only the JWT middleware and the dashboard SDK need to change.
