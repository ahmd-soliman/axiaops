# CLAUDE.md — API Service

## Purpose

REST API server for AxiaOps. Reads zombie/resource data from PostgreSQL, serves it to the
dashboard. Manages cloud account CRUD and triggers ingestion scans via HTTP to the ingestion service.

## Module

`axiaops.io/api` — Go module at `services/api/`. Entry point: `cmd/main.go`.

## Endpoints

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | /health | No | Deep healthcheck — pings DB; 503 if DB unreachable. Used by Docker `depends_on` and nginx; kept for back-compat |
| GET | /livez | No | Liveness — always 200 unless the process can't reply. Wire orchestrator instance health to this |
| GET | /readyz | No | Readiness — pings DB (503 if down) and reports Redis status (informational; "ok" / "unreachable" / "skipped"). Wire monitoring/synthetic checks to this |
| GET | /metrics | No | Prometheus metrics (internal only) |
| GET | /version | Yes | Build identifier — `{service, version, commit, env}` |
| GET | /zombies | Yes | List zombie resources for organization |
| GET | /summary | Yes | Aggregate savings + per-service breakdown |
| GET | /trend | Yes | Zombie snapshots over time (?account_id, ?service, ?resource_type) |
| GET | /trend/services | Yes | Distinct services available in trend data |
| GET | /trend/resource-types | Yes | Distinct resource types for a service (?service) |
| GET | /resources | Yes | All resource records (zombies + active) |
| GET | /accounts | Yes | List connected cloud accounts |
| POST | /accounts | Yes | Connect new account (encrypts secret) |
| PATCH | /accounts/{id} | Yes | Update label, region, secret_key, scan_interval_hours |
| DELETE | /accounts/{id} | Yes | Remove account |
| POST | /accounts/{id}/scan | Yes | Trigger on-demand ingestion scan |
| POST | /dismissals | Yes | Dismiss or snooze a zombie resource |
| GET | /dismissals | Yes | List active dismissals (?account_id) |
| DELETE | /dismissals/{id} | Yes | Revoke a dismissal |
| GET | /audit | Yes | Organization audit timeline (?user_id, ?resource_type, ?resource_id, ?action, ?since, ?until, ?limit, ?cursor) |
| GET | /me | Yes | Current user's role + permission set; no permission required beyond authn |
| GET | /memberships | Yes | List organization memberships |
| POST | /memberships | Yes | Invite an existing user (by user_id) and assign a role; admin+ only |
| PATCH | /memberships/{id}/role | Yes | Promote or demote a member; permission tier depends on target role |
| DELETE | /memberships/{id} | Yes | Remove a member; self-leave bypasses permission check (last-owner guard still applies) |
| POST | /organizations/transfer-ownership | Yes | Atomically transfer owner role to another user; owner only |
| DELETE | /users/me | Yes | Right-to-erasure: hard-delete the caller. 409 if sole owner of any organization — must transfer or delete those organizations first. Anonymises audit_log across all organizations. |
| DELETE | /organizations/me | Yes | Right-to-erasure: cascade-delete the entire organization (every per-organization table including audit_log). Owner-only (`organization:delete`). |

## Key Patterns

- **Route registration:** `handler.Register(mux)` — uses Go 1.22+ `mux.HandleFunc("GET /path", fn)`
- **Organization context:** Auth middleware extracts organization from JWT, sets via `middleware.OrganizationID(ctx)`.
  Handlers must call `storage.WithOrganizationID(ctx, organizationID)` before any DB operation.
- **Async scans:** `POST /accounts/{id}/scan` sets status to `scanning`, fires goroutine with
  `context.WithTimeout(context.Background(), 15*time.Minute)`, POSTs to ingestion at `:8081/scan`
- **Scan lock:** In-memory `sync.Mutex` map prevents duplicate concurrent scans per account
- **Stuck scan recovery:** Background ticker (5min) resets accounts stuck in `scanning` >15min

## Adding New Endpoints

1. Add handler method to `Handler` struct in `internal/api/handler.go`
2. Register route in `Register(mux)` with correct HTTP method prefix
3. Extract organization: `tid := middleware.OrganizationID(r.Context())`
4. Set organization on context: `ctx := storage.WithOrganizationID(r.Context(), tid)`
5. Use `writeJSON(w, data)` for responses
6. Add test in `internal/api/handler_test.go` using `httptest.NewRecorder`

## Auth Middleware

- Location: `internal/middleware/auth.go`
- Fetches JWKS from Kinde's `.well-known/jwks.json` endpoint
- Verifies RS256 signature + expiry
- `DEV_MODE=true` → auth bypassed, uses `DEV_ORGANIZATION_ID`
- Organization mapped: Kinde `org_code` → `organizations.id` via `UpsertOrganization()`

## Prometheus Metrics (Phase 2.6)

See `../../OBSERVABILITY.md` for full observability guide.

### HTTP Metrics (Automatic)

The request logging middleware (line 161–182 of main.go) records:

- `axiaops_http_requests_total` — counter per method/route/status
- `axiaops_http_request_duration_seconds` — histogram per method/route
- `axiaops_http_responses_total` — responses by method/route/status
- `axiaops_http_errors_total` — 5xx errors by method/route/status
- `axiaops_http_requests_in_flight` — active requests (gauge)

These are exposed via `/metrics` endpoint for Prometheus scraping.

### Database Metrics (To Add)

Wrap database calls with `observability.DatabaseObserver`:

```go
import "axiaops.io/shared/observability"

observer := observability.NewDatabaseObserver("LOAD_ZOMBIES")
defer observer.Observe()
zombies, err := h.store.LoadZombies(ctx)
if err != nil {
    observer.ObserveError()
    // ... handle error
}
```

### Error Handling (Structured Logging)

Use `observability.LogError()` for API errors:

```go
import "axiaops.io/shared/observability"

if err != nil {
    observability.LogError(r.Context(), err,
        "operation", "list_zombies",
        "endpoint", "GET /v1/zombies",
    )
}
```

Errors are logged to stdout with structured context (JSON format in production).

## Environment Variables

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| DATABASE_URL | Yes | — | PostgreSQL app-user connection |
| MIGRATION_DATABASE_URL | Yes | — | PostgreSQL owner connection (migrations) |
| API_ADDR | No | :8080 | Listen address |
| KINDE_ISSUER | Prod | — | Kinde organization URL |
| DEV_MODE | No | false | Skip auth, use fixed organization |
| DEV_ORGANIZATION_ID | No | dev-organization-axiaops | Organization ID in dev mode |
| DEV_USER_ID | No | dev-user-axiaops | User ID seeded in dev mode; `EnsureDevMembership` assigns it `owner` |
| DEV_USER_EMAIL | No | dev@axiaops.local | Email for the dev user row |
| CORS_ORIGIN | No | * | Allowed CORS origin |
| ENCRYPTION_KEY | Yes | — | 32-byte hex for AES-256-GCM |
| APP_ENV | No | — | Environment (production, staging, development) |
| APP_VERSION | No | — | Release version (e.g., 2.6.0); surfaced via `GET /v1/version` |
| APP_COMMIT_SHA | No | — | Short git SHA of the build; surfaced via `GET /v1/version` |
| LOG_LEVEL | No | info | Log level (debug, info, warn, error) |
| LOG_OUTPUT | No | json | Log format (json or text) |

## Testing

```bash
cd services/api && go test ./...
```

Tests use `httptest.NewRecorder`, mock Store implementation, and RSA-generated JWTs.
No real DB or network calls in unit tests.
