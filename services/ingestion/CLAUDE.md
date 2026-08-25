# CLAUDE.md — Ingestion Service

## Purpose

Long-lived HTTP server that fetches cloud cost and usage data, runs zombie detection,
and writes results to PostgreSQL. Triggered by the API service via `POST /scan`.

## Module

`axiaops.io/ingestion` — Go module at `services/ingestion/`. Entry point: `cmd/main.go`.

## Build tags

The ingestion binary supports a `production` build tag (B1.7 layer 3 — plan §4.10.2), mirroring the api binary so the bypass story is symmetric. Two siblings of `cmd/main.go` define `devModeEnabled() bool`:

- `cmd/devmode_dev.go` (`//go:build !production`, default) — reads `DEV_MODE` env var.
- `cmd/devmode_production.go` (`//go:build production`) — returns false unconditionally.

Every site that previously read `os.Getenv("DEV_MODE")=="true"` routes through `devModeEnabled()` so the build-tag split lives at one seam. **Any new feature gated on dev-vs-prod must consult `devModeEnabled()`, never read the env directly** — bypassing the helper re-introduces the runtime-bypass attack the build tag closes.

Asymmetric stripping (api stripped, ingestion not) would still leak the bypass at the ingestion-side scan-gate (`POST /scan`, `scanScheduledAccounts`, the worker), so both binaries get the same treatment.

There is only one build shape now — no license gate, no per-tenant entitlement gate, no `selfhosted` opt-in. Scans run unconditionally for any connected account. Build commands:
- `go build ./cmd/` — DEV_MODE honoured. Used by local `make start-dev` + dev-1/dev-2.
- `go build -tags production ./cmd/` — DEV_MODE stripped (staging/prod). Wired via `make build-production` + the `BUILD_TAGS` Dockerfile arg.

Test pairs in `cmd/devmode_{dev,production}_test.go` regression-pin the DEV_MODE-stripping shape; CI runs `make build-production` + `go test -tags production ./cmd/` on every pipeline.

## Endpoints

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | /health | No | Healthcheck |
| GET | /metrics | No | Prometheus metrics |
| POST | /scan | HMAC | Run ingestion for a specific account |
| POST | /v1/credentials/verify | HMAC | sts:AssumeRole probe for role-onboarding |

Both POST endpoints are authenticated via shared-secret HMAC-SHA256 over the
api → ingestion hop. User-level authz is
enforced upstream at the api hop (`authz.PermAccountsScan` + audit_log write);
ingestion trusts that gate and only verifies "is this caller part of our
deployment?" via the `X-AxiaOps-Ingestion-{Timestamp,Signature}` headers.

## Ingestion Flow

```
POST /scan {account_id, organization_id}
  → Fetch account from DB
  → Decrypt AWS secret (AES-256-GCM)
  → Set AWS credentials as env vars
  → FetchCosts (Cost Explorer API)
  → FetchUsage (CloudWatch + Describe APIs)
  → Detect() — apply threshold rules
  → SaveZombies → SaveSnapshot → SaveSnapshotServices → DB
  → DispatchForScan — notify the org's enabled channels (best-effort, non-fatal)
  → SaveResources → DB
```

## Provider Interface

`internal/provider/Provider` — the abstraction for data sources:

- `aws` — AWS SDK calls: Cost Explorer, CloudWatch, EC2/RDS/Lambda/ELB Describe APIs

When adding a new cloud provider (Azure, GCP), implement this interface. The analyzer
and storage layers are provider-agnostic.

## AWS SDK Patterns

- Uses AWS SDK v2 (`github.com/aws/aws-sdk-go-v2`)
- Pagination: `ce.GetCostAndUsage` uses `NextPageToken` — always handle multi-page responses
- Resource discovery: Describe APIs per service type (EC2, RDS, Lambda, ELB, NAT Gateway)
- CloudWatch: `GetMetricStatistics` per discovered resource — one call per resource
- Mock: `mockCEClient` implements `CostExplorerAPI` interface for tests

## Adding New Detection Rules

1. Add the service's metric to `services/shared/analyzer/detector.go` → `serviceRules` map
2. Add the Describe API call to the per-service file in `internal/provider/aws/` (e.g. `discover_ec2.go`, `discover_rds.go`); create a new `discover_<service>.go` if no file exists for that service
3. Add CloudWatch metric mapping in `internal/provider/aws/cloudwatch.go`
4. Add unit test in `shared/analyzer/` covering the new threshold
5. Update the IAM read-only policy list in `docs/connect-aws-account.md`

## Observability (Phase 2.6)

See `../../OBSERVABILITY.md` for full observability guide.

### Prometheus Metrics

Already instrumented in `cmd/main.go`:

- `axiaops_ingestion_records_fetched_total` — cost records fetched by provider
- `axiaops_ingestion_records_saved_total` — records saved by provider and status
- `axiaops_ingestion_zombies_detected_total` — zombie count by provider
- `axiaops_potential_monthly_savings_usd` — potential savings by provider

Additional metrics to add via `observability`:

- `axiaops_scan_duration_seconds` — scan stage duration (fetch, analyze, save)
- `axiaops_aws_api_call_duration_seconds` — AWS API latency (CostExplorer, CloudWatch, etc.)
- `axiaops_aws_api_errors_total` — AWS API errors
- `axiaops_scan_errors_total` — scan errors by account_id and error_type

### Error Handling (Structured Logging)

Wrap AWS API calls with error logging:

```go
import "axiaops.io/shared/observability"

observer := observability.NewAWSObserver("CostExplorer")
defer observer.Observe()

records, err := awsClient.FetchCosts(ctx, start, end)
if err != nil {
    observer.ObserveError()
    observability.LogError(ctx, err,
        "operation", "fetch_costs",
        "account_id", accountID,
        "provider", "aws",
    )
    return err
}
```

Errors are logged with structured context (JSON format in production).

### Scan Lifecycle Tracking

Record scan operation stages:

```go
// Fetch stage
fetchObserver := observability.NewScanObserver("fetch")
records, err := p.FetchCosts(ctx, start, end)
fetchObserver.Observe()

// Analyze stage
analyzeObserver := observability.NewScanObserver("analyze")
zombies := analyzer.Detect(allRecords, usage)
analyzeObserver.Observe()

// Save stage
saveObserver := observability.NewScanObserver("save")
if err := store.SaveZombies(ctx, zombies); err != nil {
    observability.RecordScanError(accountID, "save_zombies_failed")
}
saveObserver.Observe()

// Update summary
observability.Global.ZombiesDetected.WithLabelValues("aws", organizationID).Set(float64(summary.TotalZombies))
observability.Global.PotentialMonthlySaving.WithLabelValues("aws", organizationID).Set(summary.PotentialMonthlySave)
```

## Environment Variables

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| DATABASE_URL | Yes | — | PostgreSQL app connection (`axiaops`, RLS-enforced) |
| RUNTIME_ADMIN_DATABASE_URL | Yes outside DEV_MODE | — | Least-privilege RLS-bypass role (`axiaops_runtime`) for the cross-org scheduled-scan enumeration (`ListAllAccounts`). No DDL/ownership. DEV_MODE collapses to a single pool. See `docs/runtime-admin-db-role.md`. |
| MIGRATION_DATABASE_URL | No | — | PostgreSQL owner connection (`axiaops_owner`). **Migrate task only** — not read by the ingestion runtime. |
| AWS_REGION | Prod | eu-central-1 | AWS region for API calls |
| DAYS_BACK | No | 30 | Cost lookback window (days) |
| COST_RECORDS_RETENTION_DAYS | No | 90 | Age cutoff for the daily `cost_records` retention sweep (midnight UTC, cross-org). |
| NOTIFICATION_DISPATCH_RETENTION_DAYS | No | 90 | Age cutoff (by `created_at`) for the daily `notification_dispatches` retention sweep — runs in the same midnight-UTC pass as the cost sweep, cross-org via the admin pool. |
| ENCRYPTION_KEY | Yes | — | Decrypt account secrets; also decrypts `notification_channels.config_ciphertext` in the post-scan notification dispatch |
| APP_ENV | No | — | Environment (production, staging, development) |
| APP_VERSION | No | — | Release version (e.g., 2.6.0); attached to slog `version` attribute |
| APP_COMMIT_SHA | No | — | Short git SHA of the build; attached to slog logs |
| LOG_LEVEL | No | info | Log level (debug, info, warn, error) |
| LOG_OUTPUT | No | json | Log format (json or text) |
| INGESTION_PORT | No | 8081 | HTTP listen port |
| RUN_ONCE | No | false | One-shot mode (run once and exit) |
| INGESTION_SHARED_SECRET | Yes outside DEV_MODE | — | 32-byte hex (`openssl rand -hex 32`). Verifier-side shared secret for C-1 HMAC. DEV_MODE allows empty (passthrough with one-shot warning on inbound signed traffic). |
| INGESTION_SHARED_SECRET_NEXT | No | — | Optional verifier-side staging slot for zero-downtime rotation. Set on ingestion before flipping api over; clear after rotation. |
| INGESTION_HMAC_MAX_SKEW_SECONDS | No | 300 | Replay window in seconds. Widen only when an NTP fix is in flight; never permanently. |
| INGESTION_HMAC_SOFT_ENFORCE | No | false | Transition-only: when `true`, HMAC failures are logged + counted but NOT rejected. Used for the initial rollout's ingestion-before-api gap. Flip to `false` after one stable cycle per env; the `axiaops_ingestion_hmac_enforce_mode{mode="soft"}` alert fires if left on. |
| REDIS_URL | No | — | Connection URL for the cache + scan-queue backend (RESP wire protocol — Valkey post-migration; Redis-compatible). Empty disables the worker (`worker: skipped_no_redis` at startup). Format: `redis://:<password>@<host>:6379` with `REDIS_PASSWORD` propagated into the userinfo. |
| REDIS_PASSWORD | When the backend is `requirepass`-protected | — | Used by the `valkey-cli` healthcheck in deploy/*.yml and propagated into REDIS_URL's userinfo. Set as a per-env CI variable. |
| PUBLIC_HOST | No | — | Externally-reachable origin (`https://app.example.com`) used to build the dashboard deep-link in scan-digest notifications. Empty → the link is omitted. Mirrors the api's `PUBLIC_HOST`. |

## Testing

```bash
cd services/ingestion && go test ./...
```

Tests mock AWS SDK interfaces. No real AWS calls. Coverage includes pagination
and error propagation.

## Cost Awareness

- Each scan calls Cost Explorer (free) + CloudWatch GetMetricStatistics (~$0.01/1000 requests)
- At 100 resources/account, one scan costs ~$0.001 in CloudWatch
- Batch describe calls where possible to minimize API requests
- CloudWatch ListMetrics is free tier — use for auto-discovery
