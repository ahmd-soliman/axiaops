# Feature: Raw Cost View

**Status:** In Development | **Target:** Phase 2  
**Author:** Claude Code | **Last Updated:** April 21, 2026

---

## Overview

Expose AWS billing data (`cost_records` table) through a new `/v1/costs` API endpoint and "Cost" dashboard screen. Users can view raw service costs by time period, account, and service with filtering.

**Problem:** AxiaOps ingests real AWS cost data daily but never displays it — users only see detected ghosts (zombie resources). This feature surfaces the underlying cost data for visibility.

**Solution:** Add a minimal read-only endpoint + frontend screen following existing patterns (DashboardScreen, TrendScreen).

---

## Architecture

### Backend

**New Storage Method:**
```go
type CostFilter struct {
    AccountID string
    Service   string
    Days      int  // lookback, default 30
}

// In Store interface:
ListCostRecords(ctx context.Context, filter CostFilter) ([]model.CostRecord, error)
```

**Implementation:**
- Dynamic SQL query with optional `account_id`, `service` filters
- Time filter: `period_end >= NOW() - (days interval)`
- Amount filter: `amount > 0` (exclude $0.000005 entries)
- RLS enforcement: automatic via `setTenant(ctx, tx)`
- Uses existing `idx_cost_records_tenant_period_end` index

**API Endpoint:**
- `GET /v1/costs?account_id=...&service=...&days=30`
- Returns: `[]model.CostRecord`
- Auth: Required (tenant isolation via middleware)

### Frontend

**New Screen:**
- `CostScreen.jsx` — main component with filters + display
- `pages/Cost.jsx` — page wrapper (account selection state)
- Route: `/cost`
- Nav item: "Cost" tab

**Filters:**
- Period buttons: 7d / 30d / 90d (same pattern as TrendScreen)
- Service filter pills (auto-derived from data)
- Account selector (same as DashboardScreen)

**Display:**
- Cost cards grouped by service → region → period
- Total cost summary + record count
- Service color indicators
- Loading/error/empty states

---

## Files Modified

| File | Change |
|------|--------|
| `services/shared/storage/storage.go` | Add `CostFilter` struct, `ListCostRecords` interface |
| `services/shared/storage/postgres/postgres.go` | Implement `ListCostRecords` with dynamic SQL |
| `services/api/internal/api/handler.go` | Add `listCosts` handler, register route |
| `services/api/internal/api/test_helpers_test.go` | Add mock for `ListCostRecords` |
| `services/dashboard/src/api/client.js` | Add `fetchCosts()` function |
| `services/dashboard/src/screens/CostScreen.jsx` | **New** — main screen component |
| `services/dashboard/src/pages/Cost.jsx` | **New** — page wrapper |
| `services/dashboard/src/App.jsx` | Add `/cost` route |
| `services/dashboard/src/components/AppShell.jsx` | Add nav item |

---

## Implementation Details

### Backend Storage

**File:** `services/shared/storage/postgres/postgres.go`

```go
func (s *Store) ListCostRecords(ctx context.Context, filter storage.CostFilter) ([]model.CostRecord, error) {
    tx, err := s.pool.Begin(ctx)
    if err != nil { return nil, fmt.Errorf("ListCostRecords: begin: %w", err) }
    defer tx.Rollback(ctx)
    if err := setTenant(ctx, tx); err != nil { return nil, err }

    days := filter.Days
    if days <= 0 { days = 30 }

    query := `SELECT provider, account_id, service, region, resource_id,
                     amount, currency, period_start, period_end, tags, fetched_at
              FROM cost_records
              WHERE amount > 0 AND period_end >= NOW() - ($1 || ' days')::interval`
    args := []any{days}
    argN := 2

    if filter.AccountID != "" {
        query += fmt.Sprintf(" AND account_id = $%d", argN)
        args = append(args, filter.AccountID)
        argN++
    }
    if filter.Service != "" {
        query += fmt.Sprintf(" AND service = $%d", argN)
        args = append(args, filter.Service)
        argN++
    }

    query += " ORDER BY period_start DESC, amount DESC"

    rows, err := tx.Query(ctx, query, args...)
    if err != nil { return nil, fmt.Errorf("ListCostRecords: query: %w", err) }
    defer rows.Close()

    var records []model.CostRecord
    for rows.Next() {
        var r model.CostRecord
        err := rows.Scan(
            &r.Provider, &r.AccountID, &r.Service, &r.Region, &r.ResourceID,
            &r.Amount, &r.Currency, &r.PeriodStart, &r.PeriodEnd, &r.Tags, &r.FetchedAt,
        )
        if err != nil { return nil, err }
        records = append(records, r)
    }
    
    return records, tx.Commit(ctx)
}
```

**Pattern:** Follows `ListSnapshotsByService` for dynamic query building with tenant isolation.

### Backend API Handler

**File:** `services/api/internal/api/handler.go`

```go
func (h *Handler) listCosts(w http.ResponseWriter, r *http.Request) {
    ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))
    days, _ := strconv.Atoi(r.URL.Query().Get("days"))
    
    filter := storage.CostFilter{
        AccountID: r.URL.Query().Get("account_id"),
        Service:   r.URL.Query().Get("service"),
        Days:      days,
    }
    
    records, err := h.store.ListCostRecords(ctx, filter)
    if err != nil {
        slog.Error("listCosts: load failed", "error", err)
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }
    if records == nil { records = []model.CostRecord{} }
    writeJSON(w, records)
}
```

Register in `Register(mux)`: `mux.HandleFunc("GET /v1/costs", h.listCosts)`

### Frontend API Client

**File:** `services/dashboard/src/api/client.js`

```js
export async function fetchCosts(accountId, service, days = 30) {
  const params = new URLSearchParams();
  if (accountId) params.set('account_id', accountId);
  if (service)   params.set('service', service);
  params.set('days', String(days));
  
  const res = await fetch(`${BASE_URL}/v1/costs?${params}`, { 
    headers: authHeaders() 
  });
  if (!res.ok) throw new Error('Failed to fetch costs');
  return res.json();
}
```

### Frontend Screen Component

**File:** `services/dashboard/src/screens/CostScreen.jsx`

Key patterns:
- Period buttons: 7d / 30d / 90d (same pill style as TrendScreen)
- Service filter pills (auto-derived from fetched data)
- React Query: `queryKey: ['costs', selectedAccount, period]`
- Data grouped by service → region → period_start
- Section header: "TOTAL COST · $X.XX  |  N records"
- Each row: service color dot + name + region + period + amount right-aligned
- Loading/error/empty states follow existing templates

---

## What Is NOT Included

- ❌ **No new migrations** — `cost_records` table already exists
- ❌ **No aggregation** — raw records only; client can group if needed
- ❌ **No pagination** — consistent with other endpoints; use `days` param to limit
- ❌ **No write endpoints** — read-only (cost records are ingestion-only)
- ❌ **No CloudTrail integration** — deferred to Phase 4+ (see `docs/cloudtrail-analysis.md`)

---

## Testing

### Backend

```bash
make test                    # Unit tests pass
make test-storage            # Integration tests pass

# Manual endpoint tests
curl http://localhost:8080/v1/costs | jq .
curl "http://localhost:8080/v1/costs?days=7" | jq .
curl "http://localhost:8080/v1/costs?service=AWSCostExplorer" | jq .
curl "http://localhost:8080/v1/costs?account_id=123456789012&days=90" | jq .
```

### Frontend

```bash
make start-dev

# Open dashboard (Vite port 5173)
open http://localhost:5173
# Navigate to /cost
# Verify:
# - Data loads from API
# - Period buttons (7d/30d/90d) filter data
# - Service pills filter rows
# - Account selector filters
# - Loading state appears initially
# - Error/empty states work
```

---

## Verification Checklist

- [ ] Unit tests pass (`make test`)
- [ ] Storage tests pass (`make test-storage`)
- [ ] Backend endpoint returns correct data
- [ ] Endpoint respects `account_id`, `service`, `days` filters
- [ ] RLS enforces tenant isolation
- [ ] Frontend page loads at `/cost`
- [ ] Period buttons filter data correctly
- [ ] Service filter pills work
- [ ] Account selector filters
- [ ] Loading/error/empty states render
- [ ] Cost amounts display correctly

---

## Related Docs

- **Task tracking:** `tasks.md`
- **CloudTrail analysis:** `cloudtrail-analysis.md` (Phase 4+ feature)
- **AWS coverage:** `tmp/aws-coverage-and-cost-explorer-notes.md`
- **Store interface:** `services/shared/CLAUDE.md`
- **API conventions:** `services/api/CLAUDE.md`
- **Implementation plan:** `/Users/ahmed/.claude/plans/abstract-wibbling-cookie.md`
