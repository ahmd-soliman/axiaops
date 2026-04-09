# CLAUDE.md — API Service

## Purpose

REST API server for AxiaOps. Reads ghost/resource data from PostgreSQL, serves it to the
dashboard. Manages cloud account CRUD and triggers ingestion scans via HTTP to the ingestion service.

## Module

`axiaops.io/api` — Go module at `services/api/`. Entry point: `cmd/main.go`.

## Endpoints

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | /health | No | Healthcheck (Docker depends_on) |
| GET | /metrics | No | Prometheus metrics (internal only) |
| GET | /ghosts | Yes | List zombie resources for tenant |
| GET | /summary | Yes | Aggregate savings + per-service breakdown |
| GET | /accounts | Yes | List connected cloud accounts |
| POST | /accounts | Yes | Connect new account (encrypts secret) |
| DELETE | /accounts/{id} | Yes | Remove account |
| POST | /accounts/{id}/scan | Yes | Trigger on-demand ingestion scan |

## Key Patterns

- **Route registration:** `handler.Register(mux)` — uses Go 1.22+ `mux.HandleFunc("GET /path", fn)`
- **Tenant context:** Auth middleware extracts tenant from JWT, sets via `middleware.TenantID(ctx)`.
  Handlers must call `storage.WithTenantID(ctx, tenantID)` before any DB operation.
- **Async scans:** `POST /accounts/{id}/scan` sets status to `scanning`, fires goroutine with
  `context.WithTimeout(context.Background(), 15*time.Minute)`, POSTs to ingestion at `:8081/scan`
- **Scan lock:** In-memory `sync.Mutex` map prevents duplicate concurrent scans per account
- **Stuck scan recovery:** Background ticker (5min) resets accounts stuck in `scanning` >15min

## Adding New Endpoints

1. Add handler method to `Handler` struct in `internal/api/handler.go`
2. Register route in `Register(mux)` with correct HTTP method prefix
3. Extract tenant: `tid := middleware.TenantID(r.Context())`
4. Set tenant on context: `ctx := storage.WithTenantID(r.Context(), tid)`
5. Use `writeJSON(w, data)` for responses
6. Add test in `internal/api/handler_test.go` using `httptest.NewRecorder`

## Auth Middleware

- Location: `internal/middleware/auth.go`
- Fetches JWKS from Kinde's `.well-known/jwks.json` endpoint
- Verifies RS256 signature + expiry
- `DEV_MODE=true` → auth bypassed, uses `DEV_TENANT_ID`
- Tenant mapped: Kinde `org_code` → `tenants.id` via `UpsertTenant()`

## Prometheus Metrics

- `axiaops_api_requests_total` — counter per method/route/status
- `axiaops_api_request_duration_seconds` — histogram per method/route
- Registered via `prometheus.MustRegister()` in `main.go`

## Environment Variables

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| DATABASE_URL | Yes | — | PostgreSQL app-user connection |
| MIGRATION_DATABASE_URL | Yes | — | PostgreSQL owner connection (migrations) |
| API_ADDR | No | :8080 | Listen address |
| KINDE_ISSUER | Prod | — | Kinde tenant URL |
| DEV_MODE | No | false | Skip auth, use fixed tenant |
| DEV_TENANT_ID | No | dev-tenant-axiaops | Tenant ID in dev mode |
| CORS_ORIGIN | No | * | Allowed CORS origin |
| ENCRYPTION_KEY | Yes | — | 32-byte hex for AES-256-GCM |
| SENTRY_DSN | No | — | Sentry error tracking |

## Testing

```bash
cd services/api && go test ./...
```

Tests use `httptest.NewRecorder`, mock Store implementation, and RSA-generated JWTs.
No real DB or network calls in unit tests.
