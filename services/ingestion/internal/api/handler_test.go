package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/ingestion/internal/analyzer"
	"axiaops.io/ingestion/internal/api"
	"axiaops.io/ingestion/internal/model"
)

func testHandler() *api.Handler {
	zombies := []model.ZombieResource{
		{
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
		},
	}
	summary := analyzer.Summary{
		TotalZombies:         1,
		PotentialMonthlySave: 210.00,
		Currency:             "USD",
		ByService: map[string]analyzer.ServiceSummary{
			"AmazonRDS": {Zombies: 1, Savings: 210.00},
		},
	}
	noopIngest := func() ([]model.ZombieResource, analyzer.Summary, error) {
		return zombies, summary, nil
	}
	return api.New(zombies, summary, noopIngest)
}

// ── GET /zombies ──────────────────────────────────────────────────────────────

func TestGetZombies_Returns200(t *testing.T) {
	mux := http.NewServeMux()
	h := testHandler()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/zombies", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGetZombies_ContentType(t *testing.T) {
	mux := http.NewServeMux()
	h := testHandler()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/zombies", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}
}

func TestGetZombies_ReturnsZombieList(t *testing.T) {
	mux := http.NewServeMux()
	h := testHandler()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/zombies", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var zombies []model.ZombieResource
	if err := json.NewDecoder(w.Body).Decode(&zombies); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie, got %d", len(zombies))
	}
	if zombies[0].ResourceID != "db-stag-01" {
		t.Errorf("expected resource db-stag-01, got %s", zombies[0].ResourceID)
	}
	if zombies[0].MonthlyCost != 210.00 {
		t.Errorf("expected cost 210.00, got %f", zombies[0].MonthlyCost)
	}
}

func TestGetZombies_CORSHeader(t *testing.T) {
	mux := http.NewServeMux()
	h := testHandler()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/zombies", nil)
	w := httptest.NewRecorder()
	h.Handler(mux).ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected CORS header, got: %s", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestGetZombies_OPTIONSPreflight(t *testing.T) {
	mux := http.NewServeMux()
	h := testHandler()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodOptions, "/zombies", nil)
	w := httptest.NewRecorder()
	h.Handler(mux).ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
	}
}

// ── GET /summary ──────────────────────────────────────────────────────────────

func TestGetSummary_Returns200(t *testing.T) {
	mux := http.NewServeMux()
	h := testHandler()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/summary", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestGetSummary_ReturnsSavings(t *testing.T) {
	mux := http.NewServeMux()
	h := testHandler()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/summary", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var summary analyzer.Summary
	if err := json.NewDecoder(w.Body).Decode(&summary); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if summary.TotalZombies != 1 {
		t.Errorf("expected 1 zombie, got %d", summary.TotalZombies)
	}
	if summary.PotentialMonthlySave != 210.00 {
		t.Errorf("expected savings 210.00, got %f", summary.PotentialMonthlySave)
	}
	if summary.Currency != "USD" {
		t.Errorf("expected USD, got %s", summary.Currency)
	}
}
