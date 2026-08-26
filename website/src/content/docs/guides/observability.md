---
title: Observability
description: Prometheus metrics, structured logging, and AWS-scan error handling.
---

Both `api` and `ingestion` expose a `/metrics` endpoint and emit structured
JSON logs — everything below works the same whether you're running a single
`docker compose` install or a full Kubernetes deployment.

## Metrics

All metrics are prefixed `axiaops_` and labelled to avoid cardinality
blowup — route labels are the URL **pattern** (`/v1/accounts/{id}`), never a
raw ID.

| Group | Metrics | Labels |
|---|---|---|
| HTTP | `_http_requests_total`, `_http_request_duration_seconds`, `_http_requests_in_flight`, `_http_responses_total`, `_http_errors_total` (5xx only) | `method`, `route`, `status` |
| Database | `_db_query_duration_seconds`, `_db_query_errors_total`, `_db_connections_active`, `_db_transaction_duration_seconds` | `operation`, `type` |
| AWS / ingestion | `_aws_api_call_duration_seconds`, `_aws_api_errors_total`, `_cost_records_fetched_total`, `_resources_analyzed_total`, `_zombies_detected`, `_potential_monthly_savings_usd` | `service`, `provider`, `organization_id` |
| Scan lifecycle | `_scan_duration_seconds`, `_scan_errors_total`, `_scan_queue_depth`, `_accounts_scanning` | `stage`, `account_id`, `error_type` |
| Application | `_application_uptime_seconds`, `_application_errors_total` | — |

### Example Prometheus scrape config (docker-compose / single-host)

```yaml
scrape_configs:
  - job_name: 'axiaops-api'
    static_configs: [{ targets: ['localhost:8080'] }]
  - job_name: 'axiaops-ingestion'
    static_configs: [{ targets: ['localhost:8081'] }]
```

A fuller example with alerting rule stanzas ships at
[`deploy/observability/prometheus.yml.example`](https://github.com/ahmd-soliman/axiaops/blob/main/deploy/observability/prometheus.yml.example).

### On Kubernetes

No config needed — the chart's `api`/`ingestion` Services already carry
`prometheus.io/scrape`/`port`/`path` annotations, picked up automatically by
the `kubernetes-service-endpoints` job most Prometheus Helm charts ship by
default.

### Grafana

A ready-to-import dashboard covering request rate, error rate, p95 latency,
the zombie-detection/savings trend, and AWS API error rate lives at
[`deploy/observability/grafana-dashboard.json`](https://github.com/ahmd-soliman/axiaops/blob/main/deploy/observability/grafana-dashboard.json).

## Structured logging

Both services log structured JSON via `log/slog`, auto-attaching `service`,
`env`, `version`, and `commit_sha` to every line. Set `LOG_OUTPUT=text` for
human-readable output during local development.

## AWS-scan error handling

The ingestion pipeline classifies every AWS error so retries and
circuit-breaking are consistent instead of ad hoc per call site:

| Category | Examples | Retryable | Fails the scan |
|---|---|:---:|:---:|
| Credentials | `InvalidAccessKeyId`, `ExpiredToken` | ❌ | ✅ |
| Permissions | `AccessDenied`, `UnauthorizedOperation` | ❌ | ✅ |
| Throttling | `RequestLimitExceeded`, `Throttling` | ✅ | ❌ |
| Network | connection timeout, DNS errors | ✅ | ❌ |
| Data unavailable | Cost Explorer not enabled | ❌ | ✅ |
| Internal | AWS `ServiceUnavailable` | ✅ | ❌ |

Retries use exponential backoff (3 attempts, 100ms → 200ms → 400ms, capped at
5s). A circuit breaker opens after 3 consecutive failures, blocking further
scan attempts for a 30s cooldown, then allows 2 test scans through before
fully closing again. A failure in one part of a scan doesn't abort the
whole thing — cost-fetch failing skips that provider and continues with
others; a single resource failing is skipped, not fatal.

This surfaces in the dashboard as the account's status: `connected` (green,
normal), `error` (red — check credentials/permissions), `scan_timeout`
(orange — exceeded the 10-minute scan budget), `circuit_breaker_open`
(purple — auto-clears after the 30s cooldown).

## Learn more

The full metrics reference, code-level usage examples, and the retry/circuit-breaker
implementation details live in the repo's
[`docs/OBSERVABILITY.md`](https://github.com/ahmd-soliman/axiaops/blob/main/docs/OBSERVABILITY.md).
