# CLAUDE.md — Ingestion Service

## Purpose

Long-lived HTTP server that fetches cloud cost and usage data, runs zombie detection,
and writes results to PostgreSQL. Triggered by the API service via `POST /scan`.

## Module

`axiaops.io/ingestion` — Go module at `services/ingestion/`. Entry point: `cmd/main.go`.

## Endpoints

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | /health | No | Healthcheck |
| POST | /scan | Yes | Run ingestion for a specific account |

## Ingestion Flow

```
POST /scan {account_id, tenant_id}
  → Fetch account from DB
  → Decrypt AWS secret (AES-256-GCM)
  → Set AWS credentials as env vars
  → FetchCosts (Cost Explorer API)
  → FetchUsage (CloudWatch + Describe APIs)
  → Detect() — apply threshold rules
  → SaveZombies + SaveResources → DB
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
5. Update IAM policy docs in `docs/production.md`

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
observability.Global.ZombiesDetected.WithLabelValues("aws", tenantID).Set(float64(summary.TotalZombies))
observability.Global.PotentialMonthlySaving.WithLabelValues("aws", tenantID).Set(summary.PotentialMonthlySave)
```

## Environment Variables

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| DATABASE_URL | Yes | — | PostgreSQL app connection |
| MIGRATION_DATABASE_URL | Yes | — | PostgreSQL owner connection |
| AWS_REGION | Prod | eu-central-1 | AWS region for API calls |
| DAYS_BACK | No | 30 | Cost lookback window (days) |
| ENCRYPTION_KEY | Yes | — | Decrypt account secrets |
| APP_ENV | No | — | Environment (production, staging, development) |
| APP_VERSION | No | — | Release version (e.g., 2.6.0); attached to slog `version` attribute |
| APP_COMMIT_SHA | No | — | Short git SHA of the build; attached to slog logs |
| LOG_LEVEL | No | info | Log level (debug, info, warn, error) |
| LOG_OUTPUT | No | json | Log format (json or text) |
| INGESTION_PORT | No | 8081 | HTTP listen port |
| RUN_ONCE | No | false | One-shot mode (run once and exit) |

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
