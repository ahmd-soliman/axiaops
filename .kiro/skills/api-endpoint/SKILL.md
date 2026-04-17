---
name: api-endpoint
description: "Add a new HTTP API endpoint to the AxiaOps API service. Use this skill when someone wants to create a new route, handler, or REST endpoint. Also trigger when the conversation mentions adding a GET/POST/PATCH/DELETE route, extending the API, creating a new v1 endpoint, or working with the API handler pattern. Covers the full flow: handler method, route registration, Store interface updates, and tests."
---

# API Endpoint Skill

This skill guides you through adding a new HTTP endpoint to the AxiaOps API service. The API follows a consistent pattern — understanding it means every new endpoint is mechanical.

## Before You Start

Read these files to internalize the patterns:

- `services/api/internal/api/handler.go` — the `Handler` struct, `Register()`, and all route handlers
- `services/api/internal/api/handler_test.go` — how tests are structured
- `services/api/internal/api/test_helpers_test.go` — `MockStore` and helper functions
- `services/shared/storage/storage.go` — the `Store` interface

## The Handler Pattern

Every endpoint follows this lifecycle:

```
Constructor New(store) → Register(mux) → route handler methods
```

The `Handler` struct holds a `storage.Store` and any config (like `ingestionURL`). Routes are registered in `Register()` using Go 1.22+ method-pattern syntax:

```go
mux.HandleFunc("GET /v1/things", h.listThings)
mux.HandleFunc("POST /v1/things", h.createThing)
mux.HandleFunc("PATCH /v1/things/{id}", h.updateThing)
mux.HandleFunc("DELETE /v1/things/{id}", h.deleteThing)
```

### Handler Method Template

```go
func (h *Handler) listThings(w http.ResponseWriter, r *http.Request) {
    ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))

    things, err := h.store.ListThings(ctx)
    if err != nil {
        slog.Error("listThings: query failed", "error", err)
        writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
        return
    }

    writeJSON(w, http.StatusOK, things)
}
```

Key conventions:
- Always extract tenant ID from context: `storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))`
- Use `slog.Error/Info/Warn` for logging — never `log.Printf`
- Use `writeJSON()` helper for all JSON responses — never raw `json.NewEncoder`
- Error format: `"handlerName: what failed"`
- Path parameters via `r.PathValue("id")` (Go 1.22+ ServeMux)
- Parse request body with `json.NewDecoder(r.Body).Decode(&req)` and validate

### For POST/PATCH endpoints with a request body:

```go
func (h *Handler) createThing(w http.ResponseWriter, r *http.Request) {
    ctx := storage.WithTenantID(r.Context(), middleware.TenantID(r.Context()))

    var req struct {
        Name   string `json:"name"`
        Value  int    `json:"value"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
        return
    }
    if req.Name == "" {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
        return
    }

    // ... store call and response
}
```

## Step-by-Step

### 1. Add the Store method (if needed)

If the endpoint needs new data access, add a method to the `Store` interface in `services/shared/storage/storage.go`:

```go
// ListThings returns all things for the tenant in ctx.
ListThings(ctx context.Context) ([]model.Thing, error)
```

Then implement it in `services/shared/storage/postgres/postgres.go`. Remember:
- RLS is enforced via `SET app.tenant_id` — the implementation calls `s.setTenant(ctx, tx)` inside transactions
- Use `defer tx.Rollback(ctx)` immediately after `Begin()`
- Wrap errors: `fmt.Errorf("postgres: ListThings: %w", err)`

### 2. Add the domain model (if needed)

If the endpoint deals with a new entity, add a struct in `services/shared/model/`:

```go
type Thing struct {
    ID        string    `json:"id"`
    TenantID  string    `json:"-"` // never expose tenant_id in JSON
    Name      string    `json:"name"`
    CreatedAt time.Time `json:"created_at"`
}
```

Note: `TenantID` uses `json:"-"` to prevent leaking tenant isolation details.

### 3. Write the handler method

Add it to `services/api/internal/api/handler.go` following the template above.

### 4. Register the route

Add the route in `Register()`:

```go
mux.HandleFunc("GET /v1/things", h.listThings)
```

### 5. Write tests

In `handler_test.go`, follow the existing pattern:

```go
func TestListThings_Returns200(t *testing.T) {
    store := NewMockStore().WithThings([]model.Thing{{ID: "t1", Name: "test"}})
    h := api.New(store)
    mux := http.NewServeMux()
    h.Register(mux)

    w := httptest.NewRecorder()
    mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/v1/things"))

    if w.Code != http.StatusOK {
        t.Errorf("expected 200, got %d", w.Code)
    }
}
```

Update `test_helpers_test.go` to add mock methods for any new Store interface methods.

### 6. Run all tests

```bash
make test
```

## Checklist

- [ ] Store interface method added (if needed) in `storage.go`
- [ ] PostgreSQL implementation added in `postgres.go`
- [ ] Domain model added (if needed) in `services/shared/model/`
- [ ] Handler method written in `handler.go`
- [ ] Route registered in `Register()`
- [ ] Tests written in `handler_test.go`
- [ ] MockStore updated in `test_helpers_test.go`
- [ ] `make test` passes
