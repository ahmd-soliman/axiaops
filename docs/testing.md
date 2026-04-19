# Testing

## Overview

AxiaOps uses a two-tier test strategy:

| Tier | Location | Purpose | Dependencies |
|------|----------|---------|--------------|
| **Unit** | `services/*/internal/...` | Test individual functions/logic | None |
| **Integration** | `test/integration/` | Test component interaction | PostgreSQL, Redis |

## Running Tests

### Unit Tests

```bash
make test          # All unit tests
make test-shared   # Shared module tests
make test-api      # API service tests
make test-ingestion # Ingestion service tests (includes fake provider)
make test-all      # Unit + integration

# Fake provider tests only
cd services/ingestion && go test ./internal/provider/fake/
```

### Integration Tests

Integration tests verify component interaction (API + DB + Redis). They require a live stack.

```bash
# Start dev environment
make start-dev

# Run integration tests
make test-integration
```

Or with custom URLs:
```bash
SMOKE_API_URL=http://staging.example.com \
SMOKE_REDIS_URL=redis://localhost:6379 \
make test-integration
```

## Test Categories

### Fake Provider Tests (`services/ingestion/internal/provider/fake/`)

The fake provider enables testing without AWS credentials. It simulates AWS Cost Explorer and CloudWatch with predefined scenarios.

**Run tests:**
```bash
cd services/ingestion
go test ./internal/provider/fake/
```

**Scale tests:**
```bash
# 10,000 records across 100 accounts
go test -v -run=TestLargeScale_10k ./internal/provider/fake/

# 100,000 records across 500 accounts
go test -v -run=TestLargeScale_100k ./internal/provider/fake/

# All scale tests
go test -v -run=TestLargeScale ./internal/provider/fake/
```

**Performance benchmarks:**
```bash
go test -bench=. -benchmem ./internal/provider/fake/
```

**Test coverage:**

| Test | Purpose |
|------|---------|
| `TestE2E_BusinessScenarios` | Validates all 4 scenarios (startup, enterprise, all-ghosts, no-ghosts) return expected cost and usage data |
| `TestE2E_DetectionRules` | Verifies analyzer correctly identifies ghosts from fake provider data |
| `TestNew_UnknownScenario_FallsBackToStartup` | Unknown scenario names default to "startup" |
| `TestNew_EmptyScenario_FallsBackToStartup` | Empty scenario string defaults to "startup" |
| `TestFetchCosts_SetsTimestamps` | Cost records have valid timestamps within last 30 days |
| `TestScenarios` | All scenario JSON files load without errors |
| `TestAllGhosts_AllUsageIsZero` | "all-ghosts" scenario has zero usage for all resources |
| `TestNoGhosts_AllUsageAboveThreshold` | "no-ghosts" scenario has usage above detection thresholds |
| `TestScenarioNames_ReturnsAllFour` | Helper returns all 4 scenario names |
| `TestLargeScale_10kRecords` | Scale test with 10,000 records across 100 accounts (~2.4ms) |
| `TestLargeScale_100kRecords` | Scale test with 100,000 records across 500 accounts (~30ms, 3.3M records/sec) |

**Benchmarks:**
| Benchmark | Purpose |
|-----------|---------|
| `BenchmarkFullPipeline_Enterprise` | Complete ingestion pipeline (~2.7μs/op) |
| `BenchmarkFetchCosts` | Cost fetching performance (~775ns/op) |
| `BenchmarkFetchUsage` | Usage fetching performance (~6.8ns/op) |
| `BenchmarkDetection` | Ghost detection algorithm (~1.7μs/op) |

**Scenarios:**
- `startup` — 2 accounts, mix of active and idle resources
- `enterprise` — 5 accounts, realistic production workload
- `all-ghosts` — All resources idle (zero usage)
- `no-ghosts` — All resources active (usage above thresholds)

Set `DEV_SCENARIO=all-ghosts` in `services/ingestion/.env` to use a specific scenario.

### Integration Tests (`test/integration/`)

| Test | Purpose |
|------|---------|
| `TestHealth` | API service health endpoint |
| `TestMetrics` | Prometheus `/metrics` endpoint |
| `TestAccounts` | Account creation and listing |
| `TestGhosts` | Ghost resources endpoint |
| `TestSummary` | Summary statistics endpoint |
| `TestResources` | Resource inventory endpoint |
| `TestTrend` | Savings trend endpoint |
| `TestRateLimit_Redis` | Redis-backed rate limiting (429 on limit) |
| `TestRateLimit_CounterInRedis` | Rate limit counter persists in Redis |
| `TestScanQueue_JobEnqueuedInRedis` | Scan jobs queued in Redis, consumed by worker |
| `TestScheduledAutoScan_ZeroInterval` | Scheduler triggers scans for overdue accounts |

## CI/CD Pipeline

### Test Stage

Runs on all branches:

1. **Unit tests** - `go test ./...` for each module
2. **Linting** - `golangci-lint run`
3. **Integration tests** - `make test-integration` (requires Redis)

### Build Stage (main branch only)

1. Build Docker images
2. Push to AWS ECR

### Deploy Stage (main branch only, after build)

1. Update App Runner services
2. Invalidate CloudFront cache

## Writing Tests

### Unit Tests

Follow Go conventions in `services/*/internal/...`:

```go
func TestSomething(t *testing.T) {
    // Arrange
    // Act
    // Assert
}
```

### Integration Tests

Create in `test/integration/`:

```go
package integration

import (
    "net/http"
    "os"
    "testing"
)

func TestSomething(t *testing.T) {
    base := apiURL(t)  // Gets SMOKE_API_URL
    // Test logic
}

func apiURL(t *testing.T) string {
    t.Helper()
    u := os.Getenv("SMOKE_API_URL")
    if u == "" {
        t.Skip("SMOKE_API_URL not set")
    }
    return u
}
```

## Test Data

Integration tests create their own test data (accounts, etc.) and clean up after themselves.

No pre-seeded data required.
