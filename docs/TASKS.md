# AxiaOps — Task List

> Derived from `development_plan.md` and `next_steps.md`.
> Phase 1 and most of Phase 2 early work are complete. This list focuses on **remaining work** with executable steps, grouped by milestone.
>
> Single developer · ~6–7 productive hours/day · AI-assisted (~3× multiplier)
> Estimated remaining: **22–32 days** with AI

---

## Legend

- `[ ]` — Not started
- `[x]` — Complete
- `[~]` — In progress / partially done

---

## ✅ Phase 1 — MVP (Complete)

- [x] Cost + usage fixture data (`fixtures/costs.json`, `fixtures/usage.json`)
- [x] Go ingestion service with `Provider` interface and `DEV_MODE` switch
- [x] Zombie detection analyzer (`Detect()`, `Summarize()`, threshold rules)
- [x] REST API: `GET /ghosts`, `GET /summary`, `GET /health`, `GET /accounts`, `POST /accounts`, `DELETE /accounts/{id}`, `POST /accounts/{id}/scan`
- [x] Kinde auth — PKCE flow, RS256 JWT middleware, tenant + user persistence
- [x] React Native (Expo) dashboard — ghost list, detail screen, connect screen, accounts bar
- [x] Docker Compose full-stack (PostgreSQL, ingestion, api, nginx)
- [x] PostgreSQL schema with RLS on all tables
- [x] 44+ unit tests across 6 packages
- [x] AWS Cost Explorer + CloudWatch + resource discovery integration
- [x] AES-256-GCM secret encryption at rest
- [x] Account management CRUD (save, list, get, delete, update status)
- [x] Resource inventory view — `GET /resources`, `resource_records` table, ghost/active annotation
- [x] GitLab CI pipeline — `go test ./...` on every push

---

## 🔄 Phase 2 — Alpha (May–August 2026)

### Milestone: May 2026

#### 2.4 Versioned Migrations (golang-migrate)

> Required before onboarding any real customer. Current `ALTER TABLE` on startup is not production-safe.

- [ ] Install `golang-migrate` CLI and add `github.com/golang-migrate/migrate/v4` to Go modules
- [ ] Create `services/shared/storage/postgres/migrations/001_initial.sql` — baseline schema (all current tables)
- [ ] Create `services/shared/storage/postgres/migrations/002_add_tenant_id.sql` — tenant_id columns + RLS policies
- [ ] Wire migration runner into `services/api/cmd/main.go` and `services/ingestion/cmd/main.go` — run on startup using `MIGRATION_DATABASE_URL`
- [ ] Remove inline `CREATE TABLE IF NOT EXISTS` and `ALTER TABLE` calls from application code
- [ ] Test: run `make test-postgres` and verify `schema_migrations` table is populated correctly
- [ ] Test: run migrations from scratch on a clean PostgreSQL container

#### 2.5 Savings History / Trend

> Must ship before production — historical ghost data is lost on every scan currently.

- [ ] Write migration `003_add_ghost_snapshots.sql` — create `ghost_snapshots(id, tenant_id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency)`
- [ ] Update `Store` interface in `services/shared/storage/storage.go` — add `SaveSnapshot(ctx, snapshot)` and `ListSnapshots(ctx, accountID, limit)` methods
- [ ] Implement `SaveSnapshot` + `ListSnapshots` in `services/shared/storage/postgres/postgres.go`
- [ ] Add `SaveSnapshot` stub to SQLite store (dev/test only)
- [ ] Update ingestion scan flow — write one snapshot row per scan after `SaveGhosts`
- [ ] Add `GET /v1/trend?account_id={id}&days=30` endpoint in `services/api/internal/api/`
- [ ] Dashboard: savings trend sparkline on the header (replace static savings banner)
- [ ] Test: scan twice, verify two snapshot rows exist and `GET /trend` returns series

#### 2.6 Observability

> Required before production deployment.

- [ ] Create `services/shared/logging/logging.go` — `slog` setup (JSON in production, text in dev based on `LOG_FORMAT` env var)
- [ ] Replace all `log.Printf` / `fmt.Println` calls in all three services with `slog.Info/Error/Warn`
- [ ] Create `services/api/internal/middleware/requestid.go` — inject `X-Request-ID` header and add to `slog` context
- [ ] Add scan lifecycle logs: `scan.started`, `scan.completed`, `scan.failed` with `tenant_id`, `account_id`, `duration_ms`, `ghost_count`
- [ ] Add Sentry Go SDK (`github.com/getsentry/sentry-go`) to `services/api` and `services/ingestion`
  - Read `SENTRY_DSN` env var; skip initialisation if unset (dev/test safe)
  - Capture panics and unhandled errors; call `sentry.Flush()` on graceful shutdown
- [ ] Create `services/api/internal/middleware/metrics.go` — Prometheus HTTP middleware
  - `axiaops_api_request_duration_seconds` histogram (endpoint + status code labels)
  - `axiaops_api_requests_total` counter
- [ ] Add `axiaops_scan_duration_seconds` histogram and `axiaops_ghosts_detected_total` counter in ingestion service
- [ ] Expose `/metrics` on the API service (internal port — not behind auth middleware)
- [ ] Extend `GET /health` to check DB connectivity (`SELECT 1`) and ingestion reachability (`GET http://ingestion:8081/health`)
- [ ] Test: run `make start-dev`, hit `/metrics`, verify Prometheus output format

#### 2.7 Scan Recovery (Stuck Scan Timeout)

- [ ] Add background ticker in `services/api` — runs every 5 minutes
- [ ] Query: find accounts with `status = 'scanning'` and `last_scanned_at < NOW() - INTERVAL '15 minutes'`
- [ ] Update those accounts to `status = 'error'` with a `scan_timeout` reason
- [ ] Log `scan.timeout_reset` with `account_id` and `stuck_duration`
- [ ] Test: manually set an account to `scanning` with an old timestamp, verify ticker resets it

#### 2.8 API Versioning

> Must be done before any external integrations or docs are published.

- [ ] Update all routes in `services/api/internal/api/` to use `/v1/` prefix
  - `GET /v1/ghosts`, `GET /v1/summary`, `GET /v1/accounts`, `POST /v1/accounts`, etc.
  - `GET /health` and `GET /metrics` stay unversioned
- [ ] Update nginx config — rewrite `/api/v1/*` → `api:8080/v1/*`
- [ ] Update dashboard API client base path to `/v1/`
- [ ] Update all handler tests to use `/v1/` paths
- [ ] Test: `make test` passes; hit `/v1/ghosts` in browser

#### 2.9 In-Memory Rate Limiting

> Temporary guard until Redis ships in July. Must be live before production.

- [ ] Create `services/api/internal/middleware/ratelimit.go`
  - `sync.Map` keyed by `tenant_id`
  - Sliding window: 60 requests/minute per tenant
  - Returns `429 Too Many Requests` with `Retry-After` header
  - No-op when `DEV_MODE=true`
- [ ] Wire into the middleware chain in `services/api/cmd/main.go` (after auth, before handlers)
- [ ] Test: write a test that fires >60 requests and asserts 429 is returned

#### 2.10 Graceful Shutdown

> App Runner sends `SIGTERM` before container termination.

- [ ] `services/api/cmd/main.go` — listen for `SIGTERM`/`SIGINT` via `signal.NotifyContext`
  - Call `server.Shutdown(ctx)` with 30-second drain timeout
  - Call `pool.Close()` after server exits
  - Log `shutdown.started` and `shutdown.complete` with drain duration
- [ ] `services/ingestion/cmd/main.go` — same pattern
  - Reject new `POST /scan` requests during drain (return `503 Service Unavailable`)
  - Allow current scan to complete before shutdown
- [ ] Test: send `SIGTERM` during an active scan; verify scan completes and server exits cleanly

---

### Milestone: June 2026

#### 2.11 GitLab CI Pipeline

- [ ] Create `.gitlab-ci.yml` at repo root with three stages: `test`, `build`, `deploy`
- [ ] **test stage:**
  - `go test ./...` for all three modules
  - `go vet ./...`
  - `golangci-lint run` (add `.golangci.yml` config)
  - Run on all branches
- [ ] **build stage:** (main branch only)
  - Build Docker images for `api`, `ingestion`, `dashboard`
  - Tag with Git SHA
  - Push to AWS ECR (use `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` CI/CD variables — masked)
- [ ] **deploy stage:** (main branch only, after build succeeds)
  - `aws apprunner update-service` for `api` and `ingestion`
  - CloudFront invalidation for dashboard static assets
- [ ] Add required CI/CD variables in GitLab: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `SENTRY_DSN`, `ENCRYPTION_KEY`
- [ ] Test: push to a feature branch — only `test` stage runs; merge to `main` — full pipeline runs

#### 2.12 Scheduled Auto-Scan

- [ ] Write migration `004_add_scan_interval.sql` — add `scan_interval_hours INTEGER NOT NULL DEFAULT 24` to `accounts` table
- [ ] Update `model.Account` struct in `services/shared/model/account.go` to include `ScanIntervalHours`
- [ ] Add `PATCH /v1/accounts/{id}` endpoint — update `label`, `region`, `scan_interval_hours` fields
- [ ] Add background ticker in `services/api/cmd/main.go`
  - Runs every 60 minutes; queries accounts where `last_scanned_at < NOW() - INTERVAL '{scan_interval_hours} hours'`
  - Skips accounts already in `scanning` status
  - Fires `POST http://ingestion:8081/scan` for eligible accounts
  - Log `scan.scheduled` and `scan.skipped_already_running`
- [ ] Dashboard: show next scheduled scan time per account in accounts bar
- [ ] Test: set `scan_interval_hours=0` (immediate), verify ticker triggers a scan within 60 seconds

#### 2.13 cost_records Retention

- [ ] Add `COST_RECORDS_RETENTION_DAYS` env var (default `90`)
- [ ] Add background ticker in `services/ingestion` — runs daily at midnight UTC
  - `DELETE FROM cost_records WHERE period_end < NOW() - INTERVAL '{n} days' AND tenant_id = $1`
  - Log `cost_records.cleanup` with `rows_deleted` and `duration_ms`
- [ ] Write migration `005_add_cost_records_index.sql` — add index on `(tenant_id, period_end)` for efficient range deletes
- [ ] Test: insert cost records with old `period_end`, run cleanup manually, verify rows deleted

---

### Milestone: July 2026

#### 2.14 Redis

- [ ] Add Redis container to `docker-compose.yml` (image: `redis:7-alpine`)
- [ ] Add `REDIS_URL` env var to all service configs (default: `redis://localhost:6379`)
- [ ] Create `services/shared/cache/cache.go` — `Cache` interface (`Get`, `Set`, `Del`)
- [ ] Create `services/shared/cache/redis/redis.go` — Redis implementation using `github.com/redis/go-redis/v9`
- [ ] Create `services/shared/cache/memory/memory.go` — in-memory fallback (used when `REDIS_URL` is unset)
- [ ] Update `services/api/internal/middleware/auth.go` — inject cache for JWKS lookup (1h TTL); fall back to in-memory fetch if cache miss
- [ ] Create `services/ingestion/cmd/worker.go` — Redis queue consumer
  - API pushes scan jobs onto a Redis list (`LPUSH axiaops:scan_queue`)
  - Worker pops (`BRPOP`) and executes ingestion
  - Falls back to synchronous execution if `REDIS_URL` is unset
- [ ] Replace in-memory rate limiter (2.9) with Redis `INCR` + `EXPIRE` counter — survives API restarts
- [ ] Test: run with and without `REDIS_URL` — verify fallback behaviour works correctly

#### 2.15 Weekly Email Digest + Slack Alerts

- [ ] Choose and set up email provider — **Resend** (preferred; add `RESEND_API_KEY` env var)
- [ ] Build digest HTML email template — ghost count, top 5 ghost resources by cost, week-over-week delta from `ghost_snapshots`
- [ ] Trigger digest after nightly ingestion: if new ghosts detected since last digest, send to all tenant users
- [ ] Add `SLACK_WEBHOOK_URL` env var per account (store in `accounts` table — write migration `006_add_slack_webhook.sql`)
- [ ] Send Slack message after scan completes if ghost count changes (new ghosts or resolved ghosts)
- [ ] Add `POST /v1/settings/notifications` endpoint — enable/disable email digest and Slack webhook per tenant
- [ ] Test: trigger a scan in staging, verify email is received and Slack message is posted

---

### Milestone: August 2026

#### 2.16 Production Deployment

- [ ] **IAM setup**
  - Create `AxiaOpsAppRunnerRole` IAM role for App Runner — access to ECR, Secrets Manager, RDS
  - Create `AxiaOpsCI` IAM user for GitLab CI — ECR push + App Runner update permissions only
- [ ] **Terraform**
  - Write Terraform modules: App Runner (api + ingestion), RDS PostgreSQL `db.t4g.micro`, ElastiCache Serverless, Secrets Manager secrets, ECR repositories, VPC + security groups (public subnets — no NAT Gateway)
  - S3 bucket + DynamoDB table for Terraform state backend
  - Store in `terraform/` directory at repo root
  - `terraform apply` on a fresh AWS account; verify all services start
- [ ] **RDS**
  - Provision `db.t4g.micro` PostgreSQL 16 in `eu-central-1`
  - Run versioned migrations against production RDS using `MIGRATION_DATABASE_URL`
  - Enable automated daily snapshots (7-day retention)
  - Set CloudWatch log retention to 7 days
- [ ] **Secrets Manager**
  - Store `ENCRYPTION_KEY`, `SENTRY_DSN`, `REDIS_URL`, `RESEND_API_KEY`, `KINDE_ISSUER`, `KINDE_CLIENT_ID` in Secrets Manager
  - Reference secrets in App Runner service definition (not environment variables)
  - Document key rotation procedure in `docs/ops.md` (re-encrypt all `secret_encrypted` rows before rotating `ENCRYPTION_KEY`)
- [ ] **App Runner**
  - Deploy `api` service on `:8080` — wire to RDS and ElastiCache
  - Deploy `ingestion` service on `:8081` — wire to RDS and ElastiCache
  - Set up custom domain + TLS via App Runner managed certificate
  - Configure App Runner health check: `GET /health`, 30-second timeout
- [ ] **Dashboard**
  - Expo EAS Build → web static export
  - Upload to S3 + CloudFront distribution
  - CloudFront invalidation in CI deploy stage
- [ ] **EventBridge** — wire EventBridge cron (`rate(24 hours)`) → `POST /v1/accounts/{id}/scan` for each tenant account
- [ ] **Smoke test production** — connect a real AWS account, trigger scan, verify ghosts appear in dashboard

---

## 📋 Phase 3 — Beta / GTM (September–December 2026)

### 3.1 Pricing & Billing — Stripe (September 2026)

- [ ] Sign up for Stripe; get API keys (`STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`)
- [ ] Write migration `007_add_billing_fields.sql` — add `plan TEXT DEFAULT 'free'`, `stripe_customer_id TEXT`, `stripe_subscription_id TEXT` to `tenants` table
- [ ] Define products in Stripe dashboard: Free, Pro (€49/mo), Team (€149/mo)
- [ ] Create `services/api/internal/middleware/billing.go` — read `tenant.plan`, enforce tier limits:
  - Free: 1 account, no auto-scan
  - Pro: up to 5 accounts, auto-scan enabled, CSV export enabled
  - Team: unlimited accounts, user roles, Slack alerts
- [ ] Add `POST /webhooks/stripe` handler — handle `customer.subscription.created/updated/deleted` events
- [ ] Dashboard: plan indicator in header; upgrade prompt when hitting limits; pricing page
- [ ] Implement 14-day Pro trial on signup (no credit card) — set `plan=pro_trial`, `trial_ends_at` in tenants table
- [ ] Test: simulate Stripe webhook events in test mode; verify plan enforcement middleware blocks correctly

### 3.2 Dismiss Ghost Workflow (September 2026)

- [ ] Write migration `008_add_dismissed_ghosts.sql` — create `dismissed_ghosts(id, tenant_id, resource_id, reason, note, dismissed_by, dismissed_at, snooze_until)`
- [ ] Add `Store` methods: `DismissGhost`, `ListDismissedGhosts`, `UndismissGhost`
- [ ] Add `POST /v1/ghosts/{id}/dismiss` — body: `{reason, note, snooze_until?}`
- [ ] Add `DELETE /v1/ghosts/{id}/dismiss` — undismiss / cancel snooze
- [ ] Update `GET /v1/ghosts` — exclude dismissed by default; support `?include_dismissed=true`
- [ ] Add background ticker to clear expired snoozes (`snooze_until < NOW()`)
- [ ] Dashboard: "Dismiss" button on DetailScreen; dismissed ghosts show grey "Intentional" badge; snooze UI with date picker
- [ ] Test: dismiss a ghost, verify it disappears from list; set snooze, verify it reappears after expiry

### 3.3 Remediation Actions (October 2026)

- [ ] Build remediation command generator in `services/shared/analyzer/remediation.go`
  - EC2: `aws ec2 stop-instances --instance-ids {id}`
  - RDS: `aws rds stop-db-instance --db-instance-identifier {id}`
  - Lambda: `aws lambda delete-function --function-name {name}`
  - ELB: `aws elbv2 delete-load-balancer --load-balancer-arn {arn}`
  - NAT Gateway: `aws ec2 delete-nat-gateway --nat-gateway-id {id}`
  - EIP: `aws ec2 release-address --allocation-id {id}`
- [ ] Add `GET /v1/ghosts/{id}/remediation` endpoint — returns command string for the resource type
- [ ] Write migration `009_add_audit_log.sql` — create `audit_log(id, tenant_id, user_id, action, resource_id, reason, created_at)`
- [ ] Log every dismiss, undismiss, and remediation view to `audit_log`
- [ ] Dashboard: "Copy CLI command" button on DetailScreen
- [ ] Test: hit `/v1/ghosts/{id}/remediation` for each service type, verify correct command returned

### 3.5 Scan History Log (October 2026)

- [ ] Write migration `010_add_scan_history.sql` — create `scan_runs(id, tenant_id, account_id, started_at, finished_at, ghost_count, total_monthly_cost, status, error)`
- [ ] Update ingestion scan flow — write one `scan_runs` row at start (`status=running`) and update on completion or failure
- [ ] Add `GET /v1/accounts/{id}/scans` endpoint — returns last N scan runs for an account
- [ ] Dashboard: scan history list under each account (timestamp, ghost count, status badge, duration)
- [ ] Test: run two scans; verify two rows in `scan_runs`; GET returns both in descending order

### 3.6 Tag / Team Filtering (October 2026)

- [ ] Update `GET /v1/ghosts` — support `?team=backend&env=prod` query params (filter on `tags` JSON column)
- [ ] Update `GET /v1/resources` — same tag filtering support
- [ ] Dashboard: tag filter chips alongside service pill filter
- [ ] Test: insert ghosts with different team tags; verify filter returns correct subset

### 3.7 CSV Export (October 2026)

- [ ] Add `?format=csv` support to `GET /v1/ghosts`
  - Columns: `resource_id`, `service`, `region`, `monthly_cost`, `reason`, `owner`, `detected_at`
  - Set `Content-Disposition: attachment; filename="ghosts.csv"` response header
- [ ] Gate behind Pro tier via billing middleware (3.1)
- [ ] Dashboard: "Export CSV" button on ghost list screen (hidden on Free tier, shows upgrade prompt)
- [ ] Test: hit `GET /v1/ghosts?format=csv`, verify valid CSV response with correct columns

### 3.8 Per-Account Summary (October 2026)

- [ ] Update `GET /v1/summary` — support `?account_id={id}` query param
- [ ] Dashboard: per-account savings shown in accounts bar alongside the Scan button
- [ ] Test: connect two accounts with different ghosts, verify `?account_id=` returns isolated totals

### 3.9 User Management (November 2026)

- [ ] Add `role TEXT NOT NULL DEFAULT 'admin'` to `users` table — migration `011_add_user_roles.sql`
- [ ] Implement `viewer` role enforcement in API middleware — block scan/connect/dismiss for viewers
- [ ] Add `GET /v1/users` — list users in tenant (admin only)
- [ ] Add `DELETE /v1/users/{id}` — remove access (admin only); anonymise audit log entries (replace `user_id` with tombstone, preserve log integrity)
- [ ] Gate user management behind Team tier
- [ ] Integrate Kinde organisation invites for adding new users
- [ ] Test: create viewer token, attempt to POST /scan, verify 403 returned

### 3.10 GDPR / Data Deletion (September 2026)

> Must be in place before acquiring paying customers in the EU.

- [ ] Add `DELETE /v1/tenants/me` endpoint — cascade delete: accounts, cost_records, ghost_records, ghost_snapshots, users, dismissed_ghosts, scan_runs, audit_log
- [ ] Trigger Stripe subscription cancellation on tenant deletion (via Stripe API, not just webhook)
- [ ] Delete all encrypted AWS secrets immediately on tenant deletion
- [ ] Add `GET /v1/export` endpoint — full JSON dump: ghosts, account metadata (no secrets), scan history
- [ ] Write privacy policy and terms of service pages (required before Phase 3 launch)
- [ ] Document data retention policy: what is stored, for how long, in which region
- [ ] Test: create tenant, add data, call DELETE, verify all rows cascade-deleted from all tables

### 3.11 Expanded Detection Rules (November 2026)

- [ ] Add rules to `services/shared/analyzer/analyzer.go`:
  - EBS: `VolumeReadOps + VolumeWriteOps = 0` (unattached volume)
  - Secrets Manager: `GetSecretValue = 0` over 90 days
  - Redshift: `DatabaseConnections = 0`
  - ElastiCache: `CurrConnections = 0`
  - S3: `GetRequests = 0` over 60 days, skip if tagged `archival=true`
  - CloudFront: `Requests = 0` over 30 days
- [ ] Add corresponding resource discovery in `services/ingestion/internal/provider/aws/discover.go`
  - `ec2:DescribeVolumes`, `secretsmanager:ListSecrets`, `redshift:DescribeClusters`, `elasticache:DescribeCacheClusters`, `s3:ListBuckets`, `cloudfront:ListDistributions`
- [ ] Update IAM policy doc in `docs/deployment.md` with new required permissions
- [ ] Write migration `012_add_detection_rules.sql` — create `detection_rules(id, tenant_id, service, metric, threshold, enabled)` table
- [ ] Add `PATCH /v1/settings/rules` endpoint — allow tenants to override thresholds (e.g. EC2 CPU from 5% to 10%)
- [ ] Fall back to built-in defaults when no custom rule exists for a service
- [ ] Test: add rule for EC2 with 10% threshold; verify analyzer uses custom threshold

### 3.12 Legal Entity (September 2026)

- [ ] Register operating entity (UG or GmbH — decide based on revenue trajectory at that point)
- [ ] Obtain Apple Developer account ($99/year) — required for TestFlight and App Store
- [ ] Obtain Google Play Developer account ($25 one-time)
- [ ] VAT registration if EU revenue exceeds threshold
- [ ] Publish privacy policy and terms of service on production domain

### 3.13 PDF Savings Report (November 2026)

- [ ] Add `GET /v1/reports/savings?format=pdf` endpoint
  - Summary page: total ghost spend, potential savings, date range
  - Per-service breakdown table
  - Ghost resource list with cost, reason, owner
  - Savings trend chart (uses `ghost_snapshots` from 2.5)
- [ ] Use a Go PDF library (`github.com/jung-kurt/gofpdf` or `github.com/unidoc/unipdf`) — no external service
- [ ] Gate behind Pro tier
- [ ] Dashboard: "Export PDF Report" button on summary screen
- [ ] Test: generate PDF for a tenant with known data; verify page count and key fields present

---

## 🔮 Phase 4 — Scale & Expand (Q1–Q2 2027)

### 4.1 Cost Forecasting (Q1 2027)

- [ ] Implement linear regression over `ghost_snapshots` in `services/shared/analyzer/forecast.go` (stdlib `math` only — no ML lib, ~50 lines)
- [ ] Minimum 60 days of snapshot data required before enabling forecasts (return `402 Insufficient Data` otherwise)
- [ ] Add `GET /v1/forecast?days=30|60|90` — project future ghost spend per account
- [ ] Anomaly detection: flag if actual spend exceeds forecast by >20% — surface as Slack/email alert
- [ ] Dashboard: forecast line overlaid on savings trend chart

### 4.2 Multi-Cloud — Azure (Q1 2027)

- [ ] Implement `Provider` interface for Azure Cost Management API (`services/ingestion/internal/provider/azure/`)
- [ ] Implement `Provider` interface for Azure Monitor metrics
- [ ] Add `provider=azure` support in `accounts` table (schema already provider-agnostic)
- [ ] Dashboard: provider icon (AWS/Azure/GCP) on each account and resource card; provider filter pill
- [ ] Only pursue after AWS has proven paying customers

### 4.3 Multi-Cloud — GCP (Q2 2027)

- [ ] Implement `Provider` interface for GCP Billing Export → BigQuery
- [ ] Implement `Provider` interface for GCP Cloud Monitoring metrics

### 4.4 FOCUS Specification (Q2 2027)

- [ ] Implement `focusfile` provider — reads FOCUS-formatted billing exports from S3/blob storage
- [ ] Map FOCUS columns (`BilledCost`, `ResourceId`, `ServiceName`, `RegionName`, `Tags`) → `model.CostRecord`
- [ ] Use FOCUS as ingestion path for Azure and GCP (one parser for all clouds)
- [ ] Offer FOCUS as optional ingestion path for AWS customers who already export to S3

### 4.5 Mobile App (Q2 2027)

- [ ] Run Expo dev build on iOS simulator: `npx expo run:ios`
- [ ] Run Expo dev build on Android emulator: `npx expo run:android`
- [ ] Fix any mobile-specific layout issues (scroll behaviour, safe areas, font scaling)
- [ ] Submit to TestFlight for internal testing (requires Apple Developer account from 3.12)
- [ ] Submit to App Store after TestFlight sign-off (legal entity from 3.12 required)

---

## 🚀 Phase 5 — Proactive Cost Simulation (Q3–Q4 2027)

### 5.1 IaC Plan Parser

- [ ] Parse `terraform plan -out=plan.json` (Terraform 1.5+ format only — stable JSON schema)
- [ ] Parse `cdk diff` output
- [ ] Extract: resource types, sizes, regions, counts

### 5.2 Cost Estimation Engine

- [ ] Fetch live pricing from AWS Pricing API, Azure Retail Prices API, GCP Cloud Billing Catalog
- [ ] Compute monthly cost delta per planned resource
- [ ] Integrate with forecast (4.1) — planned deltas adjust projected spend

### 5.3 What-if Scenarios

- [ ] "What if I use gp3 instead of gp2?" → show EBS savings
- [ ] "What if I switch region?" → show inter-region delta
- [ ] "What if I use Spot?" → show risk vs savings trade-off

### 5.4 CI/CD Budget Gate

- [ ] GitLab CI / GitHub Actions integration — post cost delta as MR/PR comment
- [ ] Configurable threshold: warn or block merge if delta exceeds limit

### 5.5 CLI Tool

- [ ] `axiaops estimate --plan plan.json` — standalone binary
- [ ] `brew install axiaops` — Homebrew formula

---

## 📊 Milestone Timeline

| Date | Milestone | Status |
|------|-----------|--------|
| April 2026 | Phase 1 complete + Phase 2 early work | ✅ Done |
| May 2026 | Versioned migrations, savings history, observability, scan recovery, API versioning, rate limiting, graceful shutdown | Planned |
| June 2026 | GitLab CI pipeline, scheduled auto-scan, cost_records retention | Planned |
| July 2026 | Redis (JWKS cache, scan queue, rate limiting), weekly digest + Slack alerts | Planned |
| August 2026 | Production deployment (App Runner, RDS, ElastiCache, Terraform, CloudFront) | Planned |
| September 2026 | Stripe billing, dismiss ghost, GDPR/data deletion, legal entity | Planned |
| October 2026 | Remediation actions, scan history, tag filtering, CSV export, per-account summary | Planned |
| November 2026 | Expanded detection rules, user management + roles, PDF report | Planned |
| December 2026 | First paying customer · target: 10 customers, €5K MRR | Planned |
| Q1 2027 | Cost forecasting, Azure integration | Planned |
| Q2 2027 | GCP integration, FOCUS spec, mobile app | Planned |
| Q3–Q4 2027 | IaC plan parser, cost estimation engine, CI/CD budget gate, CLI tool | Planned |
