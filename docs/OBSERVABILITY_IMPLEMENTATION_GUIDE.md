# Observability Implementation Guide (Phase 2.6)

This guide shows how to integrate observability (Prometheus metrics + structured logging) into AxiaOps services. Refer to `OBSERVABILITY.md` for full reference.

## Quick Start

### 1. Import the Observability Package

```go
import "axiaops.io/shared/observability"
```

### 2. Expose `/metrics` Endpoint

Both services already do this in `cmd/main.go`:

```go
mux.Handle("/metrics", promhttp.Handler())
```

### 3. Add HTTP Middleware (API Service)

The API already records HTTP metrics. For other services:

```go
handler := observability.HTTPMiddleware(http.HandlerFunc(yourHandler))
mux.HandleFunc("GET /path", handler)
```

## Implementation Examples

### Example 1: Database Query Instrumentation

**Before (no metrics):**

```go
func (h *Handler) listZombies(w http.ResponseWriter, r *http.Request) {
    ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
    zombies, err := h.store.LoadZombies(ctx)
    if err != nil {
        slog.Error("listZombies: load failed", "error", err)
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }
    writeJSON(w, zombies)
}
```

**After (with metrics + structured logging):**

```go
import "axiaops.io/shared/observability"

func (h *Handler) listZombies(w http.ResponseWriter, r *http.Request) {
    ctx := storage.WithOrganizationID(r.Context(), middleware.OrganizationID(r.Context()))
    
    // Record database query latency
    observer := observability.NewDatabaseObserver("LOAD_ZOMBIES")
    defer observer.Observe()
    
    zombies, err := h.store.LoadZombies(ctx)
    if err != nil {
        observer.ObserveError()
        observability.LogError(ctx, err,
            "operation", "list_zombies",
            "endpoint", "GET /v1/zombies",
        )
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }
    
    writeJSON(w, zombies)
}
```

**Metrics recorded:**
- `axiaops_db_query_duration_seconds{operation="LOAD_ZOMBIES"}` — query latency
- `axiaops_db_query_errors_total{operation="LOAD_ZOMBIES"}` — error count (if failed)

**Logs (JSON to stdout):**
```json
{"level":"ERROR","msg":"error","error":"...","operation":"list_zombies","endpoint":"GET /v1/zombies"}
```

---

### Example 2: AWS API Instrumentation

**Before (no metrics):**

```go
func runIngestion(ctx context.Context, store storage.Store, accountID string, keys *scanAWS) error {
    // ...
    records, err := awsClient.FetchCosts(ctx, start, end)
    if err != nil {
        slog.Error("fetch failed", "provider", "aws", "error", err)
        return fmt.Errorf("fetch failed: %w", err)
    }
    // ...
}
```

**After (with metrics + structured logging):**

```go
import "axiaops.io/shared/observability"

func runIngestion(ctx context.Context, store storage.Store, accountID string, keys *scanAWS) error {
    // ...
    
    // Record AWS API call
    awsObserver := observability.NewAWSObserver("CostExplorer")
    records, err := awsClient.FetchCosts(ctx, start, end)
    awsObserver.Observe()
    
    if err != nil {
        awsObserver.ObserveError()
        observability.LogError(ctx, err,
            "operation", "fetch_costs",
            "account_id", accountID,
            "provider", "aws",
        )
        return fmt.Errorf("fetch failed: %w", err)
    }
    
    // Record cost records fetched
    tenantID := storage.OrganizationIDFromCtx(ctx)
    observability.Global.CostRecordsFetched.WithLabelValues("aws", tenantID).Add(float64(len(records)))
    
    // ...
}
```

**Metrics recorded:**
- `axiaops_aws_api_call_duration_seconds{service="CostExplorer"}` — API latency
- `axiaops_aws_api_errors_total{service="CostExplorer"}` — error count (if failed)
- `axiaops_cost_records_fetched_total{provider="aws", organization_id="..."}` — records count

**Logs (JSON to stdout):**
```json
{"level":"ERROR","msg":"error","error":"...","operation":"fetch_costs","account_id":"abc-123","provider":"aws"}
```

---

### Example 3: Scan Lifecycle Instrumentation

**In `services/ingestion/cmd/main.go` (within `runIngestion`):**

```go
import "axiaops.io/shared/observability"

func runIngestion(ctx context.Context, store storage.Store, accountID string, keys *scanAWS) error {
    tenantID := storage.OrganizationIDFromCtx(ctx)
    
    // Record scan start
    observability.RecordScanStart(ctx)
    defer observability.RecordScanEnd(ctx)
    
    // === FETCH STAGE ===
    fetchObserver := observability.NewScanObserver("fetch")
    
    var allRecords []model.CostRecord
    for _, p := range providers {
        records, err := p.FetchCosts(ctx, start, end)
        if err != nil {
            observability.RecordScanError(accountID, "fetch_costs_failed")
            observability.LogError(ctx, err,
                "stage", "fetch",
                "account_id", accountID,
                "provider", p.Name(),
            )
            continue
        }
        
        inserted, saveErr := store.Save(ctx, records)
        if saveErr != nil {
            observability.RecordScanError(accountID, "save_records_failed")
            return fmt.Errorf("save failed: %w", saveErr)
        }
        
        skipped := int64(len(records)) - inserted
        slog.Info("fetched records", "provider", p.Name(), "total", len(records), "inserted", inserted)
        observability.Global.CostRecordsFetched.WithLabelValues(p.Name(), tenantID).Add(float64(len(records)))
        
        allRecords = append(allRecords, records...)
    }
    fetchObserver.Observe()
    
    // === ANALYZE STAGE ===
    analyzeObserver := observability.NewScanObserver("analyze")
    
    usage, err := awsClient.FetchUsage(ctx, allRecords, start, end)
    if err != nil {
        observability.RecordScanError(accountID, "fetch_usage_failed")
        return fmt.Errorf("fetch usage from cloudwatch: %w", err)
    }
    
    zombies := analyzer.Detect(allRecords, usage)
    eipZombies := aws.DiscoverUnattachedEIPs(ctx, allRecords, awsClient.AccountID(), start, end)
    zombies = append(zombies, eipZombies...)
    
    summary := analyzer.Summarize(zombies)
    slog.Info("analysis: detected zombie resources", "total", summary.TotalZombies)
    
    analyzeObserver.Observe()
    
    // === SAVE STAGE ===
    saveObserver := observability.NewScanObserver("save")
    
    if err := store.SaveZombies(ctx, zombies); err != nil {
        observability.RecordScanError(accountID, "save_zombies_failed")
        return fmt.Errorf("save zombies: %w", err)
    }
    
    resources := analyzer.AnnotateAll(allRecords, usage, zombies)
    if err := store.SaveResources(ctx, resources); err != nil {
        observability.RecordScanError(accountID, "save_resources_failed")
        return fmt.Errorf("save resources: %w", err)
    }
    
    saveObserver.Observe()
    
    // === UPDATE SUMMARY METRICS ===
    observability.Global.ZombiesDetected.WithLabelValues("aws", tenantID).Set(float64(summary.TotalZombies))
    observability.Global.PotentialMonthlySaving.WithLabelValues("aws", tenantID).Set(summary.PotentialMonthlySave)
    observability.Global.ResourcesAnalyzed.Add(float64(len(resources)))
    
    return nil
}
```

**Metrics recorded:**
- `axiaops_scan_duration_seconds{stage="fetch"}` — fetch latency
- `axiaops_scan_duration_seconds{stage="analyze"}` — analyze latency
- `axiaops_scan_duration_seconds{stage="save"}` — save latency
- `axiaops_accounts_scanning` — incremented at start, decremented at end
- `axiaops_scan_errors_total{account_id="...", error_type="..."}` — error count
- `axiaops_zombies_detected{provider="aws", organization_id="..."}` — final zombie count
- `axiaops_potential_monthly_savings_usd{provider="aws", organization_id="..."}` — savings
- `axiaops_resources_analyzed_total` — total resources

---

### Example 4: Structured Logging for Debugging

Use structured logs to track the flow and debug issues:

```go
func (h *Handler) scanAccount(w http.ResponseWriter, r *http.Request) {
    accountID := r.PathValue("id")
    tenantID := middleware.OrganizationID(r.Context())
    
    ctx := storage.WithOrganizationID(r.Context(), tenantID)
    
    // Log account retrieval
    observability.LogInfo(ctx, "retrieving account",
        "account_id", accountID,
        "organization_id", tenantID,
    )
    
    account, err := h.store.GetAccount(ctx, accountID)
    if err != nil {
        observability.LogError(ctx, err,
            "operation", "scan_account",
            "account_id", accountID,
        )
        http.Error(w, "account not found", http.StatusNotFound)
        return
    }
    
    // Log credential decryption
    observability.LogInfo(ctx, "decrypting credentials", "account_id", accountID)
    
    secret, err := crypto.Decrypt(account.SecretEncrypted)
    if err != nil {
        // Logs can be searched by account_id or operation to debug issues
        observability.LogError(ctx, err,
            "operation", "decrypt_secret",
            "account_id", accountID,
        )
        http.Error(w, "credential error", http.StatusInternalServerError)
        return
    }
    
    // ... continue with scan ...
}
```

**Log output** (JSON):
```json
{"time":"2025-04-11T10:30:45.123Z","level":"INFO","msg":"retrieving account","account_id":"abc-123","organization_id":"organization-xyz"}
{"time":"2025-04-11T10:30:45.234Z","level":"INFO","msg":"decrypting credentials","account_id":"abc-123"}
{"time":"2025-04-11T10:30:45.345Z","level":"ERROR","msg":"error","error":"decryption failed","operation":"decrypt_secret","account_id":"abc-123"}
```

---

### Example 5: Transaction Metrics (Database Transactions)

For multi-statement database operations:

```go
observer := observability.NewTransactionObserver("update_account")

tx, err := h.store.BeginTx(ctx)
if err != nil {
    observer.Observe()
    return err
}
defer tx.Rollback()

// Perform multiple queries in transaction
if err := tx.UpdateAccount(ctx, account); err != nil {
    observer.Observe()
    return err
}
if err := tx.UpdateAccountStatus(ctx, accountID, "scanning"); err != nil {
    observer.Observe()
    return err
}

if err := tx.Commit(); err != nil {
    observer.Observe()
    return err
}

observer.Observe() // Record success
```

**Metrics recorded:**
- `axiaops_db_transaction_duration_seconds{type="update_account"}` — transaction latency

---

## Configuration Checklist

### Local Development

```bash
export DEV_MODE=true
export LOG_LEVEL=debug
export LOG_OUTPUT=text
```

### Staging

```bash
export APP_ENV=staging
export APP_VERSION=2.6.0
export LOG_OUTPUT=json
export LOG_LEVEL=info
```

### Production

```bash
export APP_ENV=production
export APP_VERSION=2.6.0
export LOG_OUTPUT=json
export LOG_LEVEL=warn
```

## Testing Observability

### Unit Tests

Observability logging methods work with context:

```go
import "testing"
import "axiaops.io/shared/observability"

func TestLogging(t *testing.T) {
    ctx := context.Background()
    
    // These log to stdout (safe in tests)
    observability.LogError(ctx, fmt.Errorf("test error"), "key", "value")
    observability.LogWarn(ctx, "warning message")
    observability.LogInfo(ctx, "info message")
    
    // Should not panic
}
```

### Integration Tests

Verify metrics are recorded:

```bash
# Start services
make start-dev

# Trigger a scan
curl -X POST http://localhost:8080/v1/accounts/abc-123/scan

# Check metrics
curl http://localhost:8080/metrics | grep "axiaops_scan_duration_seconds"
curl http://localhost:8081/metrics | grep "axiaops_zombies_detected"
```

### Prometheus Scrape Test

```bash
# Start Prometheus
prometheus --config.file=prometheus.yml

# Query in Prometheus UI
http://localhost:9090
> axiaops_scan_duration_seconds
> axiaops_http_request_duration_seconds
```

## Next Steps

1. **Test locally** — Enable metrics, run `make start-dev`, curl `/metrics`
2. **Deploy to staging** — Verify logs appear in CloudWatch/your log aggregator
3. **Create Grafana dashboards** — Visualize key metrics (see `OBSERVABILITY.md` for panels)
4. **Set up alerts** — Alert on error rates, high latency, scan failures
5. **Monitor regularly** — Search logs and review Prometheus queries weekly

## Troubleshooting

### Metrics not appearing

- Check `/metrics` endpoint responds: `curl http://localhost:8080/metrics`
- Look for `axiaops_*` metric names
- Verify `promhttp.Handler()` is registered: `grep -n "promhttp" services/api/cmd/main.go`

### Logs not appearing

- Check `LOG_LEVEL` is set correctly (try `debug` to see more)
- Check `LOG_OUTPUT` is `json` or `text` (default is `json`)
- Verify services are running: `ps aux | grep api`

### High cardinality issues

- Don't use user IDs, full paths, or unbounded values as labels
- Use route patterns: `/v1/accounts/{id}` instead of `/v1/accounts/abc-123`
- Use error types: `auth_failed`, `network_error` instead of specific error messages

---

**See Also:**
- `OBSERVABILITY.md` — Full reference
- `services/shared/CLAUDE.md` — Observability package details
- `services/api/CLAUDE.md` — API service specifics
- `services/ingestion/CLAUDE.md` — Ingestion service specifics

---

**Last Updated:** 2025-04-11  
**Phase:** 2.6  
**Maintainers:** AxiaOps Team
