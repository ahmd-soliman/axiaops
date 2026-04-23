# CLAUDE.md — Shared Module

## Purpose

Core library shared between API and ingestion services. Contains domain models, storage
interface, PostgreSQL implementation, analyzer (detection logic), crypto, and logging.
No AWS SDK dependency — cloud-specific code lives in the ingestion service.

## Module

`axiaops.io/shared` — Go module at `services/shared/`. Imported by both `api` and `ingestion`.

## Package Map

| Package | Responsibility |
|---------|---------------|
| `model/` | Domain types: Tenant, User, Account, CostRecord, ZombieResource, ResourceRecord, ZombieSnapshot, SnapshotService |
| `storage/` | `Store` interface + `WithTenantID()` / `TenantIDFromCtx()` context helpers |
| `storage/postgres/` | Production Store impl — PostgreSQL with RLS, migrations |
| `analyzer/` | `Detect()`, `Summarize()`, `AnnotateAll()` — pure functions, no I/O |
| `crypto/` | AES-256-GCM encrypt/decrypt for account secrets |
| `logging/` | `Init(service)` — configures `log/slog` with JSON/text output |
| `observability/` | **Phase 2.6** — Prometheus metrics, HTTP middleware |
| `cache/` | **Phase 2.14** — `Cache` interface + Redis + memory implementations. `cache.New(redisURL)` selects backend. |
| `queue/` | **Phase 2.14** — `Queue` interface + Redis (LPUSH/BRPOP) + sync HTTP fallback. `queue.New(redisURL, ingestionURL)` selects backend. |

## Store Interface

The `Store` interface in `storage/storage.go` is the single contract for data access.
All methods accept `context.Context` — tenant ID must be set via `WithTenantID()` before
any call. PostgreSQL RLS enforces this at the DB level.

Key methods: `SaveCostRecords`, `SaveZombies`, `LoadZombies`, `Summary`,
`SaveAccount`, `ListAccounts`, `GetAccount`, `DeleteAccount`, `TryMarkAccountScanning`,
`UpsertTenant`, `UpsertUser`, `SaveSnapshot`, `ListSnapshots`,
`SaveSnapshotServices`, `ListSnapshotsByService`, `ListTrendServices`,
`ListTrendResourceTypes`.

When adding new data access, add to this interface first, then implement in
`postgres/postgres.go`.

## Analyzer

Pure functions in `analyzer/detector.go`:

- `Detect(costs, usage)` — joins on `resource_id`, applies `serviceRules` thresholds
- `Summarize(zombies)` — total savings + per-service breakdown
- `AnnotateAll(costs, usage, zombies)` — marks each cost record as zombie or active

Detection rules are a module-level map `serviceRules`. Owner is derived from the `team` tag.
Resources with no matching rule or no usage data are skipped (not flagged).

## PostgreSQL Conventions

- Migrations in `storage/postgres/migrations/` — `NNN_name.up.sql` / `NNN_name.down.sql`
- Two DB users: `axiaops_owner` (runs migrations, creates schema) and `axiaops` (app user, RLS-limited)
- Schema: `axiaops` (not public) — set via `SET search_path TO axiaops`
- RLS policy: `tenant_id = current_setting('app.tenant_id', true)` on all data tables
- Connection pool: `pgxpool.Pool` — pass `DATABASE_URL` for app, `MIGRATION_DATABASE_URL` for migrations
- Transactions: `BEGIN` → `SET app.tenant_id` → operations → `COMMIT`. Always `defer tx.Rollback()`.
- Tables: `tenants`, `users`, `cost_records`, `zombie_records`, `resource_records`, `accounts`,
  `zombie_snapshots` (aggregate per-scan), `zombie_snapshot_services` (per-service breakdown per snapshot),
  `dismissed_zombies`

## Adding New Tables

1. Create migration files: `NNN_description.up.sql` and `NNN_description.down.sql`
2. Add RLS policy: `CREATE POLICY ... USING (tenant_id = current_setting('app.tenant_id', true))`
3. Add methods to `Store` interface in `storage/storage.go`
4. Implement in `storage/postgres/postgres.go`
5. Write integration test in `storage/postgres/postgres_test.go`

## Crypto

`crypto.Encrypt(key, plaintext)` / `crypto.Decrypt(key, ciphertext)` — AES-256-GCM.
Key is a 32-byte hex string from `ENCRYPTION_KEY` env var.
Generate with: `openssl rand -hex 32`

## Logging

`logging.Init(service)` must be called once at service startup. Configures:
- JSON output (production) or text (dev) — via `LOG_OUTPUT`
- Log level via `LOG_LEVEL` (debug/info/warn/error, default: info)
- Error handling via structured logging
- Auto-attaches `service`, `env` (`APP_ENV`), `version` (`APP_VERSION`) to all logs

## Observability (Phase 2.6)

Package `observability/` provides Prometheus metrics and observability middleware.

### Metrics

Pre-registered Prometheus metrics grouped by concern:
- **HTTP**: request count, latency, in-flight, responses, errors
- **Database**: query/transaction latency, errors, active connections
- **AWS**: API call latency, errors
- **Scan**: operation duration by stage, errors, queue depth
- **Application**: uptime, error count

Expose metrics via:

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"
mux.Handle("/metrics", promhttp.Handler())
```

Use observers to record metrics:

```go
// Database
observer := observability.NewDatabaseObserver("INSERT_ZOMBIE")
defer observer.Observe()
// ... perform query ...
if err != nil {
    observer.ObserveError()
}

// AWS API
observer := observability.NewAWSObserver("CostExplorer")
defer observer.Observe()
// ... call AWS API ...
if err != nil {
    observer.ObserveError()
}

// Scan lifecycle
observability.RecordScanStart(ctx)
defer observability.RecordScanEnd(ctx)
observability.RecordScanError(accountID, "error_type")

// Update gauges
observability.Global.ZombiesDetected.WithLabelValues("aws", tenantID).Set(float64(count))
observability.Global.PotentialMonthlySaving.WithLabelValues("aws", tenantID).Set(savings)
```

### Error Handling

Log errors with structured context:

```go
// Log error with context
observability.LogError(ctx, err, "operation", "scan", "account_id", accountID)

// Log warning
observability.LogWarn(ctx, "slow operation", "duration_ms", 5000)

// Log info
observability.LogInfo(ctx, "Scan completed", "zombie_count", 42)
```

All logs include structured context (JSON in production, text in dev mode).

### HTTP Middleware

Apply HTTP observability middleware early in the handler chain:

```go
handler := observability.HTTPMiddleware(http.HandlerFunc(handler))
```

Records request duration, status, error count, and in-flight requests to Prometheus.

See `../../OBSERVABILITY.md` for full guide.

## Testing

```bash
cd services/shared && go test ./...                              # unit tests (analyzer, crypto, etc.)
cd services/shared && go test ./storage/postgres/... -count=1    # integration (needs running PostgreSQL)
```

Integration tests require env vars: `MIGRATION_DATABASE_URL` and `DATABASE_URL`.
The Makefile handles this: `make test-storage`.
