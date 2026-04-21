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
make start-dev      # Host-mode Go services + Postgres container. DEV_MODE=true
                    # (auth bypass). Fast dev loop — the default for day-to-day work.
make start-staging  # Full Docker stack (API, ingestion, dashboard, Redis, Postgres)
                    # with DEV_MODE=false → Kinde JWT auth on. Mirrors deployed env.
make stop           # Kill host-mode services AND `docker compose down` the stack.
make seed           # Populate dummy tenant/user/ghost records
make test           # All Go unit tests
make test-storage   # PostgreSQL tests (RLS, migrations) — needs running postgres
make test-smoke     # Smoke tests — needs full stack running (make start-dev in a separate terminal)
                    # Uses GOWORK=off so tests don't recompile services and kill running processes
make test-all       # Unit + postgres tests
```

## Dev Workflow

- `start-dev` = host-mode Go (API :8080, ingestion :8081, Vite dashboard :5173) against a local Postgres container. No Redis, no auth. Use this for most coding.
- `start-staging` = full docker-compose stack: Postgres + Redis + ingestion + API + dashboard (nginx :8082). Kinde auth enforced. Use when debugging auth flows, Redis features, or verifying container parity.
- Both modes use real AWS Cost Explorer + CloudWatch data.
- `start-dev` requires AWS credentials in `services/*/.env` or environment.
- `start-staging` additionally requires `KINDE_*` env vars to be populated.
- In `start-dev` the dashboard proxies `/api/*` through Vite → API on 8080. In `start-staging` nginx serves the built bundle and proxies `/api/*` to the containerised API.

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
| CloudFront | Requests | = 0 | Abandoned distribution |
| Kinesis | IncomingRecords | = 0 | Unused data stream |
| S3 | AllRequests | = 0 | Abandoned bucket (requires request metrics) |

API-only rules (no CloudWatch — state derived directly from AWS Describe APIs):

| Service | Detection Method | Threshold | Verdict | Cost |
|---------|-----------------|-----------|---------|------|
| AmazonEC2 (EBS vol) | ec2:DescribeVolumes | state = "available" | Unattached volume | $0.08–0.125/GB-month |
| AmazonEC2 (snapshot) | ec2:DescribeSnapshots + DescribeVolumes | source volume gone, not backing any AMI | Orphaned snapshot | $0.05/GB-month |
| AmazonEC2 (stopped) | ec2:DescribeInstances StateTransitionReason | stopped > 30 days | Long-stopped instance (EBS still bills) | $0.08/GB-month on attached volumes |
| AmazonEC2 (AMI) | ec2:DescribeImages + DescribeInstances | age > 90 days, no instance references it | Unused AMI + backing snapshots | $0.05/GB-month on backing snapshots |
| AmazonCloudWatch (Log Group) | logs:DescribeLogGroups | retentionInDays = null (logs stored forever) | Wasteful log group | $0.03/GB-month |
| AmazonRDS (snapshot) | rds:DescribeDBSnapshots + DescribeDBInstances | manual, age > 30 days, source DB gone | Orphaned RDS snapshot | $0.095/GB-month |
| AmazonECR (images) | ecr:DescribeRepositories + DescribeImages | untagged or age > 90 days (not latest) | Stale container images | $0.10/GB-month |
| AWSSecretsManager | secretsmanager:ListSecrets | LastAccessedDate > 90 days | Unused secret | $0.40/secret-month |

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
