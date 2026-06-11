# Phase 2 Completion Plan — Scheduled Auto-Scan, Redis, Dismiss/Snooze

> **Source:** extends `Tasks.md` sections 2.12, 2.14, 3.2 — pulling Dismiss/Snooze
> forward from Phase 3 because it unblocks paying customers, and reconciling a few
> checkmarks in 2.12 that no longer reflect the repo state.
>
> **Author:** Ahmed · **Date:** 2026-04-18 · **Scope:** three tracks, deliverable end-to-end.
>
> **Status (2026-06):** ✅ completed — all three tracks shipped (see CLAUDE.md Current Status).

---

## Context

Running the plan against the actual code at HEAD reveals that two of the three tracks
already have skeletons in place:

- **Scheduled auto-scan (Track A)** — the core loop, per-account interval column,
  PATCH API, and dashboard config already exist. The real gaps are durable per-run
  history, retry/DLQ, and extending the existing stuck-scan reset into a richer
  failure model.
- **Redis (Track B)** — Redis is already in `docker-compose.yml`, the queue uses it
  (`services/shared/queue/redis`), and the in-memory rate limiter has been replaced
  by a Redis cache-backed one. The gaps are production parity, local hardening, and
  a handful of high-leverage new use cases (idempotency, summary cache, DLQ stream).
- **Dismiss/snooze (Track C)** — genuinely greenfield. No schema, API, UI, or audit
  trail exists. Pulled from Phase 3 because it's the last user-facing gap blocking
  early paying customers.

The plan treats each track as independently shippable and orders them so you can
cut a release after any of the three.

---

## Track A — Scheduled Auto-Scan Hardening

### A.0 What already exists (don't rebuild)

| Item | Location | Status |
|---|---|---|
| `accounts.scan_interval_hours` column | `services/shared/storage/postgres/migrations/001_initial.up.sql:98` | Shipped |
| `PATCH /v1/accounts/{id}` accepting `scan_interval_hours` | `services/api/internal/api/handler.go:271-320` | Shipped, tested |
| Ticker + `scanScheduledAccounts` | `services/ingestion/cmd/main.go:169-188, 468-502` | Shipped |
| Stuck-scan reset (15 min timeout) | `services/api/cmd/main.go:25, 84, 189` via `postgres.ResetStuckScans` | Shipped |
| Dashboard UI for schedule config | `services/dashboard/src/screens/AccountSettingsScreen.js` | Shipped |
| Per-scan metrics (`axiaops_scan_duration_seconds`, `axiaops_ghosts_detected_total`) | ingestion service `/metrics` | Shipped |

Treat these as the baseline. The action items below fill in the missing half.

### A.1 Per-run scan history (`scan_runs` table)

**Why:** Today the only record of a scan run is `accounts.last_scanned_at` (a single
timestamp) and `ghost_snapshots` (aggregate counts). We have no record of runs that
failed, how long they took, or what error they hit — which means the user can't
answer "why didn't my scan run last night?" from the dashboard, and we can't build
retry logic without it.

**Schema (new migration `006_add_scan_runs.up.sql`):**

```sql
CREATE TABLE IF NOT EXISTS scan_runs (
    id                 BIGSERIAL   PRIMARY KEY,
    organization_id          TEXT        NOT NULL REFERENCES organizations(id),
    account_id         TEXT        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    trigger            TEXT        NOT NULL,  -- 'manual' | 'scheduled' | 'retry'
    status             TEXT        NOT NULL,  -- 'queued' | 'running' | 'succeeded' | 'failed' | 'timed_out' | 'dead_lettered'
    started_at         TIMESTAMPTZ,
    finished_at        TIMESTAMPTZ,
    duration_ms        INTEGER,
    ghost_count        INTEGER,
    total_monthly_cost NUMERIC,
    currency           TEXT,
    error_class        TEXT,       -- e.g. 'aws.throttled', 'aws.unauthorized', 'db.timeout'
    error_message      TEXT,       -- one-line, pii-free
    attempt            INTEGER     NOT NULL DEFAULT 1,
    idempotency_key    TEXT,       -- ties together retries of a logically-single scan
    enqueued_at        TIMESTAMPTZ NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_scan_runs_account_started ON scan_runs (organization_id, account_id, started_at DESC);
CREATE INDEX idx_scan_runs_status          ON scan_runs (status) WHERE status IN ('queued','running');

ALTER TABLE scan_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY scan_runs_organization_isolation ON scan_runs
    USING (organization_id = current_setting('app.organization_id', true));

GRANT SELECT, INSERT, UPDATE ON scan_runs TO axiaops;
GRANT USAGE, SELECT ON SEQUENCE scan_runs_id_seq TO axiaops;
```

**Code changes:**

- `services/shared/model/scan_run.go` — new `ScanRun` struct.
- `services/shared/storage/storage.go` — extend `Store` with:
  - `CreateScanRun(ctx, run) (int64, error)`
  - `UpdateScanRun(ctx, id, patch) error` (for status transitions)
  - `ListScanRuns(ctx, accountID, limit int) ([]ScanRun, error)`
  - `CountActiveScanRuns(ctx, accountID) (int, error)` — used by Track A.2 retry backoff.
- `services/shared/storage/postgres/postgres.go` — implement the three methods; wrap
  with `storage.WithOrganizationID` context propagation.
- `services/ingestion/cmd/worker.go` and `cmd/main.go` (`runScan`) —
  - On dequeue: `CreateScanRun(status=running, started_at=now, trigger=job.Trigger, attempt=job.Attempt)`.
  - On success: `UpdateScanRun(status=succeeded, finished_at, duration_ms, ghost_count, total_monthly_cost)`.
  - On error: `UpdateScanRun(status=failed, finished_at, error_class, error_message)`.
- `services/shared/queue/queue.go` — extend `ScanJob` with `Trigger string`, `Attempt int`, `IdempotencyKey string`, `ScanRunID int64`.
- `services/api/internal/api/handler.go` — new `GET /v1/accounts/{id}/scans?limit=20`
  returning the last N runs. Register in `Handler.Register` near line 49.

**Observability:**

- Emit `scan.run.created`, `scan.run.succeeded`, `scan.run.failed`, `scan.run.dead_lettered`
  slog events with the same key set used today (`organization_id`, `account_id`,
  `duration_ms`, `ghost_count`, `attempt`, `error_class`).
- Add Prometheus counters:
  - `axiaops_scan_runs_total{trigger, status}`
  - `axiaops_scan_run_attempts{outcome}` (for retry efficacy)
- Extend `/v1/summary` response with `scans_last_24h`, `scan_success_rate_7d`
  (small payload addition, consumed by a new dashboard "Scans" card).

**Dashboard:**

- New panel on `AccountSettingsScreen` (or a sibling `AccountScansScreen`): list of
  recent runs with status pill, duration, ghost count delta, and "view error"
  disclosure.
- Service-pill header gains a small "Next scan in ~4h" hint driven off
  `last_scanned_at + scan_interval_hours`.

**Tests:**

- Postgres test: insert three runs for one account, verify `ListScanRuns` returns them
  descending and RLS blocks cross-organization reads.
- Handler test: seed runs, hit `GET /v1/accounts/{id}/scans?limit=2`, assert order
  and shape.
- Integration test: call `POST /v1/accounts/{id}/scan`, poll `GET /v1/accounts/{id}/scans`
  until a `succeeded` row appears, assert `duration_ms > 0`.

### A.2 Retry + dead-letter queue

**Why:** The worker's circuit breaker at `services/ingestion/cmd/worker.go` drops
failed jobs after the breaker opens. AWS throttling and transient DB errors need a
retry path; unrecoverable errors (bad credentials, deleted account) need a dead
letter so they don't loop forever.

**Design:**

- Classify errors at the ingestion worker boundary via a new
  `services/shared/queue/errclass.go`:
  - `retryable`: `RequestLimitExceeded`, `Throttling`, `ServiceUnavailable`,
    `context.DeadlineExceeded` on transient DB ops.
  - `permanent`: `UnrecognizedClientException`, `InvalidClientTokenId`,
    `AccessDenied` on account's own credentials, `NoSuchEntity` for a deleted account.
- Retry policy: exponential backoff with jitter, cap at 3 attempts over ~8 minutes
  (30s → 2m → 5m). On permanent error skip straight to dead letter.
- Requeue via Redis: after a failure, `Enqueue` a delayed job. Redis doesn't have
  native delayed queues, so use `ZADD axiaops:scan_queue:delayed score=ready_at` and
  a 10-second ticker that moves ready jobs from the sorted set to the main list
  (`ZRANGEBYSCORE … LIMIT`, `RPOPLPUSH`-style, all in one Lua script to avoid races).
  Implementation goes in `services/shared/queue/redis/redis.go` as `EnqueueDelayed`.
- Dead letter: on final failure or permanent error, push to
  `LPUSH axiaops:scan_queue:dead <json>` and mark `scan_runs.status='dead_lettered'`.
  No auto-replay — surfaced via a new admin endpoint (Track A has no admin UI yet;
  file a follow-up or just read it via `redis-cli` for now).

**Code changes:**

- `services/shared/queue/queue.go` — add `EnqueueDelayed(ctx, job, delay)`,
  `DeadLetter(ctx, job, reason)`.
- `services/shared/queue/redis/redis.go` — Lua script for delayed → main transfer;
  a background goroutine in the worker binary runs it every 10s.
- `services/ingestion/cmd/worker.go` — wrap `runScan` call:
  ```go
  err := runScan(ctx, store, job)
  if err == nil { return }
  class := errclass.Classify(err)
  if class == errclass.Permanent || job.Attempt >= maxAttempts {
      q.DeadLetter(ctx, job, class.String())
      store.UpdateScanRun(ctx, job.ScanRunID, scan.Failed(err, deadLettered))
      return
  }
  delay := backoff(job.Attempt)
  job.Attempt++
  q.EnqueueDelayed(ctx, job, delay)
  ```
- Keep the existing circuit breaker as a coarse safety net (trips worker for 1 min
  if >50% of last 20 scans failed) but don't let it swallow jobs silently — trips
  should also log `scan.worker.circuit_opened` and pause dequeue, not drop jobs.

**Tests:**

- Unit test `errclass.Classify` against each expected AWS error type.
- Queue test with a fake clock: enqueue delayed, advance clock, assert job appears
  in main queue.
- Integration test: stub AWS provider to fail twice then succeed; assert three
  `scan_runs` rows, final status `succeeded`, `attempt=3`.
- Integration test: stub AWS provider to return `InvalidClientTokenId`; assert one
  row with `status='dead_lettered'`, no retries.

### A.3 Stuck-scan recovery — extension

**Why:** `postgres.ResetStuckScans` already flips `accounts.status` back from
`scanning` after 15 min. Gap: it doesn't update a `scan_runs` row (because that
table doesn't exist yet), and the timeout is hard-coded.

**Changes:**

- Add env var `STUCK_SCAN_TIMEOUT` (default 15m) consumed at
  `services/api/cmd/main.go:25` in place of the hard-coded constant.
- Extend `ResetStuckScans` to also `UPDATE scan_runs SET status='timed_out',
  finished_at=now(), error_class='stuck_scan' WHERE status='running' AND
  started_at < now() - $timeout`.
- Feed the stuck run back into the retry pipeline via `EnqueueDelayed` with
  `trigger='retry'`, up to `maxAttempts`.

---

## Track B — Redis Hardening

> **Note (2026-05-27):** the cache engine migrated to Valkey across all envs
> via `chore/valkey-migration`. Snippets below were written pre-migration and
> reference `redis:7-alpine` / `redis-cli` / `redis-server`; the current
> equivalents are `valkey/valkey:8.1-alpine` / `valkey-cli` / `valkey-server`.
> Wire-protocol surfaces (`REDIS_URL`, `redis://` URL scheme, the `redis`
> compose service name used as hostname in `redis://redis:6379`, the
> `/readyz` `"redis"` key) intentionally stay — Valkey speaks RESP and the
> migration is a packaging change, not a behavior change.

### B.0 Current state

- `docker-compose.yml:3-23` has `valkey/valkey:8.1-alpine` (was `redis:7-alpine`) with a `valkey-cli ping` healthcheck.
- `REDIS_URL: redis://redis:6379` is threaded into both api and ingestion services
  (`docker-compose.yml:70, 128`). The compose service is still named `redis:` so the URL host resolves; the container is `axiaops-valkey`.
- `services/shared/cache/{cache.go,redis/redis.go,memory/memory.go}` provide a
  unified cache interface with in-memory fallback.
- `services/shared/queue/{queue.go,redis/redis.go,sync/sync.go}` same pattern for
  the scan queue.
- Rate limiter at `services/api/internal/middleware/ratelimit.go` uses `cache.Incr`.
- **Missing:** no persistence, port not exposed for debugging, no production
  deployment story, no use of Redis for idempotency / result cache / DLQ.

### B.1 Local hardening

**docker-compose.yml changes (in place of lines 3-10):**

```yaml
  redis:
    image: valkey/valkey:8.1-alpine
    container_name: axiaops-valkey
    command: >
      valkey-server
      --appendonly yes
      --appendfsync everysec
      --maxmemory 256mb
      --maxmemory-policy allkeys-lru
    ports:
      - "6379:6379"           # expose for redis-cli / RedisInsight during dev
    volumes:
      - ./redis_data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5
```

Add `redis_data/` to `.gitignore` alongside the existing `pg_data/` entry. This
gives us AOF persistence so the queue survives container restarts during dev — the
source of several "lost scan" bugs in the past.

Optional convenience service for dev-only (behind a docker-compose profile):

```yaml
  redisinsight:
    profiles: ["debug"]
    image: redis/redisinsight:latest
    ports: ["5540:5540"]
```

Start with `docker compose --profile debug up` when you need a GUI.

### B.2 Prod/staging parity

**Decision gate:** pick one of these three, then fill in the Terraform module
referenced in 2.16:

| Option | Monthly cost (eu-central-1, steady ~10 req/s) | Ops burden | Notes |
|---|---|---|---|
| ElastiCache Serverless | ~€9–13 (1 GB min) | Low | In VPC; private-subnet only — reachable from the ECS Express task's ENI. Native integration with Secrets Manager. |
| Upstash Redis (pay-per-request) | ~€0–3 at this traffic, €20–30 at 100 req/s | Lowest | External SaaS, public endpoint with TLS. Zero VPC plumbing. Per-request pricing is friendly to bursty workloads. |
| Self-hosted on Fargate Spot | ~€5 | Highest | Single AZ, no HA, you own backups. Only worth it if cost is critical and traffic is very low. |

> **Status note:** the prod-design decision is "no ElastiCache in prod v1" —
> `REDIS_URL` is intentionally unset on the production ECS task definitions
> (`aws-infra` / `docs/production.md` §Cache). The comparison above is
> preserved for when budget allows turning Redis on; until then it is not
> load-bearing for prod.

**Recommended (when Redis turns on):** Upstash for the first cycle. It avoids
the ElastiCache 1 GB floor and the VPC plumbing the ECS Express tasks
otherwise have to peer into. Swap to ElastiCache Serverless when sustained
traffic makes the per-request model more expensive than the ElastiCache floor.

**Changes:**

- Add Terraform module under `terraform/modules/redis/` (aligned with 2.16). For
  Upstash, use the `upstash/upstash` provider; for ElastiCache, the AWS provider.
- Store the resulting `REDIS_URL` (with TLS for Upstash — `rediss://`) in AWS
  Secrets Manager, referenced from the ECS Express task definition's secrets.
- `services/shared/cache/redis/redis.go` and `services/shared/queue/redis/redis.go`
  already accept full Redis URLs via `redis.ParseURL`, so TLS is automatic.
- Update `.env.example` files (`services/api/.env`, `services/ingestion/.env`) with
  a commented-out `rediss://` example.

### B.3 Expanded usage

Three high-leverage additions, ordered by ROI:

#### B.3.1 Idempotency keys on `POST /v1/accounts/{id}/scan`

**Why:** Today, a user clicking "Scan" twice enqueues two jobs. Under retries
(Track A.2) this compounds: a flaky network can turn one intended scan into four
queued jobs.

**Design:** Client may send `Idempotency-Key: <uuid>` header. Server does
`cache.SetNX("idem:scan:"+organizationID+":"+key, runID, 5 minutes)`. If another request
arrives with the same key, return `200 OK` with the prior `run_id` instead of
enqueuing again.

**Code changes:**

- `services/api/internal/api/handler.go` `scanAccount` (line 341) — accept header,
  short-circuit with prior run id.
- `services/api/internal/middleware/idempotency.go` — helper for reading the header
  and producing a stable key per `(organization, account, endpoint)`.
- Dashboard `client.js` — generate a UUID per scan-button click and include it.

#### B.3.2 `/v1/summary` and `/v1/trend` cache

**Why:** Both endpoints hit the DB on every dashboard load. For an organization with
300 ghost_records the summary query does a scan + aggregation; cacheable for 30s
with no user-visible lag.

**Design:** Cache-aside with a per-organization key:

```
key:    summary:{organization_id}
value:  <json bytes>
ttl:    30s
```

Invalidate on scan completion (`cache.Del("summary:"+organizationID)` at the end of
`runScan` and after `POST/DELETE /v1/ghosts/{id}/dismiss` from Track C).

**Code changes:**

- `services/api/internal/api/handler.go` — thread `cache.Cache` into `Handler`
  (already available at `services/api/cmd/main.go:102-104`), wrap `getSummary` and
  `getTrend` with cache-aside.
- Emit `cache.hit` / `cache.miss` debug logs behind a `CACHE_DEBUG=true` env var.

#### B.3.3 DLQ as a Redis stream

Covered by Track A.2 but worth restating: one list key `axiaops:scan_queue:dead`
for failed jobs, one sorted set `axiaops:scan_queue:delayed` for scheduled retries.
No new infra — just new keys.

### B.4 Observability

- Add `axiaops_redis_ops_total{op,outcome}` counter and `axiaops_redis_op_duration_seconds`
  histogram in `services/shared/cache/redis/redis.go` via a thin wrapper around the
  `*redis.Client` methods. Same for queue ops.
- `/health` endpoint check: `cache.Ping()` with 1s timeout, surface as
  `{"redis": "ok"}` alongside the existing DB check.

### B.5 Caveat

During clarification you selected "Full" but also initially selected "Already
sufficient, skip this item". That conflict has been resolved in favour of Full
(your follow-up answer). If appetite shrinks, B.1 alone delivers durable local dev
at near-zero cost and ~30 minutes of work; B.2 alone unblocks staging/prod; B.3
can slip to Phase 3 without blocking any customer-facing feature.

---

## Track C — Dismiss / Snooze with Reason Codes

### C.0 Design pressure

`ghost_records` is **replaced wholesale** on every scan (`SaveGhosts` at
`services/shared/storage/postgres/postgres.go:126-185` deletes per-account then
re-inserts). Dismiss state therefore cannot live on the `ghost_records` row itself —
it needs a separate table keyed by a **stable resource fingerprint** so a dismissal
survives the next scan re-detecting the same resource.

### C.1 Schema (new migration `007_add_dismissed_ghosts.up.sql`)

```sql
-- Reason codes as an enum so bad values fail at the DB boundary.
CREATE TYPE dismiss_reason AS ENUM (
    'intentional',          -- production resource kept idle on purpose (DR, warm standby)
    'scheduled_deletion',   -- user has planned removal, don't nag
    'false_positive',       -- detection rule is wrong for this resource
    'cost_accepted',        -- user has accepted the cost (e.g. compliance logs)
    'other'                 -- paired with a required free-text note
);

CREATE TABLE IF NOT EXISTS dismissed_ghosts (
    id              BIGSERIAL       PRIMARY KEY,
    organization_id       TEXT            NOT NULL REFERENCES organizations(id),
    account_id      TEXT            NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    -- Fingerprint: identifies a resource across scans.
    provider        TEXT            NOT NULL,
    service         TEXT            NOT NULL,
    region          TEXT            NOT NULL,
    resource_id     TEXT            NOT NULL,
    -- Action metadata.
    action          TEXT            NOT NULL,  -- 'dismiss' | 'snooze'
    reason          dismiss_reason  NOT NULL,
    note            TEXT            NOT NULL DEFAULT '',
    snooze_until    TIMESTAMPTZ,               -- NULL when action='dismiss'
    dismissed_by    TEXT            NOT NULL REFERENCES users(id),
    dismissed_at    TIMESTAMPTZ     NOT NULL DEFAULT now(),
    revoked_at      TIMESTAMPTZ,               -- soft-undo; row is kept for audit
    revoked_by      TEXT            REFERENCES users(id),
    UNIQUE (organization_id, account_id, provider, service, region, resource_id)
        WHERE revoked_at IS NULL
);

CREATE INDEX idx_dismissed_ghosts_lookup
    ON dismissed_ghosts (organization_id, account_id, resource_id)
    WHERE revoked_at IS NULL;

CREATE INDEX idx_dismissed_ghosts_snooze_expiry
    ON dismissed_ghosts (snooze_until)
    WHERE revoked_at IS NULL AND action='snooze';

ALTER TABLE dismissed_ghosts ENABLE ROW LEVEL SECURITY;
CREATE POLICY dismissed_ghosts_organization_isolation ON dismissed_ghosts
    USING (organization_id = current_setting('app.organization_id', true));

GRANT SELECT, INSERT, UPDATE ON dismissed_ghosts TO axiaops;
GRANT USAGE, SELECT ON SEQUENCE dismissed_ghosts_id_seq TO axiaops;
```

Key design choices:

- **Partial unique index** on the fingerprint `WHERE revoked_at IS NULL` means a
  resource can only have one active dismissal at a time, but history is preserved
  for audit.
- **Soft revoke** via `revoked_at` rather than `DELETE` — gives you a full audit
  trail for free without a separate `audit_log` table (which is still scheduled for
  3.3, so this plan doesn't pre-empt it).
- **Fingerprint** is `(provider, service, region, resource_id)` — matches the
  natural key shape of `ghost_records` and survives re-scans.

### C.2 Storage layer

`services/shared/storage/storage.go` additions:

```go
type DismissAction struct {
    OrganizationID     string
    AccountID    string
    Provider     string
    Service      string
    Region       string
    ResourceID   string
    Action       string            // "dismiss" | "snooze"
    Reason       string            // dismiss_reason enum value
    Note         string
    SnoozeUntil  *time.Time
    DismissedBy  string
}

type Store interface {
    // ... existing ...
    DismissGhost(ctx context.Context, a DismissAction) (id int64, err error)
    RevokeDismissal(ctx context.Context, dismissalID int64, revokedBy string) error
    ListActiveDismissals(ctx context.Context, accountID string) ([]Dismissal, error)
    ListDismissalHistory(ctx context.Context, resourceID string) ([]Dismissal, error)
    ExpireSnoozes(ctx context.Context, now time.Time) (expired int, err error)
}
```

Implementation in `services/shared/storage/postgres/postgres.go`, alongside
`SaveGhosts`. `ExpireSnoozes` runs `UPDATE dismissed_ghosts SET revoked_at=now(),
revoked_by='system:snooze_expiry' WHERE action='snooze' AND snooze_until < $1 AND
revoked_at IS NULL` and returns the row count.

### C.3 API surface

New endpoints on `services/api/internal/api/handler.go`:

| Method | Path | Body | Response |
|---|---|---|---|
| `POST` | `/v1/ghosts/{resource_id}/dismiss` | `{account_id, reason, note?}` | `201 {dismissal_id}` |
| `POST` | `/v1/ghosts/{resource_id}/snooze` | `{account_id, reason, snooze_until, note?}` | `201 {dismissal_id}` |
| `DELETE` | `/v1/ghosts/{resource_id}/dismiss?account_id={id}` | — | `204` (soft revoke) |
| `GET` | `/v1/dismissals?account_id={id}&include_history=false` | — | `[{…}]` |

Changes to existing endpoints:

- `GET /v1/ghosts` at `handler.go:77` — left-join `dismissed_ghosts` in the query
  and filter out rows with an active dismissal. Accept `?include_dismissed=true`
  to opt back in (useful for a "Dismissed" tab in the UI).
- `GET /v1/summary` at `handler.go:132` — excludes dismissed by default.
  Bust the Track B.3.2 summary cache on dismiss/revoke.

Validation rules:

- `reason=other` requires non-empty `note` (HTTP 400 if missing).
- `snooze_until` must be in the future and ≤ 90 days out (`$SNOOZE_MAX_DAYS`, default
  90). Anything longer is functionally a dismissal — nudge the user.
- Resource must currently be a ghost for the given account/organization — prevents
  dismissing phantoms.

### C.4 Analyzer / ingestion integration

No changes to `services/shared/analyzer/analyzer.go` — detection stays pure. The
filtering happens at read time (in the API layer) so we retain the full detection
history in `ghost_records` for future use (3.5 scan history will benefit).

Optional, weak signal: in `services/ingestion/cmd/main.go` `runScan`, after
`SaveGhosts`, check whether any previously-dismissed resource is no longer detected
(auto-revoke? or just log `dismissal.auto_cleared` so ops knows). **Recommend:
just log it for now.** Auto-revoking risks re-surfacing things the user intentionally
silenced (classic "zombie resurrection" bug).

### C.5 Snooze expiry worker

`services/ingestion/cmd/main.go` already runs tickers for scan scheduling and cost
retention. Add a third:

```go
// Snooze expiry — runs every 10 minutes.
go func() {
    ticker := time.NewTicker(10 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-sigCtx.Done():
            return
        case <-ticker.C:
            n, err := store.ExpireSnoozes(context.Background(), time.Now().UTC())
            if err != nil {
                slog.Error("snooze.expire.failed", "error", err)
                continue
            }
            if n > 0 {
                slog.Info("snooze.expired", "count", n)
                // Invalidate any cached summaries so the UI shows the
                // resurrected ghosts on next fetch.
                cache.InvalidatePattern(ctx, "summary:*")
            }
        }
    }
}()
```

Ten minutes is a reasonable floor given the default scan interval is hours. Expose
as `SNOOZE_EXPIRY_INTERVAL` env var if we ever need finer-grained control.

### C.6 Dashboard

Screens touched (`services/dashboard/src/screens/`):

- `DetailScreen.js` — add "Dismiss" and "Snooze" actions in the header. Dismiss
  opens a modal with a reason-code picker (radio group) and optional note. Snooze
  adds a date picker limited to 90 days out.
- `DashboardScreen.js` — add a subtle "X dismissed" pill in the filter bar that
  toggles `?include_dismissed=true`. Dismissed rows render with reduced opacity
  and a grey reason badge (`Intentional`, `Scheduled for deletion`, etc.).
- New `DismissedScreen.js` (linked from the pill) — lists active dismissals with a
  "Restore" button.
- API client at `services/dashboard/src/api/client.js` — add `dismissGhost`,
  `snoozeGhost`, `revokeDismissal`, `listDismissals` methods.

Copy for reason codes (tested for FinOps literacy):

| Code | Label | Subtext |
|---|---|---|
| `intentional` | Intentional | DR standby, scheduled capacity, etc. |
| `scheduled_deletion` | Scheduled for deletion | Already planned — don't nag me. |
| `false_positive` | False positive | Detection rule is wrong for this resource. |
| `cost_accepted` | Cost accepted | I've decided to pay for this. |
| `other` | Other | Requires a note. |

### C.7 Tests

- Postgres: create dismissal, verify unique constraint rejects second active row
  with same fingerprint, verify revoke + re-dismiss works.
- Postgres: seed snooze with past `snooze_until`, run `ExpireSnoozes`, assert row
  count and `revoked_at` set.
- Handler: dismiss then `GET /v1/ghosts` — resource absent. With
  `?include_dismissed=true` — present with `dismissed_at` populated.
- Handler: 400 when `reason=other` and note empty.
- Handler: 409 or 400 when dismissing a resource that isn't currently a ghost.
- Integration: dismiss → scan again → verify resource still hidden.
- Integration: snooze 1 hour in future, advance clock, tick expiry, verify
  resource re-appears in `GET /v1/ghosts`.
- RLS: organization A cannot see organization B's dismissals.

---

## Sequencing, Dependencies, Risks

### Order of execution (optimised for shippable checkpoints)

1. **Week 1 — Track C (Dismiss/snooze):** biggest user-facing win; independent of
   A and B; unblocks first paying customer.
2. **Week 2 — Track A.1 (scan_runs) + A.3 (stuck-scan extension):** foundation for
   retry logic in A.2 and for the "why didn't my scan run?" UX.
3. **Week 3 — Track B.1 + B.2 (Redis local + prod):** must precede any production
   launch for 2.16. B.2 is the gating item for customers in different regions.
4. **Week 4 — Track A.2 (retry/DLQ) + B.3 (expanded Redis usage):** quality &
   performance polish; nothing blocks shipping without these but they pay back on
   every subsequent incident.

Dependencies worth naming:

- A.2 depends on A.1 (it writes to `scan_runs`) and B.1 (delayed queue needs local
  Redis persistence to be useful in dev).
- C.3 endpoint's summary-cache invalidation depends on B.3.2. If B slips, either
  remove the invalidation and accept a 30s window of stale totals after a dismiss,
  or defer the summary cache until B lands.
- 2.16 production deployment depends on B.2.

### Rollout checklist per track

- Migrations run via the existing `MIGRATION_DATABASE_URL` path at service startup.
  Both new migrations are additive with no backfill — safe to deploy ahead of the
  code that uses them.
- All new endpoints sit behind existing auth middleware. No changes to auth.
- Feature flag: none. Each track is small enough and well-tested; rolling back means
  reverting the migration + code.
- `make test` must pass; `make test-storage` covers the new RLS policies.

### Open questions

1. **Dismiss scope per user vs per organization.** The plan treats dismissals as
   organization-wide (any admin can dismiss, everyone sees the result). Confirm that's
   the intended model — otherwise we need per-user visibility rules and the
   `dismissed_by` column alone is insufficient.
2. **Snooze maximum.** Capped at 90 days in the plan. Matches AWS's cost horizon
   sensibly but shout if the right number is 30 or 180.
3. **Redis provider.** The plan recommends Upstash. If there's a preference for
   staying all-AWS, we switch to ElastiCache Serverless — same code, just a different
   Terraform module and a slightly higher floor cost.
4. **Dead-letter surfacing.** Plan leaves DLQ inspection as a `redis-cli` task.
   Worth a small admin endpoint (`GET /v1/admin/dead_letters`) or can wait?
5. **Tasks.md reconciliation.** The checkmarks on 2.12 need adjustment — the core
   sub-items are done but history + retry aren't. Happy to update Tasks.md as part
   of landing this plan, or we can leave it as a "see phase2-plan.md" redirect.

### Risks

- **Scan queue migration.** The delayed-queue Lua script is new code under load.
  Exercise it in a staging environment with fault injection before shipping — a
  bug here can silently swallow jobs.
- **Dismissal fingerprint drift.** If AWS ever changes a resource ID on you (rare
  but EIPs recycle, NAT gateways get recreated with new IDs) the dismissal won't
  follow. Acceptable for v1 — revisit only if users complain.
- **Cost floor of Redis in prod.** Upstash's free tier is 10k commands/day, which
  the scan queue can burn through on a bad day. Monitor and move to the €10/mo
  tier preemptively.
- **Summary cache + dismiss consistency.** A dismiss followed immediately by a
  summary fetch can return stale totals if cache invalidation lags. 30-second TTL
  keeps the blast radius small, and explicit invalidation on dismiss closes it.

---

## File-by-file change list

### New files

```
services/shared/storage/postgres/migrations/006_add_scan_runs.up.sql
services/shared/storage/postgres/migrations/006_add_scan_runs.down.sql
services/shared/storage/postgres/migrations/007_add_dismissed_ghosts.up.sql
services/shared/storage/postgres/migrations/007_add_dismissed_ghosts.down.sql
services/shared/model/scan_run.go
services/shared/model/dismissal.go
services/shared/queue/errclass.go
services/shared/queue/errclass_test.go
services/api/internal/api/dismissals.go          # handler registration lives here, keeps handler.go lean
services/api/internal/api/dismissals_test.go
services/api/internal/api/scan_runs.go
services/api/internal/api/scan_runs_test.go
services/api/internal/middleware/idempotency.go
services/dashboard/src/screens/DismissedScreen.js
services/dashboard/src/components/DismissModal.js
services/dashboard/src/components/SnoozeModal.js
terraform/modules/redis/main.tf
terraform/modules/redis/variables.tf
terraform/modules/redis/outputs.tf
test/integration/dismiss_test.go
test/integration/scan_retry_test.go
```

### Edited files

```
docker-compose.yml                                        # B.1: persistence, port, memory cap
.gitignore                                                # add redis_data/
services/shared/storage/storage.go                        # Store interface additions
services/shared/storage/postgres/postgres.go              # ~6 new methods
services/shared/queue/queue.go                            # Trigger, Attempt, IdempotencyKey fields; EnqueueDelayed/DeadLetter
services/shared/queue/redis/redis.go                      # delayed queue Lua, DLQ list
services/ingestion/cmd/main.go                            # snooze expiry ticker, stuck-scan extension
services/ingestion/cmd/worker.go                          # retry loop, scan_runs writes
services/api/cmd/main.go                                  # STUCK_SCAN_TIMEOUT env var, wire cache into Handler
services/api/internal/api/handler.go                      # idempotency header, ghosts join to dismissals, summary cache wrap
services/api/internal/middleware/ratelimit.go             # (none — already Redis-backed)
services/dashboard/src/screens/DetailScreen.js            # dismiss/snooze actions
services/dashboard/src/screens/DashboardScreen.js         # dismissed pill, filter toggle
services/dashboard/src/api/client.js                      # 4 new methods
Tasks.md                                                  # reconcile 2.12 checkmarks, mark 2.14 in-progress
```

### Tests

- Unit: ≥ 12 new tests across `scan_runs`, `dismissals`, `errclass`, queue delayed
  mechanics.
- Postgres: ≥ 6 RLS + constraint tests for the two new tables.
- Integration: 4 new scenarios (dismiss roundtrip, snooze expiry, scan retry,
  idempotent scan).

---

## Acceptance criteria

- [ ] `make test && make test-storage` green.
- [ ] `make test-integration` green against a live `make start-dev`.
- [ ] A user can PATCH `scan_interval_hours`, see the next scan fire, and read its
      history via a new "Scans" tab on the account settings screen.
- [ ] A flaky AWS response during a scan results in two retry rows in `scan_runs`
      and a final `succeeded` row, with total attempts ≤ `maxAttempts`.
- [ ] A deleted-credentials error results in one `scan_runs` row with
      `status='dead_lettered'` and a matching entry in
      `axiaops:scan_queue:dead`.
- [ ] `docker compose up` with no existing `redis_data/` directory creates
      persistent storage that survives a `docker compose restart`.
- [ ] Staging/prod Terraform applies and the services can reach Redis over TLS.
- [ ] A user can dismiss a ghost with a reason, see it disappear from the list,
      restore it, and snooze it for 7 days with the row reappearing after the
      snooze expires.
- [ ] Dashboard shows a dismissed pill with an accurate count and a working
      "Restore" flow.

---

*Plan generated against HEAD on 2026-04-18. Refresh the "What already exists"
tables before starting each track — code moves fast and some of these checkmarks
may shift.*
