# AxiaOps Phase 2 Status — Updated April 11, 2026

## ✅ COMPLETED (Shipped)

### 1. Observability (P0) — DONE
- **Structured logging:** `log/slog` with JSON output in production, text in dev
- **Error tracking:** Sentry SDK integrated (`services/shared/logging/logging.go`)
- **Prometheus metrics:** 
  - `axiaops_api_requests_total` — request counter per endpoint/status
  - `axiaops_api_request_duration_seconds` — request latency histogram
  - `axiaops_ingestion_records_fetched_total` — cost records counter
  - `axiaops_ingestion_ghosts_detected_total` — ghost detection gauge
  - `axiaops_potential_monthly_savings_usd` — savings gauge
- **Health endpoint:** Extended `/health` (currently reads from DB; status checks not yet implemented)
- **Files:** `services/shared/logging/logging.go`, `services/api/cmd/main.go`, `services/ingestion/cmd/main.go`

### 2. API Versioning (P0) — DONE
- **All routes prefixed `/v1/`:** 
  - `/v1/ghosts`, `/v1/summary`, `/v1/accounts`, `/v1/accounts/{id}/scan`, etc.
- **nginx proxy:** Routes `/api/v1/*` → `api:8080/v1/*`
- **Tests:** Updated to use `/v1/` paths (see middleware tests)
- **Files:** `services/api/internal/api/api.go`, nginx config in docker-compose

### 3. Rate Limiting (P0) — DONE
- **Implementation:** In-memory token bucket (`services/api/internal/middleware/ratelimit.go`)
- **Limits:** 60 requests/minute per tenant
- **Response:** 429 status with `Retry-After` header
- **Disabled in dev:** `DEV_MODE=true` bypasses limiting
- **Concurrency guard:** Account scan already protected from double-booking
- **Files:** `services/api/internal/middleware/ratelimit.go` (tests included)

---

## ❌ NOT YET STARTED — NEXT PRIORITIES (May 2026)

### 1. Graceful Shutdown (P0) — Required before App Runner deployment
**Why:** App Runner sends `SIGTERM` before terminating containers. Must drain in-flight requests cleanly.

**Current state:** Both services use simple `http.ListenAndServe()` — no signal handling.

**What needs to change:**
```go
// services/api/cmd/main.go & services/ingestion/cmd/main.go
import "os/signal"

ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer cancel()

server := &http.Server{Addr: addr, Handler: logged}
go func() {
    <-ctx.Done()
    // 30-second drain window
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer shutdownCancel()
    server.Shutdown(shutdownCtx)
    // Cleanup: close DB connection pool
    s.Close()
}()

if err := server.ListenAndServe(err); err != nil && err != http.ErrServerClosed {
    die("server error", "error", err)
}
```

**Scope:**
- [ ] API service: handle SIGTERM, drain HTTP requests
- [ ] Ingestion service: handle SIGTERM, complete current scan before shutdown
- [ ] Both: close PostgreSQL connection pool cleanly
- [ ] Log: `shutdown.started`, `shutdown.complete` with drain duration
- [ ] Tests: unit tests for signal handling (not blocking)

**Estimate:** 2–3 hours | **Owner:** — | **Blocker for:** Production deployment

---

### 2. GitLab CI Pipeline (P0) — Build & Deploy Stages
**Why:** No manual build/push steps in production. Automate test→build→deploy.

**Current state:** Only `test` stage exists (test:postgres, test:ingestion, test:api probably missing).

**What needs to change:**

```yaml
# .gitlab-ci.yml additions

stages:
  - test      # ✅ Exists
  - build     # ❌ TODO
  - deploy    # ❌ TODO

# Build stage: Docker images + ECR push
build:api:
  stage: build
  image: docker:latest
  services:
    - docker:dind
  script:
    - docker build -t $AWS_ECR_REPO/api:$CI_COMMIT_SHA -f services/api/Dockerfile .
    - aws ecr get-login-password | docker login --username AWS --password-stdin $AWS_ECR_REPO
    - docker push $AWS_ECR_REPO/api:$CI_COMMIT_SHA
  only:
    - main

build:ingestion:
  # Similar to build:api
  
build:dashboard:
  # an edge proxy build → Expo static bundle → Docker layer → ECR

# Deploy stage: App Runner update
deploy:production:
  stage: deploy
  image: amazon/aws-cli:latest
  script:
    - aws apprunner update-service --service-arn $API_SERVICE_ARN --image-repository $AWS_ECR_REPO/api:$CI_COMMIT_SHA
    - aws apprunner update-service --service-arn $INGESTION_SERVICE_ARN --image-repository $AWS_ECR_REPO/ingestion:$CI_COMMIT_SHA
  only:
    - main
```

**Scope:**
- [ ] Add API service Dockerfile (Go binary)
- [ ] Add ingestion service Dockerfile (Go binary)
- [ ] Add dashboard Dockerfile (Expo static + nginx)
- [ ] ECR repository setup (AWS account)
- [ ] App Runner service creation (AWS account)
- [ ] GitLab CI/CD variables: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_ECR_REPO`
- [ ] Branch strategy: main → full pipeline; feature branches → test only

**Estimate:** 4–6 hours | **Owner:** — | **Blocker for:** Production deployment

---

### 3. Scheduled Auto-Scan (P1) — 24h interval per account
**Why:** Users shouldn't have to manually trigger scans every time. Automate per-account interval.

**Current state:** Only manual `/accounts/{id}/scan` exists. No scheduler.

**What needs to change:**
- Add `scan_interval_hours` field to `accounts` table (default: 24, configurable per account)
- Background ticker in API service: every minute, check all accounts needing a scan
- Skip if account already `scanning` (uses existing concurrency guard)
- Respects timezone (or use UTC internally)
- Migration: `ALTER TABLE accounts ADD COLUMN scan_interval_hours INT DEFAULT 24`
- Log: `scan.scheduled`, `scan.skipped_already_running`, `scan.failed`

**Scope:**
- [ ] Migration: add `scan_interval_hours` + `last_scheduled_at` to accounts
- [ ] Background ticker: every 1 minute, find accounts past their next_scan_time
- [ ] API endpoint: `PATCH /v1/accounts/{id}` to update `scan_interval_hours`
- [ ] Structured logging for scheduler events
- [ ] Tests: mock ticker behavior, verify accounts are selected correctly

**Estimate:** 2–3 hours | **Owner:** — | **Blocker for:** User convenience (not deployment)

---

### 4. cost_records Retention (P1) — 90-day cleanup
**Why:** `cost_records` table grows unbounded. Every scan adds up to 30 days of billing data per account.

**Current state:** No retention policy; table grows indefinitely.

**What needs to change:**
- Add `created_at` index on `cost_records` for efficient range deletes
- Daily background ticker in ingestion service: `DELETE FROM cost_records WHERE period_end < NOW() - INTERVAL '90 days'`
- Configurable via `COST_RECORDS_RETENTION_DAYS` env var (default: 90)
- Log: `cost_records.cleanup` with rows deleted and duration

**Scope:**
- [ ] Migration: add `created_at` column + index on `cost_records` if not present
- [ ] Background ticker in ingestion service (similar to API's scan-recovery ticker)
- [ ] Structured logging: `cost_records.cleanup` with row count and duration
- [ ] Tests: mock ticker, verify correct records are deleted (by period_end)

**Estimate:** 1–2 hours | **Owner:** — | **Blocker for:** Performance (not deployment)

---

### 5. Redis Integration (P1) — Cache & Async Queue
**Why:** 
- JWKS key caching (avoid Kinde roundtrip on every auth)
- Scan job queue (async, decouples HTTP response from ingestion start)
- Rate limiting (survives API restarts, works across replicas)

**Current state:** None; in-memory implementations in place as fallback.

**What needs to change:**
- Add Redis container to `docker-compose.yml`
- Implement `Cache` interface: `Get(ctx, key)`, `Set(ctx, key, val, ttl)`, `Del(ctx, key)`
- Redis implementation: `github.com/redis/go-redis/v9`
- Environment: `REDIS_URL` (e.g. `redis://localhost:6379`); empty = fallback to in-memory
- Fall back gracefully if `REDIS_URL` unset

**Scope:**
- [ ] docker-compose: add Redis 7 container
- [ ] `services/shared/cache/cache.go` — interface definition
- [ ] `services/shared/cache/redis/redis.go` — Redis client wrapper
- [ ] `services/shared/cache/memory/memory.go` — in-memory fallback
- [ ] Update auth middleware: inject cache for JWKS lookup
- [ ] Update scan queue: push to Redis list in API, consume in ingestion worker
- [ ] Update rate limiter: Redis `INCR` + `EXPIRE` (replaces in-memory from 2.8)
- [ ] Tests: mock Redis, verify cache invalidation

**Estimate:** 4–6 hours | **Owner:** — | **Blocker for:** Scalability

---

### 6. Weekly Email Digest + Slack Alerts (P1) — Notifications
**Why:** Users want periodic summaries of ghost spend and real-time alerts on new detections.

**Current state:** No alerting; data only visible when user logs in.

**What needs to change:**
- Email provider: Resend or SendGrid
- Slack webhook per tenant (optional, user-provided)
- Weekly email: "You have $X in ghost spend this week" (references `ghost_snapshots` from Phase 2.5)
- Slack alert: "New ghosts detected: 3 EC2 instances + 1 RDS cluster"
- Background job: scheduled email every Sunday 08:00 UTC

**Scope:**
- [ ] Email template: HTML weekly digest with ghost list + savings trend
- [ ] Slack webhook handler: POST to user-provided webhook URL
- [ ] Background ticker in API: weekly email job (or external cron service)
- [ ] Dashboard: settings page to add Slack webhook URL, toggle email digest
- [ ] Structured logging: `email.sent`, `slack.alert`, with recipient and ghost count
- [ ] Tests: mock email/Slack, verify payload structure

**Estimate:** 3–4 hours | **Owner:** — | **Blocker for:** User engagement (not deployment)

---

### 7. Production Deployment (P0) — Final rollout
**Why:** Everything else culminates here. App Runner + RDS + Terraform.

**Current state:** Docker Compose works locally; no AWS infrastructure.

**What needs to change:**
- AWS Account setup (if not done)
- App Runner services: one for API (:8080), one for ingestion (:8081)
- RDS PostgreSQL: `db.t4g.micro` (free tier eligible; ~€10–20/month)
- ElastiCache Serverless (Redis) for caching and queue
- Secrets Manager: `ENCRYPTION_KEY`, `SENTRY_DSN`, `REDIS_URL`
- Terraform modules: reproducible IaC (state in S3 + DynamoDB lock)
- CloudFront for dashboard static assets
- Monitoring: CloudWatch Container Insights + Sentry dashboard

**Dependencies:** Graceful shutdown, CI/CD pipeline, observability, Redis

**Estimate:** 6–8 hours (infrastructure + IaC) | **Owner:** — | **Blocker for:** Revenue

---

## 📊 Summary

| Task | Status | Priority | Estimate | Target |
|------|--------|----------|----------|--------|
| Graceful Shutdown | ❌ TODO | P0 | 2–3h | May 1 |
| GitLab CI Pipeline | ❌ TODO | P0 | 4–6h | May 5 |
| Scheduled Auto-Scan | ❌ TODO | P1 | 2–3h | May 15 |
| cost_records Retention | ❌ TODO | P1 | 1–2h | May 20 |
| Redis Integration | ❌ TODO | P1 | 4–6h | June 1 |
| Email/Slack Alerts | ❌ TODO | P1 | 3–4h | June 15 |
| Production Deployment | ❌ TODO | P0 | 6–8h | July 1 |

**Phase 2 completion target:** End of July 2026 (production-ready, first beta users)

---

## 🚀 Immediate Action Items (This Sprint)

1. **Graceful Shutdown** — Required for any production deployment
2. **GitLab CI Pipeline** — Automate builds; prerequisite for App Runner
3. **Both in parallel:** Start Redis design (interfaces) while waiting on blockers

**Next review:** May 1, 2026
