# CLAUDE.md — AxiaOps

## What is AxiaOps?

FinOps SaaS that detects idle/zombie cloud resources still incurring costs despite zero usage.
"Know the value of every resource." MVP targets AWS; multi-cloud (Azure, GCP) is Phase 4.

## Current Status

Phase 1 (MVP) complete. Phase 2 in progress — real AWS integration shipped, now working on
observability, scheduled scans, and production deployment (App Runner + RDS).

## Architecture

```
services/
  api/        — HTTP server (:8080), auth middleware, reads from DB
  ingestion/  — Long-lived HTTP server (:8081), fetches AWS data, writes to DB
  shared/     — Domain models, Store interface, PostgreSQL, analyzer, crypto, logging
  dashboard/  — React Native (Expo) web app, served via nginx
```

Go workspace (`go.work`) links all three Go modules. Import paths: `axiaops.io/api`, `axiaops.io/ingestion`, `axiaops.io/shared`.

## Key Commands

```bash
make start-dev      # Docker Compose with real AWS (from .env or env vars)
make start-staging  # Real AWS + Kinde auth
make stop           # Kill all services, free ports
make seed           # Populate dummy tenant/user/ghost records
make test           # All Go unit tests
make test-postgres  # PostgreSQL tests (RLS, migrations) — needs running postgres
make test-smoke     # Smoke tests — needs full stack running (make start-dev in a separate terminal)
                    # Uses GOWORK=off so tests don't recompile services and kill running processes
make test-all       # Unit + postgres tests
```

## Dev Workflow

- Docker Compose runs: postgres (5432), ingestion (8081), api (8080), dashboard (80→nginx)
- All modes use real AWS Cost Explorer + CloudWatch data
- `make start-dev` requires AWS credentials in `.env` or environment
- `make start-staging` additionally requires Kinde JWT auth
- Dashboard proxies `/api/*` through nginx to the API service

## Database

- **Runtime:** PostgreSQL 16 with Row-Level Security (tenant isolation via `SET app.tenant_id`)
- **Migrations:** `services/shared/storage/postgres/migrations/` — versioned SQL, run on startup
- Two connection strings: `DATABASE_URL` (app user) and `MIGRATION_DATABASE_URL` (owner/admin)

## Testing Conventions

- Go standard `testing` package — no testify or third-party assertion libs
- Black-box tests: `package foo_test` (not `package foo`)
- Mock interfaces for external services (AWS SDK, HTTP clients)
- Helper functions for fixture building: `costRecord()`, `usageRecord()`
- `httptest.NewRecorder` for handler tests
- RSA key generation for JWT middleware tests (no real Kinde calls)
- Always run `make test` before committing

## Code Conventions

- **Go 1.25+** — use modern stdlib features (`log/slog`, `http.ServeMux` with `r.PathValue()`)
- **Error wrapping:** `fmt.Errorf("context: operation: %w", err)` — always wrap with `%w`
- **Logging:** `slog.Info/Error/Warn("action", "key", value)` — never `log.Printf`
- **Naming:** Explicit, no abbreviations beyond `ctx`, `err`, `tx`, `mux`
- **Handler pattern:** Constructor `New(store)` → `Register(mux)` → route handlers as methods
- **JSON responses:** Use `writeJSON()` helper, never raw `json.NewEncoder`
- **Context propagation:** `storage.WithTenantID(ctx, tenantID)` for all DB calls
- **Transactions:** `defer tx.Rollback(ctx)` immediately after `Begin()`
- **Constants:** Named duration constants (`const stuckScanTimeout = 15 * time.Minute`)

## FinOps Domain Rules

Zombie detection thresholds — do not change without business justification:

| Service | Metric | Threshold | Verdict |
|---------|--------|-----------|---------|
| AmazonEC2 | CPUUtilization | ≤ 5% | Idle instance |
| AmazonRDS | DatabaseConnections | = 0 | Abandoned DB |
| AWSLambda | Invocations | = 0 | Unused function |
| ELB | RequestCount | = 0 | Abandoned LB |
| VPC (NAT) | BytesOutToDestination | = 0 | Unused NAT GW |
| VPC (EIP) | NetworkInterfaceAttachment | = 0 | Unattached EIP |

## Security

- AES-256-GCM encrypts AWS secrets before DB storage (`ENCRYPTION_KEY` env var, 32-byte hex)
- Kinde OAuth 2.0 PKCE flow — RS256 JWT verified via JWKS endpoint
- RLS enforces tenant isolation at the DB level — never query without `app.tenant_id` set
- Never commit `.env` files, credentials, or encryption keys
- Production: IAM roles instead of access keys

## Cost Awareness (FinOps for AxiaOps itself)

- Phase 2 target: €24–34/mo (App Runner + RDS db.t4g.micro)
- Avoid NAT Gateways (~€33/mo fixed) — use public subnets with security groups
- CloudWatch log retention: 7 days max
- Clean up old ECR images (€0.10/GB)
- RDS Multi-AZ doubles cost — defer until necessary
- App Runner scales to zero — no idle compute cost

## Service-Specific Instructions

@services/api/CLAUDE.md
@services/ingestion/CLAUDE.md
@services/shared/CLAUDE.md
@services/dashboard/CLAUDE.md
