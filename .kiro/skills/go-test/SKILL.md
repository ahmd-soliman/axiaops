---
name: go-test
description: "Write Go tests following AxiaOps conventions. Use this skill when someone wants to add tests for handlers, store methods, middleware, analyzer functions, or any Go code in the project. Also trigger when the conversation mentions 'write tests', 'unit test', 'test coverage', 'handler test', 'mock store', 'httptest', or testing any part of the AxiaOps backend. Covers black-box testing, mock patterns, table-driven tests, and integration tests."
---

# Go Test Skill

AxiaOps follows strict testing conventions. Understanding them means every test you write fits naturally into the codebase.

## Core Conventions

- **Standard library only** — no testify, no assertion libraries
- **Black-box tests** — `package foo_test`, not `package foo` (test the public API)
- **Helper functions** for fixture building (not setup/teardown frameworks)
- **`httptest.NewRecorder`** for handler tests
- **Mock interfaces** for external dependencies (Store, AWS SDK, HTTP clients)

## Before You Start

Read the existing tests to absorb the style:

- `services/api/internal/api/handler_test.go` — handler tests with MockStore
- `services/api/internal/api/test_helpers_test.go` — MockStore implementation
- `services/shared/analyzer/detector_test.go` — pure function tests
- `services/api/internal/middleware/auth_test.go` — JWT middleware tests with RSA key generation

## Handler Test Pattern

Handler tests follow a consistent shape: create a mock store, build a handler, register routes on a ServeMux, fire a request with `httptest`, and assert the response.

```go
package api_test

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "axiaops.io/api/internal/api"
    "axiaops.io/shared/model"
    "axiaops.io/shared/storage"
)

func TestListThings_Returns200(t *testing.T) {
    store := NewMockStore().WithThings([]model.Thing{{ID: "t1"}})
    h := api.New(store)
    mux := http.NewServeMux()
    h.Register(mux)

    w := httptest.NewRecorder()
    mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/things"))

    if w.Code != http.StatusOK {
        t.Errorf("expected 200, got %d", w.Code)
    }
}

func TestListThings_StoreError_Returns500(t *testing.T) {
    store := NewMockStore().WithError(errors.New("db down"))
    h := api.New(store)
    mux := http.NewServeMux()
    h.Register(mux)

    w := httptest.NewRecorder()
    mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/things"))

    if w.Code != http.StatusInternalServerError {
        t.Errorf("expected 500, got %d", w.Code)
    }
}
```

### Helper functions used in handler tests

These live in `test_helpers_test.go`:

- `tenantRequest(method, path)` — creates a request with tenant_id in context (simulates auth middleware)
- `tenantRequestWithBody(method, path, body)` — same but with a JSON body
- `NewMockStore()` — returns a mock that implements `storage.Store`
- `.WithGhosts(...)`, `.WithAccounts(...)`, `.WithError(...)` — fluent builders for mock data

When adding a new Store method, add the corresponding mock method here too.

## Pure Function Test Pattern

For analyzer and other pure functions, tests are simpler — no HTTP layer, just inputs and outputs:

```go
package analyzer_test

import (
    "testing"

    "axiaops.io/shared/analyzer"
    "axiaops.io/shared/model"
)

func TestDetect_IdleEC2_FlagsGhost(t *testing.T) {
    costs := []model.CostRecord{costRecord("AmazonEC2", 100.0)}
    usage := []model.UsageRecord{usageRecord("AmazonEC2", "CPUUtilization", 2.0)}

    ghosts := analyzer.Detect(costs, usage)

    if len(ghosts) != 1 {
        t.Fatalf("expected 1 ghost, got %d", len(ghosts))
    }
}
```

Use helper functions like `costRecord()` and `usageRecord()` to build test fixtures — keep the test body focused on what's being tested, not boilerplate construction.

## Table-Driven Tests

Use table-driven tests when testing multiple input variations of the same behavior:

```go
func TestDetect_VariousThresholds(t *testing.T) {
    tests := []struct {
        name     string
        service  string
        metric   string
        usage    float64
        wantGhost bool
    }{
        {"EC2 idle", "AmazonEC2", "CPUUtilization", 2.0, true},
        {"EC2 active", "AmazonEC2", "CPUUtilization", 50.0, false},
        {"RDS abandoned", "AmazonRDS", "DatabaseConnections", 0.0, true},
        {"RDS in use", "AmazonRDS", "DatabaseConnections", 5.0, false},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            costs := []model.CostRecord{costRecord(tt.service, 100.0)}
            usage := []model.UsageRecord{usageRecord(tt.service, tt.metric, tt.usage)}
            ghosts := analyzer.Detect(costs, usage)
            got := len(ghosts) > 0
            if got != tt.wantGhost {
                t.Errorf("got ghost=%v, want %v", got, tt.wantGhost)
            }
        })
    }
}
```

## Middleware Test Pattern

Middleware tests verify request decoration and rejection. The auth middleware tests generate real RSA keys and JWTs — no mock Kinde calls:

```go
func TestAuthMiddleware_ValidToken_SetsContext(t *testing.T) {
    // Generate RSA key pair for signing
    key, _ := rsa.GenerateKey(rand.Reader, 2048)
    // ... create signed JWT, set up JWKS endpoint, verify handler receives tenant ID
}
```

See `services/api/internal/middleware/auth_test.go` for the full pattern.

## Assertion Style

Since we don't use testify, assertions are manual:

```go
// Fatal when continuing would panic or be meaningless
if len(result) != 1 {
    t.Fatalf("expected 1 result, got %d", len(result))
}

// Error for non-fatal checks (test continues)
if result[0].Name != "expected" {
    t.Errorf("expected name 'expected', got %q", result[0].Name)
}
```

Use `t.Fatalf` when a failure means subsequent checks would crash or be nonsensical. Use `t.Errorf` when the test can meaningfully continue.

## Running Tests

```bash
make test           # All unit tests across all Go modules
make test-postgres  # Integration tests needing a running Postgres
make test-smoke     # E2E tests needing the full stack (make start-dev first)
make test-all       # Unit + Postgres integration tests
```

## Naming Conventions

Test names follow `TestFunction_Scenario_ExpectedBehavior`:

```
TestHealth_Returns200
TestHealth_DatabasePingFails_Returns503
TestListGhosts_Returns200WithGhosts
TestCreateAccount_EmptyBody_Returns400
TestDetect_NoUsageData_ReturnsEmpty
```

File names: `foo_test.go` alongside `foo.go`, or `handler_lifecycle_test.go` for test groups.
