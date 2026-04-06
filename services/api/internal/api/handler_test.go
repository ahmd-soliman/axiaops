package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// stubStore is an in-memory Store for handler tests.
type stubStore struct {
	ghosts []model.GhostResource
}

func (s *stubStore) Save(_ context.Context, _ []model.CostRecord) (int64, error) { return 0, nil }
func (s *stubStore) SaveGhosts(_ context.Context, g []model.GhostResource) error {
	s.ghosts = g
	return nil
}
func (s *stubStore) LoadGhosts(_ context.Context) ([]model.GhostResource, error) {
	return s.ghosts, nil
}
func (s *stubStore) UpsertTenant(_ context.Context, _, _ string) (model.Tenant, error) {
	return model.Tenant{}, nil
}
func (s *stubStore) UpsertUser(_ context.Context, _, _, _, _ string) (model.User, error) {
	return model.User{}, nil
}
func (s *stubStore) Close() error { return nil }

var testGhost = model.GhostResource{
	Provider:    "aws",
	AccountID:   "000000000000",
	Service:     "AmazonRDS",
	Region:      "eu-central-1",
	ResourceID:  "db-stag-01",
	Tags:        map[string]string{"team": "backend", "env": "staging"},
	MonthlyCost: 210.00,
	Currency:    "USD",
	PeriodStart: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	PeriodEnd:   time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
	UsageMetric: "DatabaseConnections",
	UsageAvg:    0,
	UsageUnit:   "Count",
	Reason:      "RDS instance has zero database connections — likely abandoned",
	Owner:       "backend",
}

func testHandler() (*api.Handler, *http.ServeMux) {
	store := &stubStore{ghosts: []model.GhostResource{testGhost}}
	h := api.New(store)
	mux := http.NewServeMux()
	h.Register(mux)
	return h, mux
}

// tenantRequest creates a request with tenant_id in context (simulates auth middleware).
func tenantRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	ctx := storage.WithTenantID(r.Context(), "tenant-test-uuid")
	return r.WithContext(ctx)
}

// ── GET /ghosts ───────────────────────────────────────────────────────────────

func TestGetGhosts_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/ghosts"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGetGhosts_ContentType(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/ghosts"))
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}
}

func TestGetGhosts_ReturnsGhostList(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/ghosts"))

	var ghosts []model.GhostResource
	if err := json.NewDecoder(w.Body).Decode(&ghosts); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(ghosts) != 1 {
		t.Fatalf("expected 1 ghost, got %d", len(ghosts))
	}
	if ghosts[0].ResourceID != "db-stag-01" {
		t.Errorf("expected resource db-stag-01, got %s", ghosts[0].ResourceID)
	}
	if ghosts[0].MonthlyCost != 210.00 {
		t.Errorf("expected cost 210.00, got %f", ghosts[0].MonthlyCost)
	}
}

func TestGetGhosts_CORSHeader(t *testing.T) {
	h, mux := testHandler()
	w := httptest.NewRecorder()
	h.Handler(mux).ServeHTTP(w, tenantRequest(http.MethodGet, "/ghosts"))
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS header, got: %s", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestGetGhosts_OPTIONSPreflight(t *testing.T) {
	h, mux := testHandler()
	w := httptest.NewRecorder()
	h.Handler(mux).ServeHTTP(w, httptest.NewRequest(http.MethodOptions, "/ghosts", nil))
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
	}
}

// ── GET /summary ──────────────────────────────────────────────────────────────

func TestGetSummary_Returns200(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/summary"))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGetSummary_ReturnsSavings(t *testing.T) {
	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, tenantRequest(http.MethodGet, "/summary"))

	var summary analyzer.Summary
	if err := json.NewDecoder(w.Body).Decode(&summary); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if summary.TotalGhosts != 1 {
		t.Errorf("expected 1 ghost, got %d", summary.TotalGhosts)
	}
	if summary.PotentialMonthlySave != 210.00 {
		t.Errorf("expected savings 210.00, got %f", summary.PotentialMonthlySave)
	}
	if summary.Currency != "USD" {
		t.Errorf("expected USD, got %s", summary.Currency)
	}
}
