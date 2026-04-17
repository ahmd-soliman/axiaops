// Package integration contains integration tests that verify component interaction.
//
// These tests require a live stack (PostgreSQL, Redis) and may modify state
// (create accounts, trigger scans, etc.).
//
// # Running
//
// make test-integration
//
// # Running against a custom URL
//
//	INTEGRATION_INGESTION_URL=http://localhost:8081 make test-integration
//
package integration

import (
	"bytes"
	"net/http"
	"os"
	"testing"
)

func ingestionURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("INTEGRATION_INGESTION_URL")
	if u == "" {
		t.Skip("INTEGRATION_INGESTION_URL not set — skipping ingestion integration tests")
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
	if err := decodeJSON(resp.Body, &body); err != nil {
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
	body := `{"account_id":"","tenant_id":"ci-tenant"}`
	resp, err := http.Post(base+"/scan", "application/json", bytes.NewBufferString(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST %s/scan: %v", base, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scan: want 200, got %d", resp.StatusCode)
	}
}
