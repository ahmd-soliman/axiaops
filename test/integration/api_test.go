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

// rateLimitKey returns the Redis cache key the api binary writes for the CI
// stack's DEV_MODE identity. Mirrors middleware.RateLimiter.Wrap's subject
// composition: per-user keying `{org_id}:{user_id}:{bucket}` when both are
// set in context (DevBypass always populates both). DEV_USER_ID is unset
// in the CI compose, so cmd/main.go falls back to the documented default
// "dev-user-axiaops". Keep this helper in sync with both — drift here
// silently hides a real rate-limit regression.
func rateLimitKey(bucket int64) string {
	return fmt.Sprintf("ratelimit:ci-tenant:dev-user-axiaops:%d", bucket)
}

// TestRateLimit_Redis verifies that the Redis-backed rate limiter returns 429
// once the bucket counter reaches the limit. The CI stack sets
// RATE_LIMIT_MAX=60 so we can drain in 60 requests rather than the
// production default of 1000 — keeps the test fast without losing the
// "boundary hit returns 429" coverage.
func TestRateLimit_Redis(t *testing.T) {
	base := apiURL(t)
	rdb := redisClient(t)
	ctx := context.Background()

	// Pre-seed the current minute bucket to one below the limit.
	bucket := time.Now().Unix() / 60
	key := rateLimitKey(bucket)
	rdb.Set(ctx, key, 59, 2*time.Minute)
	t.Cleanup(func() { rdb.Del(ctx, key) })

	// Request 60 — should pass.
	resp := get(t, base+"/v1/zombies")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request 60: want 200, got %d", resp.StatusCode)
	}

	// Request 61 — should be rate limited.
	resp = get(t, base+"/v1/zombies")
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
	resp, err := http.Get(base + "/v1/zombies") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /v1/zombies: %v", err)
	}
	_ = resp.Body.Close()

	// Verify a ratelimit key exists in Redis for the ci-tenant.
	bucket := time.Now().Unix() / 60
	key := rateLimitKey(bucket)
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

	// Drain any scheduler-triggered scan before our manual POST. SCAN_INTERVAL=10s
	// in the integration compose file plus an immediate scheduler tick on ingestion
	// startup means a fresh account with last_scanned_at=nil is "overdue" the
	// moment it's created (see scanScheduledAccounts in
	// services/ingestion/cmd/main.go) — the scheduler enqueues a scan, the worker
	// flips status='scanning' via TryMarkAccountScanning, and our subsequent
	// POST /scan would 409. Wait until status leaves 'scanning' (bad fake AWS
	// creds make runScan fail fast and flip to 'error' within a few hundred ms),
	// then issue the test's POST. If the scheduler tick lands in the small
	// millisecond window between our poll exiting and our POST landing, we
	// retry once after a second poll-wait.
	waitNotScanning := func(deadline time.Duration) string {
		end := time.Now().Add(deadline)
		var lastStatus string
		for time.Now().Before(end) {
			r := get(t, base+"/v1/accounts/"+id)
			var acc map[string]any
			_ = decodeJSON(r.Body, &acc)
			r.Body.Close()
			lastStatus, _ = acc["status"].(string)
			if lastStatus != "scanning" {
				return lastStatus
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Logf("waitNotScanning: timed out after %s with status=%q for account %s", deadline, lastStatus, id)
		return lastStatus
	}
	waitNotScanning(30 * time.Second)

	// Flush the queue before triggering so we get a clean signal.
	_ = rdb.Del(ctx, "axiaops:scan_queue")

	// Trigger a scan. Retry once on 409 — the scheduler tick may have raced
	// us into the lock between waitNotScanning and this POST.
	postScan := func() *http.Response {
		r, err := http.Post(base+"/v1/accounts/"+id+"/scan", "application/json", nil) //nolint:noctx
		if err != nil {
			t.Fatalf("POST /v1/accounts/%s/scan: %v", id, err)
		}
		return r
	}
	scanResp := postScan()
	if scanResp.StatusCode == http.StatusConflict {
		_ = scanResp.Body.Close()
		t.Logf("POST scan: 409 conflict on first attempt — scheduler race; draining and retrying once")
		waitNotScanning(30 * time.Second)
		scanResp = postScan()
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
		var acc map[string]any
		decodeErr := decodeJSON(r.Body, &acc)
		r.Body.Close()
		if decodeErr != nil {
			t.Fatalf("decode account: %v", decodeErr)
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
		status := r.StatusCode
		var acc map[string]any
		decodeErr := decodeJSON(r.Body, &acc)
		r.Body.Close()
		if status != http.StatusOK {
			t.Fatalf("GET /v1/accounts/%s: want 200, got %d", id, status)
		}
		if decodeErr != nil {
			t.Fatalf("GET /v1/accounts/%s: decode: %v", id, decodeErr)
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

// TestZombies verifies GET /v1/zombies returns 200 with a JSON array.
func TestZombies(t *testing.T) {
	base := apiURL(t)
	resp := get(t, base+"/v1/zombies")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/zombies: want 200, got %d", resp.StatusCode)
	}
	var body []any
	if err := decodeJSON(resp.Body, &body); err != nil {
		t.Fatalf("GET /v1/zombies: decode: %v", err)
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
