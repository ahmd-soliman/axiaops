# Development Plan — AxiaOps FinOps

## Project Overview

**AxiaOps** — A FinOps SaaS tool that identifies idle/zombie cloud resources still incurring costs despite $0.00 usage. Target: AWS (initial), multi-cloud later.

---

## Phase 1 — Incubator / MVP (April – June 2026) ✅

### Goal: Working AxiaOps Detector (local, no auth, fixture data)

#### 1.1 Cost Fixture Data ✅
- `fixtures/costs.json` — 13 realistic `CostRecord` entries with ARN-style resource IDs
- Fields: `provider`, `account_id`, `service`, `region`, `resource_id`, `amount`, `currency`, `period_start`, `period_end`, `tags`
- Tags include `env` and `team` on every record — ownership resolution depends on this
- Covers: EC2, RDS, S3, Lambda, CloudFront, ELB, CloudWatch, NAT Gateway across `eu-central-1` and `eu-west-1`

#### 1.2 Usage Fixture Data ✅
- `fixtures/usage.json` — one CloudWatch-style metric per resource (mirrors what production will fetch from CloudWatch)
- Fields: `resource_id`, `metric`, `unit`, `avg`, `period_days`
- Some resources deliberately have zero usage to act as zombies in development and testing

#### 1.3 Backend — Go Services ✅

**Language:** Go 1.25+ | **Framework:** Standard library + `net/http` | **Data Layer:** PostgreSQL (runtime) + SQLite (unit tests only)

**Service architecture (Phase 2+):**

```
services/
  shared/     — model, storage interface + Postgres + SQLite tests, analyzer (no AWS SDK)
  api/        — HTTP server, auth middleware, reads ghost_records from DB
  ingestion/  — long-lived HTTP server, fetches AWS/fixture data, writes to DB
```

**Flow:**

```
ingestion service (long-lived HTTP server on :8081)
  ├── POST /scan      — triggered by API or scheduler
  ├── FetchCosts      → cost_records table
  ├── FetchUsage      → CloudWatch / fixture
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
- `Provider` interface — swap AWS Cost Explorer for file fixture with one env var (`DEV_MODE=true`)
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
| `shared/storage/sqlite` | 11 | Insert, dedup, empty batch, region uniqueness, tags as JSON, UpsertTenant, UpsertUser |
| `ingestion/provider/filefixture` | 5 | Returns all records, multiple records, file not found, invalid JSON, correct Name() |
| `ingestion/provider/aws` | 3 | Single-page response, multi-page pagination, API error propagation |

**Test patterns used:**
- `mockCEClient` — implements `CostExplorerAPI` interface, no real AWS calls
- `os.CreateTemp` — throwaway SQLite file per test, cleaned up via `t.Cleanup`
- `httptest.NewRecorder` — tests HTTP handlers without a real server
- RSA key generation — signs test JWTs for middleware tests without hitting Kinde

#### 1.6 Frontend — React Native (Expo) ✅
- **Stack:** Expo + React Native + React Query — same codebase runs on web, iOS, and Android
- **Web first** — Phase 1 targets web only; mobile comes later
- **Dashboard screen** — dark navy header, orange savings number, ghost list with per-service colour coding
  - Accounts bar — shows connected accounts with green/red status dot; Scan button triggers on-demand ingestion
  - Service pill filter — tap a service pill to filter the ghost list; tap again to clear
- **Connect screen** — credential form (label, Access Key ID, Secret Access Key, region) with IAM permissions hint; auto-shown on first login when no accounts are connected
- **Detail screen** — service-coloured header, stats grid, reason, remediation hint per service type
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

**Note:** SQLite is retained for unit tests only (throwaway per-test DB files). Runtime uses PostgreSQL.

#### 1.8 Storage Layer — PostgreSQL ✅

**Tables:**

```sql
cost_records   — raw billing data from Cost Explorer / fixture
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
./scripts/dev.sh          # fixture data (DEV_MODE=true)
./scripts/dev.sh --aws    # real AWS (DEV_MODE=false)
./scripts/dev.sh stop     # kill all services
```

**Inspect database:**
```bash
./scripts/check_db.sh
```

**`DEV_MODE` switch:**
- `DEV_MODE=true` → reads from `fixtures/costs.json` and `fixtures/usage.json`
- `DEV_MODE=false` → calls real AWS Cost Explorer + Describe APIs + CloudWatch

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
- DB-status concurrency guard: account is set to `scanning` before the goroutine fires; a second scan request will see the status and can be rejected at the application layer (full in-memory lock deferred to 2.7 Redis queue)

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

> **Note:** Row-Level Security and `CREATE POLICY` are PostgreSQL-only features. The SQLite dev schema omits these — tenant isolation in dev relies on application-level filtering.

**Key files:**
- `services/shared/model/account.go` — Account struct (`SecretEncrypted` omitted from JSON)
- `services/shared/crypto/crypto.go` — AES-256-GCM encrypt/decrypt (shared between api and ingestion)
- `services/shared/storage/storage.go` — Store interface extended with 5 account methods
- `services/shared/storage/postgres/postgres.go` — Full account CRUD implementation
- `services/shared/storage/sqlite/sqlite.go` — Stub implementations (dev-only, accounts not supported in SQLite)
- `services/ingestion/cmd/main.go` — Long-lived HTTP server; `POST /scan` decrypts credentials and runs ingestion

**Ingestion scan flow:**
1. API receives `POST /accounts/{id}/scan`
2. API checks in-memory scan lock — rejects if account is already scanning
3. API sets account status to `scanning`, fires async goroutine
4. Goroutine POSTs `{account_id, tenant_id}` to ingestion service at `http://localhost:8081/scan`
5. Ingestion fetches account from DB, decrypts secret, sets AWS env vars, runs full ingestion
6. API updates account status to `connected` or `error` on completion

**Scan recovery (planned — see milestone May 2026):** A background ticker (every 5 minutes) will check for accounts stuck in `scanning` status for longer than 15 minutes and reset them to `error` with a timeout reason. This prevents permanently stuck scans if the API restarts mid-scan or the ingestion service crashes. Not yet implemented — currently a stuck scan requires a manual status reset.

#### 2.3 Auth — Kinde ✅

- Chosen over Supabase Auth, Clerk, Cognito — see `docs/auth.md`
- JWT middleware in `services/api/internal/middleware/`
- Tenant + user persisted on first login
- Dashboard login screen with PKCE flow

#### 2.4 PostgreSQL Migration ✅

**Priority: Before auto-scan and Redis — eliminates SQLite concurrency bottleneck.**

- PostgreSQL is the runtime database; SQLite remains for unit tests only
- Migrate all tables: `cost_records`, `ghost_records`, `tenants`, `users`, `accounts`
- Enable Row-Level Security for tenant isolation on `accounts`, `ghost_records`, `cost_records`
- Dev: PostgreSQL container in `docker-compose.yml`
- Production: RDS PostgreSQL (`db.t4g.micro`)
- Add `services/shared/storage/postgres/migrations/` — versioned SQL migrations
- Update `DEV_MODE` to still use fixture data but write to PostgreSQL
- Retain SQLite implementation for unit tests only (throwaway per-test databases)

**Key files:**
- `services/shared/storage/postgres/postgres.go` — full `Store` implementation (partially exists for accounts)
- `services/shared/storage/postgres/migrations/*.sql` — versioned schema files

#### 2.5 Observability

**Priority: Must ship before production deployment.**

- **Structured logging:** Replace `log.Printf` with `log/slog` (stdlib) — JSON output in production, text in dev
  - Every log line includes: `tenant_id`, `account_id`, `request_id`, `service`
  - Scan lifecycle: `scan.started`, `scan.completed`, `scan.failed` with duration and ghost count
- **Error tracking:** Sentry Go SDK — captures panics, unhandled errors, and scan failures
  - Sentry DSN via `SENTRY_DSN` env var; disabled when unset
- **Metrics:** Prometheus client (`promhttp`) exposed on `/metrics` (internal port, not public)
  - `axiaops_scan_duration_seconds` — histogram per account
  - `axiaops_ghosts_detected_total` — counter per service
  - `axiaops_api_request_duration_seconds` — histogram per endpoint
  - `axiaops_api_requests_total` — counter per endpoint + status code
- **Health endpoint:** Already exists (`GET /health`); extend to include DB connectivity and ingestion service reachability
- **Production:** CloudWatch Container Insights for App Runner; Sentry for errors; Prometheus metrics scraped by Grafana Cloud (or CloudWatch custom metrics if Grafana is overkill at this stage)

**Key files to add:**
- `services/shared/logging/logging.go` — slog setup (JSON vs text based on env)
- `services/api/internal/middleware/requestid.go` — request ID injection
- `services/api/internal/middleware/metrics.go` — Prometheus HTTP middleware

#### 2.6 Scheduled Auto-Scan

- Add a `scan_interval_hours` field to the `accounts` table (default: 24)
- Background ticker in the API service triggers ingestion per account on schedule
- Configurable per account via `PATCH /accounts/{id}`
- Skips if account is already `scanning` (uses same concurrency guard from 2.2)
- Structured log: `scan.scheduled`, `scan.skipped_already_running`

#### 2.7 Savings History / Trend

- `ghost_records` is currently replaced on every run — no history is retained
- Add a `ghost_snapshots` table: `(id, tenant_id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency)`
- Ingestion job writes one snapshot row per scan instead of wiping ghost_records
- `GET /trend` — returns snapshot series for charting savings over time
- Dashboard: savings trend sparkline on the header

#### 2.8 Backup / Disaster Recovery

- **PostgreSQL:** Automated daily snapshots via RDS automated backups (7-day retention)
- **Point-in-time recovery:** RDS continuous backup — restore to any second within retention window
- **ghost_records safety:** With `ghost_snapshots` (2.7) in place, a bad scan no longer loses historical data. Current ghost_records can be regenerated by re-running a scan.
- **Secrets:** `ENCRYPTION_KEY` stored in AWS Secrets Manager (not in env vars on disk). Rotation procedure documented in `docs/ops.md`.
- **Infrastructure-as-code:** Docker Compose for dev; production infra defined in Terraform (App Runner, RDS, ElastiCache, Secrets Manager) — reproducible from scratch

#### 2.9 Redis

**Purpose:** Caching and scan job queue — keeps the API fast and decouples scan execution from HTTP requests.

**Use cases:**

| Use case | Detail |
|----------|--------|
| **JWKS key cache** | Cache Kinde's public keys in Redis with a 1h TTL — avoids a network round-trip to Kinde on every authenticated request |
| **Scan job queue** | `POST /accounts/{id}/scan` pushes a job onto a Redis list; a worker goroutine in the ingestion service pops and processes — decouples scan from the HTTP response |
| **Rate limiting** | Per-tenant request counter using `INCR` + `EXPIRE` — prevents abuse without a database query |

**Infrastructure:**
- Dev: Redis container added to `docker-compose.yml`
- Production: AWS ElastiCache Serverless (Redis-compatible) — pay-per-use, no cluster to manage
- Client: `github.com/redis/go-redis/v9`
- `REDIS_URL` env var (e.g. `redis://localhost:6379`); if unset, JWKS falls back to in-memory cache and scan stays synchronous

**Key files to add:**
- `services/shared/cache/cache.go` — `Cache` interface (`Get`, `Set`, `Del`)
- `services/shared/cache/redis/redis.go` — Redis implementation
- `services/shared/cache/memory/memory.go` — in-memory fallback for dev/test
- `services/api/internal/middleware/auth.go` — inject cache for JWKS lookup
- `services/ingestion/cmd/worker.go` — Redis queue consumer

#### 2.10 Alerting

- Weekly email digest: "You have $X in ghost spend this week"
- Email provider: Resend (preferred) or SendGrid
- Slack webhook alert — notify a channel when new ghosts appear after a scan
- Alerts reference `ghost_snapshots` (2.7) to show week-over-week delta

#### 2.11 Deployment

- API service: AWS App Runner — see `docs/deployment.md`
- Ingestion service: App Runner (long-lived, receives HTTP scan requests)
- Frontend: Expo EAS Build → web deploy (static assets behind CloudFront)
- Database: RDS PostgreSQL (`db.t4g.micro`) — see 2.4
- Cache: AWS ElastiCache Serverless (Redis) — see 2.9
- Secrets: AWS Secrets Manager for `ENCRYPTION_KEY`, `SENTRY_DSN`, `REDIS_URL`
- Infrastructure: Terraform modules for reproducible provisioning

---

## Phase 3 — Beta / GTM (September – December 2026)

### Goal: Feature-complete for first paying customers, establish revenue

**Scope principle:** This phase focuses on features that directly enable paying customers. Multi-cloud, mobile, and cost forecasting are deferred to Phase 4 — they don't block revenue and need more usage data / user demand to justify.

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

#### 3.3 Remediation Actions

- `GET /ghosts/{id}/remediation` — returns a ready-to-run AWS CLI command per resource type
- Pre-generated commands per service (e.g. `aws ec2 stop-instances`, `aws rds stop-db-instance`)
- Audit trail — all actions logged with timestamp and user

#### 3.4 Resource Inventory View ✅

**Status: Complete (shipped ahead of schedule)**

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

#### 3.10 Expanded Detection Rules

- Add rules for commonly wasted resources:
  - EBS volumes (unattached — `VolumeReadOps + VolumeWriteOps = 0`)
  - Elastic IPs (already covered in Phase 1)
  - Secrets Manager secrets (unused — `GetSecretValue` invocations = 0, >90 days old)
  - Redshift clusters (`DatabaseConnections = 0`)
  - ElastiCache nodes (`CurrConnections = 0`)
- Make detection rules configurable per tenant via `PATCH /settings/rules` — allow adjusting thresholds (e.g. EC2 CPU from 5% to 10%)
- Store custom rules in a `detection_rules` table; fall back to built-in defaults

#### 3.11 Reporting

- Exportable PDF savings report — summary page + per-service breakdown + ghost list
- Savings trend over time (chart) — powered by `ghost_snapshots` from 2.7

---

## Phase 4 — Scale & Expand (Q1–Q2 2027)

### Goal: Multi-cloud, mobile, proactive cost intelligence

#### 4.1 Cost Forecasting

- `GET /forecast?days=30|60|90` — project future spend per account and per service
- Algorithm: linear regression over `ghost_snapshots` collected by 2.7 — no ML library, ~50 lines of Go math
- Requires: minimum 60 days of `ghost_snapshots` data before forecasts are meaningful
- Anomaly detection: flag if actual spend exceeds forecast by >20% (surface as alert alongside weekly digest)
- Dashboard: forecast line overlaid on the existing savings trend chart (reuses `GET /trend` chart component)
- DB: no schema change — consumes existing `ghost_snapshots` table from 2.7

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
- Privacy policy, terms of service required for App Store submission

#### 4.4 Establish Operating UG

- Required before App Store submission (Apple/Google require a legal entity)
- Privacy policy and terms of service

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
| Database | PostgreSQL (runtime) + SQLite (unit tests only) |
| Cache / Queue | Redis (`go-redis/v9`) — JWKS cache, scan job queue, rate limiting |
| Frontend | React Native (Expo) — web first, mobile in Phase 4 |
| Auth | Kinde (see docs/auth.md) |
| Billing | Stripe (subscriptions, invoicing) |
| Hosting | AWS App Runner |
| Observability | slog (structured logging), Sentry (errors), Prometheus (metrics) |
| Backup | RDS automated backups (7-day retention, point-in-time recovery) |
| Infrastructure | Terraform (production), Docker Compose (dev) |
| CI/CD | GitLab CI |
| Cloud APIs | AWS Cost Explorer, CloudWatch, Azure Cost Mgmt (Phase 4), GCP Billing (Phase 4) |

---

## Milestones

| Date | Milestone | Status |
|------|-----------|--------|
| April 2026 | Go ingestion service + fixture data + zombie detection | ✅ Done |
| April 2026 | React Native web dashboard | ✅ Done |
| April 2026 | Docker Compose full-stack + unit tests | ✅ Done |
| April 2026 | AWS Cost Explorer + CloudWatch integration | ✅ Done |
| April 2026 | Kinde auth + tenant/user persistence | ✅ Done |
| April 2026 | API/ingestion service split + ghost_records DB | ✅ Done |
| April 2026 | Account management — connect AWS, encrypted secrets, on-demand scan | ✅ Done |
| April 2026 | Resource inventory view — all resources with ghost/active annotation | ✅ Done |
| May 2026 | PostgreSQL migration — replace SQLite for production workloads | Planned |
| May 2026 | Observability — structured logging, Sentry, Prometheus metrics | Planned |
| May 2026 | Scan recovery — timeout detection for stuck scans | Planned |
| June 2026 | Scheduled auto-scan (24h default interval per account) | Planned |
| June 2026 | Savings history / trend (`ghost_snapshots` + `GET /trend`) | Planned |
| June 2026 | Backup / disaster recovery — RDS snapshots, Secrets Manager | Planned |
| July 2026 | Redis — JWKS cache, scan job queue, rate limiting | Planned |
| July 2026 | Weekly email digest + Slack webhook alerts | Planned |
| August 2026 | Production deployment — App Runner, RDS, ElastiCache, Terraform | Planned |
| September 2026 | Pricing & billing — Stripe integration, 3 tiers | Planned |
| September 2026 | Dismiss ghost workflow + snooze | Planned |
| October 2026 | Remediation CLI commands + audit trail | Planned |
| October 2026 | Scan history log + per-account summary | Planned |
| October 2026 | Tag/team filtering + CSV export | Planned |
| November 2026 | Expanded detection rules + configurable thresholds | Planned |
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
