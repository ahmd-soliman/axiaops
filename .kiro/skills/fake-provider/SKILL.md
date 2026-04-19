---
name: fake-provider
description: "Add or modify scenarios in the AxiaOps fake provider for dev mode and testing. Use this skill when someone wants to add a new test scenario, modify existing scenario data, use DEV_MODE with realistic data, or write end-to-end tests without AWS. Also trigger when the conversation mentions 'fake provider', 'scenario', 'DEV_SCENARIO', 'test data', 'mock AWS', or 'dev mode data'."
---

# Fake Provider Skill

The fake provider (`services/ingestion/internal/provider/fake/`) supplies pre-defined cost and usage records for dev mode and tests — no AWS credentials, no network calls.

## Files

| File | Purpose |
|------|---------|
| `fake.go` | `Provider` struct implementing `provider.Provider` + `usageFetcher` |
| `scenarios.go` | Named scenario data (`costs` + `usage` slices) |
| `fake_test.go` | Unit tests for all scenarios |

## Adding a New Scenario

Edit `scenarios.go`. Add an entry to the `scenarios` map:

```go
"my-scenario": {
    costs: []model.CostRecord{
        {Provider: "aws", AccountID: "999999999999", Service: "AmazonEC2", Region: "eu-central-1", ResourceID: "i-example", Amount: 100.00, Currency: "USD"},
    },
    usage: []analyzer.UsageRecord{
        {ResourceID: "i-example", Metric: "CPUUtilization", Unit: "Percent", Avg: 1.5, PeriodDays: 30},
    },
},
```

Rules:
- `ResourceID` in `costs` must match `ResourceID` in `usage` for ghost detection to work
- `Service` must match a key in `serviceRules` (`AmazonEC2`, `AmazonRDS`, `AWSLambda`, `AmazonElasticLoadBalancing`, `AmazonVPC`)
- `Amount` must be > 0
- `Avg <= threshold` → ghost; `Avg > threshold` → active (see `services/shared/analyzer/rules.go` for thresholds)

## Running a Scenario

Dev mode (full stack):
```bash
DEV_MODE=true DEV_SCENARIO=my-scenario make start-dev
```

Or in `services/ingestion/.env`:
```
DEV_MODE=true
DEV_SCENARIO=my-scenario
```

Unit tests only (no Docker, no DB):
```bash
make test-scenarios
```

## Existing Scenarios

| Scenario | Resources | Ghosts | Use case |
|----------|-----------|--------|----------|
| `startup` | 4 | 2 | Default dev, quick dashboard check |
| `enterprise` | 12 | 6 | Summary/aggregation UI, multi-service |
| `all-ghosts` | 3 | 3 | Dismiss/snooze workflow |
| `no-ghosts` | 2 | 0 | Empty state UI |

## Writing a Scenario Test

```go
func TestScenario_MyScenario(t *testing.T) {
    p := fake.New("my-scenario")
    records, err := p.FetchCosts(context.Background(), start, end)
    if err != nil {
        t.Fatalf("FetchCosts: %v", err)
    }
    usage, err := p.FetchUsage(context.Background(), records, start, end)
    if err != nil {
        t.Fatalf("FetchUsage: %v", err)
    }
    // assert on records and usage
}
```

Add the test to `fake_test.go` alongside the existing scenario table test.

## How It Wires Into main.go

`runIngestionCore` checks `DEV_MODE`:

```go
if os.Getenv("DEV_MODE") == "true" {
    fp := fake.New(os.Getenv("DEV_SCENARIO"))
    return runPipeline(ctx, store, accountID, fp, fp)
}
```

`runPipeline` accepts `provider.Provider` (for `FetchCosts`) and `usageFetcher` (for `FetchUsage`). The fake implements both. The real `*aws.Client` also implements both.
