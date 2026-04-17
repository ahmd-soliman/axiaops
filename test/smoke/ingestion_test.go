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
//     make test-smoke-ingestion
//
// The services stay running after the tests complete.
//
// # Running against a custom URL (e.g. staging)
//
//	SMOKE_INGESTION_URL=http://staging-ingestion:8081 make test-smoke-ingestion
//
package smoke

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func ingestionURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("SMOKE_INGESTION_URL")
	if u == "" {
		t.Skip("SMOKE_INGESTION_URL not set — skipping ingestion smoke tests")
	}
	return u
}

// TestIngestionHealth verifies the ingestion service health endpoint returns 200 with status ok.
func TestIngestionHealth(t *testing.T) {
	base := ingestionURL(t)
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

// TestIngestionScan verifies POST /scan returns 200 with DEV_MODE=true.
func TestIngestionScan(t *testing.T) {
	base := ingestionURL(t)
	
	// DEV_MODE=true should return success without real AWS credentials
	body := `{"account_id":"","tenant_id":"dev-tenant"}`
	resp, err := http.Post(base+"/scan", "application/json", bytes.NewBufferString(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST %s/scan: %v", base, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scan: want 200, got %d", resp.StatusCode)
	}
}
