// Package smoke contains smoke tests that run against a live stack.
//
// Smoke tests verify services are running and responding. They should be
// read-only and not require database seed data or external state.
//
// # Running
//
// make test-smoke
//
// # Running against a custom URL
//
//	SMOKE_API_URL=https://staging.example.com make test-smoke
//
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

// TestHealth verifies the API service health endpoint returns 200 with status ok.
func TestHealth(t *testing.T) {
	base := apiURL(t)
	resp, err := http.Get(base + "/health") //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s/health: %v", base, err)
	}
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

// TestMetrics verifies the Prometheus /metrics endpoint returns 200.
func TestMetrics(t *testing.T) {
	base := apiURL(t)
	resp, err := http.Get(base + "/metrics") //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s/metrics: %v", base, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics: want 200, got %d", resp.StatusCode)
	}
}
