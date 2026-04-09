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
  → SaveGhosts + SaveResources → DB
```

## Provider Interface

`internal/provider/Provider` — the abstraction for data sources:

- `filefixture` — reads from `fixtures/costs.json` and `fixtures/usage.json` (DEV_MODE=true)
- `aws` — real AWS SDK calls: Cost Explorer, CloudWatch, EC2/RDS/Lambda/ELB Describe APIs

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
2. Add Describe API call in `internal/provider/aws/discover.go` for resource discovery
3. Add CloudWatch metric mapping in `internal/provider/aws/cloudwatch.go`
4. Add fixture entries in `fixtures/costs.json` and `fixtures/usage.json` for dev mode
5. Add unit test in `shared/analyzer/` covering the new threshold
6. Update IAM policy docs in `docs/production.md`

## Fixtures (Dev Mode)

- `fixtures/costs.json` — 13 cost records with ARN-style resource IDs, tags, amounts
- `fixtures/usage.json` — CloudWatch-style metrics per resource (some deliberately zero)
- Used when `DEV_MODE=true` — no AWS credentials needed

## Environment Variables

| Variable | Required | Default | Notes |
|----------|----------|---------|-------|
| DATABASE_URL | Yes | — | PostgreSQL app connection |
| MIGRATION_DATABASE_URL | Yes | — | PostgreSQL owner connection |
| DEV_MODE | No | false | Use fixtures instead of AWS |
| AWS_REGION | Prod | eu-central-1 | AWS region for API calls |
| DAYS_BACK | No | 30 | Cost lookback window (days) |
| ENCRYPTION_KEY | Yes | — | Decrypt account secrets |
| SENTRY_DSN | No | — | Error tracking |

## Testing

```bash
cd services/ingestion && go test ./...
```

Tests mock AWS SDK interfaces. No real AWS calls. Coverage includes pagination,
error propagation, and fixture parsing.

## Cost Awareness

- Each scan calls Cost Explorer (free) + CloudWatch GetMetricStatistics (~$0.01/1000 requests)
- At 100 resources/account, one scan costs ~$0.001 in CloudWatch
- Batch describe calls where possible to minimize API requests
- CloudWatch ListMetrics is free tier — use for auto-discovery
