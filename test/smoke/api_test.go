// Package smoke contains smoke tests that run against a live stack.
//
// # Setup
//
// Smoke tests are intentionally outside go.work (GOWORK=off in make test-smoke).
// This prevents `go test` from recompiling the workspace services, which would
// kill any running `go run` processes.
//
// # Running locally
//
//  1. Start the stack in one terminal (keep it open — it blocks on `wait`):
//
//     make start-dev
//
//  2. In a second terminal, run the smoke tests:
//
//     make test-smoke
//
// The services stay running after the tests complete.
//
// # Running against a custom URL (e.g. staging)
//
//	SMOKE_API_URL=https://staging.example.com make test-smoke
//
// # CI
//
// Smoke tests are skipped in normal CI (`make test`) because SMOKE_API_URL is
// unset. They run only in a dedicated smoke-test CI job against a live stack.
package smoke

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
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

// TestScheduledAutoScan_ZeroInterval creates an account with scan_interval_hours=0,
// then polls until last_scanned_at is set (up to 60s), verifying the scheduler triggers a scan.
//
// Requires a running stack (make start-dev) and DEV_MODE=true (no real AWS credentials needed).
// The ticker fires every 60 minutes in production, but in dev mode the scan completes quickly once triggered.
func TestScheduledAutoScan_ZeroInterval(t *testing.T) {
	base := apiURL(t)

	// Create a test account with scan_interval_hours=0 (always eligible).
	body := `{"provider":"aws","label":"smoke-test-scheduler","access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY","region":"eu-central-1"}`
	resp, err := http.Post(base+"/v1/accounts", "application/json", strings.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /v1/accounts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/accounts: want 201, got %d", resp.StatusCode)
	}

	var account map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&account); err != nil {
		t.Fatalf("POST /v1/accounts: decode: %v", err)
	}
	id, _ := account["id"].(string)
	if id == "" {
		t.Fatal("POST /v1/accounts: missing id in response")
	}

	// Set scan_interval_hours=0 so the scheduler considers it always overdue.
	patch := `{"scan_interval_hours":0}`
	req, _ := http.NewRequest(http.MethodPatch, base+"/v1/accounts/"+id, strings.NewReader(patch)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	patchResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH /v1/accounts/%s: %v", id, err)
	}
	patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /v1/accounts/%s: want 200, got %d", id, patchResp.StatusCode)
	}

	// Clean up the account after the test regardless of outcome.
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodDelete, base+"/v1/accounts/"+id, nil) //nolint:noctx
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	})

	// Poll until last_scanned_at is set (scheduler triggered a scan) or timeout.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)

		r := get(t, base+"/v1/accounts/"+id)
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("GET /v1/accounts/%s: want 200, got %d", id, r.StatusCode)
		}
		var acc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&acc); err != nil {
			t.Fatalf("GET /v1/accounts/%s: decode: %v", id, err)
		}
		if acc["last_scanned_at"] != nil {
			return // scan was triggered — pass
		}
	}

	t.Fatalf("scheduler did not trigger a scan within 30s for account %s (scan_interval_hours=0)", id)
}
