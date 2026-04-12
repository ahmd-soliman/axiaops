# API Middleware

The API middleware chain lives in `services/api/internal/middleware/` and `services/shared/observability/`. Middleware is applied in `services/api/cmd/main.go` from outermost to innermost:

```
Request
  └── RequestID          (shared/observability)
  └── Prometheus metrics (shared/observability)
  └── RateLimiter        (middleware/ratelimit.go)   — skipped in DEV_MODE
  └── Auth / DevBypass   (middleware/auth.go)
  └── Handler
```

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

1. **RequestID** — outermost so every log line has a request ID, including auth failures.
2. **Metrics** — wraps everything so failed auth and rate-limited requests are counted.
3. **RateLimiter** — before auth to reject abusive clients cheaply (no DB lookup).
4. **Auth** — innermost before handlers; injects tenant context used by all handlers.
