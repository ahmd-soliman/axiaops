# Observability — AxiaOps

Prometheus metrics, structured logging, and AWS-scan error handling/resilience.

## Overview

- `services/shared/observability/` — metrics, error tracking, middleware
- `services/api/cmd/main.go` / `services/ingestion/cmd/main.go` — each exposes `/metrics`

**Use `observability.MetricsHandler()`** to wire the endpoint, **never**
`promhttp.Handler()` directly. The helper merges the package-private registry that
holds `observability.Global.*` with `prometheus.DefaultGatherer`; wiring
`promhttp.Handler()` directly only scrapes the default registry and every metric in
`observability/` silently vanishes (caught on MR !85 — the helper is now the single
seam).

## Metrics

All metrics are defined in `observability.go`, registered automatically, exposed via `/metrics` on both services.

**HTTP** — `axiaops_http_requests_total`, `_request_duration_seconds`,
`_requests_in_flight`, `_responses_total`, `_errors_total` (5xx only). Labels:
`method`, `route` (URL **pattern**, e.g. `/v1/accounts/{id}` — never a raw ID, to
avoid label-cardinality blowup), `status`.

**Database** — `axiaops_db_query_duration_seconds`, `_query_errors_total`,
`_connections_active`, `_transaction_duration_seconds`. Labels: `operation`
(SELECT/INSERT/…), `type`.

**AWS/ingestion** — `axiaops_aws_api_call_duration_seconds`, `_aws_api_errors_total`,
`_cost_records_fetched_total`, `_resources_analyzed_total`, `_zombies_detected`,
`_potential_monthly_savings_usd`. Labels: `service` (CostExplorer, EC2, …),
`provider`, `organization_id`.

**Scan lifecycle** — `axiaops_scan_duration_seconds`, `_scan_errors_total`,
`_scan_queue_depth`, `_accounts_scanning`. Labels: `stage` (fetch/analyze/save),
`account_id`, `error_type`.

**Application** — `axiaops_application_uptime_seconds`, `_application_errors_total`.

### Using metrics in code

```go
import "axiaops.io/shared/observability"

// HTTP middleware
handler := observability.HTTPMiddleware(myHandler)

// DB query
observer := observability.NewDatabaseObserver("INSERT_ZOMBIE")
defer observer.Observe()
if err != nil { observer.ObserveError(); return err }

// AWS call
observer := observability.NewAWSObserver("CostExplorer")
defer observer.Observe()

// Scan stage
scanObserver := observability.NewScanObserver("fetch")
scanObserver.Observe()
observability.Global.ZombiesDetected.WithLabelValues("aws", organizationID).Set(n)
```

### Grafana panels worth building

- Request rate: `rate(axiaops_http_requests_total[5m])` per route
- Error rate: `rate(axiaops_http_errors_total[5m])` per route
- P95 latency: `histogram_quantile(0.95, axiaops_http_request_duration_seconds)`
- Zombie detection / savings trend: `axiaops_zombies_detected`,
  `axiaops_potential_monthly_savings_usd` by organization
- AWS API errors: `rate(axiaops_aws_api_errors_total[5m])` by service

A ready-to-import dashboard covering these is at
[`deploy/observability/grafana-dashboard.json`](../deploy/observability/grafana-dashboard.json).

### Example Prometheus scrape config

```yaml
scrape_configs:
  - job_name: 'axiaops-api'
    static_configs: [{ targets: ['localhost:8080'] }]
  - job_name: 'axiaops-ingestion'
    static_configs: [{ targets: ['localhost:8081'] }]
```

A fuller example (with comments, optional alerting/rule-file stanzas) is at
[`deploy/observability/prometheus.yml.example`](../deploy/observability/prometheus.yml.example) —
copy it to `prometheus.yml` and point `prometheus --config.file=` at it.

## Structured logging

See [ARCHITECTURE.md § 8](ARCHITECTURE.md) for the `logging.Init()` / env-var
summary. `observability.LogError/LogWarn/LogInfo(ctx, ...)` attach structured
context on top of plain `slog` calls:

```go
observability.LogError(ctx, err, "operation", "fetch_costs", "account_id", accountID)
```

## AWS-scan error handling & resilience

The ingestion pipeline classifies every AWS error so retries, circuit-breaking, and
UI status are consistent instead of ad hoc per call site.

| Category | Examples | Retryable | Fails scan |
|---|---|:---:|:---:|
| `credentials` | InvalidAccessKeyId, ExpiredToken | ❌ | ✅ |
| `permissions` | AccessDenied, UnauthorizedOperation | ❌ | ✅ |
| `throttling` | RequestLimitExceeded, Throttling | ✅ | ❌ |
| `network` | connection timeout, DNS errors | ✅ | ❌ |
| `data_unavailable` | Cost Explorer not enabled | ❌ | ✅ |
| `internal` | AWS ServiceUnavailable | ✅ | ❌ |

**Retry**: exponential backoff, 3 attempts, 100ms → 200ms → 400ms, capped at 5s.

**Circuit breaker**: opens after 3 consecutive failures, blocking further scan
attempts for a 30s cooldown; half-open state allows 2 test scans before fully
closing again. Packages: `services/shared/retry/`, `services/shared/errors/`,
`services/shared/circuitbreaker/`.

**Scan timeout**: 10 minutes, enforced via context cancellation.

**Partial recovery**: a failure in one component doesn't abort the whole scan — cost
fetch failing skips that provider and continues with others; usage fetch failing
falls back to cost-only zombie detection; a single resource failing is skipped, not
fatal.

**Account status → UI**: `connected` (green, normal), `error` (red, general failure —
check credentials/permissions), `scan_timeout` (orange, exceeded 10 min — large
account or CloudWatch API limits), `circuit_breaker_open` (purple, scan button
disabled, auto-clears after the 30s cooldown), pending/no-color (never scanned yet).
