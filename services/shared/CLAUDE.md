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
| `model/` | Domain types: Organization, User, Account, CostRecord, ZombieResource, ResourceRecord, ZombieSnapshot, SnapshotService, NotificationChannel/NotificationDispatch/TriggerRule + EmailConfig/SlackConfig (`notification.go`) |
| `storage/` | `Store` interface + `WithOrganizationID()` / `OrganizationIDFromCtx()` context helpers |
| `storage/postgres/` | Production Store impl — PostgreSQL with RLS, migrations |
| `analyzer/` | `Detect()`, `Summarize()`, `AnnotateAll()` — pure functions, no I/O |
| `crypto/` | AES-256-GCM encrypt/decrypt for account secrets |
| `logging/` | `Init(service)` — configures `log/slog` with JSON/text output |
| `observability/` | **Phase 2.6** — Prometheus metrics, HTTP middleware |
| `cache/` | **Phase 2.14** — `Cache` interface + Redis + memory implementations. `cache.New(redisURL)` selects backend. |
| `queue/` | **Phase 2.14 + C-1** — `Queue` interface + Redis (LPUSH/BRPOP) + sync HTTP fallback. `queue.New(redisURL, ingestionURL, secret)` selects backend; the secret is threaded into both adapters (Redis envelope-signs, sync HTTP wire-signs). |
| `httpauth/` | shared-secret HMAC-SHA256 (`Sign` / `Verify` / `Middleware` / `MultiSecretMiddleware` / `PassthroughWithWarning`) for service-to-service auth. Today: api → ingestion + the Redis-queue envelope (`SignEnvelope` / `VerifyEnvelope`). Reusable seam for future inter-service hops. Headers: `X-AxiaOps-Ingestion-{Timestamp,Signature}`. |
| `notifications/` | Outbound scan-digest channels. `Transport` interface + `Payload` (`transport.go`), `BuildPayload` (`renderer.go`), `Dispatcher`/`DispatchForScan` (`dispatcher.go` — gate on `trigger_rule`, per-transport timeout, one `notification_dispatches` row per attempt, non-fatal), and the two v1 transports `email_smtp.go` (SMTP/SES) + `slack_webhook.go` (incoming webhook). Each transport decrypts its own `config_ciphertext` (`crypto.Decrypt`) and scrubs its bearer secret from errors (`scrub.go`). Wired into ingestion's `runIngestionCore` and the api's `/v1/channels/{id}/test`. **Invite email** (`invite_email.go`): `InviteSender` interface + `EmailTransport.SendInvite(ctx, cfg model.EmailConfig, recipient, InviteEmail)` mail an invitation's redemption URL to a single invitee. SendInvite takes an already-resolved plaintext `EmailConfig` (not a channel) so the *caller* owns config resolution — the api's `InviteMailer` seam sources it from either the org's email channel (`DecodeEmailConfig`) or the global `SMTP_*` env config. `DecodeEmailConfig` / `ValidateEmailConfig` are exported for that. |
| `model/audit.go` | **Phase 3.3** — `AuditEvent`, `AuditFilter`, `AuditCursor`, and the `AuditAction*` constants. Consumed by `Store.AuditLogWrite/List/AnonymiseUser` and by the `axiaops.io/api/internal/audit` helper that handlers call after mutations. |

## Store Interface

The `Store` interface in `storage/storage.go` is the single contract for data access.
All methods accept `context.Context` — organization ID must be set via `WithOrganizationID()` before
any call. PostgreSQL RLS enforces this at the DB level.

Key methods: `SaveCostRecords`, `SaveZombies`, `LoadZombies`, `Summary`,
`SaveAccount`, `ListAccounts`, `GetAccount`, `DeleteAccount`, `TryMarkAccountScanning`,
`UpsertOrganization`, `UpsertUser`, `SaveSnapshot`, `ListSnapshots`,
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

## Validation (B + golden harness)

`model.CostRecord.Validate()` and `analyzer.UsageRecord.Validate()` enforce strict invariants on every record entering the detection pipeline:

- **Strict** — unknown services, malformed currencies, bad regions, negative amounts → `*model.ValidationError` with the offending field.
- **Single source of truth** — `model.KnownServices` is the canonical set of internal service identifiers. Add a new service there *first*; both validators key off it.
- **Caller-decided posture** — production scan paths may log-and-skip on validation errors; tests should fail-fast. The validators do not log or call into runtime — they return.

Why strict: a loose validator passes "wrong-spelling EC2" through, the row is silently dropped by `Detect()` because `serviceRules` doesn't recognise it, and the test that expected a zombie just shows "0 zombies" with no signal. Strict turns silent-drop into a labelled error at the boundary.

### Golden-file detection tests

`analyzer/golden_test.go` runs every folder under `analyzer/testdata/golden/<scenario>/` as a sub-test:

```
testdata/golden/<scenario>/
  input_costs.json       — []model.CostRecord (validated before Detect)
  input_usage.json       — []analyzer.UsageRecord (validated before Detect)
  expected_zombies.json  — []goldenZombie projection, sorted by resource_id
```

Add a new rule case → add a new folder. Intentionally changing a rule → run `UPDATE_GOLDEN=1 go test ./analyzer/...` to rewrite the expected files in place. Review the diff before committing — `expected_zombies.json` *is* the spec.

The harness validates inputs first (B), so a fixture that uses an unregistered service or malformed currency fails at load time with the offending field — not as a confusing "0 zombies" mismatch downstream.

## PostgreSQL Conventions

- Migrations in `storage/postgres/migrations/` — `NNN_name.up.sql` / `NNN_name.down.sql`
- Three DB roles: `axiaops_owner` (runs migrations, creates schema — migrate task only), `axiaops_runtime` (least-privilege RLS-bypass via per-table policies, no DDL — the runtime services' `adminPool`; see `docs/runtime-admin-db-role.md`), and `axiaops` (app user, RLS-limited)
- Schema: `axiaops` (not public) — set via `SET search_path TO axiaops`
- RLS policy: `organization_id = current_setting('app.organization_id', true)` on all data tables
- Connection pool: `pgxpool.Pool` — pass `DATABASE_URL` for app, `MIGRATION_DATABASE_URL` for migrations
- Transactions: `BEGIN` → `SET app.organization_id` → operations → `COMMIT`. Always `defer tx.Rollback()`.
- Tables: `organizations`, `users`, `memberships`, `pending_memberships` (email-based invitations awaiting first-login redemption),
  `cost_records`, `zombie_records`, `resource_records`, `accounts`,
  `zombie_snapshots` (aggregate per-scan), `zombie_snapshot_services` (per-service breakdown per snapshot),
  `dismissed_zombies`, `audit_log`,
  `notification_channels` + `notification_dispatches` (Phase 2.15 — outbound scan-digest channels + delivery log; encrypted `config_ciphertext`)

## Adding New Tables

1. Create migration files: `NNN_description.up.sql` and `NNN_description.down.sql`
2. Add RLS policy: `CREATE POLICY ... USING (organization_id = current_setting('app.organization_id', true))`
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

Expose metrics via the package helper, **not** `promhttp.Handler()` directly:

```go
import "axiaops.io/shared/observability"
mux.Handle("/metrics", observability.MetricsHandler())
```

`MetricsHandler()` merges `prometheus.DefaultGatherer` (per-binary `MustRegister`'d counters) with the package-private registry that holds `Global.*`. Wiring `promhttp.Handler()` directly scrapes only the default registry — every metric in this package silently vanishes. That regression broke `/metrics` on the deployed preview env (caught on MR !85); the helper is the single seam every binary now uses.

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
observability.Global.ZombiesDetected.WithLabelValues("aws", organizationID).Set(float64(count))
observability.Global.PotentialMonthlySaving.WithLabelValues("aws", organizationID).Set(savings)
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
