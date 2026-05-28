# API Middleware

The API middleware chain is composed in `services/api/internal/serverbuild/build.go`
(`ComposeServer`). Order, outermost to innermost:

```
request-logging + metrics
  → request-id
    → auth  (DevBypass  OR  WrapNative → EnforceSSO)
      → rate-limiter
        → CORS
          → mux (handlers)
```

---

## CORS (`serverbuild/build.go` → inline handler)

Sets `Access-Control-Allow-*` headers on every response.

- Reads `CORS_ORIGIN` env var. Two shapes: `*` (legacy, no credentials) or a comma-separated
  allowlist (reflects `Origin` and emits `Access-Control-Allow-Credentials: true` so the
  session cookie round-trips from a different origin).
- Short-circuits `OPTIONS` preflight requests with `204 No Content`.
- **Must be outermost** so CORS headers are present even when auth rejects the request.

```
CORS_ORIGIN=https://app.axiaops.io   # production
CORS_ORIGIN=http://localhost:5173    # local Vite dev with native auth
CORS_ORIGIN=*                        # legacy / DEV_MODE (no credentials)
```

---

## Auth (`middleware/auth.go`, `middleware/auth_native.go`)

Two modes, selected at startup:

### Production — `WrapNative`

`middleware.WrapNative(provider, next)` wraps the `auth.Provider` seam. The production
implementation is `auth.NativeProvider`: it reads the `axiaops_session` HttpOnly cookie,
looks up the session in the PostgreSQL `sessions` table (via Redis cache when available),
and resolves the bound `OrganizationID`, `UserID`, `Email`, `Role`, and `AuthMode`.

Per-request: if `provider.Authenticate(r)` returns an error, the middleware returns `401
unauthenticated` and logs a warning — the internal reason is never echoed. On success, all
resolved fields are attached to the request context.

**Public paths bypass auth** (no cookie required):

| Family | Paths |
|--------|-------|
| Infra | `/health`, `/livez`, `/readyz`, `/metrics` |
| Auth ceremony | `/v1/auth/*` (login, logout, bootstrap, invitations/redeem, etc.) |
| SSO discovery | `/v1/sso/discover` |
| OIDC ceremony | `/v1/sso/oidc/{cid}/initiate`, `/v1/sso/oidc/callback`, `/v1/sso/oidc/{cid}/callback` |

After `WrapNative`, `middleware.EnforceSSO` runs: if the org has an active OIDC connection
with `enforcement="required"`, requests authenticated with `auth_mode="password"` receive
`403 sso_required` (except `/v1/auth/logout`, which is always allowed).

### Dev mode — `DevBypass`

When `DEV_MODE=true`, `middleware.DevBypass` replaces the entire auth chain. It injects
`DEV_ORGANIZATION_ID` / `DEV_USER_ID` / `DEV_USER_EMAIL` into every request context with
no token or cookie required. No DB lookup, no session — purely for local development.

### Context helpers

```go
middleware.OrganizationID(ctx)  // internal organization UUID
middleware.UserID(ctx)          // internal user UUID
middleware.UserEmail(ctx)       // authenticated user's email
middleware.UserName(ctx)        // display name (empty when unset)
middleware.Role(ctx)            // "owner" | "admin" | "member" | "viewer"
middleware.AuthMode(ctx)        // "password" | "sso" | "bootstrap" | ""
```

---

## Rate Limiter (`middleware/ratelimit.go`)

Redis-backed token bucket, one bucket per (organization, user). Only active when
`REDIS_URL` is set — the in-memory fallback is per-replica and meaningless under
autoscaling.

- Default: `RATE_LIMIT_MAX` per minute (default 1000).
- Returns `429 Too Many Requests` with `Retry-After` header.
- Advertises current state via `X-RateLimit-Limit/-Remaining/-Reset` headers.
- Disabled in `DEV_MODE=true`.

---

## Request ID (`middleware/requestid.go`)

Injects a unique `X-Request-ID` into every request and response.

- Uses the incoming `X-Request-ID` header if present.
- Generates a new UUID v4 if absent.
- Available via `middleware.RequestIDFromCtx(ctx)`.

---

## Request Logging + Metrics (inline in `serverbuild/build.go`)

Outermost handler — records every request (including auth failures):

- `axiaops_api_requests_total` — counter per method/route/status
- `axiaops_api_request_duration_seconds` — histogram per method/route

Uses the matched route pattern as the label value (e.g. `/accounts/{id}/scan`) to avoid
high-cardinality label explosion from per-ID paths.

---

## Middleware Chain Order

1. **Request logging + metrics** — outermost so all requests are counted.
2. **Request ID** — early so every log line has an ID, including auth failures.
3. **Auth** — `WrapNative` + `EnforceSSO`, or `DevBypass`.
4. **Rate limiter** — after auth so it can bucket by (organization, user).
5. **CORS** — before the mux so `OPTIONS` preflights short-circuit cleanly.
6. **Mux** — routes to the correct handler.
