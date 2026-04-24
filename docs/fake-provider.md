# Fake Provider ✅

The fake provider runs the full ingestion pipeline — cost fetch → zombie detection → database → API — using pre-defined scenario data instead of real AWS calls.

**Status**: Complete and production-ready. Used for development, testing, and demos.

## When to Use It

| Situation | Use |
|-----------|-----|
| Building or testing the dashboard UI | `DEV_MODE=true make start-dev` |
| Demoing the product without AWS | `DEV_MODE=true DEV_SCENARIO=enterprise make start-dev` |
| Testing the dismiss/snooze workflow | `DEV_SCENARIO=all-zombies` |
| Testing the empty state | `DEV_SCENARIO=no-zombies` |
| Unit-testing the fake itself | `make test-scenarios` |
| CI/CD pipeline testing | Automated tests, no AWS/Docker required |

## Development Workflow

**For regular development:**
```bash
make seed              # 12 zombies, 19 resources, 1000 days of snapshots
make start-dev
```

Seed data includes 90 days of realistic trend snapshots (gradual growth, weekly patterns, random noise).

## Scenarios

| Scenario | Resources | Zombies | Savings | Description |
|----------|-----------|---------|---------|-------------|
| `startup` | 4 | 2 | $47/month | Small account — 1 idle EC2, 1 unused Lambda |
| `enterprise` | 12 | 6 | $400/month | Large account — mix across EC2, RDS, Lambda, ELB, NAT |
| `all-zombies` | 3 | 3 | $235/month | Every resource idle — for dismiss/snooze testing |
| `no-zombies` | 2 | 0 | $0/month | All resources active — for empty state testing |

## Running in Dev Mode

```bash
# Default scenario (startup)
DEV_MODE=true make start-dev

# Specific scenario
DEV_MODE=true DEV_SCENARIO=enterprise make start-dev
```

Or set in `services/ingestion/.env`:
```
DEV_MODE=true
DEV_SCENARIO=all-zombies
```

The ingestion service runs the full pipeline on startup and every 24h scheduler tick. The dashboard at `http://localhost` shows the scenario data immediately.

## Running Tests

### Unit Tests
```bash
make test-scenarios
```
Tests the fake provider interface, scenario loading, and business logic. No database, Docker, or AWS required. Runs in under 1 second.

### Integration Tests
```bash
cd services/ingestion && go test ./cmd -run TestFakeProvider
```
Tests the complete ingestion pipeline with fake data: costs → usage → zombie detection → verification.

### End-to-End Business Tests
```bash
cd services/ingestion && go test ./internal/provider/fake -run TestE2E
```
Verifies business scenarios produce expected zombie counts and savings amounts.

### Test Coverage
- ✅ Provider interface compliance
- ✅ Scenario data validation  
- ✅ Zombie detection accuracy
- ✅ Business logic verification
- ✅ Full pipeline integration
- ✅ Error handling and fallbacks

## Adding a New Scenario

Edit `services/ingestion/internal/provider/fake/scenarios.go`:

```go
"my-scenario": {
    costs: []model.CostRecord{
        {
            Provider:   "aws",
            AccountID:  "999999999999",
            Service:    "AmazonEC2",
            Region:     "eu-central-1",
            ResourceID: "i-example",
            Amount:     100.00,
            Currency:   "USD",
        },
    },
    usage: []analyzer.UsageRecord{
        {
            ResourceID: "i-example",
            Metric:     "CPUUtilization",
            Unit:       "Percent",
            Avg:        1.5,      // <= 5% → zombie
            PeriodDays: 30,
        },
    },
},
```

Then run it:
```bash
DEV_MODE=true DEV_SCENARIO=my-scenario make start-dev
```

### Zombie detection thresholds

| Service | Metric | Zombie if |
|---------|--------|-----------|
| `AmazonEC2` | `CPUUtilization` | avg ≤ 5% |
| `AmazonRDS` | `DatabaseConnections` | avg = 0 |
| `AWSLambda` | `Invocations` | avg = 0 |
| `AmazonElasticLoadBalancing` | `RequestCount` | avg = 0 |
| `AmazonVPC` | `BytesOutToDestination` | avg = 0 |

`ResourceID` in `costs` must match `ResourceID` in `usage` — that's how the analyzer joins them.

## How It Works

```
DEV_MODE=true
      │
      ▼
runIngestionCore
      │
      ├── fake.New(DEV_SCENARIO)
      │
      ▼
runPipeline(ctx, store, accountID, fakeProvider, fakeProvider)
      │
      ├── FetchCosts  → scenario cost records
      ├── store.Save  → PostgreSQL
      ├── FetchUsage  → scenario usage records
      ├── analyzer.Detect → zombie resources
      ├── store.SaveZombies
      ├── store.SaveSnapshot
      └── store.SaveResources
```

The same `runPipeline` function is used in production (with `*aws.Client`) and dev mode (with `*fake.Provider`). The only difference is the data source.

## Implementation

```
services/ingestion/internal/provider/fake/
├── fake.go              # Provider struct, FetchCosts, FetchUsage, ScenarioNames
├── scenarios.go         # Named scenario data loader (embedded JSON)
├── fake_test.go         # Unit tests for provider interface
├── e2e_test.go         # End-to-end business scenario tests
└── testdata/            # JSON scenario files
    ├── startup.json     # Small account scenario
    ├── enterprise.json  # Large account scenario  
    ├── all-zombies.json # All resources idle
    └── no-zombies.json  # All resources active
```

### Key Components

**`fake.Provider`** satisfies two interfaces:
- `provider.Provider` — `Name()`, `FetchCosts()`
- `usageFetcher` (defined in `cmd/main.go`) — `FetchUsage()`

**Scenario Loading**: JSON files are embedded at compile time using `//go:embed`. No external file dependencies.

**Integration**: Uses the same `runPipeline()` function as production AWS provider, ensuring identical business logic.

## Performance Testing

For scale testing, use **benchmarks** rather than creating massive JSON files:

```bash
cd services/ingestion
go test -bench=. -benchmem ./internal/provider/fake/
```

**Available benchmarks:**
- `BenchmarkFullPipeline_Enterprise` — Complete ingestion pipeline
- `BenchmarkFetchCosts` — Cost fetching only
- `BenchmarkFetchUsage` — Usage fetching only
- `BenchmarkDetection` — Zombie detection only

**Best practices:**
1. Use existing scenarios (enterprise has realistic multi-account data)
2. Benchmark performance instead of storing 10k+ records in JSON
3. Generate test data programmatically when needed
4. Profile with pprof for deeper analysis

**Current performance (M1 Pro):**
- Full pipeline: ~2.7μs per operation
- Fetch costs: ~775ns per operation
- Zombie detection: ~1.7μs per operation
