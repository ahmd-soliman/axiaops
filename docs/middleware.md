# API Middleware

The API middleware chain lives in `services/api/internal/middleware/` and `services/shared/observability/`. Middleware is applied in `services/api/cmd/main.go` from outermost to innermost:

```
Request
  └── CORS               (api/handler.go)            — outermost, always runs
  └── Logger + Metrics   (cmd/main.go inline)
  └── RequestID          (shared/observability)
  └── Auth / DevBypass   (middleware/auth.go)
  └── RateLimiter        (middleware/ratelimit.go)   — skipped in DEV_MODE
  └── Mux (router)
```

---

## CORS (`handler.go`)

Sets `Access-Control-Allow-*` headers on every response so browsers allow cross-origin requests (e.g. dashboard on `:3000` calling API on `:8080` in local dev).

- Reads `CORS_ORIGIN` env var — defaults to `*`. Set to your domain in production (e.g. `https://app.axiaops.com`).
- Short-circuits `OPTIONS` preflight requests with `204 No Content` immediately — no auth or rate limiting applied.
- **Must be outermost** so CORS headers are present even when auth or rate limiting rejects the request. If auth returns `401` without CORS headers, the browser cannot read the error response.

### Before (broken)

CORS was innermost — wrapped only the mux. Auth ran first, so a rejected request never reached CORS:

```
Browser → Auth (401, no CORS headers) ✗
                ↓ never reached
              CORS → Mux
```

The browser received a `401` with no `Access-Control-Allow-Origin` header and blocked the response entirely — showing a CORS error instead of an auth error.

### After (fixed)

CORS is outermost — wraps the entire chain. Headers are set before anything else runs:

```
Browser → CORS (sets headers) → Auth → Rate Limiter → Mux ✓
```

Even if auth returns `401`, the CORS headers are already written and the browser can read the response.

```
CORS_ORIGIN=https://app.axiaops.com  # production
CORS_ORIGIN=*                        # default (local dev)
```

**In production with same-domain deployment** (dashboard and API behind the same load balancer/CDN), CORS headers are not strictly required since there is no cross-origin request. The middleware is harmless to keep.

---

## Auth (`auth.go`)

Verifies Kinde RS256 JWTs on every request.

- Fetches JWKS from `{KINDE_ISSUER}/.well-known/jwks` at startup; keys refresh automatically.
- Extracts `org_code` claim → looks up tenant in DB → injects `tenant_id`, `tenant_name`, `user_id` into request context.
- Returns `401` for missing, expired, wrong-issuer, or malformed tokens.
- Passes `OPTIONS` preflight requests through without auth (CORS support).

**Context helpers:**
```go
middleware.TenantID(ctx)   // internal tenant UUID
middleware.TenantName(ctx) // tenant display name
middleware.UserID(ctx)     // internal user UUID
```

**Dev mode:** when `DEV_MODE=true`, `DevBypass` replaces the JWT verifier and injects `DEV_TENANT_ID` into every request context. No token required.

---

## Rate Limiter (`ratelimit.go`)

In-memory token bucket, one bucket per tenant.

- Default: 60 requests/minute per tenant (1 token/sec, burst of 60).
- Returns `429 Too Many Requests` with a `Retry-After` header when the bucket is empty.
- Disabled in `DEV_MODE=true`.
- Background ticker in `main.go` calls `CleanupStaleBuckets(1h)` every 5 minutes to prevent memory growth.

> Temporary until Phase 2.14 (Redis-backed distributed rate limiting).

---

## Request ID (`requestid.go`)

Injects a unique `X-Request-ID` header into every request and response.

- Uses the incoming `X-Request-ID` header if present (allows tracing across services).
- Generates a new UUID v4 if absent.
- Stores the ID in context — available via `middleware.RequestIDFromCtx(ctx)` for structured log lines.

---

## Prometheus Metrics (`shared/observability/middleware.go`)

HTTP middleware that records per-request metrics.

- `axiaops_api_request_duration_seconds` — histogram, labels: `method`, `path`, `status`
- `axiaops_api_requests_total` — counter, same labels

Exposed at `GET /metrics` (unversioned, not behind auth).

---

## Middleware Chain Order

Order matters — each layer wraps the next:

1. **CORS** — outermost so headers are always set, even on rejected requests. Browser needs them to read any response.
2. **Logger + Metrics** — wraps everything so all requests (including auth failures) are logged and counted.
3. **RequestID** — early so every log line has a request ID, including auth failures.
4. **Auth** — before rate limiter; injects tenant context used by the rate limiter and all handlers.
5. **RateLimiter** — after auth so it can bucket by tenant ID rather than IP.
6. **Mux** — innermost; routes to the correct handler.
