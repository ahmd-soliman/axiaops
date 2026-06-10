# AxiaOps — Task Tracker

_Single source of truth for project work. Last updated: 2026-05-28._

> User-story tracking (GitLab issues) lives in `docs/USER_STORIES_STATUS.md`
> and the two scripts in `scripts/`. This file is the engineering view —
> phases, features, and the granular subtasks under each.

---

## Legend

- ✅ Feature shipped
- 🔲 Feature pending
- `[x]` Subtask complete
- `[ ]` Subtask not started
- `[~]` Subtask in progress

---

## ✅ Phase 1 — MVP (Complete, April 2026)

- [x] Go ingestion service with AWS integration
- [x] Zombie detection analyzer (`Detect()`, `Summarize()`, threshold rules)
- [x] REST API: `GET /zombies`, `GET /summary`, `GET /health`, `GET /accounts`, `POST /accounts`, `DELETE /accounts/{id}`, `POST /accounts/{id}/scan`
- [x] Native cookie auth — argon2id passwords, sessions table, OIDC SSO ceremony (replaced Kinde — see 2.7.1)
- [x] Vite + React dashboard — zombie list, detail screen, connect screen, accounts bar
- [x] Docker Compose full-stack (PostgreSQL, ingestion, api, nginx)
- [x] PostgreSQL schema with RLS on all tables
- [x] 44+ unit tests across 6 packages
- [x] AWS Cost Explorer + CloudWatch + resource discovery integration
- [x] AES-256-GCM secret encryption at rest
- [x] Account management CRUD (save, list, get, delete, update status)
- [x] Resource inventory view — `GET /resources`, `resource_records` table, zombie/active annotation
- [x] GitLab CI pipeline — `go test ./...` on every push

---

## Phase 2 — Alpha (target August 2026)

### ✅ Shipped

| Feature | Notes |
|---------|-------|
| AWS Cost Explorer + CloudWatch integration | Real AWS data, dev mock fallback |
| Native cookie auth + OIDC SSO | argon2id password hashing, sessions table, JIT provisioning |
| Multi-tenancy (RLS) | Row-level security on all tables |
| Account management | Connect/delete AWS accounts, encrypted secrets (AES-256-GCM), on-demand scan |
| Resource inventory view | All resources with zombie/active annotation |
| Savings history / trend | `zombie_snapshots` table + `GET /v1/trend` |
| Observability | Structured logging (slog), Prometheus metrics |
| API versioning | `/v1/` prefix on all endpoints |
| In-memory rate limiting | Token bucket per tenant, falls back to Redis when available |
| Graceful shutdown | SIGTERM handling across API + ingestion |
| GitLab CI pipeline | test + build stages, all unit/integration/migration jobs |
| Redis — JWKS cache | `cache.Cache` interface, Redis + memory impls; injected into auth middleware |
| Redis — scan job queue | `queue.Queue` interface, Redis + sync impls; worker in ingestion `cmd/worker.go` |
| Redis — rate limiting | `RateLimiter` backed by `cache.Cache.Incr`; uses Redis when available |
| Scheduled auto-scan | Background ticker in ingestion; `scanScheduledAccounts` every `SCAN_INTERVAL` |
| `cost_records` 90-day retention | Daily cleanup ticker in ingestion; `DeleteOldCostRecords` |
| Dismiss / snooze workflow | `dismissed_zombies` table (migration 002), REST endpoints, dashboard UI |
| Snooze expiry worker | Background ticker expires snoozed records via `ExpireSnoozes` |
| Pricing rates — YAML config | Hardcoded `const` rates in `discover.go` moved to `services/shared/pricing/rates.yml`; loader + per-region override support; fixes the credibility bug from the CE anomaly monitor ($3/mo claim with no source). |
| Wire Redis in API `main.go` | `cache.New(REDIS_URL)` injected into `NewAuth` + `NewRateLimiter`; falls back to memory if unset |
| CSV export — unified across screens | TrendScreen added; DashboardScreen + CostAnalyticsScreen migrated to single convention defined in `csv-export` skill (`.claude/skills/csv-export/SKILL.md`). |
| Raw cost view | `GET /v1/costs` endpoint (`services/api/internal/api/handler.go:87`) + `CostAnalyticsScreen.jsx` shipped. |
| Rename ghost → zombie across the stack | DB tables, Go types, API routes (`/zombies`), and dashboard field reads are all aligned on "zombie". `grep -ri "ghost" services/` returns zero non-historical matches. |
| Remove CE anomaly-monitor "ghost" detection | `DiscoverIdleCEAnomalyMonitors`, its call site, test, constant, and IAM policy lines (`ce:GetAnomalyMonitors`, `ce:GetAnomalies`) all removed. AWS Cost Anomaly Detection is free — the `$3/mo` pricing claim was fabricated. |
| Tier 1 detections (API-only) | EBS unattached + orphaned snapshots, long-stopped EC2, old AMIs, unattached EIPs |
| Tier 2 detections (CloudWatch + API) | ElastiCache, OpenSearch, Redshift, SageMaker, DynamoDB, EKS, Secrets Manager, CloudFront, Kinesis, S3, RDS snapshots, ECR, log groups |
| User-friendly error pages | Reusable `components/ErrorPage.jsx` (logo, code label, human-language heading, primary/secondary actions, optional support ref; `embedded` prop drops the logo header when rendered inside AppShell). Route pages: 404 (`pages/NotFound.jsx`, embedded inside AppShell), 500 (`pages/ServerError.jsx`), 503 (`pages/ServiceUnavailable.jsx`). Top-level `AppErrorBoundary` in `App.jsx` catches uncaught render errors → renders 500 fallback. Static `services/dashboard/public/maintenance.html` lives outside the SPA bundle for true 503 when the SPA itself is unreachable (nginx `error_page` wiring is a follow-up infra change). 403 page deliberately omitted — cross-org access already resolves to 404 via RLS, role gates surface inline, SSO-required lockout has its own callout. |

### 🔲 Remaining

| # | Task | Notes |
|---|------|-------|
| 3 | **Production deployment** | 🟡 **Largely shipped — architecture pivoted App Runner → ECS Express.** Prod is live (account `123456789012`, eu-central-1, `app.axiaops.io`): ECS Express gateway services (api/ingestion), RDS Postgres, Valkey/Redis, S3+CloudFront dashboard, GitLab-OIDC deploy (no static keys), SSM platform inventory + Secrets Manager, migrations as a one-off Fargate task, tag-gated manual `deploy:production`. IaC lives in the sibling `aws-infra` repo, not this one. Per-item status in §2.16. Remaining: RDS password auto-rotation, snapshot/log-retention verification (aws-infra-owned). |
| 5 | **Weekly email digest** | New zombies after scan → Resend/SendGrid email. References `zombie_snapshots` for delta. |
| 6 | **Slack webhook alert** | Notify channel when new zombies appear post-scan. |
| 9 | **Rename tenant → organization across the stack** (✅ shipped on `refactor/25-rename-tenant-to-organization`, see `docs/refactor-tenant-to-organization.md`) | Same shape as the ghost→zombie rename. Dashboard UI prose was already migrated (commit `3ff0146`); this work brought the DB schema, Go code, API URLs, permissions, audit actions, Prometheus labels, and engineer docs into line. JWT format untouched (Kinde sends `org_code` — auth-boundary contract). Shipped as 12 commits keeping `make test` green at each boundary: (1) planning doc; (2) Prometheus label rename + `axiaops_organization_deletions_total`; (3) `model.Tenant` → `model.Organization`; (4) `WithTenantID` → `WithOrganizationID` + slog labels; (5) DB migration 016 (`tenants` → `organizations`, every `tenant_id` column, RLS predicates, `app.tenant_id` GUC, indexes, `memberships_tenant_id_user_id_key` constraint) + SQL strings in `postgres.go` (`make test-storage` gate); (6) permission constants `PermTenant*` → `PermOrganization*`; (7) audit action `tenant_deleted` → `organization_deleted`; (8) API URLs `/v1/tenants/*` → `/v1/organizations/*` + dashboard URL/permissions mirror + JSON tags; (9) handler internals + slog sweep + test names; (10) dashboard JS residue + audit `resource_type='organization'`; (11) docs sweep across CLAUDE.md and docs/; (12) `DEV_TENANT_ID` → `DEV_ORGANIZATION_ID` env var + `scripts/check-tenant-terminology.sh` CI grep guard. **Unblocks Phase 3 #14, #15, #16** — multi-organization UX, org-level dashboard, and zombie lineage stories. |

### Implementation detail — Phase 2

#### 2.4 Versioned Migrations (golang-migrate) ✅

- [x] Install `golang-migrate` CLI and add `github.com/golang-migrate/migrate/v4` to Go modules
- [x] Create `services/shared/storage/postgres/migrations/000_init.up.sql` — app user, schema, default grants
- [x] Create `services/shared/storage/postgres/migrations/001_initial.up.sql` — baseline schema + RLS policies
- [x] Wire migration runner into `services/api/cmd/main.go` and `services/ingestion/cmd/main.go` — run on startup using `MIGRATION_DATABASE_URL`
- [x] Remove inline `CREATE TABLE IF NOT EXISTS` and `ALTER TABLE` calls from application code
- [x] Test: run `make test-storage` and verify `schema_migrations` table is populated correctly
- [x] Test: run migrations from scratch on a clean PostgreSQL container

#### 2.5 Savings History / Trend ✅

- [x] Migration `002_zombie_snapshots.up.sql` — create `zombie_snapshots(id, tenant_id, account_id, snapshot_at, ghost_count, total_monthly_cost, currency)`
- [x] Update `Store` interface in `services/shared/storage/storage.go` — add `SaveSnapshot(ctx, snapshot)` and `ListSnapshots(ctx, accountID, limit)` methods
- [x] Implement `SaveSnapshot` + `ListSnapshots` in `services/shared/storage/postgres/postgres.go`
- [x] Update ingestion scan flow — write one snapshot row per scan after `SaveZombies`
- [x] Add `GET /v1/trend?account_id={id}&days=30` endpoint
- [x] Dashboard: savings trend sparkline on the header (replaces the static savings banner)
- [x] Test: scan twice, verify two snapshot rows exist and `GET /v1/trend` returns series

#### 2.6 Observability ✅

- [x] Create `services/shared/logging/logging.go` — `slog` setup (JSON in production, text in dev based on `LOG_OUTPUT` env var)
- [x] Replace all `log.Printf` / `fmt.Println` calls in all three services with `slog.Info/Error/Warn`
- [x] Create `services/api/internal/middleware/requestid.go` — inject `X-Request-ID` header and add to `slog` context
- [x] Add scan lifecycle logs: `scan.started`, `scan.completed`, `scan.failed` with `tenant_id`, `account_id`, `duration_ms`, `zombie_count`
- [x] ~~Add Sentry Go SDK~~ — SKIPPED: structured logging chosen for cost reasons
- [x] Create `services/api/internal/middleware/metrics.go` — Prometheus HTTP middleware
- [x] Add `axiaops_scan_duration_seconds` histogram and `axiaops_zombies_detected_total` counter in ingestion service
- [x] Expose `/metrics` on the API service (internal port — not behind auth middleware)
- [x] Extend `GET /health` to check DB connectivity (`SELECT 1`); `/livez` + `/readyz` follow Kubernetes conventions
- [x] Test: run `make start-dev`, hit `/metrics`, verify Prometheus output format

#### 2.7 Scan Recovery (Stuck Scan Timeout) ✅

- [x] Add background ticker in `services/api` — runs every 5 minutes
- [x] Query: find accounts with `status = 'scanning'` and `last_scanned_at < NOW() - INTERVAL '15 minutes'`
- [x] Update those accounts to `status = 'error'` with a `scan_timeout` reason
- [x] Log `scan.timeout_reset` with `account_id` and `stuck_duration`
- [x] Test: manually set an account to `scanning` with an old timestamp, verify ticker resets it

#### 2.8 API Versioning ✅

- [x] Update all routes in `services/api/internal/api/` to use `/v1/` prefix
- [x] Update nginx config — rewrite `/api/v1/*` → `api:8080/v1/*`
- [x] Update dashboard API client base path to `/v1/`
- [x] Update all handler tests to use `/v1/` paths
- [x] Test: `make test` passes; hit `/v1/zombies` in browser

#### 2.9 In-Memory Rate Limiting ✅

- [x] Create `services/api/internal/middleware/ratelimit.go` — `sync.Map` keyed by `tenant_id`, sliding window 60 req/min, returns 429 with `Retry-After`, no-op when `DEV_MODE=true`
- [x] Wire into the middleware chain in `services/api/cmd/main.go` (after auth, before handlers)
- [x] Test: write a test that fires >60 requests and asserts 429 is returned

#### 2.10 Graceful Shutdown ✅

- [x] `services/api/cmd/main.go` — listen for SIGTERM/SIGINT via `signal.NotifyContext`; `server.Shutdown(ctx)` with 30s drain; `pool.Close()` after; log `shutdown.started` and `shutdown.complete`
- [x] `services/ingestion/cmd/main.go` — same pattern; reject new `POST /scan` during drain (503); allow current scan to complete
- [x] Test: send SIGTERM during an active scan; verify scan completes and server exits cleanly

#### 2.11 GitLab CI Pipeline ✅

- [x] Create `.gitlab-ci.yml` at repo root with three stages: `test`, `build`, `deploy`
- [x] **test stage:** `go test ./...` for all three modules; `golangci-lint run`; on all branches
- [x] **build stage** (main only): build Docker images; tag with Git SHA; push to AWS ECR
- [x] **deploy stage** (main only): ECS Express service update (`update-express-gateway-service`) for `api` + `ingestion`; CloudFront invalidation for dashboard
- [x] Add required CI/CD variables in GitLab: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `ENCRYPTION_KEY`
- [x] Test: feature branch only runs `test`; merge to main runs full pipeline

#### 2.12 Scheduled Auto-Scan ✅

- [x] Migration `004_add_scan_interval.sql` — add `scan_interval_hours INTEGER NOT NULL DEFAULT 24` to `accounts`
- [x] Update `model.Account` to include `ScanIntervalHours`
- [x] `PATCH /v1/accounts/{id}` — update `label`, `region`, `scan_interval_hours`
- [x] Background ticker in ingestion — runs every 60 min; queries accounts where `last_scanned_at < NOW() - INTERVAL '{scan_interval_hours} hours'`; skips `scanning`; fires `POST :8081/scan`; logs `scan.scheduled`, `scan.skipped_already_running`
- [x] Dashboard: show next scheduled scan time per account in accounts bar
- [x] Integration test: create an account with `scan_interval_hours=0`, wait up to 90s, verify a scan is triggered

#### 2.13 cost_records Retention ✅

- [x] Add `COST_RECORDS_RETENTION_DAYS` env var (default `90`)
- [x] Daily background ticker in ingestion — `DELETE FROM cost_records WHERE period_end < NOW() - INTERVAL '{n} days' AND tenant_id = $1`
- [x] Migration `005_add_cost_records_index.sql` — index on `(tenant_id, period_end)` for efficient range deletes
- [x] Test: insert records with old `period_end`, run cleanup manually, verify rows deleted

#### 2.14 Redis ✅

> **Note (2026-05-27):** the cache engine migrated to Valkey across all envs
> via `chore/valkey-migration`. Wire-protocol surfaces (`REDIS_URL`, the
> `Cache` interface, `/readyz` `"redis"` key, go-redis SDK) intentionally
> stay redis-named — Valkey speaks RESP, the migration is a packaging
> change. Container image and CLI invocations flipped: `redis:7-alpine` →
> `valkey/valkey:8-alpine`, `redis-server`/`redis-cli` → `valkey-server`/`valkey-cli`.

- [x] Add cache container to `docker-compose.yml` (originally `redis:7-alpine`; `valkey/valkey:8-alpine` since the Valkey migration)
- [x] Add `REDIS_URL` env var to all service configs (default: `redis://localhost:6379`)
- [x] `services/shared/cache/cache.go` — `Cache` interface (`Get`, `Set`, `Del`)
- [x] `services/shared/cache/redis/redis.go` — Redis impl using `github.com/redis/go-redis/v9`
- [x] `services/shared/cache/memory/memory.go` — in-memory fallback (used when `REDIS_URL` is unset)
- [x] Update `services/api/internal/middleware/auth.go` — inject cache for JWKS lookup (1h TTL)
- [x] Create `services/ingestion/cmd/worker.go` — Redis queue consumer (`LPUSH axiaops:scan_queue` / `BRPOP`); falls back to synchronous execution if `REDIS_URL` is unset
- [x] Replace in-memory rate limiter with Redis `INCR` + `EXPIRE` counter
- [x] Test: run with and without `REDIS_URL` — verify fallback behaviour

#### 2.15 Notification Channels (Email + Slack scan digests) ✅

Shipped design diverged from the original sketch (Resend / per-account webhook column /
`/v1/settings/notifications`): channels are **org-level rows** in dedicated tables, email is
**SMTP/SES** (no third-party SDK), and CRUD lives under `/v1/channels`. See
[`docs/notifications-plan.md`](docs/notifications-plan.md) for the full design.

- [x] Migration `031_notification_channels` — `notification_channels` + `notification_dispatches`, RLS + runtime-bypass policies
- [x] `services/shared/notifications/` — `Transport` seam, `Dispatcher`, email (SMTP) + Slack (webhook) transports; secret-scrubbing on errors
- [x] Post-scan dispatch wired into `runIngestionCore` (after `SaveSnapshotServices`) — gated on `trigger_rule.min_monthly_savings_usd`, non-fatal
- [x] `/v1/channels` CRUD + `/test` + `/dispatches`; `channels:read` (viewer+) / `channels:manage` (admin+); encrypt-on-write, redact-on-read
- [x] Dashboard `/settings/integrations` pane — list, add/edit (email+slack), enable toggle, test, deliveries drawer
- [ ] **Post-deploy smoke** (staging, after merge): configure a real SMTP relay + Slack webhook on an org, trigger a scan, confirm delivery. Can't run in CI — needs a deployed env + real credentials.

v2 enhancements (weekly/scheduled digest, per-zombie alerts, retry/DLQ) are tracked under §3.17 — they're distinct features, not unfinished v1 work.

#### 2.16 Production Deployment 🟡 (architecture pivoted App Runner → ECS Express)

> **Pivot note (2026-05):** the original App Runner + ElastiCache + EventBridge plan
> below was superseded by **ECS Express Mode**. Production runs in AWS account
> `123456789012` (eu-central-1) at `app.axiaops.io`. **Infrastructure-as-code lives in
> the sibling [`axiaops/aws-infra`](https://gitlab.com/axiaops/aws-infra) repo, NOT a
> `terraform/` dir in this repo** — `terraform apply` there publishes the platform
> inventory (ARNs/IDs/URLs) under SSM `/axiaops/prod/platform/*`, which the tag-gated
> `deploy:production` CI job reads at runtime. CI auth is GitLab OIDC
> (`sts:AssumeRoleWithWebIdentity`, `AWS_CI_ROLE_ARN`) — no static access keys. See
> `aws-infra/docs/refactor-to-ecs-express-mode-plan.md`.

- [x] **IAM** — CI deploy role assumed via GitLab OIDC (replaces the planned `AxiaOpsCI` IAM user + static keys); ECS task/execution + Express infra roles TF-owned in `aws-infra` (replaces `AxiaOpsAppRunnerRole`).
- [x] **Terraform (in `aws-infra`)** — ECS Express services (api/ingestion), RDS PostgreSQL, Valkey/Redis, Secrets Manager, ECR (immutable), VPC + SGs (public subnets, no NAT GW), S3+DynamoDB state backend. `apply` done — services live, SSM inventory published.
  - [x] `terraform apply` on the prod account; services start (busybox bootstrap container until the first CI image rollout flips `primary_container`).
  - [ ] ~~Store TF in `terraform/` at repo root~~ — **obsolete:** IaC deliberately lives in the sibling `aws-infra` repo.
- [x] **RDS** — `db.t4g.micro` PostgreSQL 16 in `eu-central-1`; migrations run against it via the one-off Fargate migrate task.
  - [ ] Verify automated daily snapshots (7-day retention) — aws-infra-owned, not confirmed from this repo.
  - [ ] Verify CloudWatch log retention = 7 days — aws-infra-owned, not confirmed from this repo.
- [x] **Secrets Manager** — `DATABASE_URL`, `ENCRYPTION_KEY`, `AXIAOPS_LICENSE`, `INGESTION_SHARED_SECRET` (+`_NEXT`), `REDIS_URL` referenced by ARN in the ECS `secrets:` block (not plain env). `ENCRYPTION_KEY` is generate-once with a hard-abort guard against regeneration + api↔ingestion sync. `LICENSE_SIGNING_KEY` (issuer side) is a GitLab CI variable; `AXIAOPS_LICENSE` minted per-deploy by `mint:license:production`.
  - [ ] `RESEND_API_KEY` — blocked on 2.15 (email digest not built).
  - [ ] Document key-rotation procedure (`docs/ops.md`) — license rotation lives in `docs/license-issuance.md`; ENCRYPTION_KEY is generate-once (no rotation path documented).
- [x] **Migration execution separated from runtime** — migrations run as a one-off ECS Fargate task (TD revision pinned to the release image) BEFORE the service updates; dev/staging keep the self-bootstrapping on-startup pattern.
  - [x] Remove `MIGRATION_DATABASE_URL` from the runtime services. **Shipped in MR !257:** least-privilege `axiaops_runtime` role via migration 029 (DML + per-table permissive RLS-bypass policies, no DDL — RDS can't grant `BYPASSRLS`), `NewWithRuntimeAdmin` + readiness probe, `RUNTIME_ADMIN_DATABASE_URL` threaded through both services + compose + scripts + the storage test job, AND the non-dev self-hosted deploy wiring (`deploy/{preview,staging,demo,integration}.yml` + the CI deploy template). Verified on dev-2 (DEV_MODE → role NOLOGIN, app-pool fallback) and preview (DEV_MODE=false → role LOGIN, API healthy). Design in `docs/runtime-admin-db-role.md`. **Production:** aws-infra provisioned the runtime-role secret + per-service SSM params + migrate task-def env (`aws-infra` `d6f354f`, merged 2026-05-29) and **`apply:production` succeeded 2026-05-30** (pipeline #137 on `main` HEAD `d56cb87`). App-side CI wiring (`3f42d1a3`) shipped in tags `0.1.0-alpha.22`/`.23`; prod deployed + smoke-verified 2026-06-04, so the runtime now connects as `axiaops_runtime`. Final cleanup: dropped the now-unread `MIGRATION_DATABASE_URL` secret (+ its sanity-gate) from the prod ECS api/ingestion task-defs in `.gitlab-ci.yml` — the migrate Fargate task keeps its own TF-managed `MIGRATION_DATABASE_URL`.
  - [ ] RDS password auto-rotation via Secrets Manager + remove `ALTER USER` startup logic in prod — not done.
- [x] **ECS Express services** — api on `:8080` (ALB health `/livez`) + ingestion on `:8081` (ALB health `/health`), wired to RDS + Valkey; rolled out via `update-express-gateway-service` (ingestion first so api's `INGESTION_URL` resolves), steady-state polled via `describe-express-gateway-service`.
  - [x] Custom domain + TLS — `app.axiaops.io` fronted by CloudFront (ACM cert).
- [x] **Dashboard** — Vite prod bundle (VITE_* baked at build time) → `aws s3 sync` to the S3 bucket + `aws cloudfront create-invalidation` in the deploy job.
- [ ] ~~**EventBridge** cron → `POST /v1/accounts/{id}/scan`~~ — **superseded:** scheduled scans run from the in-app ingestion scheduler ticker (gated on license state), not an external EventBridge rule.
- [x] **Smoke test production** — connect a real AWS account, trigger a scan, verify zombies appear. **Verified 2026-06-04:** two real accounts (`123456789012`, `987654321098`) connected + scanned in prod (2026-06-03), each flagged a real unattached-EIP zombie ($3.60/mo) with correct reason text — confirmed by direct DB query (`aws-prod-sql`). Full pipeline connect→scan→Detect→SaveZombies→DB working end-to-end. ✅ **resource_type classification fixed:** the gap was broader than EIPs — *no* detection path populated `resource_type`, so the trend resource-type filter (`zombie_snapshot_services.resource_type`) was empty for every service. Added `analyzer.ResourceType(service, usage_metric)` (same `(service, usage_metric)` classification migration 006 used; EC2 slugs match the historical `zombie_snapshot_services` rows, RDS instance relabeled `primary`→`db_instance` for clarity — no live trend data carried the old slug), stamped onto every zombie in `Detect()` + a backfill loop in `runIngestionCore` (covers the API-only discoverers), threaded into `zombie_records`/`resource_records`, and the per-snapshot breakdown is now grouped by `(service, resource_type)` via `analyzer.SummarizeByServiceResourceType`. Schema/storage/API already supported it (no UNIQUE constraint; `ListSnapshotsByService` SUMs across sub-types). Golden projection extended + regenerated; unit tests for the classifier + breakdown.

#### Tier 1 — API-Only Detection Rules ✅

- [x] `DiscoverUnattachedEIPs` — `ec2:DescribeAddresses`, flags EIPs with no attached network interface ($3.60/mo)
- [x] `DiscoverUnattachedEBSVolumes` — `ec2:DescribeVolumes` filtered to `state=available`; cost = `sizeGB × $0.08/mo`
- [x] `DiscoverOrphanedEBSSnapshots` — `ec2:DescribeSnapshots` cross-referenced against existing volume IDs and AMI-backing snapshots; cost = `sizeGB × $0.05/mo`
- [x] `DiscoverLongStoppedInstances` — `ec2:DescribeInstances` + `StateTransitionReason` timestamp; flags instances stopped > 30 days; cost from attached EBS
- [x] `DiscoverOldAMIs` — `ec2:DescribeImages` cross-referenced against `ec2:DescribeInstances`; flags AMIs > 90 days old not referenced by any instance
- [x] All five wired into `runIngestionCore` in `services/ingestion/cmd/main.go` — each is non-fatal (permission gap skips that check)
- [x] Unit tests in `discover_test.go`
- [x] Seed data extended with all four new types across all three seed accounts

#### Tier 2 — CloudWatch Detection Rules ✅

- [x] Rules in `services/shared/analyzer/rules.go`: ElastiCache (`CurrConnections`), OpenSearch (`SearchRate`), Redshift (`DatabaseConnections`), SageMaker (`Invocations`), DynamoDB (`ConsumedReadCapacityUnits`), EKS (`cluster_node_count` via Container Insights)
- [x] CloudWatch namespace/dimension mapping in `services/ingestion/internal/provider/aws/cloudwatch.go`
- [x] Discovery functions with full pagination: `discoverElastiCache`, `discoverOpenSearch`, `discoverRedshift`, `discoverSageMaker`, `discoverDynamoDB`, `discoverEKS`
- [x] All six registered in `DiscoverResources` switch statement
- [x] Unit tests: 10 new test cases in `services/shared/analyzer/detector_test.go`
- [x] README and CLAUDE.md updated with new IAM permissions and detection rule tables

#### Integration & Smoke Tests ✅

- [x] `test/integration/api_test.go` — covers `GET /health`, `GET /metrics`, account creation, scan queue, rate limiting, scheduled auto-scan
- [x] `test/integration/go.mod` — standalone module
- [x] `make test-integration` target — `GOWORK=off SMOKE_API_URL=... SMOKE_REDIS_URL=... go test ./...`
- [x] Old `test/smoke/` directory removed; replaced by integration tests
- [x] `.vscode/launch.json` updated — smoke test launch configs removed

#### UX — Scan Completion Polling ✅

- [x] Shared `useScanStatus()` hook polls `GET /v1/accounts` every 4s after scan trigger (`services/dashboard/src/hooks/useScanStatus.js`)
- [x] Toast on flip from `scanning` → `connected` (success) or `error` / `scan_timeout` / `circuit_breaker_open` (failure)
- [x] Auto-refetch `accounts`, `summary`, `zombies`, `resources`, `costs`, `trend`, `dismissals` React Query keys on completion
- [x] 2-minute timeout with "taking longer than expected" warning toast
- [x] Polling intervals + timeouts cleaned up on component unmount
- [x] Applied to DashboardScreen, AccountSettingsScreen, CostAnalyticsScreen — replaces optimistic `setTimeout(refresh, 5000)` pattern

---

## Phase 2.7 — Self-hosted v1 (per [ADR-0001](docs/decisions/0001-deployment-model.md), target Q3 2026)

**Workstream introduced 2026-04-29** following ADR-0001 acceptance. This is the v1 GTM: package AxiaOps so 3–5 enterprise design partners can run it in their own AWS/GCP/Azure/on-prem infrastructure on annual contracts. Multi-tenant SaaS plumbing (Phase 3 #1, #17) is deferred until ≥3 self-hosted customers are paying.

### Status overview

| # | Task | Status | Notes |
|---|------|--------|-------|
| 2.7.1 | **Native auth replacing Kinde** | ✅ | Email/password baseline + native cookie sessions shipped via SSO Phase B1 (MR !85 → `develop`); `services/api/internal/kinde/` package + the `AUTH_PROVIDER` strangler tier deleted in this MR (`chore/remove-kinde-auth`). Migration `024_drop_kinde_residue.up.sql` dropped the `kinde_invitation_id` / `kinde_user_id` columns and renamed `users.kinde_sub` → `users.external_id`. The `auth.Provider` interface is preserved as a single-impl seam so a future SaaS reactivation can swap providers without touching the middleware chain. |
| 2.7.2 | **Native SSO (SAML/OIDC/Entra)** | 🟡 partial | Per `docs/sso-integration-design.md` Phases B–D (native OIDC + SAML + generic OIDC). Effort: 14–19w total. **B1 done** (native cookie-auth replacement, MR !69 → `feat/sso`). **B1.5 done** (multi-org access — `Store.ListUserMemberships`, `/v1/auth/{login,select-org,switch-org}` flow, `OrgPickerScreen`, `OrgSwitcher` dropdown, AcceptInvite existing-email handling for cross-org invitees, audit `session_org_switched`, observability counters per plan §4.7.4 — branch `feat/sso-b1.5-multi-org`). **B1.6 done** (license-file TTL + scan-gate — `services/shared/license/` package with offline RS256 verification, embedded pubkey, hourly state ticker; api + ingestion scan-gates; `cmd/license-issue` CLI; `LicenseBanner.jsx`; `docs/license-issuance.md` runbook — MR !71 → `feat/sso`). **B2 slice 3 done** (backend skeleton: auth types, permissions, audit constants, storage interface + postgres impl, seams for Discoverer/Connector, handler CRUD, domain verification, JIT provisioning, sweep ticker, test mocks — per plan §5.2; OIDC ceremony + frontend deferred to slices 4–6). **B2 slice 4 done** (merged via !76; branch `feat/sso-b2-oidc-ceremony` deleted): `oidc.go` ID-token validator landed — discovery-doc cache, alg-confusion guard (whitelist of asymmetric algs ∩ IdP-published, hard-reject `none`/HS256 even if IdP advertises them), per-connection JWKS via `services/shared/jwks/`, JWKS auto-refresh on signature failure (architect S5: evict cache + retry once on key rotation), issuer/audience/nonce/iat checks, all errors collapse to `ErrIDTokenInvalid` externally; 7 unit tests cover happy path + alg-confusion (none, HS256) + wrong audience/issuer/nonce + expired + auto-refresh. **Slice 4 commit 2** added PKCE + state primitives (`state.go`: `GenerateState`, `StateStore` Persist/Consume with single-use `sso:state:{state}` 10min TTL, `CodeChallenge` S256 per RFC 7636) and a real `initiate.go` (lookup connection unscoped via new `Store.GetSSOConnectionByID`, validate active+OIDC, fetch discovery doc through `Validator.Discovery`, build authorize URL with response_type=code/PKCE/nonce/state, 302; open-redirect-defended `?return_to=` validation per architect N4; PUBLIC_HOST scheme warnings at startup). **Slice 4 commit 3 done** — full OIDC callback orchestration: state consume + CID-binding CSRF check, client_secret decrypt via `crypto.Decrypt`, token-endpoint POST with `io.LimitReader` cap, `ValidateIDToken` with state-bound nonce, claim extraction (Entra `oid` else `sub`; email fallback chain `email → preferred_username → upn`), anti-spoofing domain check (verified row bound to THIS connection AND THIS org), `UpsertUser`, `RedeemPendingInvitation` precedence (HARD-FAIL on redeem error so admin-chosen role is never silently bypassed), JIT path with new `JITOutcome` enum routing audit to `sso_jit_provisioned` (created) vs `sso_jit_role_updated` (re-login role change) vs no-op (unchanged), `MintSession` with `auth_mode='sso'`, cookie via `auth.SetSession`, `sso_login_succeeded` audit, 302 to `state.RedirectAfterLogin` or `/dashboard`. All post-connection failure paths emit `sso_login_failed` with coarse reason buckets. Routes wired in `cmd/main.go` (initiate unconditionally, callback inside native-auth block since it needs `auth.Manager`); `publicPath` extended to bypass `/v1/sso/oidc/`; new `middleware.WithOrganizationID/WithUserID/WithUserEmail` setters; `AuditActionSSOJITRoleUpdated` constant added. 14 new callback tests (happy paths for default-role JIT, group→role precedence, role-update on re-login, invite precedence; failure paths for missing code/state, unknown state, CID mismatch, token-endpoint reject, nonce mismatch, domain unverified, cross-connection domain, single-use replay, invitation-redeem-error). **B2 slice 4 merged into `feat/sso` via !76 (6 commits): three feature commits + three review-fix follow-ups. Final review fixes addressed: `publicPath` suffix-match for `/initiate`/`/callback`, `/token` fixture form-shape validation, JITOutcomeNoop audit-silence test, discovery-doc `io.LimitReader` cap, `crypto.Encrypt` at the connection POST/PATCH boundary, `net/mail.ParseAddress` for email-fallback, paired initiate+callback route registration**. **Post-merge structural fixes** (branch `fix/sso-switch-org-authmode`): (1) auth_mode preservation across org switches (fix cookie binding + session record update), (2) JIT provenance guard (skip role reconciliation on admin-placed memberships via `provisioned_via` check, closing cross-flow race between `/auth/invitations/redeem` and SSO callback). **B2 slice 5 done** (frontend SSO settings UI — Settings tab with tabbed Connections/Domains/GroupMappings/Enforcement panes, owner-only gated on `PERM.SSO_MANAGE`, calls existing backend endpoints, code-reviewer findings on sanitization/error parsing/dirty-detection/per-row state already addressed — MR [pending] → `feat/sso`). **B2 slice 6 partial** — login email-blur SSO discovery shipped on `feat/sso-b2-frontend` (NativeLoginScreen state machine email→discovering→sso→password; race-safe via monotonic generation counter; password-fallback escape hatch when has_sso=true; transport-failure fallback to password phase). **B2 slice 6 mock-OIDC integration done** (MR !79 → `feat/sso`): lightweight `test-infra/integration/docker-compose.test.yml` (Postgres-only) + `services/api/internal/sso/oidc_integration_test.go` with build tag `integration` — drives a full authorization-code + PKCE round-trip against an in-process minimal RS256 OIDC issuer, asserts membership.role from JIT group→role mapping, verifies anti-spoofing (unverified-domain reject), and exercises architect-S5 JWKS auto-refresh by rotating the IdP signing key mid-test. New `make test-integration-sso` target. Also fixed a latent INSERT bug in `services/shared/storage/postgres/sso.go` where nil ciphertext byteas wrote SQL NULL instead of the column default — never surfaced because the SSO postgres path had no integration coverage until now. **Owner-never-via-JIT pin done** (`services/api/internal/sso/permission_matrix_test.go`, plan §5.5): three-layer security matrix — resolver discards `role=owner` mappings (admin still wins on mixed input), `JITProvisionMembership(role="owner")` returns `ErrJITOwnerForbidden` with no Save/Update store calls, and reconcile branch noops on an existing `Role="owner"` row regardless of `provisioned_via` (no JIT demote). File header is the canonical place future role-assignment paths register their assertion. **Discover constant-shape + domain-confusion fuzz done** (MR !81 → `feat/sso`, `services/api/internal/sso/discover_test.go`, plan §5.5 / architect N4): `TestDiscoverHandler_ConstantShape` covers six input branches (verified, unknown, empty, three malformed, DB-error degrades) — all return 200 + `application/json` with body keys exactly `{has_sso}` or `{has_sso, redirect_url}`, no leak of internal `connection_id`/`organization_id`/`protocol`. `TestDiscoverHandler_LatencyFloor` pins the 5ms `minDiscoverLatency` pad. `TestNativeDiscoverer_DomainConfusion` runs 17 dnstwist-style variants against verified `acme.com` (TLD swaps, suffix/subdomain attacks, Cyrillic/Greek homoglyph, punycode, three typosquats, longer-TLD shared-prefix `acme.community`) — all reject; five canonical-form variants match. **Open-redirect fuzz done** (MR !82 → `feat/sso`, `services/api/internal/sso/redirect_fuzz_test.go`, plan §5.5 architect N4): three layers. Layer 1 — boundary at initiate via the now-exported `sso.ValidatedReturnTo`: 22 hostile shapes (canonical absolute URLs, protocol-relative, `javascript:`/`data:`/`vbscript:`/`file:`/`about:` schemes, mixed-case schemes, authority-confusion `https://evil.com@app.example.com`, control chars, length-cap `>1024`) all drop to `""`; 6 legitimate paths preserved; idempotency invariant verified. Layer 2 — defense-in-depth at callback: 15 hostile shapes manually persisted into `StateData.RedirectAfterLogin` BYPASSING the initiate boundary, callback ceremony driven, final `Location` asserted as `/dashboard`. Closes the architect-N4 "regardless of state content" acceptance — without this, a corrupted state record (storage bug, hostile cache write) could still open-redirect. Production change: `validatedReturnTo` exported as `ValidatedReturnTo` and called at both initiate.go entry boundary and oidc_callback.go redirect site. **enforcement=required 403 done** (`services/api/internal/middleware/sso_enforcement.go` + `_test.go`, plan §5.5): new `EnforceSSO(resolver, skipPaths...)` middleware wired in `cmd/main.go` AFTER `WrapNative` so the chain is "auth → enforcement → handlers"; `/v1/auth/logout` is in the skip-set (blocked password-session users can still cleanly end their session). Production resolver `NewStoreEnforcementResolver(store)` reads RLS-scoped `ListSSOConnections` and picks the highest enforcement across the org's `active`+`oidc` rows (draft/disabled/non-OIDC connections do NOT contribute — pinned by an `enforcementRank` total order). Test matrix: required+password→403 `{"error":"sso_required"}`, required+sso→passthrough, required+bootstrap→passthrough (first-owner install can't be bricked), preferred/optional/empty+password→passthrough, fail-open on resolver-error / missing-org-context / nil-resolver, skip-path bypass with exact-match-only assertion (a future "make it prefix" refactor breaks the test before it ships). **Pending-invitation precedence done** (MR !84 → `feat/sso`, `services/api/internal/sso/oidc_integration_test.go`, build tag `integration`, plan §5.5): `TestOIDC_PendingInvitationPrecedence` seeds a `pending_memberships` row at role=viewer for an email whose IdP groups would JIT-resolve to admin, drives the full OIDC ceremony, and asserts (a) resulting membership is role=viewer (invite wins over JIT-admin), (b) provisioned_via='invitation' so the JIT provenance guard at re-login still recognises this row as admin-placed, (c) pending_memberships row is consumed (DELETE), (d) audit posture has SSO_LOGIN_SUCCEEDED but NO SSO_JIT_PROVISIONED — proves the invite branch fired and the JIT branch was bypassed entirely. Direction chosen: invite=viewer beats JIT=admin (the precedence-stronger direction). **ssoSweep ticker observability done** (`services/api/internal/sso/sweep_test.go`, plan §5.5; direct commit to `feat/sso`): seven properties pinned via a `fakeSweepStore` (storage.Store with embedded-nil-interface trick; only `SweepStaleSSODomains` implemented, panics elsewhere). Lifecycle: kick-off tick fires immediately on Run() (not after the 24h interval), interval ticks fire on cadence, ticker continues past store errors (transient DB issue must not take it out), Run() returns promptly on context cancel. Observability: when count>0 emits `sso: sweep stale domains marked_stale=N` (the line ops grep for); when count=0 the info log is suppressed (otherwise every 24h tick would emit `marked_stale=0` noise); on store error emits `sso: sweep stale domains failed` carrying the wrapped error message. Tests use a process-global `slog.SetDefault` swap with cleanup (no t.Parallel for that reason). **JWKS package consumers parity verified** (architect S3, plan §5.5; doc-only flip): both consumers already routed through `services/shared/jwks` — `services/api/internal/middleware/auth.go:46` calls `jwks.FromCache(ctx, issuer, jwksURL, c)` for Kinde (issuer-bound shape), `services/api/internal/sso/oidc.go:167` calls `jwks.FromCache(ctx, conn.ID, doc.JWKSURI, c)` for OIDC (per-connection shape). Test parity in `services/shared/jwks/jwks_test.go`: `TestFromCache_CacheHit_SkipsHTTPFetch` covers issuer-bound; `TestFromCache_PerConnectionShape` covers per-connection (two distinct connections sharing one HTTP origin → two cache entries, proving the cache key derives from cacheID not jwksURL); `TestFromCache_ForceRefreshViaCacheKey` covers the architect-S5 auto-refresh pattern. Plus four shared-property tests (cache-error fallback, nil cache, non-OK status, malformed payload). All seven currently green. **Seam D11 + drop-in test extended done** (`services/api/internal/serverbuild/{build.go,tickers.go,build_test.go}`, plan §5.5 / §4.8.3 / §4.8.6; direct commit to `feat/sso`): extracted `~250 lines` of wiring (api handler construction, route registration, SSO routes, native-auth + OIDC ceremony routes, middleware composition, request-logging, Prometheus instruments) from `cmd/main.go` into the new `serverbuild` package. `cmd/main.go` is now bootstrap-only (env reads, license verify, store/cache/queue init, signal handling, graceful shutdown) — zero handler registrations remain in main, satisfying §4.8.6 line 570 in spirit (572 → 380 lines, with the wiring moved not summed). Drop-in smoke test boots `ComposeServer` with mock impls of all five SaaS-extension seams (storage.Store via embedded-nil-interface trick, auth.Provider returning fixed Identity, kinde.Client via `kinde.NewStub()`, sso.Discoverer returning has_sso=false, sso.Connector returning sentinel errors per method) and asserts (a) the chain composes without compile error, (b) `GET /v1/sso/discover` responds 200 with `has_sso:false` JSON proving Discoverer mock was consulted through the full request-id + dev-bypass + rate-limit + CORS chain, (c) the same handler in non-DevMode (NativeAuthActive=true) requires SessionManager + SSOValidator + SSOStateStore deps and serves the same smoke endpoint, (d) ComposeServer fail-fast errors when Store / Discoverer / Connector / AuthProvider are missing — composition-root bugs surface at boot, not on the first request. The `Deps` struct is the SaaS reactivation seam: a future `cmd/api-saashosted/main.go` swaps a few constructors and calls the same `ComposeServer`. Verified locally: full api unit suite + full make test-integration-sso (all 5 OIDC integration tests) green after the refactor — no existing test required updates. **B1.7 layer 1 done** (`.dev-mode-gate` template in `.gitlab-ci.yml:304`; `gate:devmode:staging` + `gate:devmode:production` concrete jobs wired into `deploy:staging` / `deploy:production` `needs:` — direct commit to `feat/sso`): refuses `DEV_MODE=true` on any deploy target other than `dev-1` / `dev-2`. Mirrors the existing strangler-gate pattern (one job per env for failure attribution clarity). Validated via `glab ci lint`. **B1.6 amendment + B1.7 layers 2–3 + license-pane + signing-key ceremony done** (this session, all on `feat/sso` ahead of !85): (1) **B1.6 amendment** retired hostile boot-refusal in favour of feature-gating at the scan path — `b976169` flipped `VerifyAtBoot` from die-on-error to log-loud-and-continue across missing/expired states, introduced `enforcementBypass` + distinct `license_expired`/`license_not_loaded` 403 codes, dashboard banner picks install-vs-renewal CTA from state; full rationale in new `docs/b1.6-amendment-feature-gating.md`. (2) **CI license-mint at deploy time** — `b94b773` adds `.gitlab-ci.yml` `.mint-license` template + `mint:license:preview` / `mint:license:staging` concrete jobs producing 30-day non-prod JWTs as 2h-expiry dotenv artifacts (post-review tightened iteratively from 1h to 30min to 2h to cover manual-gate workflows); `deploy/preview.yml` + `deploy/staging.yml` plumb `AXIAOPS_LICENSE`. (3) **B1.7 layer 2 done** — `caccb6c`: `VerifyAtBoot` refuses `DEV_MODE=true` when a license is configured (env or file), `licensePresent()` helper, `LicenseLoadErrorsTotal{reason="dev_mode_with_license"}` increment, error references both plan §4.10.2 + amendment doc. (4) **B1.7 layer 3 done** — `7ac3a06` + architect-review follow-ups `8cbf35a` + code-review follow-ups `e90f9be` + review-of-review `5b4cd6b`: build-tag-gated `devModeEnabled()` in `services/{api,ingestion}/cmd/devmode_{dev,production}.go` strips DEV_MODE from `-tags production` builds; cross-package `os.Getenv("DEV_MODE")` removed from `services/shared/logging/logging.go` (now reads `LOG_OUTPUT` only); `scripts/start.sh` exports `LOG_OUTPUT=text` in dev; `test:lint:no-direct-devmode` CI grep enforces single seam; `build:production-shape` CI job runs `make build-production` + `go test -tags production ./cmd/` per binary; paired regression-pin tests + `license.AllStates` exhaustive-coverage seam pin both build modes. (5) **Plan acceptance reconciliation** — `96a95f0`: 43 of 58 stale `[ ]` boxes in `docs/sso-implementation-plan.md` flipped to `[x]` with as-shipped annotations (§4.6 B1, §4.7.4 B1.5, §4.8.6 seams, §5.5 line 1000); 15 boxes deliberately retained as `[ ]` (Internal Entra test + 14 Phase C SAML criteria not yet shipped). (6) **Settings → License inspector pane** — `3554f12`: new `services/dashboard/src/pages/settings/License.jsx` owner-gated (`PERM.ORGANIZATION_DELETE`) read-only inspector showing all five state shapes (valid / valid-with-<14d / in_grace / expired / not_loaded × DEV_MODE-bake); closes the affirmative-state UX gap LicenseBanner deliberately leaves silent; reuses cached `['api-version']` query — no second `/v1/version` request. (7) **Runbook made store-agnostic** — `ae7ccf4`: `docs/license-issuance.md` Pre-flight Requirements table replaces the 1Password-only assumption; new "Pick a store" subsection covers macOS Keychain (Option A), Bitwarden (Option B), 1Password (Option C); incident-response section uses vendor-neutral language. (8) **macOS Keychain hex-encoding gotcha documented** — `7b1e4df`: `security find-generic-password -w` hex-encodes any value with embedded newlines; runbook mandates `xxd -r -p` decode on read-back; round-trip `openssl rsa -check -noout` sanity-check shape. (9) **Production signing key ceremony complete** — `09021a6`: 4096-bit RSA keypair generated, private key in macOS Keychain (`axiaops-license-signing` / `axiaops-ops/license-signing-key`) + GitLab CI variable `LICENSE_SIGNING_KEY` (File-type, **not** Protected so feat-branch MR pipelines can read), placeholder `services/shared/license/pubkey.pem` swapped with the production public key (DER SHA-256 `0f84a12521d51754c19eff916c506e8653010a01d448be83140c642dfe7e79ca`). (10) **Issue #75 filed** (`B1.7 layer 4 — decouple license verification from DEV_MODE`) tracking the post-layer-3 cleanup that lands BEFORE first paying self-hosted customer ship. **Verified end-to-end**: pipeline `2499274160` on MR !85 head fully green — `mint:license:preview` runs against the new signing key, `build:production-shape` confirms layer-3 stripping compiles + tests both modes. **Deployment config documentation done** — `docs(deploy)` commit this session: `.gitlab-ci.yml` comment block documents PUBLIC_HOST (externally-reachable origin, redirect_uri key) + INTERNAL_DNS (split-horizon LAN resolver for self-hosted IdPs, cures Cloudflare WAF rejection of Go HTTP client UA on discovery-doc fetch). CLAUDE.md deployment-topology section expanded with matching bullet points and GitLab Environment scoping pattern. Closes the ops documentation gap surfaced during SSO debugging (commits f0fb9e2, d7cdfb5, 0e7adc2 plumbed the knobs; this doc explains them). **Internal Entra connection-test done** (2026-05-07, this session): full OIDC round-trip validated against a free `*.onmicrosoft.com` test tenant — discover → initiate → Entra authorize+MFA → callback → JIT-provisioned user with `external_id` = Entra `oid` (not `sub`), session minted with `auth_mode='sso'`, audit posture clean (`sso_jit_provisioned` then `sso_login_succeeded`). Group→role JIT mapping also exercised end-to-end (Engineering → admin) — caught the "Emit groups as role claims" Entra gotcha that routes group IDs to the `roles` claim instead of `groups`, baked into the runbook so the next person doesn't lose 30 min to it. Walkthrough + AADSTS decoder ring captured in new `docs/sso-local-entra.md`. `docs/sso-implementation-plan.md` line 1008 flipped to `[x]`; matches `docs/sso-integration-design.md` line 1042 ("Internal AxiaOps team logs into self-hosted instance via Entra OIDC with JIT provisioning"). **B1.7 layer 4 done** (this session, branch `feat/license-decouple-devmode` — closes issue #75): `services/shared/license/{embed_dev.go,embed_production.go,pubkey-dev.pem,fixture-dev.jwt}` add a build-tag-gated dev-only RS256 keypair + 100-year fixture JWT (`customer_id=axiaops-dev-fixture`). `Load()` tries the production pubkey first, falls back to the dev pubkey on `jwt.ErrTokenSignatureInvalid` only when the dev key is compiled in (`-tags production` zeros both seams → fallback unreachable, leaked dev fixture useless against customer binaries). `VerifyAtBoot(devMode=true)` now loads the embedded fixture through the same chain a real customer license travels (Load → CheckExpiry → SetCurrent → state=valid) instead of flipping `enforcementBypass` — closes the dev/prod-parity gap layers 1–3 left open. `enforcementBypass` preserved as a seam for `cmd/api-saashosted` (plan §4.9.6, not yet built) but no runtime path sets it. Tests: `TestVerifyAtBoot_DevModeLoadsFixture` + `TestEmbedDev_FixtureRoundTrips` (default build), `TestEmbedProduction_DevFixtureNotCompiledIn` + `TestEmbedProduction_DevModeWithoutFixtureSoftFails` (`-tags production`). Both `make test` and `go test -tags production ./...` green; `make build-production` clean. Plan amendment §4.10.8–4.10.10 (`docs/sso-implementation-plan.md`) documents the new dev-fixture story including the re-mint procedure (dev private key destroyed at generation time).
**Still pending**: Phase C (SAML SP — plan §6). The two pre-merge gates from the prior wording cleared on 2026-05-16: MR !85 merged into `develop` (`5e1ece2`), and `deploy:preview` ran green on pipeline `2499274160` (the MR !85 verification pipeline). |
| 2.7.3 | **Self-hosted bundle (docker-compose)** | ⏸️ deferred | **Deferred 2026-06-03** on the same cost/benefit grounds that deferred the Helm chart: building the compose bundle + install runbook speculatively, before a named self-hosted prospect with a buying motion, produces shelfware that drifts. The enabling plumbing (B1.6 license gating, B1.7 build-tag DEV_MODE stripping, embedded license fixtures, signing-key ceremony) is already shipped, so the bundle is small (~1w) and can be picked up the moment a prospect or self-hosted launch date appears — re-open as active then. Prod hardening (§2.16 remainders) and live SaaS take priority while self-hosted is speculative. `deploy/compose/docker-compose.selfhosted.yml` for single-VM / docker-host customers — the dominant self-hosted shape (regulated mid-market, EU public sector, on-prem ops; FinOps tooling buyers don't typically run third-party tools inside their K8s estate). Publishes via existing GitLab CI as OCI images. Postgres + Redis included as services with override paths for customer-supplied DBs (so a customer can point at a managed Postgres / Redis if they already operate one). On-host install runbook: `docs/install-selfhosted.md`. Effort: S (~1w). **Helm chart deferred** — the original scope included `deploy/helm/axiaops/`, removed 2026-05-16 (this row's edit) on cost/benefit grounds: building a chart speculatively for a 5-container workload before a prospect has asked for it means ongoing values.yaml / upgrade-hook maintenance against guessed requirements. Re-open as a new row when **a real customer asks in writing with a buying motion attached** — by then the values.yaml shape is informed by that conversation rather than guessed. |
| 2.7.4 | **Self-hosted release versioning policy** | ✅ | Shipped as a six-MR release-infra series merged into `develop` (commits `b56321e` semver scheme → `b949253` develop→main promotion → `fa59cf5` tag pipelines enabled + production gated to semver → `a3d347a` demo env wired tag-only → `f1ceb7e` CHANGELOG bootstrap → `f709a09` CHANGELOG fixes + Phase 2 gap fills → `2267872` link-rotation runbook), closed out by MR !153 which added release cadence + support window + migration backwards-compat policy. Lives in [`docs/versioning.md`](docs/versioning.md) (a scope superset of the originally-scoped `docs/release-policy.md`) + [`CHANGELOG.md`](CHANGELOG.md) (Keep a Changelog 1.1.0). Tag scheme is bare `X.Y.Z` (no `v` prefix — CI substitutes `APP_VERSION=$CI_COMMIT_TAG` verbatim). Pre-1.0 latitude on cadence; post-1.0: quarterly-minor (max) + as-needed patches every 4–6w, last two minor lines supported with previous-minor on security patches only, `golang-migrate` rules pinned (never delete a released migration, never reuse numbers, renames/drops are a two-release dance). |
| 2.7.5 | **License / entitlement model** | ✅ | Shipped as Phase B1.6 (MR !71). ADR-0001 D12 advanced this from "deferred until ≥3 paying customers" to v1 because the high-trust 3–5-design-partner assumption breaks for any churned customer running yesterday's binary indefinitely. Implementation: offline-verified RS256 JWT carrying `customer_id, expires_at, max_organizations, features, grace_period_days`; embedded pubkey; boot-time refusal past `exp + grace`; hourly runtime ticker (no `os.Exit`); single mid-flight feature gate at `POST /v1/accounts/{id}/scan` + scheduled scan ticker. Self-hosted only — SaaS binary doesn't check licenses. Plan §4.9, runbook `docs/license-issuance.md`. |
| 2.7.5a | **SaaS entitlement — Phase 1 + 2A scaffold + 2B license-removal** | 🟡 partial | Per `docs/saas-platform-admin-design.md` §7 + ADR-0002 (**Proposed**, sequencing per §7.1). **Phase 1 (platform license)**: runbook in `docs/license-issuance.md` to mint a long-lived (`-days=36500`) `customer_id=axiaops-saas-platform` JWT with the production signing key + inject as `AXIAOPS_LICENSE` — keeps the existing license scan-gate happy with zero gate code. **Phase 2A scaffold** (MR !328 → `288956d`): migration `033_entitlements` (system-scoped, no RLS, `axiaops_runtime`-only); `model.Entitlement` + `EntitlementStatus`; `services/shared/entitlement/` package (pure `IsScanAllowed` predicate, `Resolver`, **fail-closed** `IsScanAllowedForOrg`, `ApplyBillingEvent` billing seam stub); `storage.EntitlementStore` + postgres impl on the admin pool; `cmd/entitlement-seed` CLI. **Phase 2B — license removal in SaaS mode** (branch `feat/saas-license-removal-2b`, founder-directed ahead of the §7.1 activation gate): the 4 scan gates (api `scanAccount` + ingestion `scanHandler`/worker/`scanScheduledAccounts`) route through a `gateAllowsScan` helper — nil resolver → license gate (self-hosted, byte-identical); non-nil → per-tenant entitlement gate. Scheduler's pass-wide license check → per-org entitlement check batched via `ListAllEntitlements`. Selection is a **`saashosted` build tag** (`services/{api,ingestion}/cmd/saasmode_*.go`) — NOT separate `cmd/*-saashosted` roots (ingestion wiring is `package main`, un-importable by a sibling main). `-tags "production saashosted"` builds call `license.SetEnforcementBypass()` at boot + supply the store as resolver; `/v1/version` collapses to `state:"managed"`; dashboard hides the License tab/banner (Settings.jsx + LicenseBanner). `ENTITLEMENT_GRACE_DAYS` (default 21). `make build-saashosted` + `build:saashosted-shape` CI (build-only — **no SaaS deploy target yet**; billing/Stripe still unbuilt, rows seeded via `cmd/entitlement-seed`). Tests: entitlement-gate handler test (7 statuses), managed-version test, `TestScanScheduledAccounts_SaaS_PerOrgEntitlement` golden. **Follow-up #131** (plan/usage page replacing the hidden License page). Decisions: entitlement-table system-scoped, missing-row = deny (fail-closed), platform license prod-key-minted in CI. |
| 2.7.6 | **Opt-in `phone-home` telemetry channel** | 🔲 | Anonymized detection-rule efficacy reporting back to AxiaOps. Opt-in, default off in regulated verticals; explicit consent in MSA. Payload: rule fire counts by service, false-positive flag rates, version. No customer identifiers, no resource identifiers, no costs. Endpoint: `https://telemetry.axiaops.io/v1/efficacy`. Effort: S (1w). |
| 2.7.7 | **Email-invitation native flow** | 🔲 | See Phase 3 #14 (rescoped to native). Pick SMTP provider (Resend/Postmark/SendGrid). Effort: ~10 days. |
| 2.7.8 | **MSA + DPA templates + security-questionnaire pre-fill** | 🔲 | Out-of-code business deliverable but on the critical path. Standard MSA template with our default redlines flagged; DPA template (narrowed scope per Phase 3 #9p); pre-filled SIG Lite + CAIQ knowledge base. Hire a fractional GC or use a template service (e.g. Common Paper). Effort: ~1w of founder time + €X for legal review. |
| 2.7.9 | **Pricing sheet for first 3 design-partner contracts** | 🔲 | ~€5–10k/yr per organization is the hypothesis (ADR-0001 §Decision). Validate in first 3 sales conversations; iterate. No public price list yet. |
| 2.7.10 | **Sub-processor list (narrowed)** | 🔲 | Public page listing the sub-processors AxiaOps uses for its *own product operations* (not customer data, since customer self-hosts): GitLab (CI/source), AWS/whoever-hosts-our-marketing-site, Resend (if chosen). Required by GDPR Art. 28 even at narrowed scope. Effort: S. |
| 2.7.11 | **Password breach-corpus check (offline embedded HIBP)** | 🔲 | The B1 native-auth password policy is currently length-only (≥12 chars) — the one outright NIST SP 800-63B §5.1.1.2 violation in our password story. **Approach amended (2026-06-09 — see `docs/password-breach-check-design.md`): embed an offline top-N HIBP SHA-1 subset, do NOT call the live k-anonymity API.** Rationale: AxiaOps is self-hosted; a customer instance may be egress-restricted/air-gapped, so a live `api.pwnedpasswords.com` call is unavailable by design and "soft warning / fail-open" silently no-ops. Follow GitLab/Django prior art (both self-hosted, both bundle offline). Design: top-1M prevalence-ordered SHA-1 digests as a sorted 20-byte-record binary blob (`services/api/internal/breachlist/`, `//go:embed`, ~20 MB — note it lands in both the `api` and `api-admin` images; 500k/10 MB fallback knob), binary-search membership, **hard-block on hit** (deterministic — no availability branch), wired into the existing `auth.CheckPolicy` seam (all 6 set-password call sites already funnel through it; add `CheckPolicyWithIdentity` for the 4 identity-bearing sites rather than breaking the signature) + a GitLab-style name/email similarity add-on. **Licence:** HIBP data is CC BY 4.0 / attribution-welcomed — ship a root `NOTICE` crediting HIBP (establishes the repo's first third-party-notice convention); the downloader tool is not embedded so its licence doesn't propagate. Effort: ~½w. |
| 2.7.12 | **[POST-B1] Re-evaluate DEV_MODE auth bypass** | ✅ | **Decision: keep DEV_MODE, no code change.** The original concern ("DEV_MODE accidentally set in prod" + a live auth-bypass branch in customer binaries) is closed by what already shipped in B1.7: layer 1 (CI `gate:devmode:*` refuses `DEV_MODE=true` on staging/production deploys), layer 2 (`license.VerifyAtBoot` hard-refuses `DEV_MODE=true` when a license is configured — `services/shared/license/startup.go:85`), layer 3 (the `production` build tag strips `devModeEnabled()` to `false` unconditionally — `services/{api,ingestion}/cmd/devmode_production.go`, so customer-shipping binaries can't honour the env at all), and layer 4 (license parity — DEV_MODE now loads the embedded dev fixture through the same Load → CheckExpiry → state chain a real customer license travels, not via a bypass flag — `startup.go:130`). What remains is ~30 LoC of `DevBypass` middleware (`services/api/internal/middleware/auth.go:126`) + the dashboard `App.jsx:120` short-circuit, paying for real local-dev ergonomics (skip bootstrap → login → session every fresh-DB cycle). Options (a) drop / (b) `make seed-bootstrap` / (c) auto-bootstrap-on-fresh-DB were all weighed: not worth the trade — option (b) replaces shipped/tested code with new Makefile + curl plumbing, slightly slower iteration on fresh DBs, dashboard still needs a stub path. Revisit only if real operator feedback (post-first-paying-customer) shows the bootstrap step is actually painful — guessing at it now is premature. |
| 2.7.13 | **[POST-B1] Redis-outage end-to-end smoke** | ✅ unit / 🔲 integration | Plan §4.6 AC19. The **unit-level** outage simulation lands in B1 (slice 11): `services/api/internal/auth/cache_outage_test.go::TestSessionValidation_FailsOpenOnCacheOutage` drives the entire MintSession → ValidateSession → RevokeSession lifecycle through an `erroringCache` stub, asserts requests still succeed via PG fallthrough. **Deferred to post-B1**: a real-Redis integration variant via `make test-integration` that pauses the Redis container mid-test and asserts `axiaops_session_cache_errors_total` actually increments in Prometheus. Useful for catching wiring drift but not a B1 blocker — the unit shape covers the code path. Effort once scheduled: S (½d). |
| 2.7.14 | **[POST-B1] OpenAPI / Swagger spec for /v1/auth/\*** | 🔲 | Plan §4.6 AC26. The B1 native-auth endpoints (`POST /v1/auth/{bootstrap,login,logout,invitations/redeem,password-reset/redeem}` plus admin `POST /v1/users/{id}/password-reset`) need OpenAPI 3.x descriptions so the dashboard team and external integrators have a machine-readable spec. Today the routes are documented in `services/api/CLAUDE.md` only. Decide format: hand-written `openapi.yaml` (low ceremony, drifts from code) vs. `kin-openapi` reflection (couples docs to handler types but stays in sync). Recommend hand-written for v1; revisit if drift becomes painful. Effort: S–M (1–2d). |
| 2.7.15 | **[partial — extend] Operator smoke-test runbook** | 🟡 | Plan §4.6 AC1, AC4. **Done in B1**: `docs/native-auth-bootstrap.md` covers the local `make start-staging` flow end-to-end — install-token retrieval (with the zsh-`%`-not-part-of-token gotcha), bootstrap form, post-bootstrap verification, and a troubleshooting section for the common failure modes. The local stack runs plain HTTP (TLS termination is the edge proxy's job in real deployments); cookie posture is correct because the api's helper reads `X-Forwarded-Proto` from the propagated header. **Still to do post-B1**: (a) member invite / password-reset round-trip walkthroughs, (b) the install path for self-hosted customers including edge-proxy guidance (the customer's nginx-with-real-certs role) — covered when 2.7.3 (Helm chart + docker-compose bundle) lands. Effort: S (≤½d) for (a); (b) lives in 2.7.3. |
| 2.7.16 | **[POST-B2] First-run auto-redirect to `/bootstrap`** | ✅ | Shipped on `feat/bootstrap-first-run-redirect`. GET `/v1/auth/bootstrap/state` endpoint (public path, Cache-Control: no-store, returns `{available: bool}`) reads the `bootstrap_state` row to detect fresh install. Dashboard App.jsx mount-time useEffect probes the endpoint and navigates to `/bootstrap` when available and no session cookie present. Handles edge cases: session present → straight to `/`; endpoint errors or DEV_MODE → fall through to `/login`. BootstrapScreen mirrors the probe; if bootstrap is already consumed, redirects to `/login` with a friendly error message from `location.state`. Updated `docs/native-auth-bootstrap.md` step 5 to direct operators to the dashboard root instead of manually navigating `/bootstrap`, plus curl probe examples. All paths tested (unit + integration; production-mode DEV_MODE=false verified). |
| 2.7.17 | **Per-connection `force_reauth` override for SSO `prompt=login`** | ✅ | Shipped same session as commit `9f30ad8` (the unconditional `prompt=login` security fix). Migration `023_sso_force_reauth` adds `force_reauth BOOLEAN NOT NULL DEFAULT TRUE` to `sso_connections`; `model.SSOConnection.ForceReauth` exposed in CRUD JSON via `connectionRequest.ForceReauth *bool` (nil = use default on create / no change on patch); `buildAuthorizeURL` branches on `p.ForceReauth` — only emits `prompt=login` when true; dashboard's Connections form gets a "Force re-authentication" checkbox with hint copy explaining the Azure AD conditional-access trade-off. Default-true preserves the security posture for everyone who doesn't explicitly opt out. |
| 2.7.18 | **[BEFORE-FIRST-PAYING-CUSTOMER] Lock down `/metrics` endpoint** | ✅ | Shipped on `chore/lock-metrics-from-public-ingress`. Added `location = /api/metrics { return 404; }` to `services/dashboard/nginx.conf` before the `/api/` prefix block; exact-match `=` beats prefix regardless of order, keeping the security intent visually obvious. Internal Prometheus scrapes `http://api:8080/metrics` directly via container DNS (standard self-hosted-app pattern). Regression test `scripts/smoke-metrics-locked.sh` probes `/api/livez` (sanity, exit 2 on fail) then `/api/metrics` (exit 1 on regression when !=404). Documentation in `docs/license-issuance.md` §Operational states explains internal scraping. |
| 2.7.19 | **[POST-B2] Pass `login_hint=<email>` on OIDC `/initiate`** | ✅ | Shipped on `feat/sso-oidc-login-hint`. The `/initiate` plumbing was already in place from B2 slice 4 (`buildAuthorizeURL` reads `?email=` and emits `login_hint`); the missing seam was the discover hop, which was building `redirect_url` without the email query. Fix in `services/api/internal/sso/discoverer.go`: `NativeDiscoverer.Discover` now appends `?email=<urlencoded>` to the redirect URL via `url.Values.Encode()`, so a `+` in `alice+test@acme.com` survives the round-trip into `login_hint` instead of decoding to space. Dashboard needed no change — `window.location.assign(res.redirect_url)` is opaque about the query. Test pin `TestNativeDiscoverer_RedirectURL_CarriesEmailLoginHint` in `discover_test.go` covers three shapes: plain email, `+` subaddressing (encoding-bug catch), and uppercase passthrough (we don't normalise — the IdP does). |
| 2.7.20 | **[POST-B2] Annotate invitation `redemption_url` when SSO enforcement=required** | ✅ | Shipped on `feat/invitation-sso-enforcement-hint`. Implementation simplified: original spec required domain-verification join; actual check is "any active OIDC connection in org has enforcement=required" (org-wide posture, not per-email-domain). Backend: `POST /v1/invitations` response gains optional `enforcement_hint='sso_required'` field set when at least one active OIDC connection has `enforcement=required`. Store error on `ListSSOConnections` → no hint (logged at `slog.Debug`). Dashboard: yellow callout (fde047 border) renders above the redemption URL when `enforcement_hint='sso_required'`, with copy "SSO is enforced for this organization. The invitee will be auto-onboarded on first SSO login — ask them to sign in via your SSO provider instead of clicking this link. The URL still works as a break-glass for cross-org or IdP-outage cases, but the password-based session it mints will be blocked on the next request." Helper `orgHasRequiredSSO(ctx)` in `services/api/internal/api/invitations.go` implements the check. Test: `TestCreateInvitation_EnforcementHint` with 7 table subcases (no_sso, optional, preferred, required, draft_required, disabled_required, highest_wins). Code reviewed + eslint clean. |
| 2.7.21 | **[POST-B2 / Phase E candidate] Required-groups allowlist on SSO connection** | 🔲 | Surfaced during the `docs/sso-local-keycloak.md` walkthrough on 2026-05-06 when discussing how a corporate AxiaOps admin restricts access to a subset of employees. Today the connection has `default_role` (fallback when no groups map) + `(group, role)` mappings + `enforcement` (blocks password sessions in favor of SSO) but **no app-side "required groups" allowlist**. Effect: anyone who passes the IdP gate AND has an email on a verified domain ends up in AxiaOps with at minimum `default_role` (lowest is `viewer`, which still has read access to all FinOps data — not "no access"). For corps that don't want to delegate the full allowlist to IT (or who have IT setting "user assignment required" on the IdP-side as primary gate but want belt-and-suspenders), there's no app-side equivalent. The cleanest workaround today is IdP-side restriction (Entra: Enterprise Application → "User assignment required" + group assignment; Okta: Application Assignments; Keycloak: client authorization policies) — which is the IT-owned correct answer, but some self-hosted customers want app-side gating too. **Shape**: (a) migration adds `required_groups TEXT[] NOT NULL DEFAULT '{}'` to `sso_connections` (empty = allowlist disabled, current behavior preserved). (b) JIT short-circuit in `services/api/internal/sso/oidc_callback.go` — after extracting groups from the ID token claims AND BEFORE the role-resolver runs, if `len(required_groups) > 0` AND `intersection(claim.groups, conn.required_groups) == ∅`, audit `sso_login_failed{reason: 'required_groups_unmet'}` + 302 to `/login?error=sso_access_denied` (NOT a 403 — keeps the failure shape consistent with the existing `domain_unverified` failure path the user sees). (c) Dashboard wizard step in Settings → SSO → Group Mappings: optional "Restrict to these groups" multi-select, with helper copy "Users not in any of these IdP groups will be denied access. Leave empty to allow anyone with a verified-domain email." (d) Acceptance test in `services/api/internal/sso/oidc_integration_test.go` pinning the `required_groups` short-circuit fires before JIT, no `users` row is created on a denied login, audit row is written with the right reason. **Why Phase E candidate, not B2 follow-up**: belongs to the same scope as SCIM (lifecycle-management features that go beyond the v1 "configure once + JIT" story). Customer-driven priority — file the row, surface in the v1 release notes as a "known limitation, IdP-side restriction is the recommended workaround", revisit if a paying customer asks. **Effort**: M (~½d code + 1 migration + 1 test + 1 dashboard pane). |
| 2.7.22 | **[POST-B2] Drop connection ID from OIDC callback path** | ✅ | Shipped on `feat/sso-callback-drop-cid`. **⚠ BREAKING CHANGE for self-hosted upgrades**: existing customers MUST register `https://<axiaops-host>/v1/sso/oidc/callback` in their IdP **BEFORE** deploying this release, or the next SSO ceremony fails at the IdP with `redirect_uri_mismatch` (AADSTS50011 / Keycloak `invalid_redirect_uri`). The legacy route does NOT rescue blind upgrades — initiate now always sends the new redirect_uri at authorize time, so an IdP that only knows the old URI rejects the request before the legacy callback is ever reached. The deprecation window only helps customers who add the new URI alongside the old one. Standard cid-less callback URL `/v1/sso/oidc/callback` is now the canonical redirect URI — connection identity flows through `StateData.CID` after `StateStore.Consume`, matching every other OIDC RP shape (Auth0, Kinde, Clerk). Code changes: (a) `sso.CallbackPath` constant + initiate's `redirect_uri` flipped to the cid-less form (`services/api/internal/sso/initiate.go`); (b) callback handler reads cid from state instead of path, with the path-cid match check preserved as defence-in-depth ONLY when the legacy route fires (`services/api/internal/sso/oidc_callback.go`); (c) `serverbuild.ComposeServer` registers both routes — standard `/v1/sso/oidc/callback` plus legacy `/v1/sso/oidc/{cid}/callback` for one release as a deprecation window. **Counter**: `axiaops_sso_legacy_callback_total{cid}` increments on every legacy-route hit; remove the path-cid registration in the release after this drops to zero across all customers. **Test pins**: `TestCallback_LegacyPathCID_StillWorks`, `TestCallback_LegacyPathCID_IncrementsCounter`, `TestCallback_StandardRoute_DoesNotIncrementLegacyCounter` in `oidc_callback_test.go`; `TestCallback_StateCIDMismatch_RedirectsToLoginError` switched to `hitLegacy()` since the path-cid mismatch CSRF guard is only enforceable on the legacy route (the standard route relies on state.CID alone). Integration test (`oidc_integration_test.go`) wires both routes mirroring production. `TestInitiate_Redirects_WithAllRequiredParams` updated to assert the new redirect_uri shape. **Customer migration**: register `https://<axiaops-host>/v1/sso/oidc/callback` in the IdP before upgrading; the legacy `<cid>` URI keeps working until the next release. Runbooks (`docs/sso-local-entra.md`, `docs/sso-local-keycloak.md`) and `services/api/CLAUDE.md` all updated. |
| 2.7.23 | **[POST-B2] Close 2026-05-09 security audit findings** | 🟡 partial (14/27) | The 2026-05-09 audit (`docs/security-audit-2026-05-09.md`) flagged 27 findings — 3 Critical, 5 High, 10 Medium, 9 Low. **First pass shipped via MR !141 → branch `security/hardening-2026-05`**: closes **12 of 27** — C-2 (committed `ENCRYPTION_KEY` literal fallback removed; dev-1/dev-2 keys rotated out-of-band + `accounts.secret_encrypted` wiped on both hosts — seed-fixture rows only, no real customer credentials in the exposed ciphertext), C-3 (new `services/api/internal/httpip` package defeating leftmost-XFF spoofing; deletes three duplicate implementations across auth/audit/sso), H-3 (https-only OIDC discovery/metadata URLs with loopback exemption — exported as `sso.RequireHTTPS`), H-4 (new `services/api/internal/httpjson` package — 64 KiB body cap + DisallowUnknownFields on every JSON decoder, 14 sites swapped), H-5 (CSP/HSTS/X-Frame-Options/Referrer-Policy/X-Content-Type-Options on dashboard nginx with `always` so 4xx/5xx carry them too), M-1 (clear `BOOTSTRAP_INSTALL_TOKEN` env post-consume), M-2 (atomic `Cache.GetDel` primitive — Redis `GETDEL` + memory mutex-protected; race-free SSO state-token consume), M-3 (startup warn on wildcard/unset `CORS_ORIGIN` outside DEV_MODE + per-env CI variable propagation through all five `deploy/*.yml` + both `.gitlab-ci.yml` compose-up sites), M-4 + M-5 (new `auth.IPRateLimiter` with separate key-prefix scopes — 30/min/IP on `/v1/auth/bootstrap/state` and `/v1/sso/discover` so they don't share the login budget), M-6 (OIDC §3.1.3.7 `azp` check on multi-aud ID tokens), M-9 (strip `existing_user_name` from invitation preview to prevent cross-org name enumeration). Test posture: 7-case `httpip` package, 4-case `httpjson` package, 5-case `RequireHTTPS` (incl. substring-trick rejection `http://attacker.evil/localhost.txt`), `Cache.GetDel` atomicity test across both backends with 20-goroutine winner-count assertion, 4 M-6 cases pinning single-aud + multi-aud azp branches, M-9 field-absence regression test. Reviewed `APPROVE_WITH_NITS` by `code-reviewer` agent; three should-fix items (M-9 field-absence assertion, H-3 black-box-test refactor exporting `RequireHTTPS`, M-2 GETDEL failure-mode docstring) addressed in commit `01ad35d` before push. **Open work** tracked in issue **#94** (canonical tracker for all remaining findings + the additional concerns surfaced during the hardening pass): **C-1** ✅ **SHIPPED** (api↔ingestion HMAC; 916-line plan `docs/c1-hmac-plan.md` via MR !142 / `f410a2c`; implementation merged to `develop` via `dbbf30fc`, branch `security/c1-ingestion-hmac-impl` — feature commits `cb0ad863` new `services/shared/httpauth/` HMAC-SHA256 package + queue envelope signing, `420c1f27` HMAC middleware + signed verify caller + envelope worker, `351defa5` wired two-secret `INGESTION_SHARED_SECRET` + `_NEXT` rotation slots across all five envs. Both `POST /scan` and `POST /v1/credentials/verify` now HMAC-gated; observability metric `axiaops_ingestion_auth_failures_total` live; closes issue **#96**. **Redis `requirepass`** ✅ **SHIPPED** alongside C-1 in `351defa5` — `requirepass` set across all five `deploy/*.yml` envs with `REDIS_PASSWORD` propagated into `REDIS_URL` userinfo, closing the unauthenticated-LPUSH gap on `axiaops-${env}-network` (plan §8 / §12 Q7)), **H-1** (users-table RLS — needs prerequisite app-pool refactor of `GetUserByID`/`UpsertUser`/`EnsureUser` in `services/shared/storage/postgres/postgres.go` so they don't break under RLS), **H-2** (`/metrics` auth-gate — external exposure already closed at nginx via 2.7.18 `return 404`; remaining piece is the auth-gate-on-the-endpoint decision: bearer-token-via-`METRICS_BEARER_TOKEN` vs separate `:9090` listener bound to the docker bridge), **M-7** ✅ **SHIPPED** (sessions cap atomicity — `MintSession` now calls `store.CreateSessionEnforcingCap`, folding the INSERT + cap-revoke into one tx with full rollback; a cap-revoke failure now 5xx's the login instead of leaving an over-cap leftover — see the "audit M-7" block in `services/api/internal/auth/session.go`), **M-8** (CSRF token system — origin-bound `X-CSRF-Token` + second non-HttpOnly cookie issued at session-mint), **M-10** (audit-log hash chain — deferred by audit itself until SOC2/ISO becomes a customer ask), **L-1..L-9** (mostly cosmetic or paired with H-2). Additional concerns surfaced during the hardening pass and folded into #94: H-3 IPv6 loopback gap in `RequireHTTPS` (`http://[::1]` not exempted), missing `Permissions-Policy` header (sibling of H-5), 413-vs-400 detection for `*http.MaxBytesError` in `services/api/internal/api/handler.go` callers, SSRF surface on `oidc_discovery_url` private IPs (H-3 enforces scheme but not destination IP range), dashboard wire-compat audit for `DisallowUnknownFields` (the backend now 400s any request body carrying a stale field name — frontend not exercised end-to-end yet). **Kinde residue cleanup** (separate scope, surfaced while triaging) tracked in issue **#95** — `.env.example` files advertising dead `KINDE_*` env vars, `scripts/start.sh` exporting `VITE_KINDE_*` into the dev runtime, `deploy/README.md` listing `KINDE_*` as required production vars, banner-stamped-but-body-still-Kinde docs (`auth.md` / `auth_flow.md` / `middleware.md` / `invitation-flow.md`), and unstamped misleading docs (`production.md` / `deployment.md` / `CI_CD_SECRETS_SETUP.md` / `rbac-design.md` / `onboarding-wizard.md`). **Operator follow-ups already executed**: dev-1/dev-2 `ENCRYPTION_KEY` rotated (new 32-byte hex keys minted, set as GitLab CI vars scoped to `dev-1` and `dev-2`, masked/raw/env_var); `accounts.secret_encrypted` wiped on both dev hosts (3 seed-fixture rows each, account_id=`111111111111`); `CORS_ORIGIN` set per-env (`preview` / `staging` / `production`) mirroring `PUBLIC_HOST`. Exposure window dated: literal `ENCRYPTION_KEY` lived in `origin/develop` from `a50032e` (2026-04-13 21:59 +0200) to `541d7c1` on this branch (2026-05-12 00:54 +0200) — about 29 days; still in develop until !141 merges. **Effort remaining**: ~~C-1 + Redis `requirepass` + M-7~~ ✅ shipped (see above), H-1 ~1d (refactor-then-migration), H-2 ~½d once decided, M-8 ~1–2d, Kinde docs ~½d. |

> **Phase 3 dependency**: items #1, #14, #17 in Phase 3 below are explicitly affected by ADR-0001 and rescoped/deferred. See those rows for current status.

## Phase 3 — Beta / GTM (target December 2026)

### Status overview

| # | Task | Status | Notes |
|---|------|--------|-------|
| 1 | Stripe billing | ⏸ Deferred per [ADR-0001](docs/decisions/0001-deployment-model.md) | Originally Starter €49 / Growth €149 / Team €399. Self-hosted-first GTM uses annual contracts (~€5–10k/yr) per design partner — no Stripe needed. Reopen if/when a managed-hosted SKU lands (ADR-0001 review trigger: ≥3 self-hosted customers paying). |
| 2 | **Copy-paste remediation commands** | ✅ | `remediationCommand()` in `services/dashboard/src/screens/DetailScreen.jsx` generates exact AWS CLI per service (EC2 stop, RDS delete-db-instance, Lambda delete-function, ELBv2 delete-load-balancer); `CLICommand` component provides copy-to-clipboard. No write IAM required. |
| 3 | **Tag / team filtering** | 🔲 | Filter zombie list by `owner` tag — "show me only the payments team's zombies" |
| 4 | **CSV export** | ✅ | Shipped as part of Phase 2 #2 (unified convention). DashboardScreen + CostAnalyticsScreen + TrendScreen all expose CSV download via shared `utils/csv` helpers; convention defined in `.claude/skills/csv-export/SKILL.md`. |
| 5 | **Audit trail UI for dismissals** | ✅ | `GET /v1/audit` (with `user_id`/`resource_type`/`resource_id`/`action`/`since`/`until`/`limit`/`cursor` filters) backs `services/dashboard/src/screens/AuditScreen.jsx`, surfaced under Settings → Audit. Dismissal mutations write `audit_log` rows via `model.AuditAction*`. |
| 6 | Scan history log | 🔲 | Per-account scan log with timestamps and zombie delta |
| 7 | Cost forecast | 🔲 | "If nothing changes, you'll waste $X this month" — linear projection over `zombie_snapshots` |
| 8 | **User management + roles** | ✅ | `memberships(user_id, organization_id, role)` table (migration 015 + 016 column rename) + `model.Membership` with owner/admin/member/viewer roles. Endpoints: `GET/POST /v1/memberships`, `PATCH /v1/memberships/{id}/role`, `DELETE /v1/memberships/{id}`, `POST /v1/organizations/transfer-ownership`, `GET /v1/me`. Permission constants (`PermMembersInvite`, `PermMembersManage`, …) enforced in handlers; Settings → Team tab exposes the UI. Invite-by-email is designed in [`docs/invitation-flow.md`](docs/invitation-flow.md) and tracked in 3.9b below. |
| 9 | **GDPR — engineering surface** | ✅ | `DELETE /v1/users/me`, `DELETE /v1/organizations/me`, `GET /v1/export` shipped (handlers in `services/api/internal/api/deletion.go` + `export.go`) |
| 9p | **GDPR — paperwork (narrowed scope per [ADR-0001](docs/decisions/0001-deployment-model.md))** | 🔲 | Privacy policy / ToS / DPA / sub-processors / RoPA / DPIA / breach runbook / pen-test. **Scope narrowed 2026-04-29**: under self-hosted-first, customer is the data controller for their billing/usage data; AxiaOps is processor only of *its own product's* user data (admins logging into our product, not their cloud telemetry). DPA template, sub-processors list, and RoPA shrink accordingly. Pen-test still required before first paying customer. Implementation plan in `docs/compliance/gdpr_plan.md` to be revised. Must ship before first paying EU design partner. |
| 10 | **Expanded detection rules — Custodian backlog** | 🔲 | 13+ new rules ported from `cloud-custodian/cloud-custodian` filters: unused security groups, idle ALB target groups, overprovisioned RDS, idle ElastiCache replication groups, VPC endpoints, TGW attachments, Lambda PCU, IAM access keys, etc. M1 (per-service file split) shipped. Full backlog with priorities, per-rule template, and milestone sequencing in `docs/custodian-rule-backlog.md`. The original entry ("EBS, S3, CloudFront, Redshift, ElastiCache") is superseded — those are all live. |
| 11 | Operating entity | 🔲 | Holding GmbH + Operating UG (target August 2026) |
| 12 | **Pricing rates — live from AWS Pricing API** | 🔲 | Migrate `services/shared/pricing/rates.yml` to a DB-backed `pricing_rates` table refreshed from `pricing:GetProducts`. YAML works fine while rates change ~yearly; this becomes load-bearing once (a) customers complain that numbers don't match their bill, (b) we add Azure/GCP and need multi-provider rate tables, or (c) we backfill historical savings and need point-in-time rates. Until one of those triggers, stay on YAML. |
| 13 | **CUR ingestion — actual-cost mode** | 🔲 | Replace list-price estimates with customer-specific actual costs by ingesting AWS Cost and Usage Reports (CUR). Customer opts in, enables CUR to their S3 bucket, grants us cross-account read. Two deployment shapes: Athena in-place queries (customer pays ~$1–5/mo in query costs, zero egress for us) or ingest to our DB (faster queries, we pay egress). Unlocks exact per-resource cost including Savings Plans / RIs / EDP discounts — the numbers match the customer's actual invoice to the cent. The real differentiator vs Terraform cost estimators (Infracost, AWS Pricing Calculator) that can only use list prices. Ahead of #12 in priority — CUR solves accuracy, #12 only solves "keeping list prices fresh." |
| 14 | **Email-based team invitations (~~Kinde Mgmt API~~ → native, per [ADR-0001](docs/decisions/0001-deployment-model.md))** | ✅ | Shipped on `feat/invite-email`: best-effort SMTP delivery of redemption URLs via org's existing email notification channel. POST /v1/invitations endpoint response includes `email_delivery` field (sent | failed | skipped_no_channel | skipped_no_public_host | error). New `InviteSender` interface + `EmailTransport.SendInvite` in `services/shared/notifications`. Non-fatal — mail failure never fails the invite. Docs updated in `docs/invitation-flow.md`. |
| 15 | **Org-level dashboard** | ✅ | Shipped: read-only org summary at `/` (`screens/OrgSummaryScreen.jsx`); the account workbench moved to `/account` (route-only — `OverviewScreen` untouched; the spec's "split DashboardScreen" was stale, no such file existed). All seven widgets: headline tiles, org-wide trend, by-service, per-account breakdown, account-health strip, top zombies, recent-activity. One new endpoint `GET /v1/summary/by-account` (handler-layer grouping, reuses `enrichWithDismissals`). Single-account orgs redirect to the workbench; scans-pending empty state. Plan: `docs/org-summary-dashboard-plan.md`. |
| 16 | **Historical zombie lineage** | 🔲 | Today `zombie_records` is replaced on every scan — only the *current* zombies are queryable. Per-snapshot aggregates are preserved (`zombie_snapshots`, `zombie_snapshot_services`), but the per-resource history is lost. Add an append-only `zombie_history` table — one row per (snapshot, resource_id) with cost + service + reason + the existing zombie metadata. Storage cost ~36k rows/year/account at 100 zombies × daily scans. Unlocks per-resource timeline, stale reports, audit/compliance evidence, and per-resource snapshot drill-down. |
| 17 | **SOC 2 compliance — Type I → Type II** | ⏸ Deferred per [ADR-0001](docs/decisions/0001-deployment-model.md) | Originally: ship Drata + policy library + evidence pipeline by Q4 2026; Type I Q2 2027; Type II Q4 2027. **Deferred 2026-04-29**: under self-hosted-first, customer holds their own data — multi-tenant SOC 2 scope shrinks dramatically. We may still pursue a narrowed SOC 2 (covering our own internal systems, GitLab pipelines, customer-support tooling) but the multi-tenant data-processing TSCs no longer apply. Reopen + rescope when a managed-hosted SKU is introduced or when a design partner specifically requires SOC 2 evidence beyond what we can offer with narrowed scope. Implementation plan `docs/compliance/soc2_plan.md` to be revised. |

### Implementation detail — Phase 3

#### 3.1 Pricing & Billing — Stripe (September 2026)

- [ ] Sign up for Stripe; get API keys (`STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`)
- [ ] Migration `007_add_billing_fields.sql` — add `plan TEXT DEFAULT 'free'`, `stripe_customer_id TEXT`, `stripe_subscription_id TEXT` to `tenants`
- [ ] Define products in Stripe dashboard: Free, Starter (€49/mo), Growth (€149/mo), Team (€399/mo)
- [ ] Create `services/api/internal/middleware/billing.go` — read `tenant.plan`, enforce tier limits
  - Free: 1 account, no auto-scan
  - Starter: up to 5 accounts, auto-scan enabled, CSV export enabled
  - Growth: unlimited accounts
  - Team: roles + Slack alerts
- [ ] `POST /v1/webhooks/stripe` — handle `customer.subscription.created/updated/deleted`
- [ ] Dashboard: plan indicator in header; upgrade prompt when hitting limits; pricing page
- [ ] 14-day Starter trial on signup (no credit card) — set `plan=starter_trial`, `trial_ends_at`
- [ ] Test: simulate Stripe webhook events in test mode; verify enforcement blocks correctly

#### 3.2 Dismiss Zombie Workflow ✅

- [x] Migration `002_dismiss_snooze.up.sql` — `dismissed_zombies(id, tenant_id, resource_id, reason, note, dismissed_by, dismissed_at, snooze_until)`
- [x] `Store` methods: `DismissZombie`, `ListDismissedZombies`, `UndismissZombie`
- [x] `POST /v1/dismissals` — body: `{reason, note, snooze_until?}`
- [x] `DELETE /v1/dismissals/{id}` — undismiss / cancel snooze
- [x] `GET /v1/zombies` — exclude dismissed by default
- [x] Background ticker to clear expired snoozes (`snooze_until < NOW()`)
- [x] Dashboard: "Dismiss" button on DetailScreen; dismissed zombies show grey "Intentional" badge; snooze UI with date picker
- [x] Test: dismiss a zombie, verify it disappears from list; set snooze, verify it reappears after expiry

#### 3.3 Remediation Actions ✅

- [x] Build remediation command generator in `services/dashboard/src/screens/DetailScreen.jsx` — `remediationCommand()`
  - EC2: `aws ec2 stop-instances --instance-ids {id}`
  - RDS: `aws rds delete-db-instance --db-instance-identifier {id}`
  - Lambda: `aws lambda delete-function --function-name {name}`
  - ELB: `aws elbv2 delete-load-balancer --load-balancer-arn {arn}`
  - NAT Gateway / EIP / EBS / snapshot variants
- [x] `CLICommand` component provides copy-to-clipboard
- [x] Migration `014_audit_log.up.sql` — `audit_log(id, tenant_id, user_id, action, resource_type, resource_id, metadata, created_at)`
- [x] Log every dismiss, undismiss, membership change to `audit_log` via `model.AuditAction*`
- [x] Test: hit each service type, verify correct command returned

#### 3.5 Scan History Log 🔲

- [ ] Migration `010_add_scan_history.sql` — `scan_runs(id, tenant_id, account_id, started_at, finished_at, zombie_count, total_monthly_cost, status, error)`
- [ ] Update ingestion scan flow — write one `scan_runs` row at start (`status=running`), update on completion or failure
- [ ] `GET /v1/accounts/{id}/scans` — last N scan runs for an account
- [ ] Dashboard: scan history list under each account (timestamp, zombie count, status badge, duration)
- [ ] Test: run two scans; verify two rows in `scan_runs`; GET returns both descending

#### 3.6 Tag / Team Filtering 🔲

- [ ] Update `GET /v1/zombies` — support `?team=backend&env=prod` query params (filter on `tags` JSON column)
- [ ] Update `GET /v1/resources` — same tag filtering support
- [ ] Dashboard: tag filter chips alongside service pill filter
- [ ] Test: insert zombies with different team tags; verify filter returns correct subset

#### 3.7 CSV Export ✅ (See Phase 2 #2)

Shipped as part of the unified Phase 2 CSV export convention.

#### 3.8 Per-Account Summary ✅ (folded into #15)

- [x] Update `GET /v1/summary` — support `?account_id={id}` query param (already in `getSummary`)
- [x] Add `GET /v1/summary/by-account` for one-shot multi-account aggregates (replaces N+1 calls; shipped with #15)
- [x] Dashboard: per-account savings shown — in the org dashboard's per-account breakdown / health strip (#15) rather than the old accounts bar
- [x] Test: `TestGetSummaryByAccount` (two-account isolation) + `TestGetSummaryByAccount_ExcludesDismissed` cover the per-account isolation

#### 3.9 User Management ✅ (with caveats)

- [x] `memberships(user_id, tenant_id, role)` table in migration `015_memberships.up.sql`
- [x] Permission constants in `services/shared/authz/roles.go`: `PermMembersRead`, `PermMembersInvite`, `PermMembersManageBasic`, `PermMembersManageAdmin`, `PermTenantTransfer`
- [x] Endpoints (handler.go:109–113): `GET/POST /v1/memberships`, `PATCH /v1/memberships/{id}/role`, `DELETE /v1/memberships/{id}`, `POST /v1/organizations/transfer-ownership`, `GET /v1/me`
- [x] Self-leave bypass on DELETE; last-owner guard at the store level
- [x] Dashboard Settings → Team tab
- [ ] **Deferred** Invite-by-email flow (`pending_memberships` table + Kinde Mgmt API) — designed in [`docs/invitation-flow.md`](docs/invitation-flow.md), tracked in 3.9b below
- [ ] **Deferred** `viewer` role enforcement audit — confirm middleware blocks scan/connect/dismiss for viewers across every endpoint

#### 3.9b Email-based team invitations (Phase 3 #14) 🟡 partial — superseded by 2.7.1

> **HISTORICAL.** This section captures the original Kinde-Mgmt-API design for invitations. Per [ADR-0001](docs/decisions/0001-deployment-model.md) the project flipped to self-hosted and Kinde was removed (see Tasks.md row 2.7.1). The token-bearing native invitation flow shipped via SSO Phase B1 (MR !85): token + hash in `pending_memberships`, redemption URL returned in the API response for OOB sharing, redeemed at first login. Read the section below as "what we wanted before — left for context, do not act on it." Items still applicable (e.g. CRUD endpoints, audit constants) are marked done; Kinde-only items are struck through where they would mislead.
>
> **The previous "User onboarding + app-owned organisations (pattern B)" plan is superseded.** See [`docs/onboarding-and-app-owned-orgs.md`](onboarding-and-app-owned-orgs.md) (now marked superseded) for the rejected alternative.
>
> **What was originally proposed (now historical):** Kinde-primitive invitations via Kinde's Management API. The Kinde-org-per-AxiaOps-org 1:1 mapping. None of this remains in tree post-Kinde-removal — the actual shipped invitation flow is native end-to-end.
>
> Full design (data model, API surface, middleware hook, edge cases, testing): [`docs/invitation-flow.md`](docs/invitation-flow.md) on `feat/team-invitations`. Effort plan: ~7.75 days (vs. the original ~10 commits / ~2 weeks).

> **Outstanding: actually *send* invitation email (AxiaOps system email).** Today invitations
> return an OOB `redemption_url` the admin shares manually — no email is sent. To deliver it
> (and password-reset emails), AxiaOps needs a **deployment-level transactional sender**,
> distinct from the per-org notification channels in §2.15 (those are org-configured + DB-stored;
> this is AxiaOps's *own* mail). The shared SMTP transport (`services/shared/notifications`)
> can be reused, but the sender config is infra, not a DB channel. **For the hosted product
> this is an `aws-infra` change** (sibling repo): SES domain identity for `axiaops.io` + DKIM,
> an IAM user with `ses:SendRawEmail` → SMTP credential (SES-via-SMTP needs an IAM user, not
> the ECS task role) → Secrets Manager → env wiring (`PUBLIC_HOST` already in place). Plus
> SPF/DKIM/DMARC on the domain. Track the infra side in `aws-infra`; the app side here.

Backend (Go):

- [ ] Migration `017_pending_memberships.up.sql` / `.down.sql` — table per `docs/invitation-flow.md §3`. RLS policy `pending_memberships_organization_isolation`. Partial unique index on `(organization_id, lower(email)) WHERE status='pending'`.
- [ ] `model.PendingInvitation` + invitation-status constants (`pending`, `expired`, `revoked`).
- [ ] `Store` interface methods: `CreatePendingInvitation`, `ListPendingInvitations`, `GetPendingInvitation`, `RevokePendingInvitation`, `RedeemPendingInvitation`, `ExpirePendingInvitations`. Sentinel errors (`ErrInvitationAlreadyMember`, `ErrUserExistsNoMembership`, …). Postgres impl + integration tests.
- [ ] `services/api/internal/kinde/` package — Management API client. M2M `client_credentials` token flow with in-process cache (refresh at 80% of TTL). Methods `InviteUser(ctx, orgCode, email, fullName)` and `RemoveUser(ctx, orgCode, kindeUserID)`. `httptest.NewServer`-backed unit tests. `NewStub()` for `DEV_MODE=true`.
- [ ] `services/api/internal/api/invitations.go` — `POST /v1/invitations`, `GET /v1/invitations`, `DELETE /v1/invitations/{id}` handlers. Two-phase commit pattern (insert pending row → call Kinde → compensating-revoke on Kinde failure). Audit-log `AuditActionMemberInvited` (action constant already exists — `services/shared/model/audit.go:42`, no new constants needed).
- [ ] `services/api/internal/middleware/auth.go` — add `RedeemPendingInvitation` call after `EnsureFirstMembership` (line 187), before the `ctx = context.WithValue(...)` assignment. Best-effort: errors logged, never block the request.
- [ ] `services/api/cmd/main.go` — wire the Kinde client + `NewInvitationsHandler`. New env vars: `KINDE_M2M_CLIENT_ID`, `KINDE_M2M_CLIENT_SECRET`, `KINDE_MGMT_API_URL` (defaults to `KINDE_ISSUER`), `INVITATION_TTL_DAYS` (default 14).

Tests:

- [ ] Postgres integration tests: `CreatePendingInvitation` happy + upsert, sentinel errors, `RedeemPendingInvitation` insert+delete in one txn, expired/revoked filtering, RLS isolation.
- [ ] Handler unit tests (mock `Store` + mock `kinde.Client`): 201 happy path, 403 permission tiers, 409 `already_a_member` / `user_exists_use_memberships`, 502 with compensating revoke on Kinde failure, re-invite returns 200, `DELETE` 410-on-already-revoked.
- [ ] Auth middleware end-to-end test: seed pending row → JWT with matching email → assert `memberships` row created, pending row deleted, `RoleOf` returns the stored role.
- [ ] Smoke test: `make test-smoke` flow that exercises `POST /v1/invitations` against the running stack with the dev-mode Kinde stub.

Dashboard:

- [ ] Members screen with "Invite member" form (email + role + optional name) → `POST /v1/invitations`.
- [ ] Pending invitations list with revoke button → `DELETE /v1/invitations/{id}`.
- [ ] Active members list (existing data) — no UX change beyond now living next to the pending list.
- [ ] Inline error handling for 409 (`already_a_member` / `user_exists_use_memberships`) and 502 retry.
- [ ] Use the `dashboard-screen` skill — data shape and error states fully specified by `docs/invitation-flow.md §4`.

Configuration:

- [ ] Document the four new env vars in `services/api/CLAUDE.md` and `.env.example`.
- [ ] Create the M2M application in Kinde admin and grant Management API scopes (`read:users`, `create:users`, `update:user_properties`, `delete:users`, `read:organizations`, `update:organization_users`, `delete:organization_users`).
- [ ] Verify "Create organization on sign up" is enabled in Kinde admin so self-serve signup creates a fresh org.

Out of scope / explicit follow-ups:

- [ ] Background sweeper for `ExpirePendingInvitations` — wire into the existing 5-min stuck-scan ticker (~0.25 d).
- [ ] `audit.WriteFromContext` overload so middleware can emit audit events without `*http.Request` (~0.25 d).
- [ ] Multi-org-per-user — out of scope per prior decision; revisit after first user request.
- [ ] Kinde org membership sync (user removed from Kinde org out of band) — defer; needs Kinde webhooks (paid tier).

#### 3.10 GDPR Compliance

> Full plan: [`docs/compliance/gdpr_plan.md`](compliance/gdpr_plan.md).

Engineering deliverables:

- [x] `DELETE /v1/users/me` — anonymises audit log; sole-owner guard returns 409
- [x] `DELETE /v1/organizations/me` — cascade hard-delete: accounts, cost_records, zombie_records, zombie_snapshots, users, dismissed_zombies, audit_log
- [x] `GET /v1/export` — full JSON dump (zombies, account metadata sans secrets, scan history, audit log entries)
- [x] Anonymise audit log on user hard-delete (replace `user_id` with tombstone, preserve row)
- [ ] Trigger Stripe subscription cancellation on tenant deletion (depends on Stripe billing #1)
- [ ] Hard-delete cron job — sweeps soft-deleted tenants past the 14-day grace window (currently hard-delete only — soft-delete grace not yet implemented)
- [ ] Notification preferences UI + `POST /v1/settings/notifications` for legitimate-interest opt-out
- [ ] CloudWatch log redaction — strip `tags` JSONB content from request logs
- [ ] DSR intake mailbox (`gdpr@axiaops.io`) wired to a ticketing flow with audit log entries on every step
- [ ] Test: create tenant, add data, call DELETE, verify hard-delete cascade → backup roll-off documented

Paperwork deliverables (tracked in `docs/compliance/gdpr_plan.md`):

- [ ] Privacy policy + terms of service + DPA template + sub-processors page
- [ ] Records of Processing Activities (Art. 30) — `docs/compliance/ropa.md`
- [ ] Data Protection Impact Assessment — `docs/compliance/dpia.md`
- [ ] Breach response runbook — `docs/compliance/breach_runbook.md` + tabletop exercise #0
- [ ] External pen-test (Phase 3, before first paying customer)
- [ ] Restore drill #0 (also a SOC 2 deliverable)

#### 3.Fake AWS Client for Tier 1 Testing 🔲

- [ ] Define `AWSClientAPI` interface in `services/ingestion/internal/provider/aws/` — wraps every method called by Tier 1 `Discover*` functions
- [ ] Refactor real `Client` to satisfy this interface
- [ ] Implement `FakeAWSClient` in `fake_client_test.go` — returns canned responses loaded from JSON fixtures
- [ ] Write scenario fixtures in `testdata/tier1/` — one JSON file per Discover function with both zombie and active examples
- [ ] Write table-driven tests for each `Discover*` function using `FakeAWSClient`
- [ ] Benefit: Tier 1 logic tested end-to-end without real AWS calls

#### 3.11 Expanded Detection Rules

Already shipped (Tier 1/2 above):

- [x] EBS unattached volumes (Tier 1)
- [x] Orphaned EBS snapshots (Tier 1)
- [x] Long-stopped EC2 instances (Tier 1)
- [x] Old unused AMIs (Tier 1)
- [x] ElastiCache idle clusters (Tier 2)
- [x] OpenSearch / ES unused domains (Tier 2)
- [x] Redshift abandoned clusters (Tier 2)
- [x] SageMaker forgotten endpoints (Tier 2)
- [x] DynamoDB unused provisioned tables (Tier 2)
- [x] EKS control plane with no nodes (Tier 2)
- [x] CloudWatch Log Groups (no retention or zero stored bytes)
- [x] RDS Snapshots (manual, >30 days, source DB gone)
- [x] ECR Images (untagged or >90 days)
- [x] Secrets Manager (LastAccessedDate >90 days)
- [x] CloudFront (`Requests = 0`)
- [x] Kinesis (`IncomingRecords = 0`)
- [x] S3 (`AllRequests = 0`)

Remaining for Phase 3:

- [ ] Custom per-tenant thresholds: migration `012_add_detection_rules.sql`, `detection_rules(id, tenant_id, service, metric, threshold, enabled)` table
- [ ] `PATCH /v1/settings/rules` — allow tenants to override thresholds
- [ ] Fall back to built-in defaults when no custom rule exists
- [ ] Custodian backlog (~13 rules) — see `docs/custodian-rule-backlog.md`

#### 3.12 Legal Entity 🔲

- [ ] Register operating entity (UG or GmbH — decide based on revenue trajectory)
- [ ] Apple Developer account ($99/year) — required for TestFlight + App Store
- [ ] Google Play Developer account ($25 one-time)
- [ ] VAT registration if EU revenue exceeds threshold
- [ ] Publish privacy policy and terms of service on production domain

#### 3.13 PDF Savings Report 🔲

- [ ] `GET /v1/reports/savings?format=pdf`
  - Summary page: total zombie spend, potential savings, date range
  - Per-service breakdown table
  - Zombie resource list with cost, reason, owner
  - Savings trend chart (uses `zombie_snapshots`)
- [ ] Use a Go PDF library (`github.com/jung-kurt/gofpdf` or `github.com/unidoc/unipdf`) — no external service
- [ ] Gate behind Pro / Growth tier
- [ ] Dashboard: "Export PDF Report" button on summary screen
- [ ] Test: generate PDF for a tenant with known data; verify page count and key fields

#### 3.14 Database Security Hardening 🔲

- [ ] Create separate DB users per service in migration `000_init.up.sql`:
  - `axiaops_api` — SELECT, INSERT, UPDATE on API-facing tables
  - `axiaops_ingestion` — SELECT, INSERT, UPDATE on `cost_records`, `resource_records`, `zombie_snapshots`
  - Retire shared `axiaops` app user
- [ ] Update `DATABASE_URL` env var per service to use its dedicated user
- [ ] Integration test: connect as `axiaops_ingestion`, assert it cannot SELECT from `tenants` or `users`
- [ ] Integration test: connect as `axiaops_api`, assert it cannot INSERT into `cost_records` directly

#### 3.15 Migration History Log ✅

> golang-migrate's `schema_migrations` keeps only the latest applied version (single row). We want a per-migration audit log with filename + checksum (Flyway-style) so we can see when each migration was applied and detect in-place edits of already-applied migration files. Keeps golang-migrate as the engine — adds a sibling table populated by `migrate.go`.

- [x] ~~Migration `NNN_migration_history.up.sql`~~ — shipped as bootstrap DDL in `migration_history.go` (not a numbered migration); `axiaops.migration_history` table with richer columns (`file_sha256`, `applied_by_actor`, `applied_by_image`, `direction`, `status`). `SELECT` granted to `axiaops`, DML revoked via `025_migration_history_revoke_dml.up.sql`
- [x] Extend `services/shared/storage/postgres/migrate.go` — `migration_history.go` records one row per up/down/force with the embedded-file SHA-256 (`recordStarted`, `recordForce`, `backfillIfEmpty`)
- [x] Drift detection: `detectDrift` compares each embedded `.up.sql` SHA-256 against the most-recent succeeded checksum and emits `slog.Warn("migration_history: file checksum drift detected", ...)`; `MIGRATION_HISTORY_STRICT=true` refuses boot
- [x] Document backfill behaviour: on existing DBs the history table is backfilled (`status='backfilled'`, one row per applied version) — documented in `docs/migrations.md`
- [x] Integration test: clean DB → one row per applied version (`TestMigrationHistory_RowsExistForAppliedVersions`, `migration_history_test.go`)
- [x] Integration test: drift injection → strict Migrate errors (`TestMigrationHistory_StrictModeFailsOnDrift`, `migration_history_test.go`)
- [x] `docs/migrations.md` — documents the table (`## Migration history`) + drift detection (`### Drift detection`)

#### 3.16 SOC 2 Compliance — Type I → Type II 🔲

> Full plan: [`docs/compliance/soc2_plan.md`](compliance/soc2_plan.md). Targets aligned with `docs/business_plan.md`: Type I Q2 2027, Type II Q4 2027 (6-month window May–Oct 2027). Heavy overlap with §3.10 GDPR.

Phase 2 finish (May–Aug 2026) — set the stage:

- [ ] Stand up status page (Instatus / Statuspage)
- [ ] Hardware key (YubiKey) on AWS root, GitLab admin
- [ ] CloudWatch alarms — failed-auth spikes, secret access pattern, off-hours deploys
- [ ] Data classification doc — `docs/compliance/data_classification.md`
- [ ] Quarterly access review process (build the muscle even with one user)

Phase 3 (Sep–Dec 2026) — operational baseline:

- [ ] Sign Drata (or Vanta / Secureframe / Sprinto). Target October 2026
- [ ] Policy library: 15 docs in `docs/compliance/policies/`
- [ ] Risk register v1 — `docs/compliance/risk_register.md`
- [ ] Incident response runbook — `docs/compliance/runbooks/incident_response.md`
- [ ] Restore drill #1 — actually execute, capture evidence
- [ ] Pen-test #0 (shared with GDPR §3.10)
- [ ] Vendor questionnaire pack
- [ ] Public security page at `axiaops.io/security`
- [ ] "SOC 2 in progress, Type II Q4 2027" published statement

Phase 4 / Q1–Q2 2027 — Type I:

- [ ] Drata gap-analysis pass — close any control showing "Not implemented"
- [ ] Boutique auditor selection (Prescient / Schellman / A-LIGN / Insight / Johanson)
- [ ] Type I audit (Q2 2027) — point-in-time, 4–6 weeks
- [ ] Publish Type I report (NDA-gated)

Q2–Q4 2027 — Type II:

- [ ] Type II observation window opens (May 1, 2027)
- [ ] Quarterly cadence: access review, risk review, vendor review, restore drill
- [ ] Pen-test #2 within window
- [ ] Tabletop exercise within window
- [ ] Type II audit (Q4 2027) — fieldwork + report by Dec 2027
- [ ] Publish Type II report

Ongoing (2028+):

- [ ] Annual Type II renewal (rolling 12-month windows)
- [ ] Reconsider Privacy TSC if EU enterprise customers ask
- [ ] Layer ISO 27001 if a major customer demands it (~70% control overlap)

#### 3.17 Notification enhancements (v2) 🔲

Builds on the per-scan email/Slack channels shipped in §2.15. All deferred by the
v1 plan — see `docs/notifications-plan.md` "Risks + deferred".

- [ ] Weekly / scheduled email digest — aggregate findings across scans on its own ticker (dedupe against the last send) instead of v1's one-digest-per-scan
- [ ] Per-zombie alert mode — `trigger_rule.on = ["new_zombie"]` joining against the previous snapshot (the `on` field is already provisioned + validated)
- [ ] In-process retry / DLQ for failed dispatches (the `attempts` column is reserved)
- [x] Email-channel **provider presets** in the Add-channel form — a dropdown prefilling `smtp_host`/`smtp_port` for **Amazon SES** and **Google Workspace relay**, plus a **Custom SMTP** fallback (shipped on MR !289). Pure frontend sugar; backend stays generic SMTP. Per the steering decision, **no** "single mailbox / App Password" preset — that path stays "Custom" + runbook only.

---

## Phase 4 — Multi-cloud + Mobile + FOCUS (Q1–Q2 2027)

### Status overview

- 🔲 Multi-cloud (Azure, GCP)
  - **Terminology audit before second provider lands.** Today the dashboard mixes generic ("Cloud Accounts" nav, `/cloud-accounts` route, "Connect Account" button) and AWS-specific ("AWS Account ID" column, "AWS account" body copy) labels — fine while we ship AWS only. When Azure / GCP arrive: replace per-place "AWS account" with `${PROVIDER_LABEL[a.provider]}` lookups, add a Provider column with per-row icons, refactor `/connect` into a provider-picker. The umbrella terms ("Cloud Accounts", `/cloud-accounts`) stay as-is — they're already provider-neutral. Mirror in the API: `model.Account.Provider` already exists (`"aws"|"azure"|"gcp"`) but only `"aws"` is ever written today.
- 🔲 Mobile app (iOS + Android) — Capacitor or React Native wrapper around the existing Vite + React dashboard
- 🔲 Cost forecasting (linear regression over snapshot history)
- 🔲 IaC plan parser (Terraform / CDK) + CI/CD budget gate — _moved to Phase 5_
- 🔲 **FOCUS conformance** — Consumer (Q2 2027) → Producer (Q3 2027) → Foundation conformance assertion (Q4 2027). Plan: `docs/compliance/focus_plan.md`. Unlocks one-parser multi-cloud ingestion (replaces per-cloud cost SDKs in §4.2/§4.3) and Team-tier FOCUS Parquet export for customer data lakes. Depends on CUR ingestion (Phase 3 #13) for credible Producer role.
- 🔲 **Enterprise SSO (SAML/OIDC/Entra)** — design doc: `docs/sso-integration-design.md`. **Moved out of Phase 4 speculative bucket** — SSO is now part of the **Self-hosted v1** workstream below (Phase 2/3) per [ADR-0001](docs/decisions/0001-deployment-model.md). Recommendation flipped from Kinde-brokered to native (Option B). Phase E (SCIM) remains post-v1.

### Implementation detail — Phase 4

#### 4.1 Cost Forecasting (Q1 2027)

- [ ] Implement linear regression over `zombie_snapshots` in `services/shared/analyzer/forecast.go` (stdlib `math` only — no ML lib, ~50 lines)
- [ ] Minimum 60 days of snapshot data required before enabling forecasts (return `402 Insufficient Data` otherwise)
- [ ] `GET /v1/forecast?days=30|60|90` — project future zombie spend per account
- [ ] Anomaly detection: flag if actual spend exceeds forecast by >20% — surface as Slack/email alert
- [ ] Dashboard: forecast line overlaid on savings trend chart

#### 4.2 Multi-Cloud — Azure (Q1 2027)

- [ ] Implement `Provider` interface for Azure Cost Management API (`services/ingestion/internal/provider/azure/`)
- [ ] Implement `Provider` interface for Azure Monitor metrics
- [ ] Add `provider=azure` support in `accounts` table (schema already provider-agnostic)
- [ ] Dashboard: provider icon (AWS/Azure/GCP) on each account and resource card; provider filter pill
- [ ] Only pursue after AWS has proven paying customers

#### 4.3 Multi-Cloud — GCP (Q2 2027)

- [ ] Implement `Provider` interface for GCP Billing Export → BigQuery
- [ ] Implement `Provider` interface for GCP Cloud Monitoring metrics

#### 4.4 FOCUS Conformance (Q2 2027 → Q4 2027)

> Full plan: [`docs/compliance/focus_plan.md`](compliance/focus_plan.md).

Roles and rollout:

- [ ] Pin FOCUS spec version in `services/shared/focus/VERSION` (latest GA minus one minor)
- [ ] Build `services/shared/focus/` package — schema, parse, emit, mapping, validate
- [ ] `ServiceCategory` lookup table (~30 service entries) — can ship before CUR

**Q2 2027 — Consumer role:**

- [ ] `focusfile` provider in `services/ingestion/internal/provider/focusfile/` — ingests FOCUS Parquet/CSV from S3 / blob storage, satisfies the existing `Provider` interface
- [ ] Validate against the foundation's reference dataset; `make test-focus` target
- [ ] `docs/focus_ingestion.md` — customer setup (enable FOCUS export at AWS/Azure/GCP, grant cross-account read)

**Q3 2027 — Producer role (gated on CUR ingestion — Phase 3 #13):**

- [ ] `GET /v1/export/focus?period=YYYY-MM&format=parquet|csv` endpoint — streams FOCUS-conformant export
- [ ] Plan-gate to Team tier and above
- [ ] `docs/focus_export.md` — examples for Snowflake / BigQuery / Athena / Power BI
- [ ] Audit log entry on every export (sibling to GDPR `gdpr.dsr.export`)

**Q3 2027 — Multi-cloud unification (the strategic payoff):**

- [ ] Replace Azure-specific cost ingestion (§4.2) with Azure → FOCUS export → `focusfile` provider
- [ ] Same for GCP (§4.3): GCP Billing Export → FOCUS → `focusfile` provider
- [ ] Update `docs/multicloud-coverage.md` with the new ingestion topology

**Q4 2027 — Foundation conformance assertion:**

- [ ] Submit conformance assertion (self-attestation as of plan write date; check for formal review process)
- [ ] Add badge to `axiaops.io/security` and homepage
- [ ] Annual re-assertion + on-bump workflow

#### 4.5 Mobile App (Q2 2027)

- [ ] Decide native shell: Capacitor wrapper around the Vite bundle, or a new RN/Expo project
- [ ] iOS build with no web-only code paths
- [ ] Android build with no web-only code paths
- [ ] ~~Kinde PKCE login on native (iOS keychain / Android keystore)~~ — Phase 4 mobile is post-ADR-0001 and post-Kinde-removal; reconsider the mobile auth shape (cookie-on-WebView vs OIDC-PKCE-against-native-IdP) separately if/when mobile work is greenlit.
- [ ] Push notification for new zombies (replaces email digest on mobile)
- [ ] Submit to TestFlight for internal testing (requires Apple Developer account from 3.12)
- [ ] Submit to Google Play internal track (requires legal entity from 3.12)

---

## Phase 5 — Proactive Cost Simulation (Q3–Q4 2027)

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

## CI — Containerize every job, drop custom runners

Context: current CI uses a shell executor that assumes Go, golangci-lint, and Docker are
pre-installed on the runner host, plus a shared `gitlab-runner-network` for service
containers. This ties CI to bespoke runner images and broke on the new self-hosted runner (see
commit `dafac6b`, IP-lookup workaround). Instead of owning a custom runner image, make
every job self-contained by specifying `image:` + `services:` — then any generic Docker
runner (GitLab.com shared or a vanilla self-hosted one) can run the pipeline.

End state: the runner needs only Docker. CI YAML pins every tool version. Tests run
identically locally and in CI.

**Phase 1 — Containerize test jobs**

- [x] `test:unit` → add `image: golang:1.25`, drop `.go_setup` reliance.
- [x] `test:lint` → `image: golangci/golangci-lint:v2.11.4`.
- [x] `test:storage` → `image: golang:1.25` + `services: [{ name: postgres:16-alpine, alias: postgres, variables: … }]`. Drop manual `docker run`, readiness probe, `after_script`, IP lookup.
- [x] `test:redis` → `image: golang:1.25` + `services: [{ name: valkey/valkey:8-alpine, alias: redis }]`. Alias stays `redis` so `TEST_REDIS_URL=redis://redis:6379` resolves against the service container regardless of the underlying image (RESP wire-protocol).
- [x] Remove `.go_setup` block once nothing references it.
- [x] Revert commit `dafac6b` (IP-lookup hack) — replaced by `services:` aliases; no `getent`/`docker inspect` IP lookup remains.
- [ ] Drop unused variables (`RUNNER_NETWORK`, `PG_CONTAINER`, `REDIS_CONTAINER`, `POSTGRES_PASSWORD`, `POSTGRES_OWNER_PASSWORD`) if nothing else uses them. — `PG_CONTAINER`/`REDIS_CONTAINER` gone; `RUNNER_NETWORK` still dangling (defined at `.gitlab-ci.yml:129`, unused).

**Phase 2 — Containerize infrastructure jobs**

- [x] `test:integration:*` → `image: docker:24` (`before_script: apk add make docker-cli-compose`), runs against the runner's docker socket — the repo's deliberate model — rather than a `docker:24-dind` service. Makefile target unchanged.
- [x] `build:images` → `image: docker:24` against the runner's docker socket (same model as `test:integration:*`).
- [ ] `deploy:*` → `image: docker:24` + `apt-get install awscli` in `before_script`, or `image: amazon/aws-cli` with nested docker.

**Phase 3 — Swap the runners**

- [ ] Try GitLab.com shared runners on a test branch. Confirm all jobs green.
- [ ] Decide: move everything to shared runners, or keep a minimal self-hosted runner for deploys that need VPC access (ECS, RDS).
- [ ] If mixed model: tag deploy jobs with `tags: [self-hosted]`, leave test/build on shared.
- [ ] Decommission old socket-mount runner.
- [ ] Decommission self-hosted runner (or repurpose with stock `gitlab-runner` image and docker executor, no custom image).

**Phase 4 — Cleanup**

- [x] Pin Postgres service image tag to `postgres:17.5-alpine` (matches prod RDS minor; source of truth: `aws-infra` `terraform/environments/production/main/terraform.tfvars` `engine_version = "17.5"`). Done in this MR; valkey + dashboard nginx pinning tracked separately.
- [ ] Pin Go image tag (`golang:1.25.3`, not `golang:1.25`).
- [ ] Pin DinD tag.
- [ ] Document the CI model in `docs/` — one page, "runners are disposable, images are pinned."
- [ ] Add a `make test-ci` target that runs the exact same image invocations locally so engineers can reproduce CI failures.

**Risks to watch**

- First-job-on-runner image pull latency. Use GitLab's image caching or accept ~5-10s on cold runs.
- Host-behavior-dependent tests (timezone, DNS, filesystem case sensitivity) may pass locally and fail in CI. Expect 1-2 surprises across a year.
- Deploy jobs that need VPC/VPN access can't run on shared runners — requires a small self-hosted runner or ECS/Fargate one-off tasks for migrations (already the pattern for production).

---

## Milestone Timeline

| Date | Milestone | Status |
|------|-----------|--------|
| April 2026 | Phase 1 complete + Phase 2 early work + Phase 3 dismiss/remediation/audit/memberships/GDPR shipped early | ✅ Done |
| May 2026 | Versioned migrations, savings history, observability, scan recovery, API versioning, rate limiting, graceful shutdown | ✅ Done |
| June 2026 | GitLab CI pipeline, scheduled auto-scan, cost_records retention | ✅ Done |
| July 2026 | Redis (JWKS cache, scan queue, rate limiting), unified CSV, raw cost view | ✅ Done |
| August 2026 | Production deployment (ECS Express + RDS + Valkey — largely shipped, see §2.16), notification channels (email + Slack scan digests — shipped, see §2.15), tenant→organization rename | 🟡 In progress |
| September 2026 | Stripe billing, GDPR paperwork live, legal entity | Planned |
| October 2026 | Tag filtering, scan history log, per-account summary, custom detection thresholds | Planned |
| November 2026 | Custodian rule backlog, multi-org UX, org dashboard, PDF report, cost forecast | Planned |
| December 2026 | Historical zombie lineage, per-service DB users, migration history log, first paying customer (target: 10 customers, €5K MRR), Drata + SOC 2 policy library in place | Planned |
| Q1 2027 | Azure integration, SOC 2 Type I prep (auditor selected), FOCUS package scaffolding | Planned |
| Q2 2027 | Mobile app, **SOC 2 Type I audit + report**, **FOCUS Consumer role shipped** (`focusfile` ingestion) | Planned |
| Q2–Q3 2027 | SOC 2 Type II observation window (May–Oct 2027) | Planned |
| Q3 2027 | GCP integration via FOCUS, **FOCUS Producer role shipped** (`GET /v1/export/focus`), multi-cloud ingestion unified through FOCUS, IaC plan parser begins | Planned |
| Q4 2027 | Cost estimation engine, what-if scenarios, CI/CD budget gate, CLI tool, **SOC 2 Type II audit + report**, **FOCUS Foundation conformance assertion** | Planned |

---

## Design & Decision Docs

- **CloudTrail Integration:** `docs/cloudtrail-analysis.md` — Why CloudTrail detection was deferred to Phase 4+, ROI analysis, when to reconsider
- **AWS Service Coverage:** `tmp/aws-coverage-and-cost-explorer-notes.md` — Why certain services are prioritized, detection patterns
- **Tier 2 Detections:** `docs/tier2_detections_status.md` — ElastiCache, OpenSearch, Redshift, SageMaker, DynamoDB, EKS detection status
- **Custodian Rule Backlog:** `docs/custodian-rule-backlog.md` — Phase 3 #10 priority + per-rule template
- **Compliance Plans:** `docs/compliance/{gdpr,soc2,focus}_plan.md`
