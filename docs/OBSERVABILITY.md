# Observability (2.6)

AxiaOps Phase 2.6 adds comprehensive observability via **Prometheus metrics** and **structured logging**. This guide explains how to use, configure, and monitor these systems.

## Overview

The observability stack consists of:

1. **Prometheus Metrics** — Instrumentation for HTTP, database, AWS API, and scan operations
2. **Structured Logging** — JSON logs with service, environment, and version context (via `log/slog`)

### Files

- `services/shared/observability/` — Metrics, error tracking, and middleware
- `services/api/cmd/main.go` — HTTP service with `/metrics` endpoint
- `services/ingestion/cmd/main.go` — Ingestion service with `/metrics` endpoint

## Metrics

All Prometheus metrics are defined in `observability.go` and automatically registered with the default registry. They are exposed via `/metrics` on both services.

### HTTP Metrics

```
axiaops_http_requests_total               — Total requests (counter)
axiaops_http_request_duration_seconds     — Request latency (histogram)
axiaops_http_requests_in_flight           — Active requests (gauge)
axiaops_http_responses_total              — Responses by method/route/status (counter)
axiaops_http_errors_total                 — 5xx errors by method/route/status (counter)
```

**Labels:**
- `method` — HTTP verb (GET, POST, PATCH, DELETE)
- `route` — URL pattern (e.g., `/v1/zombies`, `/v1/accounts/{id}/scan`)
- `status` — HTTP status code (200, 401, 500, etc.)

### Database Metrics

```
axiaops_db_query_duration_seconds         — Query latency (histogram)
axiaops_db_query_errors_total             — Query errors (counter)
axiaops_db_connections_active             — Active connections (gauge)
axiaops_db_transaction_duration_seconds   — Transaction latency (histogram)
```

**Labels:**
- `operation` — Query type (SELECT, INSERT, UPDATE, DELETE, INSERT_ZOMBIE, etc.)
- `type` — Transaction type (scan, account_update, etc.)

### AWS/Ingestion Metrics

```
axiaops_aws_api_call_duration_seconds     — API call latency (histogram)
axiaops_aws_api_errors_total              — API errors (counter)
axiaops_cost_records_fetched_total        — Records fetched (counter)
axiaops_resources_analyzed_total          — Resources analyzed (counter)
axiaops_zombies_detected                  — Zombies detected (gauge)
axiaops_potential_monthly_savings_usd     — Savings USD (gauge)
```

**Labels:**
- `service` — AWS service (CostExplorer, CloudWatch, EC2, etc.)
- `provider` — Cloud provider (aws)
- `organization_id` — Organization UUID

### Scan Lifecycle Metrics

```
axiaops_scan_duration_seconds             — Scan stage duration (histogram)
axiaops_scan_errors_total                 — Scan errors (counter)
axiaops_scan_queue_depth                  — Queue length (gauge)
axiaops_accounts_scanning                 — Accounts being scanned (gauge)
```

**Labels:**
- `stage` — Scan phase (fetch, analyze, save)
- `account_id` — Account UUID
- `error_type` — Error category (network, auth, parse, etc.)

### Application Metrics

```
axiaops_application_uptime_seconds        — Uptime in seconds (gauge)
axiaops_application_errors_total          — Total errors (counter)
```

## Using Metrics in Code

### HTTP Middleware

The API service already includes HTTP metrics. To add to other handlers:

```go
import "axiaops.io/shared/observability"

handler := observability.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Write([]byte("OK"))
}))
```

### Database Queries

Wrap database operations with observers:

```go
import "axiaops.io/shared/observability"

observer := observability.NewDatabaseObserver("INSERT_ZOMBIE")
defer observer.Observe()

// ... perform query ...

if err != nil {
    observer.ObserveError()
    return err
}
```

### AWS API Calls

Track AWS API latency:

```go
observer := observability.NewAWSObserver("CostExplorer")
defer observer.Observe()

records, err := client.GetCostAndUsage(ctx, ...)
if err != nil {
    observer.ObserveError()
    return err
}
```

### Scan Operations

Record scan metrics in the ingestion service:

```go
observability.RecordScanStart(ctx)
defer observability.RecordScanEnd(ctx)

// ... perform scan ...

if err != nil {
    observability.RecordScanError(accountID, "network_error")
}
```

### Ingestion Pipeline

In `services/ingestion/cmd/main.go`:

```go
// Fetch stage
scanObserver := observability.NewScanObserver("fetch")
// ... fetch costs ...
scanObserver.Observe()

// Analyze stage
analyzeObserver := observability.NewScanObserver("analyze")
// ... analyze resources ...
analyzeObserver.Observe()

// Save stage
saveObserver := observability.NewScanObserver("save")
// ... save to DB ...
saveObserver.Observe()

// Update summary metrics
observability.Global.ZombiesDetected.WithLabelValues("aws", tenantID).Set(float64(summary.TotalZombies))
observability.Global.PotentialMonthlySaving.WithLabelValues("aws", tenantID).Set(summary.PotentialMonthlySave)
```

## Error Handling

Use structured logging for error reporting via `observability.LogError()`:

```go
import "axiaops.io/shared/observability"

if err != nil {
    observability.LogError(ctx, err, "operation", "fetch_costs", "account_id", accountID)
}
```

This logs the error with structured context to stdout/files, where it can be aggregated and searched:

```json
{
  "time": "2025-04-11T10:30:45.123Z",
  "level": "ERROR",
  "msg": "error",
  "error": "connection refused",
  "operation": "fetch_costs",
  "account_id": "abc-123"
}
```

For warnings and info:

```go
observability.LogWarn(ctx, "slow query", "operation", "list_zombies", "duration_ms", 1250)
observability.LogInfo(ctx, "scan completed", "zombie_count", 42, "savings_usd", 1234.56)
```

## Structured Logging

Logging is configured via `logging.Init()` and respects:

```bash
LOG_LEVEL="debug|info|warn|error"     # default: info
LOG_OUTPUT="text|json"                # default: json (unless DEV_MODE=true)
APP_ENV="production"                  # Added to all log lines
APP_VERSION="1.0.0"                   # Added to all log lines
```

Logs use Go's `log/slog` package:

```go
import "log/slog"

slog.Info("scan started", "account_id", accountID, "provider", "aws")
slog.Error("scan failed", "account_id", accountID, "error", err)
```

### Log Output Examples

**Text mode** (DEV_MODE=true):
```
2025-11-15T10:30:45.123Z	INFO	api	scan started	{"account_id": "abc-123", "provider": "aws"}
```

**JSON mode** (production):
```json
{
  "time": "2025-11-15T10:30:45.123Z",
  "level": "INFO",
  "service": "api",
  "env": "production",
  "version": "1.0.0",
  "msg": "scan started",
  "account_id": "abc-123",
  "provider": "aws"
}
```

## Metrics Endpoints

Both services expose Prometheus metrics at `/metrics`:

```bash
# API service
curl http://localhost:8080/metrics | grep axiaops

# Ingestion service
curl http://localhost:8081/metrics | grep axiaops
```

### Example Scrape Configuration (Prometheus)

Add to `prometheus.yml`:

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: 'axiaops-api'
    static_configs:
      - targets: ['localhost:8080']
    metrics_path: '/metrics'

  - job_name: 'axiaops-ingestion'
    static_configs:
      - targets: ['localhost:8081']
    metrics_path: '/metrics'
```

Start Prometheus:

```bash
prometheus --config.file=prometheus.yml
```

Access dashboards at `http://localhost:9090`.

## Grafana Dashboards

Create a dashboard to visualize AxiaOps metrics:

### Key Panels

1. **Request Rate** — `rate(axiaops_http_requests_total[5m])` per route
2. **Error Rate** — `rate(axiaops_http_errors_total[5m])` per route
3. **Latency** — `histogram_quantile(0.95, axiaops_http_request_duration_seconds)` by route
4. **Active Requests** — `axiaops_http_requests_in_flight`
5. **Zombie Detection Rate** — `axiaops_zombies_detected` by organization
6. **Potential Savings** — `axiaops_potential_monthly_savings_usd` by organization
7. **Scan Duration** — `rate(axiaops_scan_duration_seconds_sum[5m])` by stage
8. **Database Query Time** — `histogram_quantile(0.99, axiaops_db_query_duration_seconds)` by operation
9. **AWS API Errors** — `rate(axiaops_aws_api_errors_total[5m])` by service

## Development Workflow

### Local Development

```bash
# Start services with structured logging
export DEV_MODE=true
export LOG_LEVEL=debug
export LOG_OUTPUT=text
make start-dev

# Curl metrics endpoint
curl http://localhost:8080/metrics | grep http_requests
```

### Staging Deployment

```bash
export APP_ENV=staging
export APP_VERSION=2.6.0
export LOG_OUTPUT=json
export LOG_LEVEL=info
make start-staging
```

### Production Checks

- ✓ `APP_ENV` is "production"
- ✓ `APP_VERSION` matches git tag
- ✓ `LOG_OUTPUT` is "json"
- ✓ `LOG_LEVEL` is "info" or "warn"
- ✓ Prometheus scrape interval matches expectations
- ✓ Log retention is configured (CloudWatch, ELK, etc.)

## Testing Observability

Unit tests are in `observability_test.go`. They verify:

- HTTP middleware captures status codes and bytes written
- Observers record metrics without panicking
- Scan and AWS metrics are recorded

Run tests:

```bash
make test
```

## Monitoring Best Practices

1. **Alert on error rates** — 5x baseline or >1% errors
2. **Alert on latency** — P95 >2s or P99 >5s
3. **Alert on scan failures** — Consecutive failures >5 minutes
4. **Track zombie trends** — Compare month-over-month zombie counts
5. **Review error logs** — Search CloudWatch/ELK for error patterns weekly

## Troubleshooting

### Metrics not appearing at `/metrics`

- Verify Prometheus client library is imported: `github.com/prometheus/client_golang/prometheus/promhttp`
- Check mux.Handle("/metrics", promhttp.Handler()) is registered
- Curl the endpoint: `curl http://localhost:8080/metrics | grep axiaops`

### Structured logs not appearing

- Check `LOG_OUTPUT` env var: set to `json` for production, `text` for dev
- Check `LOG_LEVEL`: increase to `debug` to see more logs
- Verify service is running: `ps aux | grep api`

### High cardinality labels

Avoid labels with unbounded values (e.g., user IDs, full paths). Use route patterns instead:
- ✓ Good: `/v1/accounts/{id}` (label = route pattern)
- ✗ Bad: `/v1/accounts/abc-123` (label = specific ID)

## Future Enhancements (Phase 2.7+)

- [ ] Distributed tracing (OpenTelemetry)
- [ ] Custom metrics for FinOps-specific insights
- [ ] Automated alerting rules
- [ ] Metrics-based auto-scaling
- [ ] Real-time anomaly detection

---

**Last Updated:** 2025-04-11  
**Phase:** 2.6  
**Maintainers:** AxiaOps Team
