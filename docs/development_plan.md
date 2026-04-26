# Development Plan — AxiaOps FinOps

## Project Overview

**AxiaOps** — A FinOps SaaS tool that identifies idle/zombie cloud resources still incurring costs despite $0.00 usage. Target: AWS (initial), multi-cloud later.

---

## Phase 1 — Incubator / MVP (April – June 2026) ✅

### Goal: Working AxiaOps Detector (local, real AWS integration)

#### 1.1 Backend — Go Services ✅

**Language:** Go 1.25+ | **Framework:** Standard library + `net/http` | **Data Layer:** PostgreSQL

**Service architecture (Phase 2+):**

```
services/
  shared/     — model, storage interface + Postgres tests, analyzer (no AWS SDK)
  api/        — HTTP server, auth middleware, reads ghost_records from DB
  ingestion/  — long-lived HTTP server, fetches AWS data, writes to DB
```

**Flow:**

```
ingestion service (long-lived HTTP server on :8081)
  ├── POST /scan      — triggered by API or scheduler
  ├── FetchCosts      → cost_records table
  ├── FetchUsage      → CloudWatch
  ├── Detect()        → analyzer flags zombies
  └── SaveGhosts      → ghost_records table

API service (always running on :8080)
  ├── LoadGhosts      → reads ghost_records from DB
  ├── GET /ghosts     → list of zombie resources
  ├── GET /summary    → aggregate savings
  ├── GET /health     → healthcheck (no auth)
  ├── GET /accounts   → list connected cloud accounts for the current tenant
  ├── POST /accounts  → connect a new cloud account (encrypts secret with AES-256-GCM)
  ├── DELETE /accounts/{id}  → remove a connected account
  └── POST /accounts/{id}/scan → trigger on-demand ingestion scan
```

**Go workspace (`go.work`)** links all three modules locally — no publishing required.

**Ingestion (`services/ingestion/internal/provider`):**
- `Provider` interface — AWS Cost Explorer and CloudWatch API integration
- `INSERT OR IGNORE` deduplication — safe to re-run; logs inserted vs skipped counts

**Analysis (`services/shared/analyzer`):**
- `Detect()` joins cost records with usage records on `resource_id`
- Applies per-service threshold rules (see table below)
- A resource is flagged when `usage.avg <= threshold` for the entire billing period
- Resources with no rule or no usage record are skipped
- `owner` is derived from the `team` tag

**Detection rules:**

| Service | Metric | Threshold | Reason shown |
|---------|--------|-----------|--------------|
| AmazonEC2 | CPUUtilization | ≤ 5% | Instance CPU below 5% — likely idle |
| AmazonRDS | DatabaseConnections | = 0 | Zero connections — likely abandoned |
| AWSLambda | Invocations | = 0 | Zero invocations — likely unused |
| AmazonElasticLoadBalancing | RequestCount | = 0 | Zero requests — likely abandoned |
| AmazonVPC | BytesOutToDestination | = 0 | NAT Gateway zero bytes — likely unused |
| AmazonVPC (EIP) | NetworkInterfaceAttachment | = 0 | Elastic IP not attached — $0.005/hour idle charge |

> **S3 and CloudFront exclusion:** Both services are intentionally excluded from Phase 1 detection rules. S3 detection is ambiguous — many buckets are archival by design and zero `GetRequests` is expected. CloudFront distributions can be legitimately dormant. Detection rules for both services are deferred to Phase 3 (see 3.11 Expanded Detection Rules) pending usage data from real customers to set meaningful thresholds.

**API (`services/api/internal/api`):** ✅
- `GET /ghosts` — list of detected zombie resources with cost, usage metric, reason, and owner
- `GET /summary` — aggregate savings figure and per-service breakdown
- `GET /health` — healthcheck, bypasses auth
- `GET /accounts` — list connected cloud accounts for the current tenant
- `POST /accounts` — connect a new cloud account (encrypts secret with AES-256-GCM)
- `DELETE /accounts/{id}` — remove a connected account
- `POST /accounts/{id}/scan` — trigger an on-demand ingestion scan for an account
- CORS middleware — permissive in dev, locked to domain in production

#### 1.4 Auth — Kinde ✅

- **Provider:** Kinde (chosen over Supabase Auth, Clerk, Cognito — see `docs/auth.md`)
- **Flow:** PKCE OAuth via `expo-auth-session` on dashboard → JWT verified by Go middleware
- **Middleware:** `services/api/internal/middleware/auth.go` — RS256 JWT verification via JWKS
- **Tenant persistence:** `org_code` → internal UUID in `tenants` table on first login
- **User persistence:** `kinde_sub` + email in `users` table, `last_seen` updated on each login
- **Migration path:** swap `AUTH_ISSUER` env var — schema is provider-agnostic (see `docs/auth.md`)

#### 1.5 Testing ✅

**Coverage:**

| Package | Tests | What is covered |
|---------|-------|-----------------|
| `shared/analyzer` | 9 | Flags zero-usage resources, skips active, skips missing data, owner fallback, aggregate savings |
| `api/internal/api` | 7 | GET /ghosts, GET /summary — 200, JSON payload, content type, CORS, OPTIONS preflight |
| `api/internal/middleware` | 9 | Valid/invalid/expired/wrong-issuer tokens, missing org_code, OPTIONS passthrough, tenant in context |
| `shared/storage/postgres` | 30+ | Insert, dedup, empty batch, region uniqueness, tags, UpsertTenant, UpsertUser, RLS isolation, accounts CRUD, snapshots |
| `ingestion/provider/aws` | 3 | Single-page response, multi-page pagination, API error propagation |

**Test patterns used:**
- `mockCEClient` — implements `CostExplorerAPI` interface, no real AWS calls
- `httptest.NewRecorder` — tests HTTP handlers without a real server
- RSA key generation — signs test JWTs for middleware tests without hitting Kinde

#### 1.6 Frontend — React Native (Expo) ✅
- **Stack:** Expo + React Native + React Query — same codebase runs on web, iOS, and Android
- **Web first** — Phase 1 targets web only; mobile comes later
- **Dashboard screen** — dark navy header, orange savings number, ghost list with per-service colour coding
  - Accounts bar — shows connected accounts with green/red status dot; Scan button triggers on-demand ingestion
  - Service pill filter — tap a service pill to filter the ghost list; tap again to clear
- **Connect screen** — credential form (label, Access Key ID, Secret Access Key, region) with IAM permissions hint; auto-shown on first login when no accounts are connected
- **Detail screen** — service-coloured header, stats with light orange labels (no borders), reason, remediation hint per service type
- **Auth:** Kinde PKCE login screen → token stored in `localStorage` (web) / `SecureStore` (native)
- **API client** — sends `Authorization: Bearer <token>` on every request

#### 1.7 Infrastructure — Docker Compose ✅

```
browser
   │
   ▼
nginx (dashboard:80)
   │  serves Expo static build
   │  proxies /api/* → api:8080
   ▼
api service (Go binary, :8080)
   │  reads ghost_records from DB, serves REST API
   │  POST /accounts/{id}/scan → triggers ingestion via HTTP
   ▼
PostgreSQL (persisted)

ingestion service (long-lived HTTP server on :8081)
   │  POST /scan  — fetches costs + usage, runs analyzer, writes ghost_records
   ▼
PostgreSQL (same DB)
```

**Key decisions:**
- nginx proxy eliminates cross-origin requests
- API healthcheck uses `/health` (no auth) — Docker `depends_on: service_healthy`
- PostgreSQL container for local dev — survives container restarts
- Expo web built at Docker image build time — no Node.js runtime in production
- `EXPO_PUBLIC_*` vars passed as Docker build args — baked into static bundle

**Note:** Runtime uses PostgreSQL exclusively. Integration tests run against a real PostgreSQL instance via `make test-storage`.

#### 1.8 Storage Layer — PostgreSQL ✅

**Tables:**

```sql
cost_records   — raw billing data from Cost Explorer
ghost_records  — detected zombie resources (replaced on each ingestion run)
tenants        — Kinde org_code → internal UUID mapping
users          — Kinde users, linked to tenant, last_seen updated on login
accounts       — connected cloud accounts, secrets encrypted at rest
```

**Schema:** Versioned migrations in `services/shared/storage/postgres/migrations/` (includes RLS policies).

**Duplicate handling:** `ON CONFLICT ... DO NOTHING` — safe to re-run ingestion for same date range.

**Production path:** `Store` interface (`services/shared/storage/storage.go`) is the only contract. Swapping to PostgreSQL requires a new implementation — no changes to providers, analyzer, or API.

#### 1.9 Dev Environment ✅

**Run locally:**
```bash
make start-dev      # real AWS (from env or .env)
make start-staging  # real AWS + Kinde auth
make stop           # kill all services
```

**Inspect database:**
```bash
./scripts/check_db.sh
```

**AWS Integration:**
- Always uses real AWS Cost Explorer + Describe APIs + CloudWatch
- AWS credentials from `.env` or environment variables

---

## Phase 2 — Alpha (May – August 2026)

### Goal: Production-grade infrastructure, real cloud connectivity, first beta users

#### 2.1 AWS Integration ✅

**Status: Complete (shipped ahead of schedule)**

**Ingestion flow:**

```
1. Cost Explorer (ce:GetCostAndUsage)
      │  grouped by SERVICE + REGION
      ▼
2. Resource Discovery (Describe APIs)
      │  ec2:DescribeInstances, rds:DescribeDBInstances,
      │  lambda:ListFunctions, elb:DescribeLoadBalancers,
      │  ec2:DescribeNatGateways
      ▼
3. CloudWatch (cloudwatch:GetMetricStatistics)
      │  one call per discovered resource
      ▼
4. Analyzer → Detect() + Summarize()
      ▼
5. SaveGhosts → ghost_records table
      ▼
6. API reads ghost_records → GET /ghosts, GET /summary
```

**IAM policy required (`AxiaOpsReadOnly`):**

```json
{
  "Action": [
    "ce:GetCostAndUsage",
    "cloudwatch:GetMetricStatistics",
    "cloudwatch:ListMetrics",
    "ec2:DescribeInstances",
    "ec2:DescribeNatGateways",
    "ec2:DescribeAddresses",
    "rds:DescribeDBInstances",
    "lambda:ListFunctions",
    "elasticloadbalancing:DescribeLoadBalancers"
  ]
}
```

**Key files:**
- `services/ingestion/internal/provider/aws/aws.go` — Cost Explorer client
- `services/ingestion/internal/provider/aws/discover.go` — resource discovery
- `services/ingestion/internal/provider/aws/cloudwatch.go` — CloudWatch usage fetcher

#### 2.2 Account Management ✅

**Status: Complete (shipped ahead of schedule)**

**Design goals:**
- Provider-agnostic `accounts` table (not `aws_accounts`) — ready for Azure/GCP without schema changes
- Secrets encrypted at rest with AES-256-GCM — `ENCRYPTION_KEY` env var (32-byte hex)
- On-demand scan per account — no fixed schedule needed for MVP
- DB-status concurrency guard: account is set to `scanning` before the goroutine fires; a second scan request will see the status and can be rejected at the application layer (full in-memory lock deferred to 2.14 Redis queue)

**Schema (`accounts` table — PostgreSQL production schema):**

```sql
CREATE TABLE IF NOT EXISTS accounts (
    id                TEXT        PRIMARY KEY,
    tenant_id         TEXT        NOT NULL REFERENCES tenants(id),
    provider          TEXT        NOT NULL DEFAULT 'aws',
    label             TEXT        NOT NULL DEFAULT '',
    access_key_id     TEXT        NOT NULL DEFAULT '',
    secret_encrypted  TEXT        NOT NULL DEFAULT '',
    region            TEXT        NOT NULL DEFAULT 'us-east-1',
    status            TEXT        NOT NULL DEFAULT 'connected',
    last_scanned_at   TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL
);
ALTER TABLE accounts ENABLE ROW LEVEL SECURITY;
CREATE POLICY accounts_tenant_isolation ON accounts
    USING (tenant_id = current_setting('app.tenant_id', true));
```

> **Note:** Row-Level Security and `CREATE POLICY` are PostgreSQL-only features.

**Key files:**
- `services/shared/model/account.go` — Account struct (`SecretEncrypted` omitted from JSON)
- `services/shared/crypto/crypto.go` — AES-256-GCM encrypt/decrypt (shared between api and ingestion)
- `services/shared/storage/storage.go` — Store interface extended with 5 account methods
- `services/shared/storage/postgres/postgres.go` — Full account CRUD implementation
- `services/ingestion/cmd/main.go` — Long-lived HTTP server; `POST /scan` decrypts credentials and runs ingestion

**Ingestion scan flow:**
1. API receives `POST /accounts/{id}/scan`
2. API checks in-memory scan lock — rejects if account is already scanning
3. API sets account status to `scanning`, fires async goroutine
4. Goroutine POSTs `{account_id, tenant_id}` to ingestion service at `http://localhost:8081/scan`
5. Ingestion fetches account from DB, decrypts secret, sets AWS env vars, runs full ingestion
6. API updates account status to `connected` or `error` on completion

**Scan recovery (planned — see milestone May 2026):** A background ticker (every 5 minutes) will check for accounts stuck in `scanning` status for longer than 15 minutes and reset them to `error` with a timeout reason. This prevents permanently stuck scans if the API restarts mid-scan or the ingestion service crashes. Not yet implemented — currently a stuck scan requires a manual status reset.

**Key rotation:** Rotating `ENCRYPTION_KEY` is not a simple env var swap — all `secret_encrypted` values in the `accounts` table must be decrypted with the old key and re-encrypted with the new key before the var is updated. A migration script must be written and tested before any key rotation in production. Document this in `docs/ops.md`.

#### 2.3 Auth — Kinde ✅

- Chosen over Supabase Auth, Clerk, Cognito — see `docs/auth.md`
- JWT middleware in `services/api/internal/middleware/`
- Tenant + user persisted on first login
- Dashboard login screen with PKCE flow

#### 2.4 PostgreSQL Migration ✅

- PostgreSQL is the runtime database
- Versioned migrations via `golang-migrate/v4` — embedded in binary, run on startup using `MIGRATION_DATABASE_URL`
- `000_init.up.sql` — app user (`axiaops`), schema, default grants
- `001_initial.up.sql` — all tables + RLS policies
- `002_ghost_snapshots.up.sql` — `ghost_snapshots` table (see 2.5)
- Both `api` and `ingestion` call `postgres.Migrate(migrationURL)` on startup; advisory lock makes concurrent calls safe
- No inline `CREATE TABLE` / `ALTER TABLE` in application code
- Dev: PostgreSQL container in `docker-compose.yml`
- Production: RDS PostgreSQL (`db.t4g.micro`)

**Key files:**
- `services/shared/storage/postgres/migrate.go` — `Migrate()` runner + `ResetStuckScans()`
- `services/shared/storage/postgres/migrations/*.sql` — versioned schema files
- `services/shared/storage/postgres/postgres.go` — full `Store` implementation

#### 2.5 Savings History / Trend ✅

**Status: Complete**

- `ghost_records` is currently replaced on every run — no history is retained
- Add a `ghost_snapshots` table: `(id, tenant_id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency)`
- Ingestion job writes one snapshot row per scan instead of wiping ghost_records
- `GET /trend` — returns snapshot series for charting savings over time
- Dashboard: savings trend sparkline on the header

#### 2.6 Observability

**Priority: Must ship before production deployment.**

- **Structured logging:** Replace `log.Printf` with `log/slog` (stdlib) — JSON output in production, text in dev
  - Every log line includes: `tenant_id`, `account_id`, `request_id`, `service`
  - Scan lifecycle: `scan.started`, `scan.completed`, `scan.failed` with duration and ghost count
- **Error handling:** Structured logging via `log/slog` — errors logged as JSON to stdout for aggregation
- **Metrics:** Prometheus client (`promhttp`) exposed on `/metrics` (internal port, not public)
  - `axiaops_scan_duration_seconds` — histogram per account
  - `axiaops_ghosts_detected_total` — counter per service
  - `axiaops_api_request_duration_seconds` — histogram per endpoint
  - `axiaops_api_requests_total` — counter per endpoint + status code
- **Health endpoint:** Already exists (`GET /health`); extend to include DB connectivity and ingestion service reachability
- **Production:** CloudWatch logs for structured logging; Prometheus metrics scraped by Grafana Cloud (or CloudWatch custom metrics)

**Key files to add:**
- `services/shared/logging/logging.go` — slog setup (JSON vs text based on env)
- `services/api/internal/middleware/requestid.go` — request ID injection
- `services/api/internal/middleware/metrics.go` — Prometheus HTTP middleware

#### 2.7 API Versioning

**Decision:** All endpoints are prefixed `/v1/` from the first production deployment. This must be established before any external integrations or documentation is published — retrofitting a version prefix after customers are using the API is a breaking change.

- Update all routes: `GET /v1/ghosts`, `GET /v1/summary`, `GET /v1/accounts`, etc.
- `GET /health` and `GET /metrics` remain unversioned (infrastructure endpoints)
- nginx proxy rewrite: `/api/v1/*` → `api:8080/v1/*`
- Dashboard API client updated to use `/v1/` base path
- Deprecation policy: a version will receive 6 months notice before removal

#### 2.8 Rate Limiting — In-Memory

**Priority: Must be in place before production, before Redis is available.**

Redis-based rate limiting isn't available until 2.14, but the API is public-facing from day one. Add a per-tenant in-memory token bucket as a temporary guard that is replaced by the Redis implementation in 2.14.

- Implementation: `sync.Map` keyed by `tenant_id` + sliding window counter
- Limits: 60 requests/minute per tenant (API endpoints); scan requests already have a concurrency guard
- Returns `429 Too Many Requests` with `Retry-After` header
- **Key file:** `services/api/internal/middleware/ratelimit.go`

#### 2.9 Graceful Shutdown

**Priority: Before App Runner deployment — App Runner sends `SIGTERM` before terminating containers.**

Both the API (`:8080`) and ingestion (`:8081`) services must handle `SIGTERM` cleanly to avoid dropped requests or corrupted scans.

- Listen for `SIGTERM` / `SIGINT` via `signal.NotifyContext`
- API: call `server.Shutdown(ctx)` with a 30-second drain timeout — completes in-flight HTTP requests
- Ingestion: complete the current scan before shutting down; reject new `/scan` requests during drain
- PostgreSQL connection pool: `pool.Close()` after HTTP server exits
- Log `shutdown.started` and `shutdown.complete` with drain duration

#### 2.10 GitLab CI Pipeline

**Priority: Before production deployment — no manual build/push steps in production.**

- **Stages:** `test` → `build` → `deploy`
- **test:** `go test ./...` across all three modules; `go vet ./...`; `golangci-lint run`
- **build:** Docker image build for `api`, `ingestion`, `dashboard`; push to AWS ECR
- **deploy:** `aws apprunner update-service` for `api` and `ingestion`; CloudFront invalidation for dashboard
- **Branch strategy:** `main` triggers full pipeline; feature branches trigger `test` only
- **Secrets:** `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `ENCRYPTION_KEY` stored as GitLab CI/CD variables (masked)

**Key file:** `.gitlab-ci.yml` at repo root

#### 2.11 Scheduled Auto-Scan

- Add a `scan_interval_hours` field to the `accounts` table (default: 24) — requires a new migration
- Background ticker in the API service triggers ingestion per account on schedule
- Configurable per account via `PATCH /accounts/{id}`
- Skips if account is already `scanning` (uses same concurrency guard from 2.2)
- Structured log: `scan.scheduled`, `scan.skipped_already_running`

#### 2.12 cost_records Retention

**Purpose:** `cost_records` grow unbounded — each scan inserts up to 30 days of billing data per account. Without a retention policy, the table will grow indefinitely and degrade query performance.

- Retention window: 90 days (configurable via `COST_RECORDS_RETENTION_DAYS` env var)
- Cleanup: background ticker in the ingestion service runs daily; `DELETE FROM cost_records WHERE period_end < NOW() - INTERVAL '90 days' AND tenant_id = $1`
- Log `cost_records.cleanup` with rows deleted and duration
- Migration: add `created_at` index on `cost_records` if not already present for efficient range deletes

#### 2.13 Backup / Disaster Recovery

- **PostgreSQL:** Automated daily snapshots via RDS automated backups (7-day retention)
- **Point-in-time recovery:** RDS continuous backup — restore to any second within retention window
- **ghost_records safety:** With `ghost_snapshots` (2.5) in place, a bad scan no longer loses historical data. Current ghost_records can be regenerated by re-running a scan.
- **Secrets:** `ENCRYPTION_KEY` stored in AWS Secrets Manager (not in env vars on disk). Rotation procedure (including re-encryption of all account secrets) documented in `docs/ops.md`.
- **Infrastructure-as-code:** Docker Compose for dev; production infra defined in Terraform (App Runner, RDS, ElastiCache, Secrets Manager) — reproducible from scratch. State backend: S3 bucket + DynamoDB lock table.

#### 2.14 Redis

**Purpose:** Caching and scan job queue — keeps the API fast and decouples scan execution from HTTP requests.

**Use cases:**

| Use case | Detail |
|----------|--------|
| **JWKS key cache** | Cache Kinde's public keys in Redis with a 1h TTL — avoids a network round-trip to Kinde on every authenticated request |
| **Scan job queue** | `POST /accounts/{id}/scan` pushes a job onto a Redis list; a worker goroutine in the ingestion service pops and processes — decouples scan from the HTTP response |
| **Rate limiting** | Replace the in-memory token bucket (2.8) with a Redis `INCR` + `EXPIRE` counter — survives API restarts and works across multiple replicas |

**Infrastructure:**
- Dev: Redis container added to `docker-compose.yml`
- Production: AWS ElastiCache Serverless (Redis-compatible) — pay-per-use, no cluster to manage
- Client: `github.com/redis/go-redis/v9`
- `REDIS_URL` env var (e.g. `redis://localhost:6379`); if unset, JWKS falls back to in-memory cache, scan stays synchronous, and rate limiting falls back to the in-memory implementation from 2.8

**Key files to add:**
- `services/shared/cache/cache.go` — `Cache` interface (`Get`, `Set`, `Del`)
- `services/shared/cache/redis/redis.go` — Redis implementation
- `services/shared/cache/memory/memory.go` — in-memory fallback for dev/test
- `services/api/internal/middleware/auth.go` — inject cache for JWKS lookup
- `services/ingestion/cmd/worker.go` — Redis queue consumer

#### 2.15 Alerting

- Weekly email digest: "You have $X in ghost spend this week"
- Email provider: Resend (preferred) or SendGrid
- Slack webhook alert — notify a channel when new ghosts appear after a scan
- Alerts reference `ghost_snapshots` (2.5) to show week-over-week delta

#### 2.16 Deployment

- API service: AWS App Runner — see `docs/deployment.md`
- Ingestion service: App Runner (long-lived, receives HTTP scan requests)
- Frontend: Expo EAS Build → web deploy (static assets behind CloudFront)
- Database: RDS PostgreSQL (`db.t4g.micro`) — see 2.4
- Cache: AWS ElastiCache Serverless (Redis) — see 2.14
- Secrets: AWS Secrets Manager for `ENCRYPTION_KEY`, `REDIS_URL`
- Infrastructure: Terraform modules for reproducible provisioning (state in S3 + DynamoDB lock)

---

## Phase 3 — Beta / GTM (September – December 2026)

### Goal: Feature-complete for first paying customers, establish revenue

**Scope principle:** This phase focuses on features that directly enable paying customers. Multi-cloud and mobile are deferred to Phase 4 — they don't block revenue and need more usage data / user demand to justify.

#### 3.1 Pricing & Billing

**Priority: Must be in place before beta users convert to paid.**

- **Tiers:**
  - **Free:** 1 connected account, manual scan only, 30-day ghost history
  - **Pro (€49/month):** Up to 5 accounts, auto-scan, full history, email digest, CSV export
  - **Team (€149/month):** Unlimited accounts, user roles, Slack alerts, priority support
- **Billing provider:** Stripe — subscription management, invoicing, usage metering
- **Implementation:**
  - `services/api/internal/middleware/billing.go` — checks tenant plan tier, enforces limits
  - `tenants` table extended with `plan`, `stripe_customer_id`, `stripe_subscription_id`
  - Stripe webhook handler: `POST /webhooks/stripe` — subscription lifecycle events
  - Dashboard: plan indicator in header, upgrade prompt when hitting limits
- **Free trial:** 14 days of Pro tier on signup, no credit card required

#### 3.2 Dismiss Ghost

- `POST /ghosts/{id}/dismiss` — mark a ghost as intentional with a reason and optional note
- `dismissed_ghosts` table: `(id, tenant_id, resource_id, reason, note, dismissed_by, dismissed_at)`
- Dismissed ghosts are excluded from `/ghosts` and `/summary` by default; `?include_dismissed=true` shows them
- Dashboard: "Dismiss" button on DetailScreen; dismissed ghosts shown with a grey "Intentional" badge
- Snooze variant: `snooze_until` field — ghost reappears automatically after the date passes
  - **Note:** Snooze reactivation requires a background ticker in the API service that periodically checks `snooze_until < NOW()` and clears the dismissed status. Add this alongside the dismiss implementation.

#### 3.3 Remediation Actions

- `GET /ghosts/{id}/remediation` — returns a ready-to-run AWS CLI command per resource type
- Pre-generated commands per service (e.g. `aws ec2 stop-instances`, `aws rds stop-db-instance`)
- Audit trail — all actions logged with timestamp and user

#### 3.4 Resource Inventory View ✅

**Status: Complete (shipped ahead of schedule in Phase 2)**

- `model.ResourceRecord` — all resources with cost + usage + `is_ghost bool`
- `resource_records` table populated by ingestion job (replace-on-run, like `ghost_records`)
- `analyzer.AnnotateAll(costs, usage, ghosts)` — marks each cost record as ghost or active using the pre-computed ghost slice (EIP ghosts included automatically)
- `GET /resources` — full inventory; frontend filters client-side
- Dashboard "Ghost Resources / All Resources" toggle — ghost-only by default
- Resource cards show usage metric for active resources; ghost badge + reason for zombies
- DetailScreen adapts: hides "Why flagged" and "Suggested Action" for non-ghost resources

#### 3.5 Scan History Log

- Track each scan run: `(id, tenant_id, account_id, started_at, finished_at, ghost_count, total_monthly_cost, status, error)`
- `GET /accounts/{id}/scans` — returns scan history for an account
- Dashboard: scan history list under each account (last N scans, ghost count, timestamp)

#### 3.6 Tag / Team Filtering

- `GET /ghosts?team=backend&env=prod` — filter ghost list by resource tag values
- `GET /resources?team=backend` — same for full inventory
- Dashboard: tag filter chips alongside the existing service pill filter

#### 3.7 CSV Export

- `GET /ghosts?format=csv` — returns ghost list as a downloadable CSV
- Columns: resource_id, service, region, monthly_cost, reason, owner, detected_at
- Dashboard: "Export CSV" button on the ghost list
- Plan-gated: Pro tier and above (see 3.1)

#### 3.8 Per-Account Summary

- `GET /summary?account_id={id}` — filter summary to a single connected account
- Dashboard: per-account savings shown in the accounts bar

#### 3.9 User Management

- Invite team members via Kinde organisation invites
- Roles: `admin` (full access) and `viewer` (read-only — no scan, no connect/disconnect)
- `GET /users` — list users in tenant; `DELETE /users/{id}` — remove access
- Plan-gated: Team tier only (see 3.1)

#### 3.10 GDPR / Data Deletion

**Priority: Must be in place before acquiring paying customers in the EU.**

- ✅ **Right to erasure (tenant):** `DELETE /v1/tenants/me` — owner-only (`tenant:delete`); cascades through `dismissed_zombies`, `zombie_snapshot_services`, `zombie_snapshots`, `zombie_records`, `resource_records`, `cost_records`, `accounts`, `audit_log`, `memberships`, `users` (where this is their primary tenant), and finally `tenants`. Implemented in `Store.DeleteTenantCascade`.
- ✅ **User deletion:** `DELETE /v1/users/me` — authn-only, refused with 409 if the caller is the sole owner of any tenant (must transfer ownership or delete those tenants first). Anonymises that user's audit_log rows across all tenants (`user_id = NULL`, `actor_email = 'deleted-user'`) before removing memberships and the user row. Implemented in `Store.DeleteUser`.
- **Data retention disclosure:** Document what is stored and for how long in the privacy policy.
- **Account offboarding:** When a tenant deletes their account, encrypted AWS secrets are deleted immediately; billing is cancelled via Stripe webhook *(Stripe hook lands with the billing feature)*.
- **Data portability:** `GET /v1/export` — full JSON dump of the tenant's data (zombies, accounts metadata without secrets, scan history). *Not yet implemented.*
- Privacy policy and terms of service pages required before Phase 3 launch.

#### 3.11 Expanded Detection Rules

- Add rules for commonly wasted resources:
  - EBS volumes (unattached — `VolumeReadOps + VolumeWriteOps = 0`)
  - Elastic IPs (already covered in Phase 1)
  - Secrets Manager secrets (unused — `GetSecretValue` invocations = 0, >90 days old)
  - Redshift clusters (`DatabaseConnections = 0`)
  - ElastiCache nodes (`CurrConnections = 0`)
  - S3 buckets (`GetRequests = 0` over 60 days, excluding buckets tagged `archival=true`)
  - CloudFront distributions (`Requests = 0` over 30 days)
- Make detection rules configurable per tenant via `PATCH /settings/rules` — allow adjusting thresholds (e.g. EC2 CPU from 5% to 10%)
- Store custom rules in a `detection_rules` table; fall back to built-in defaults

#### 3.12 Operating Entity / Legal

**Priority: Must be established before mobile app submission and before first paying customer.**

- Register operating entity (UG or GmbH depending on revenue trajectory)
- Required by Apple and Google for App Store / Play Store accounts
- Privacy policy and terms of service (prerequisite for 3.10 GDPR work)
- VAT registration if revenue exceeds threshold

#### 3.13 Reporting

- Exportable PDF savings report — summary page + per-service breakdown + ghost list
- Savings trend over time (chart) — powered by `ghost_snapshots` from 2.5

---

## Phase 4 — Scale & Expand (Q1–Q2 2027)

### Goal: Multi-cloud, mobile, proactive cost intelligence

#### 4.1 Cost Forecasting

- `GET /forecast?days=30|60|90` — project future spend per account and per service
- Algorithm: linear regression over `ghost_snapshots` collected by 2.5 — no ML library, ~50 lines of Go math
- Requires: minimum 60 days of `ghost_snapshots` data before forecasts are meaningful
- Anomaly detection: flag if actual spend exceeds forecast by >20% (surface as alert alongside weekly digest)
- Dashboard: forecast line overlaid on the existing savings trend chart (reuses `GET /trend` chart component)
- DB: no schema change — consumes existing `ghost_snapshots` table from 2.5

#### 4.2 Multi-cloud

- **Azure:** Azure Cost Management API + Azure Monitor metrics
- **GCP:** GCP Billing Export → BigQuery + Cloud Monitoring metrics
- Provider interface already supports this — new implementations of `Provider` for Azure and GCP
- Dashboard: provider icon on each account and resource card; filter by provider
- Only pursue after AWS is proven with paying customers and demand is validated

#### 4.3 Mobile App

- Same Expo codebase — `npm run ios` / `npm run android`
- Apple Developer account ($99/year) required for TestFlight
- Only ship after web product has active paying users who request mobile access
- Privacy policy, terms of service, and legal entity (3.12) required before App Store submission

---

## Phase 5 — Proactive Cost Simulation (Q3–Q4 2027)

### Goal: Anticipate costs before deployment

```
Plan → Deploy → Run
 ↑         ↑       ↑
Simulate  Gate   Optimize   ← AxiaOps owns all three
```

#### 5.1 IaC Plan Parser
- Parse `terraform plan -out=plan.json` and AWS CDK `cdk diff` output
- Extract resource types, sizes, regions, counts
- Minimum supported Terraform version: 1.5 (required for stable `plan` JSON schema — earlier versions have breaking format differences)

#### 5.2 Cost Estimation Engine
- Fetch live pricing from AWS Pricing API, Azure Retail Prices API, GCP Cloud Billing Catalog
- Compute estimated monthly cost delta per resource
- Integrates with cost forecasting (4.1) — planned resource deltas adjust forecast

#### 5.3 What-if Scenarios
- "What if I use gp3 instead of gp2?" → show savings
- "What if I switch region?" → show delta
- "What if I use Spot?" → show risk vs savings

#### 5.4 CI/CD Budget Gate
- GitLab CI / GitHub Actions integration
- Posts cost delta as MR comment
- Configurable threshold: warn or block

#### 5.5 CLI Tool
- `axiaops estimate --plan plan.json`
- `brew install axiaops`

---

## Tech Stack Summary

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.25+ |
| Database | PostgreSQL |
| Cache / Queue | Redis (`go-redis/v9`) — JWKS cache, scan job queue, rate limiting |
| Frontend | React Native (Expo) — web first, mobile in Phase 4 |
| Auth | Kinde (see docs/auth.md) |
| Billing | Stripe (subscriptions, invoicing) |
| Hosting | AWS App Runner |
| Observability | slog (structured logging), Prometheus (metrics) |
| Backup | RDS automated backups (7-day retention, point-in-time recovery) |
| Infrastructure | Terraform (production, state in S3 + DynamoDB), Docker Compose (dev) |
| CI/CD | GitLab CI |
| Cloud APIs | AWS Cost Explorer, CloudWatch, Azure Cost Mgmt (Phase 4), GCP Billing (Phase 4) |

---

## Milestones

| Date | Milestone | Status |
|------|-----------|--------|
| April 2026 | Go ingestion service + AWS integration + zombie detection | ✅ Done |
| April 2026 | React Native web dashboard | ✅ Done |
| April 2026 | Docker Compose full-stack + unit tests | ✅ Done |
| April 2026 | AWS Cost Explorer + CloudWatch integration | ✅ Done |
| April 2026 | Kinde auth + tenant/user persistence | ✅ Done |
| April 2026 | API/ingestion service split + ghost_records DB | ✅ Done |
| April 2026 | Account management — connect AWS, encrypted secrets, on-demand scan | ✅ Done |
| April 2026 | Resource inventory view — all resources with ghost/active annotation | ✅ Done |
| April 2026 | PostgreSQL migration — full production storage layer | ✅ Done |
| May 2026 | Savings history / trend (`ghost_snapshots` + `GET /trend`) — prioritised to prevent data loss | ✅ Done |
| May 2026 | Observability — structured logging, Prometheus metrics | ✅ Done |
| May 2026 | Scan recovery — timeout detection for stuck scans | ✅ Done |
| May 2026 | API versioning — `/v1/` prefix on all endpoints | ✅ Done |
| May 2026 | In-memory rate limiting — per-tenant token bucket before Redis | ✅ Done |
| May 2026 | Graceful shutdown — SIGTERM handling for API and ingestion | ✅ Done |
| May 2026 | GitLab CI pipeline — test, build, deploy stages | ✅ Done |
| June 2026 | Scheduled auto-scan (24h default interval per account) | Planned |
| June 2026 | cost_records retention — 90-day cleanup job | Planned |
| June 2026 | Backup / disaster recovery — RDS snapshots, Secrets Manager | Planned |
| July 2026 | Redis — JWKS cache, scan job queue, rate limiting (replaces in-memory) | Planned |
| July 2026 | Weekly email digest + Slack webhook alerts | Planned |
| August 2026 | Production deployment — App Runner, RDS, ElastiCache, Terraform | Planned |
| September 2026 | Pricing & billing — Stripe integration, 3 tiers | Planned |
| September 2026 | Dismiss ghost workflow + snooze | Planned |
| September 2026 | GDPR / data deletion — right to erasure, data export | Planned |
| September 2026 | Operating entity / legal registration | Planned |
| October 2026 | Remediation CLI commands + audit trail | Planned |
| October 2026 | Scan history log + per-account summary | Planned |
| October 2026 | Tag/team filtering + CSV export | Planned |
| November 2026 | Expanded detection rules (EBS, S3, CloudFront, Redshift, ElastiCache) + configurable thresholds | Planned |
| November 2026 | User management + roles (admin/viewer) | Planned |
| November 2026 | PDF savings report | Planned |
| December 2026 | First paying customer | Planned |
| December 2026 | 10 customers, €5K MRR target | Planned |
| Q1 2027 | Cost forecasting (linear regression, anomaly alerts) | Planned |
| Q1 2027 | Multi-cloud — Azure Cost Management API | Planned |
| Q2 2027 | Multi-cloud — GCP Billing Export | Planned |
| Q2 2027 | Mobile app — iOS + Android via Expo | Planned |
| Q3 2027 | IaC plan parser + cost estimation engine | Planned |
| Q4 2027 | CI/CD budget gate + CLI tool | Planned |
