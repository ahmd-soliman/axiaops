package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/model"
)

var testCostRecords = []model.CostRecord{
	{
		Provider:    "aws",
		AccountID:   "123456789012",
		Service:     "AmazonEC2",
		Region:      "eu-central-1",
		ResourceID:  "i-0abc123",
		Amount:      42.50,
		Currency:    "USD",
		PeriodStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC),
	},
	{
		Provider:    "aws",
		AccountID:   "123456789012",
		Service:     "AmazonRDS",
		Region:      "eu-central-1",
		ResourceID:  "db-prod-01",
		Amount:      120.00,
		Currency:    "USD",
		PeriodStart: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC),
	},
}

// ── GET /v1/costs ────────────────────────────────────────────────────────────

func TestListCosts_Returns200(t *testing.T) {
	store := NewMockStore().WithCostRecords(testCostRecords)
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/costs"))

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestListCosts_ContentType(t *testing.T) {
	store := NewMockStore().WithCostRecords(testCostRecords)
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/costs"))

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}
}

func TestListCosts_ReturnsCostRecords(t *testing.T) {
	store := NewMockStore().WithCostRecords(testCostRecords)
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/costs"))

	var costs []model.CostRecord
	if err := json.NewDecoder(w.Body).Decode(&costs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(costs) != 2 {
		t.Fatalf("expected 2 cost records, got %d", len(costs))
	}
	if costs[0].Service != "AmazonEC2" {
		t.Errorf("expected AmazonEC2, got %s", costs[0].Service)
	}
	if costs[1].Amount != 120.00 {
		t.Errorf("expected 120.00, got %f", costs[1].Amount)
	}
}

func TestListCosts_EmptyStoreReturnsEmptyArray(t *testing.T) {
	store := NewMockStore()
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/costs"))

	var costs []model.CostRecord
	if err := json.NewDecoder(w.Body).Decode(&costs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if costs == nil {
		t.Error("expected non-nil empty array, got JSON null")
	}
	if len(costs) != 0 {
		t.Errorf("expected 0 records, got %d", len(costs))
	}
}

func TestListCosts_StoreError_Returns500(t *testing.T) {
	store := NewMockStore().WithListCostRecordsError(errors.New("db connection lost"))
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/costs"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestListCosts_ServiceFilter(t *testing.T) {
	store := NewMockStore().WithCostRecords(testCostRecords)
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/costs?service=AmazonEC2"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	filter := store.GetLastCostFilter()
	if filter.Service != "AmazonEC2" {
		t.Errorf("expected service filter AmazonEC2, got %q", filter.Service)
	}
}

func TestListCosts_DaysFilter(t *testing.T) {
	store := NewMockStore().WithCostRecords(testCostRecords)
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/costs?days=90"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	filter := store.GetLastCostFilter()
	if filter.Days != 90 {
		t.Errorf("expected days filter 90, got %d", filter.Days)
	}
}

func TestListCosts_SinceUntilFilter(t *testing.T) {
	// The Custom… date picker sends since/until (ISO dates) — these must reach
	// the store filter as an absolute window so a fixed calendar range no longer
	// degrades to a trailing "last N days" fetch.
	store := NewMockStore().WithCostRecords(testCostRecords)
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/costs?since=2026-05-01&until=2026-05-15"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	filter := store.GetLastCostFilter()
	wantSince := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	wantUntil := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	if !filter.Since.Equal(wantSince) {
		t.Errorf("expected since %s, got %s", wantSince, filter.Since)
	}
	if !filter.Until.Equal(wantUntil) {
		t.Errorf("expected until %s, got %s", wantUntil, filter.Until)
	}
}

func TestListCosts_MalformedSinceIgnored(t *testing.T) {
	// A garbage since= must not 500 or leak a bogus time; it's simply ignored
	// and the request falls back to the trailing window.
	store := NewMockStore().WithCostRecords(testCostRecords)
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/costs?since=not-a-date&days=7"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	filter := store.GetLastCostFilter()
	if !filter.Since.IsZero() {
		t.Errorf("expected zero since on malformed input, got %s", filter.Since)
	}
	if filter.Days != 7 {
		t.Errorf("expected fallback days 7, got %d", filter.Days)
	}
}

func TestListCosts_AccountIDFilter_SetsInternalAccountID(t *testing.T) {
	// account_id is always treated as the internal account UUID, verbatim --
	// no AWS-account-ID lookup or fallback. Two account rows can share the
	// same underlying AWS account number (e.g. the same AWS account
	// connected twice under different billing sources), so filtering must
	// key off this UUID alone.
	store := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-uuid-1", AccountID: "123456789012", Provider: "aws"},
		}).
		WithCostRecords(testCostRecords)
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/costs?account_id=acc-uuid-1"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	filter := store.GetLastCostFilter()
	if filter.InternalAccountID != "acc-uuid-1" {
		t.Errorf("expected InternalAccountID acc-uuid-1, got %q", filter.InternalAccountID)
	}
}

func TestListCosts_NoAccountID_LeavesFilterEmpty(t *testing.T) {
	store := NewMockStore().WithCostRecords(testCostRecords)
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/costs"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	filter := store.GetLastCostFilter()
	if filter.InternalAccountID != "" {
		t.Errorf("expected empty InternalAccountID, got %q", filter.InternalAccountID)
	}
}
