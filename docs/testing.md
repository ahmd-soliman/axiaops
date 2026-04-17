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
make test-ingestion # Ingestion service tests
make test-all      # Unit + integration
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
