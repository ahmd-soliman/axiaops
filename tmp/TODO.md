# 2.14 Redis — Implementation

## Overview

Introduce Redis as a shared state layer for:
1. **JWKS caching** — reduce Kinde API load and cold-start auth latency
2. **Async scan queue** — decouple API from ingestion, enable horizontal scaling
3. **Distributed rate limiter** — survives API restarts

Every Redis-dependent path degrades gracefully to in-memory behaviour when `REDIS_URL` is unset.

---

## PR Merge Order

All branches are chained. Merge strictly in order.

| # | Branch | Status | Merge into |
|---|--------|--------|------------|
| 1 | `feature/2.14-redis-cache-interface` | ✅ Ready | `develop` |
| 2 | `feature/2.14-jwks-cache` | ✅ Ready | after PR1 |
| 3 | `feature/2.14-redis-ratelimit` | ✅ Ready | after PR2 |
| 4 | `feature/2.14-scan-queue` | ✅ Ready | after PR3 |
| 5 | `feature/2.14-wire-queue` | ✅ Ready | after PR4 |
| 6 | `feature/2.14-observability-docs` | ✅ Ready | after PR5 |

### Merge procedure

```bash
# Merge current PR
git checkout develop
git merge --no-ff feature/2.14-<name>
git push origin develop

# Rebase next branch onto develop
git checkout feature/2.14-<next>
git rebase develop
git push --force-with-lease origin feature/2.14-<next>
```

---

## What Each PR Changes

### PR1 — `feature/2.14-redis-cache-interface`
`cache.Cache` interface + Redis + memory implementations + tests

- `shared/cache/cache.go` — `Cache` interface, `ErrNotFound`, `New(redisURL)` factory
- `shared/cache/redis/redis.go` — Redis backend (`go-redis/v9`), `PExpire` for ms TTL, observability instrumented
- `shared/cache/memory/memory.go` — in-memory fallback, 60s sweep goroutine, per-key mutex for `Incr`
- `shared/cache/cache_test.go` — shared suite (miss, hit, TTL expiry, Incr, Del); Redis skipped without `TEST_REDIS_URL`
- `shared/go.mod` — adds `go-redis/v9`
- `.gitlab-ci.yml` — `test:redis` job with Redis container; `feature/*` branch rules on all test jobs

### PR2 — `feature/2.14-jwks-cache`
JWKS caching in auth middleware

- `api/internal/middleware/auth.go` — `NewAuth` accepts `cache.Cache`; JWKS cached under `jwks:{issuer}` for 1h; cache error → live fetch fallback
- `api/cmd/main.go` — `cache.New(REDIS_URL)` initialised and passed to `NewAuth`
- `api/internal/middleware/auth_test.go` — cache hit skips HTTP fetch; cache error falls back gracefully

### PR3 — `feature/2.14-redis-ratelimit`
Redis-backed rate limiter

- `api/internal/middleware/ratelimit.go` — INCR fixed-window counter (60 req/min); key `ratelimit:{tenant}:{unix/60}`; TTL 2 min; cache error → fail-open
- `api/cmd/main.go` — `NewRateLimiter(cache)` wired; `CleanupStaleBuckets` goroutine removed
- `api/internal/middleware/ratelimit_test.go` — limit enforcement, isolation, 429, bypass, fail-open, survives restart

### PR4 — `feature/2.14-scan-queue`
`queue.Queue` interface + implementations

- `shared/queue/queue.go` — `Queue` interface, `ScanJob` struct, `New(redisURL, ingestionURL)` factory, adapters
- `shared/queue/redis/redis.go` — LPUSH enqueue, BRPOP dequeue (blocking), JSON encoding
- `shared/queue/sync/sync.go` — sync fallback: `Enqueue` POSTs to `/scan`; `Dequeue` blocks on ctx cancel
- `shared/queue/queue_test.go` — Redis round-trip (skipped without `TEST_REDIS_URL`); sync Dequeue respects ctx cancel
- `.gitlab-ci.yml` — `test:redis` runs `./cache ./queue`

### PR5 — `feature/2.14-wire-queue`
Wire queue into API + worker loop in ingestion

- `api/internal/api/handler.go` — `scanAccount` enqueues `ScanJob` instead of goroutine POST
- `api/cmd/main.go` — `queue.New(REDIS_URL, INGESTION_URL)` wired
- `ingestion/cmd/worker.go` — `startWorker` goroutine: dequeue → `runScan` → update status; stops on ctx cancel
- `ingestion/cmd/main.go` — `runScan(ctx, store, accountID)` refactored (loads credentials from DB); queue wired; scheduler uses queue; `triggerScan` HTTP self-call removed
- `ingestion/cmd/scheduler_test.go` — rewritten with `captureQueue`; asserts job count not HTTP calls

### PR6 — `feature/2.14-observability-docs`
Metrics + documentation

- `shared/observability/metrics.go` — `axiaops_cache_operations_total` counter + `axiaops_cache_operation_duration_seconds` histogram
- `shared/CLAUDE.md` — `cache/` and `queue/` packages documented
- `tmp/TODO.md` — this file

---

## Remaining Work (not in these PRs)

- `docker-compose.yml` — add `redis:7-alpine` service with healthcheck
- `services/ingestion/.env.example` — add `REDIS_URL=` (commented)
- Smoke tests with Redis (`SMOKE_REDIS_URL`)
- ElastiCache provisioning (milestone 2.16 — production deployment)
- GitLab CI/CD variable: `REDIS_URL` (masked, protected) for staging/production

---

## Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| Redis outage breaks auth | Cache miss → live JWKS fetch; never fails auth |
| Redis outage breaks rate limiter | Cache error → fail-open (allow request, log warn) |
| Worker crashes mid-scan → job lost | Known limitation; revisit with Redis Streams in a later phase |
| Rate limit bucket boundary burst | Acceptable for MVP; sliding window (sorted sets) as follow-up if needed |
