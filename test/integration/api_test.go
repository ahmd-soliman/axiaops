//go:build integration

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
//	INTEGRATION_API_URL=https://staging.example.com INTEGRATION_REDIS_URL=redis://localhost:6379 make test-integration
//
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func apiURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("INTEGRATION_API_URL")
	if u == "" {
		t.Skip("INTEGRATION_API_URL not set — skipping integration tests")
	}
	return u
}

func redisClient(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("INTEGRATION_REDIS_URL")
	if url == "" {
		t.Skip("INTEGRATION_REDIS_URL not set — skipping Redis integration tests")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse INTEGRATION_REDIS_URL: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func decodeJSON(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

// TestRateLimit_Redis verifies that the Redis-backed rate limiter returns 429
// once the bucket counter reaches the limit.
func TestRateLimit_Redis(t *testing.T) {
	base := apiURL(t)
	rdb := redisClient(t)
	ctx := context.Background()

	// Pre-seed the current minute bucket to one below the limit.
	bucket := time.Now().Unix() / 60
	key := fmt.Sprintf("ratelimit:ci-tenant:%d", bucket)
	rdb.Set(ctx, key, 59, 2*time.Minute)
	t.Cleanup(func() { rdb.Del(ctx, key) })

	// Request 60 — should pass.
	resp := get(t, base+"/v1/ghosts")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request 60: want 200, got %d", resp.StatusCode)
	}

	// Request 61 — should be rate limited.
	resp = get(t, base+"/v1/ghosts")
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("request 61: want 429, got %d", resp.StatusCode)
	}
}

// TestRateLimit_CounterInRedis verifies that the rate limit counter persists in Redis.
func TestRateLimit_CounterInRedis(t *testing.T) {
	base := apiURL(t)
	rdb := redisClient(t)
	ctx := context.Background()

	// Make a request to ensure a bucket key exists.
	resp, err := http.Get(base + "/v1/ghosts") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /v1/ghosts: %v", err)
	}
	_ = resp.Body.Close()

	// Verify a ratelimit key exists in Redis for the ci-tenant.
	bucket := time.Now().Unix() / 60
	key := fmt.Sprintf("ratelimit:ci-tenant:%d", bucket)
	val, err := rdb.Get(ctx, key).Int64()
	if err != nil {
		t.Fatalf("expected ratelimit key %q in Redis, got error: %v", key, err)
	}
	if val < 1 {
		t.Fatalf("expected counter >= 1, got %d", val)
	}
}

// TestScanQueue_JobEnqueuedInRedis verifies that triggering a scan via the API
// results in a job appearing in the Redis scan queue and being consumed by the worker.
func TestScanQueue_JobEnqueuedInRedis(t *testing.T) {
	base := apiURL(t)
	rdb := redisClient(t)
	ctx := context.Background()

	// Create a test account.
	body := `{"provider":"aws","label":"integration-queue-test","access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY","region":"eu-central-1"}`
	resp, err := http.Post(base+"/v1/accounts", "application/json", strings.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /v1/accounts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/accounts: want 201, got %d", resp.StatusCode)
	}
	var account map[string]any
	if err := decodeJSON(resp.Body, &account); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	id, _ := account["id"].(string)
	if id == "" {
		t.Fatal("missing account id")
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodDelete, base+"/v1/accounts/"+id, nil) //nolint:noctx
		r, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = r.Body.Close()
		}
	})

	// Flush the queue before triggering so we get a clean signal.
	_ = rdb.Del(ctx, "axiaops:scan_queue")

	// Trigger a scan.
	scanResp, err := http.Post(base+"/v1/accounts/"+id+"/scan", "application/json", nil) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /v1/accounts/%s/scan: %v", id, err)
	}
	_ = scanResp.Body.Close()
	if scanResp.StatusCode != http.StatusOK {
		t.Fatalf("POST scan: want 200, got %d", scanResp.StatusCode)
	}

	// Verify the job appears in the queue within 2s.
	// Note: worker may consume it immediately, so also check if scan completed.
	deadline := time.Now().Add(5 * time.Second)
	var queueLen int64
	var jobFound bool
	for time.Now().Before(deadline) {
		queueLen, err = rdb.LLen(ctx, "axiaops:scan_queue").Result()
		if err == nil && queueLen > 0 {
			jobFound = true
			break
		}
		// Check if worker already processed it
		r := get(t, base+"/v1/accounts/"+id)
		var acc map[string]any
		if decodeJSON(r.Body, &acc) == nil && acc["last_scanned_at"] != nil {
			jobFound = true
			r.Body.Close()
			break
		}
		r.Body.Close()
		time.Sleep(100 * time.Millisecond)
	}
	if !jobFound {
		t.Fatal("scan job not found in Redis queue and not processed within 5s")
	}

	// Verify worker consumed the job by checking last_scanned_at is set.
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		r := get(t, base+"/v1/accounts/"+id)
		defer r.Body.Close()
		var acc map[string]any
		if err := decodeJSON(r.Body, &acc); err != nil {
			t.Fatalf("decode account: %v", err)
		}
		if acc["last_scanned_at"] != nil {
			return // worker consumed the job and scan completed
		}
	}
	t.Fatalf("worker did not process scan job within 30s for account %s", id)
}

// TestScheduledAutoScan_ZeroInterval verifies the scheduler triggers a scan for accounts with scan_interval_hours=0.
func TestScheduledAutoScan_ZeroInterval(t *testing.T) {
	base := apiURL(t)

	// Create a test account with scan_interval_hours=0 (always eligible).
	body := `{"provider":"aws","label":"integration-scheduler-test","access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY","region":"eu-central-1"}`
	resp, err := http.Post(base+"/v1/accounts", "application/json", strings.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /v1/accounts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/accounts: want 201, got %d", resp.StatusCode)
	}

	var account map[string]any
	if err := decodeJSON(resp.Body, &account); err != nil {
		t.Fatalf("POST /v1/accounts: decode: %v", err)
	}
	id, _ := account["id"].(string)
	if id == "" {
		t.Fatal("POST /v1/accounts: missing id in response")
	}

	// Set scan_interval_hours=0 so the scheduler considers it always overdue.
	patch := `{"scan_interval_hours":0}`
	req, _ := http.NewRequest(http.MethodPatch, base+"/v1/accounts/"+id, bytes.NewReader([]byte(patch))) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	patchResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH /v1/accounts/%s: %v", id, err)
	}
	patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /v1/accounts/%s: want 200, got %d", id, patchResp.StatusCode)
	}

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
		if err := decodeJSON(r.Body, &acc); err != nil {
			t.Fatalf("GET /v1/accounts/%s: decode: %v", id, err)
		}
		if acc["last_scanned_at"] != nil {
			return // scan was triggered — pass
		}
	}

	t.Fatalf("scheduler did not trigger a scan within 30s for account %s (scan_interval_hours=0)", id)
}

// TestAccounts creates an account and verifies it appears in the list.
func TestAccounts(t *testing.T) {
	base := apiURL(t)

	// Create a test account.
	body := `{"provider":"aws","label":"integration-account-test","access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY","region":"eu-central-1"}`
	resp, err := http.Post(base+"/v1/accounts", "application/json", strings.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST /v1/accounts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/accounts: want 201, got %d", resp.StatusCode)
	}

	var account map[string]any
	if err := decodeJSON(resp.Body, &account); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	id, _ := account["id"].(string)
	if id == "" {
		t.Fatal("missing account id")
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest(http.MethodDelete, base+"/v1/accounts/"+id, nil) //nolint:noctx
		r, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = r.Body.Close()
		}
	})

	// Verify account appears in list.
	r := get(t, base+"/v1/accounts")
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/accounts: want 200, got %d", r.StatusCode)
	}

	var accounts []map[string]any
	if err := decodeJSON(r.Body, &accounts); err != nil {
		t.Fatalf("decode accounts: %v", err)
	}

	found := false
	for _, acc := range accounts {
		if acc["id"] == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("account %s not found in list", id)
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
	if err := decodeJSON(resp.Body, &body); err != nil {
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
	if err := decodeJSON(resp.Body, &body); err != nil {
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
	if err := decodeJSON(resp.Body, &body); err != nil {
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
	if err := decodeJSON(resp.Body, &body); err != nil {
		t.Fatalf("GET /v1/trend: decode: %v", err)
	}
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}
