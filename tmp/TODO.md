.14 Redis — Implementation Plan
Overview
Introduce Redis as a shared state layer to enable: (1) JWKS caching to reduce Kinde API load and cold-start auth latency, (2) an async scan queue to decouple API from ingestion and enable horizontal scaling, and (3) a distributed rate limiter that survives API restarts. Every Redis-dependent path must degrade gracefully to in-memory behavior when REDIS_URL is unset, so local dev and lightweight deployments keep working without Redis.

Step 1 — Infrastructure & Dependencies
File: docker-compose.yml

Add redis service: image: redis:7-alpine, port 6379:6379, persistent volume redis_data:/data, healthcheck redis-cli ping
Make api and ingestion services depend on it (depends_on: { redis: { condition: service_healthy } })

Files: services/api/go.mod, services/ingestion/go.mod, services/shared/go.mod

Add github.com/redis/go-redis/v9 to shared (since Cache lives there)

File: .env.example

Add REDIS_URL=redis://localhost:6379 (commented out by default so absence is the tested fallback path)


Step 2 — Cache Abstraction (Shared Module)
File: services/shared/cache/cache.go (new)
gotype Cache interface {
Get(ctx context.Context, key string) ([]byte, bool, error)
Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
Del(ctx context.Context, key string) error
// For rate limiter + counters
Incr(ctx context.Context, key string, ttl time.Duration) (int64, error)
Close() error
}

Define sentinel ErrNotFound (though bool return covers the hit/miss case)
Factory: New(redisURL string) (Cache, error) — returns Redis impl if URL set, in-memory otherwise
Log which backend was selected at startup (observability matters here)

File: services/shared/cache/redis/redis.go (new)

Wraps redis.Client
Incr uses INCR + EXPIRE in a pipeline (only set TTL on first increment — check return of INCR == 1)
Wrap all calls with observability.NewDatabaseObserver("REDIS_GET") etc. so Prometheus metrics cover Redis too

File: services/shared/cache/memory/memory.go (new)

sync.Map keyed by string, stores {value []byte, expiresAt time.Time}
Background goroutine sweeps expired keys every 60s
Incr uses sync.Mutex per key — adequate for single-process fallback

File: services/shared/cache/cache_test.go (new)

Table-driven tests that run the same suite against both implementations (Redis tests skip when TEST_REDIS_URL unset)
Must cover: miss, hit, TTL expiry, Incr sequence, Del


Step 3 — JWKS Cache in Auth Middleware
File: services/api/internal/middleware/auth.go

Constructor change: NewAuth(issuer, devMode, devTenantID string, cache cache.Cache) — inject cache
Cache key: jwks:{issuer} — TTL 1h
On cache miss: fetch from .well-known/jwks.json, Set with 1h TTL
On Redis error: log warn + fall through to in-process fetch (never fail auth because of cache)
Existing in-memory JWKS map inside auth middleware stays as a second-tier fallback for "Redis down" case

File: services/api/internal/middleware/auth_test.go

Add test: cache hit short-circuits HTTP call (mock cache returns pre-populated JWKS)
Add test: cache miss fetches, then second call hits cache (verify HTTP client called once)
Add test: cache error falls through gracefully


Step 4 — Redis-Backed Rate Limiter
File: services/api/internal/middleware/ratelimit.go

Replace sync.Map state with Redis INCR + EXPIRE pattern
Key format: ratelimit:{tenant_id}:{minute_bucket} where bucket is time.Now().Unix() / 60
TTL: 120s (covers the current minute plus the next bucket boundary)
Returns 429 with Retry-After header when count > 60
When REDIS_URL is unset → use in-memory fallback (Cache factory handles this transparently)
Keep DEV_MODE=true no-op behavior

File: services/api/internal/middleware/ratelimit_test.go

Update existing tests to pass a mock cache
Add test: state survives simulated "restart" (new middleware instance sees the same cache)


Step 5 — Scan Queue (Async Ingestion)
This is the largest change — currently the API fires a goroutine that POSTs to ingestion. Moving to a queue makes ingestion horizontally scalable and crash-resilient.
File: services/shared/queue/queue.go (new)

Queue interface: Enqueue(ctx, job ScanJob) error, Dequeue(ctx) (ScanJob, error) (blocking)
ScanJob struct: TenantID, AccountID, EnqueuedAt, RequestID
Factory: New(redisURL string) (Queue, error) — returns Redis impl or synchronous fallback

File: services/shared/queue/redis/redis.go (new)

LPUSH axiaops:scan_queue to enqueue (JSON-encoded job)
BRPOP axiaops:scan_queue 0 to dequeue (blocks until job available)
JSON encode/decode with explicit schema version for future migrations

File: services/shared/queue/sync/sync.go (new)

Synchronous fallback: Enqueue calls the ingestion HTTP endpoint directly (current behavior)
Allows dev without Redis

File: services/api/internal/api/handler.go (modify triggerScan)

Replace go postToIngestion(...) with queue.Enqueue(ctx, ScanJob{...})
Still sets account status to scanning synchronously (UX feedback)
Scan lock (sync.Mutex map) stays — prevents enqueuing duplicates

File: services/ingestion/cmd/worker.go (new)

Runs alongside HTTP server in main.go — starts a worker goroutine
Loop: job := queue.Dequeue(ctx) → execute scan → continue
Reuses existing handleScan logic (refactor it out of the HTTP handler into a pure function runScan(ctx, accountID, tenantID))
Respects graceful shutdown: current job finishes, no new dequeue after SIGTERM
When REDIS_URL unset → worker is a no-op (HTTP POST /scan handler remains the entry point)

File: services/ingestion/cmd/main.go

Start worker if REDIS_URL is set; log worker.started or worker.skipped_no_redis

File: services/ingestion/internal/scan/scan_test.go (new or moved)

Refactor existing tests to target runScan directly — no HTTP required


Step 6 — Wiring & Startup
File: services/api/cmd/main.go

cache := cache.New(os.Getenv("REDIS_URL")) — log backend chosen
queue := queue.New(os.Getenv("REDIS_URL"))
Pass cache to auth middleware and rate limiter
Pass queue to handler constructor
Add defer cache.Close() and defer queue.Close() in graceful shutdown path

File: services/ingestion/cmd/main.go

Same cache/queue setup
Start worker loop if queue is Redis-backed

File: services/shared/CLAUDE.md

Document the new cache/ and queue/ packages and their interfaces


Step 7 — Observability
File: services/shared/observability/metrics.go

Add axiaops_cache_operations_total{op, backend, status} counter
Add axiaops_cache_operation_duration_seconds{op, backend} histogram
Add axiaops_scan_queue_depth gauge (sampled by a ticker in ingestion — LLEN axiaops:scan_queue)
Add axiaops_scan_queue_wait_seconds histogram (job EnqueuedAt → dequeue time)

Logging

Log at startup: chosen cache backend, chosen queue backend
Log on every job dequeue: scan.dequeued with wait duration


Step 8 — Tests
Unit tests (added per-step above):

Cache interface — both implementations pass same suite
Queue interface — both implementations pass same suite
Auth middleware — JWKS caching behavior
Rate limiter — survives restart, correct 429 behavior

Integration / smoke tests:

test/smoke/redis_test.go (new) — when SMOKE_API_URL and SMOKE_REDIS_URL are both set:

Hit /v1/accounts/{id}/scan → verify job appears in Redis queue → verify worker picks it up → verify last_scanned_at updated
Hit /v1/ghosts 61 times within 1 minute → verify 429 on request 61
Restart API container between requests 30 and 31 → verify counter preserved (tests Redis persistence of rate limit)



Manual verification checklist:

make start-dev with REDIS_URL unset — full stack works exactly as today (regression check)
make start-dev with Redis — docker logs api shows cache.backend=redis, queue.backend=redis
redis-cli MONITOR during a scan shows LPUSH + BRPOP


Step 9 — Documentation

Update CLAUDE.md (root) — note Redis added to architecture section
Update services/api/CLAUDE.md — new env var, auth middleware signature change
Update services/ingestion/CLAUDE.md — worker pattern, REDIS_URL behavior
Update services/shared/CLAUDE.md — cache/ and queue/ packages
Update docs/TASKS.md — check off 2.14 items as each lands


Suggested PR Breakdown
Keeping PRs small makes review easier and lets you ship incrementally:

PR 1 — cache.Cache interface + Redis + memory implementations + tests (no callers yet)
PR 2 — JWKS caching in auth middleware
PR 3 — Redis-backed rate limiter
PR 4 — queue.Queue interface + implementations + refactor runScan out of HTTP handler
PR 5 — Wire queue into API and worker loop into ingestion; deprecate direct HTTP call path (keep fallback)
PR 6 — Observability metrics + documentation updates

---

## Merge Order & Branch Chain

All branches are chained — each one is based on the previous. Merge in strict order:

| # | Branch | Based on | Status | Merge into |
|---|--------|----------|--------|------------|
| 1 | `feature/2.14-redis-cache-interface` | `develop` | ✅ Ready | `develop` |
| 2 | `feature/2.14-jwks-cache` | PR1 branch | ✅ Ready | after PR1 merges |
| 3 | `feature/2.14-redis-ratelimit` | PR2 branch | ✅ Ready | after PR2 merges |
| 4 | `feature/2.14-scan-queue` | PR3 branch | ✅ Ready | after PR3 merges |
| 5 | `feature/2.14-wire-queue` | PR4 branch | ✅ Ready | after PR4 merges |
| 6 | `feature/2.14-observability-docs` | PR5 branch | ✅ Ready | after PR5 merges |

### Merge procedure (repeat for each PR in order)

```bash
# Example: merging PR1
git checkout develop
git merge --no-ff feature/2.14-redis-cache-interface
git push origin develop

# Then rebase the next branch onto develop
git checkout feature/2.14-jwks-cache
git rebase develop
git push --force-with-lease origin feature/2.14-jwks-cache
```

### What each PR changes

**PR1** `feature/2.14-redis-cache-interface`
- `shared/cache/` — Cache interface, Redis impl, memory impl, tests
- `shared/go.mod` — adds go-redis/v9
- `.gitlab-ci.yml` — test:redis job + feature/* branch rules

**PR2** `feature/2.14-jwks-cache`
- `api/internal/middleware/auth.go` — NewAuth accepts cache.Cache; JWKS cached 1h
- `api/cmd/main.go` — cache.New(REDIS_URL) init

**PR3** `feature/2.14-redis-ratelimit`
- `api/internal/middleware/ratelimit.go` — INCR fixed-window counter replaces token bucket
- `api/cmd/main.go` — NewRateLimiter(cache) wired

**PR4** `feature/2.14-scan-queue`
- `shared/queue/` — Queue interface, Redis impl (LPUSH/BRPOP), sync HTTP fallback, tests
- `.gitlab-ci.yml` — test:redis runs ./cache ./queue

**PR5** `feature/2.14-wire-queue`
- `api/internal/api/handler.go` — scanAccount enqueues job
- `api/cmd/main.go` — queue.New wired
- `ingestion/cmd/worker.go` — worker goroutine
- `ingestion/cmd/main.go` — runScan refactor, queue wired, scheduler uses queue

**PR6** `feature/2.14-observability-docs`
- `shared/observability/metrics.go` — cache metrics added
- `shared/CLAUDE.md` — cache/ and queue/ documented
- `tmp/TODO.md` — this merge plan


Risks & Mitigations
Risk: Redis outage breaks auth or scans.
Mitigation: Every Redis call has an in-process fallback. Cache miss on JWKS → fetch live. Queue unavailable → log error and reject with 503 (don't silently drop scan requests).
Risk: Queue and HTTP-scan codepaths diverge over time.
Mitigation: Refactor scan logic into runScan(ctx, accountID, tenantID) — both paths call the same function.
Risk: Rate limit bucket boundary creates brief burst tolerance (user gets 60 in bucket N, 60 more in bucket N+1 within a few seconds).
Mitigation: Acceptable for MVP. True sliding window (sorted sets) can land in a follow-up if abuse becomes an issue.
Risk: Worker crashes mid-scan → job lost.
Mitigation: Out of scope for this PR. Document as known limitation; revisit with Redis Streams + consumer groups in a later phase if needed.

Estimated effort: 4–6 days with AI assistance, assuming no surprises in auth middleware refactor. The queue/worker piece is the biggest chunk; everything else is fairly mechanical.
Want me to start on PR 1 (cache interface + implementations)?Add CIThought processThought processAddendum: CI Integration for 2.14 Redis
Adding a new Step 10 to the plan to cover GitLab CI updates.

Step 10 — CI Pipeline Updates
The existing .gitlab-ci.yml (from 2.11) runs go test ./... and golangci-lint on every push. Redis introduces two new CI concerns: (1) tests that need a running Redis, and (2) the new worker binary needs to be built and deployed alongside ingestion.
10.1 Add Redis Service to Test Jobs
File: .gitlab-ci.yml
Add Redis as a job-level service for integration tests:
yamltest:integration:
stage: test
image: golang:1.25
services:
- name: postgres:16
alias: postgres
- name: redis:7-alpine
alias: redis
variables:
POSTGRES_DB: axiaops_test
POSTGRES_USER: axiaops_owner
POSTGRES_PASSWORD: testpass
TEST_REDIS_URL: redis://redis:6379
MIGRATION_DATABASE_URL: postgres://axiaops_owner:testpass@postgres:5432/axiaops_test?sslmode=disable
DATABASE_URL: postgres://axiaops:testpass@postgres:5432/axiaops_test?sslmode=disable
script:
- make test-all
only:
- merge_requests
- main
Unit tests (Cache interface, Queue interface) automatically skip the Redis backend when TEST_REDIS_URL is unset — so the fast test:unit job stays Redis-free.
10.2 Split Test Stages
Reorganize into three test jobs for faster feedback:
yamltest:unit:
stage: test
script: make test          # No services needed — runs in ~30s

test:postgres:
stage: test
services: [postgres:16]
script: make test-postgres

test:cache-queue:
stage: test
services: [redis:7-alpine]
variables:
TEST_REDIS_URL: redis://redis:6379
script: cd services/shared && go test ./cache/... ./queue/... -count=1
These run in parallel — total CI time should stay under 3 minutes.
10.3 Smoke Tests with Redis
File: .gitlab-ci.yml
Add a new smoke test job that spins up the full stack (including Redis) on main:
yamlsmoke:
stage: test
image: docker/compose:latest
services: [docker:dind]
script:
- docker-compose up -d
- docker-compose exec -T api wait-for-it redis:6379 -- echo "redis up"
- SMOKE_API_URL=http://localhost:8080 make test-smoke
after_script:
- docker-compose logs api ingestion
- docker-compose down
only:
- main
This catches regressions where the Redis-backed code path behaves differently from the fallback — exactly the kind of bug that's easy to miss locally.
10.4 Update Lint Config
File: .golangci.yml
Ensure new packages are covered:
yamlrun:
modules-download-mode: readonly
build-tags:
- integration
linters:
enable:
- errcheck
- govet
- staticcheck
- gosec           # Extra scrutiny on Redis — don't log connection URLs with passwords
- revive
Add an exclusion for services/shared/cache/memory so the sync.Map usage doesn't trip gocritic.
10.5 Build Stage — Worker Binary
File: .gitlab-ci.yml (build stage)
The ingestion service now bundles both the HTTP server and the worker in the same binary (cmd/main.go orchestrates both). No separate Docker image needed — but verify the build includes worker code:
yamlbuild:ingestion:
stage: build
script:
- docker build -t $ECR_REGISTRY/axiaops-ingestion:$CI_COMMIT_SHA services/ingestion
- docker run --rm $ECR_REGISTRY/axiaops-ingestion:$CI_COMMIT_SHA /app/ingestion --help | grep -q "worker"
- docker push $ECR_REGISTRY/axiaops-ingestion:$CI_COMMIT_SHA
only: [main]
The grep check is a belt-and-suspenders regression guard — fails fast if someone accidentally strips the worker code.
10.6 Deploy Stage — ElastiCache Placeholder
ElastiCache provisioning is part of 2.16 Production Deployment (August 2026), not this PR. For now:

Add REDIS_URL as a CI/CD variable in GitLab (masked, protected — pointing at staging Redis once it exists)
Update aws apprunner update-service commands to include REDIS_URL in the environment block
Document in docs/deployment.md: "Redis is optional in staging; production cutover happens in milestone 2.16"

10.7 Required GitLab CI/CD Variables
Add to GitLab project settings (Settings → CI/CD → Variables):
VariableScopeProtectionNotesREDIS_URLstaging, productionmasked, protectedPoints at ElastiCache endpointTEST_REDIS_URLtest—Set at job level, not globally

Updated PR Breakdown
Insert between PR 1 and PR 2:
PR 1.5 — CI updates: split test stages, add Redis service job, update .gitlab-ci.yml and .golangci.yml
This way cache tests start running in CI the moment PR 1 merges, before any production code depends on Redis.

Verification Checklist
Before merging the CI changes:

Open a draft MR → verify all three test jobs (test:unit, test:postgres, test:cache-queue) run and pass
Push a commit that deliberately breaks a Redis test → verify test:cache-queue catches it while test:unit still passes
Verify total pipeline time stays under 3 minutes on a cache-warm run
Confirm no masked variable leaks in job logs (especially REDIS_URL if it contains credentials)

Want me to start on PR 1 (cache interface) or PR 1.5 (CI updates) first? Doing CI first means every subsequent PR gets automatic Redis test coverage.Create branch start implementation the the  PR1 , update ci if needed to run on feature branches