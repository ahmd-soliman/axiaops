// Package smoke contains smoke tests that run against a live stack.
//
// Prerequisites: the full Docker Compose stack must be running in dev mode:
//
//	make start-dev
//
// Set SMOKE_API_URL to the API base URL before running:
//
//	SMOKE_API_URL=http://localhost:8080 go test ./test/smoke/... -v
//
// All tests are skipped when SMOKE_API_URL is unset, so they never run during
// normal `make test` or CI unit-test jobs.
package smoke

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func apiURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("SMOKE_API_URL")
	if u == "" {
		t.Skip("SMOKE_API_URL not set — skipping smoke tests")
	}
	return u
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// TestHealth verifies the health endpoint returns 200 with status ok.
func TestHealth(t *testing.T) {
	base := apiURL(t)
	resp := get(t, base+"/health")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health: want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET /health: decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("GET /health: want status=ok, got %v", body["status"])
	}
}

// TestGhosts verifies GET /v1/ghosts returns 200 with a JSON array.
func TestGhosts(t *testing.T) {
	base := apiURL(t)
	resp := get(t, base+"/v1/ghosts")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/ghosts: want 200, got %d", resp.StatusCode)
	}
	var body []any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET /v1/ghosts: decode: %v", err)
	}
}

// TestSummary verifies GET /v1/summary returns 200 with total_monthly_cost field.
func TestSummary(t *testing.T) {
	base := apiURL(t)
	resp := get(t, base+"/v1/summary")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/summary: want 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET /v1/summary: decode: %v", err)
	}
	if _, ok := body["potential_monthly_savings"]; !ok {
		t.Fatalf("GET /v1/summary: missing potential_monthly_savings field, got %v", body)
	}
}

// TestResources verifies GET /v1/resources returns 200 with a JSON array.
func TestResources(t *testing.T) {
	base := apiURL(t)
	resp := get(t, base+"/v1/resources")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/resources: want 200, got %d", resp.StatusCode)
	}
	var body []any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET /v1/resources: decode: %v", err)
	}
}

// TestTrend verifies GET /v1/trend returns 200 with a JSON array.
func TestTrend(t *testing.T) {
	base := apiURL(t)
	resp := get(t, base+"/v1/trend")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/trend: want 200, got %d", resp.StatusCode)
	}
	var body []any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET /v1/trend: decode: %v", err)
	}
}

// TestAccounts verifies GET /v1/accounts returns 200 with a JSON array.
func TestAccounts(t *testing.T) {
	base := apiURL(t)
	resp := get(t, base+"/v1/accounts")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/accounts: want 200, got %d", resp.StatusCode)
	}
	var body []any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("GET /v1/accounts: decode: %v", err)
	}
}

// TestMetrics verifies the Prometheus /metrics endpoint returns 200.
func TestMetrics(t *testing.T) {
	base := apiURL(t)
	resp := get(t, base+"/metrics")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics: want 200, got %d", resp.StatusCode)
	}
}
