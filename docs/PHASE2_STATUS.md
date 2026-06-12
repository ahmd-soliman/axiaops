# AxiaOps Phase 2 Status — Updated June 11, 2026 (originally April 19, 2026)

> Source of truth for the Phase 2 roadmap lives in `Tasks.md` (section 2.x) and
> the detailed execution plan in `docs/phase2-plan.md`. This file is the at-a-glance
> status readout — updated when a task materially changes state.

---

## ✅ Completed (Shipped)

All May 2026 milestone items and most June 2026 items are in `main`.

### Observability (2.6)
- Structured logging via `log/slog` (JSON in prod, text in dev) in `services/shared/logging/logging.go`.
- Request ID middleware (`services/api/internal/middleware/requestid.go`) threads `X-Request-ID` into `slog` context.
- Scan lifecycle logs (`scan.started`, `scan.completed`, `scan.failed`) carry `tenant_id`, `account_id`, `duration_ms`, `ghost_count`.
- Prometheus metrics exposed at `/metrics` on both API and ingestion:
  - `axiaops_api_request_duration_seconds` (histogram, route + status labels)
  - `axiaops_api_requests_total` (counter)
  - `axiaops_scan_duration_seconds`, `axiaops_ghosts_detected_total`
  - `axiaops_ingestion_records_fetched_total`, `axiaops_potential_monthly_savings_usd`
- `GET /health` checks DB connectivity and ingestion reachability.
- Sentry intentionally skipped — structured logs are sufficient at current scale.

### API Versioning (2.8)
- All routes live under `/v1/`. `/health` and `/metrics` stay unversioned.
- nginx proxies `/api/v1/*` → `api:8080/v1/*`.
- Dashboard client and all handler tests updated.

### Rate Limiting (2.9 + Redis refactor)
- `services/api/internal/middleware/ratelimit.go` — 60 req/min per tenant, `Retry-After` on 429.
- Backed by `cache.Incr` (Redis when `REDIS_URL` set, in-memory fallback otherwise).
- Bypassed when `DEV_MODE=true`.

### Graceful Shutdown (2.10)
- `signal.NotifyContext(SIGTERM, SIGINT)` in both `services/api/cmd/main.go` and `services/ingestion/cmd/main.go`.
- 30-second drain window via `server.Shutdown(ctx)`; DB pool closed after.
- Ingestion rejects new `POST /scan` with `503` during drain; current scan is allowed to finish.
- Emits `shutdown.started` / `shutdown.complete` with drain duration.

### Versioned Migrations (2.4)
- `golang-migrate/v4` wired into both service `main.go`s; runs on startup against `MIGRATION_DATABASE_URL`.
- All `CREATE TABLE IF NOT EXISTS` / `ALTER TABLE` removed from application code.
- Migrations under `services/shared/storage/postgres/migrations/`.

### Savings History / Trend (2.5)
- `ghost_snapshots` table + `SaveSnapshot` / `ListSnapshots` on the `Store` interface.
- Ingestion writes a snapshot row per scan after `SaveGhosts`.
- `GET /v1/trend?account_id={id}&days=30` powers the dashboard sparkline.

### Scan Recovery (2.7)
- 5-minute background ticker in API resets accounts stuck in `scanning` > 15 min.
- Emits `scan.timeout_reset` with `account_id` and `stuck_duration`.

### GitLab CI Pipeline (2.11)
- `.gitlab-ci.yml` with `test` → `build` → `deploy` stages.
- Test runs `go test ./...` + `golangci-lint` on every branch.
- `main` builds Docker images for api / ingestion / dashboard, pushes to ECR, and deploys to ECS Express (architecture pivoted from App Runner — see `Tasks.md` §2.16 pivot note).
- CloudFront invalidation for dashboard assets.

### Scheduled Auto-Scan (2.12)
- `accounts.scan_interval_hours` column (migration `004`), default 24.
- `PATCH /v1/accounts/{id}` accepts `scan_interval_hours`.
- Background ticker in ingestion fires scans for accounts past their interval; skips those already `scanning`.
- Emits `scan.scheduled` / `scan.skipped_already_running`.
- Dashboard shows next scan time per account.

### cost_records Retention (2.13)
- Daily midnight-UTC ticker in ingestion deletes rows with `period_end < NOW() - COST_RECORDS_RETENTION_DAYS` (default 90).
- Migration `005` added index on `(tenant_id, period_end)` for efficient range deletes.
- Emits `cost_records.cleanup` with `rows_deleted` + `duration_ms`.

### Integration Test Suite
- `test/integration/` standalone Go module.
- Covers `/health`, `/metrics`, account creation, scan queue, rate limiting, scheduled auto-scan.
- `make test-integration` runs against a live `make start-dev` stack.
- Old `test/smoke/` directory and `make test-smoke` removed — integration tests superseded them.

---

## 🟡 In Progress / Partially Shipped

### Redis (2.14) — core shipped, hardening pending

> **Note (2026-05-27):** the cache engine migrated from Redis to Valkey across
> all envs as part of `chore/valkey-migration`. The application surface
> (`REDIS_URL`, `cache.Cache` interface, `/readyz` `"redis"` key, go-redis SDK)
> is unchanged — wire-protocol surfaces stay redis-named on purpose; only the
> container image + container name + `redis-cli`→`valkey-cli` invocations
> flipped. Entries below were written pre-migration; the architecture they
> describe is identical, just with `valkey/valkey:8.1-alpine` as the running
> image.

**Shipped:**
- `valkey/valkey:8.1-alpine` in `docker-compose.yml` with `valkey-cli ping` healthcheck (was `redis:7-alpine` + `redis-cli ping` pre-migration).
- `REDIS_URL` threaded into api + ingestion service env.
- Unified `Cache` interface at `services/shared/cache/cache.go` with Redis and in-memory implementations.
- Unified queue interface at `services/shared/queue/queue.go` — Redis-backed scan queue, synchronous fallback.
- API `main.go` wires `cache.New(REDIS_URL)` into:
  - Auth middleware (`middleware.NewAuth(..., c)`) for JWKS caching.
  - Rate limiter (`middleware.NewRateLimiter(c)`) — enabled when `REDIS_URL != ""`.
- Ingestion worker pops jobs off the Redis list; API pushes via `q.Enqueue`.

**Pending (per `docs/phase2-plan.md` Track B):**
- AOF persistence + volume mount in `docker-compose.yml` (lose queue on restart today).
- Production Redis provisioning — decision gate between Upstash (recommended) vs. ElastiCache Serverless vs. self-hosted Fargate Spot.
- Terraform module under `terraform/modules/redis/` with `REDIS_URL` stored in Secrets Manager.
- Expanded Redis usage: idempotency keys on enqueue, summary cache, delayed-job sorted set for retries, dead-letter list.
- `.env.example` updates with a `rediss://` sample URL.

---

## ❌ Not Yet Started

### Weekly Email Digest + Slack Alerts (2.15) — July 2026
- Pick provider (Resend preferred); add `RESEND_API_KEY` env var.
- HTML digest template: ghost count, top 5 resources by cost, week-over-week delta from `ghost_snapshots`.
- `SLACK_WEBHOOK_URL` per account (migration `006_add_slack_webhook.sql`).
- `POST /v1/settings/notifications` to toggle digest + webhook per tenant.
- Post Slack message when ghost count changes after a scan.

### Production Deployment (2.16) — ✅ shipped June 2026 on ECS Express

> **Pivot note:** the plan below predates the App Runner → ECS Express pivot (2026-05); App Runner references are historical.

**IAM**
- `AxiaOpsAppRunnerRole` — ECR / Secrets Manager / RDS access.
- `AxiaOpsCI` — ECR push + `apprunner:UpdateService` only.

**Terraform**
- Modules: App Runner (api + ingestion), RDS `db.t4g.micro`, ElastiCache/Upstash, Secrets Manager, ECR, VPC + security groups (public subnets — no NAT Gateway).
- S3 + DynamoDB state backend under `terraform/`.

**RDS**
- Provision `db.t4g.micro` PostgreSQL 17 in `eu-central-1`.
- Run migrations as a one-off container job (not inside long-running services).
- Daily snapshots, 7-day retention. CloudWatch log retention 7 days.

**Secrets Manager**
- Store `ENCRYPTION_KEY`, `REDIS_URL`, `RESEND_API_KEY`. (Kinde variables removed with Kinde — 2026-05.)
- Document `ENCRYPTION_KEY` rotation procedure in `docs/ops.md` (re-encrypt `secret_encrypted` before rotating).

**Database Password Management**
- Separate migration job from service runtime — only `DATABASE_URL` available to running services in prod.
- AWS Secrets Manager with automatic rotation; remove `ALTER USER` bootstrap logic from prod startup.
- Keep the self-bootstrap pattern for dev/staging only.

**App Runner**
- api on `:8080`, ingestion on `:8081`, wired to RDS + Redis.
- Custom domain + managed TLS. Health check: `GET /health`, 30s timeout.

**Dashboard**
- Vite static build → S3 + CloudFront. CI stage invalidates.

**EventBridge**
- `rate(24 hours)` → `POST /v1/accounts/{id}/scan` per account (complements in-service scheduler as a safety net).

**Smoke test production** — connect a real AWS account, trigger scan, verify ghosts appear.

---

## 📊 Summary

| Task | Section | Status | Target |
|------|---------|--------|--------|
| Versioned Migrations | 2.4 | ✅ Shipped | May 2026 |
| Savings History | 2.5 | ✅ Shipped | May 2026 |
| Observability | 2.6 | ✅ Shipped | May 2026 |
| Scan Recovery | 2.7 | ✅ Shipped | May 2026 |
| API Versioning | 2.8 | ✅ Shipped | May 2026 |
| Rate Limiting | 2.9 | ✅ Shipped | May 2026 |
| Graceful Shutdown | 2.10 | ✅ Shipped | May 2026 |
| GitLab CI Pipeline | 2.11 | ✅ Shipped | June 2026 |
| Scheduled Auto-Scan | 2.12 | ✅ Shipped | June 2026 |
| cost_records Retention | 2.13 | ✅ Shipped | June 2026 |
| Redis (core) | 2.14 | 🟡 Shipped locally — prod hardening pending | July 2026 |
| Email Digest + Slack | 2.15 | ❌ Not started | July 2026 |
| Production Deployment | 2.16 | ❌ Not started | August 2026 |

---

## 🚀 Immediate Action Items (This Sprint)

1. **Redis persistence + prod target** — add AOF + volume in compose; pick Upstash vs. ElastiCache so Terraform can be written against a concrete target.
2. **Scan runs + retry/DLQ** — per `phase2-plan.md` Track A, the next user-visible gap is durable `scan_runs` history with retry/dead-letter on top of the Redis queue. Unblocks "why didn't my scan run last night?" in the dashboard.
3. **Notifications scaffolding** — wire Resend client and Slack webhook sender behind a feature flag so 2.15 can ship incrementally.

**Next review:** May 1, 2026
