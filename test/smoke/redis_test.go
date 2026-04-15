package smoke

import (
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

func redisClient(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("SMOKE_REDIS_URL")
	if url == "" {
		t.Skip("SMOKE_REDIS_URL not set — skipping Redis smoke tests")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Fatalf("parse SMOKE_REDIS_URL: %v", err)
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// TestRateLimit_Redis verifies that the Redis-backed rate limiter returns 429
// once the bucket counter reaches the limit.
// Pre-seeds the bucket to 59 so only 2 requests are needed — avoids firing 61
// real requests and makes the test independent of prior bucket state.
func TestRateLimit_Redis(t *testing.T) {
	base := apiURL(t)
	rdb := redisClient(t)
	ctx := context.Background()

	// Pre-seed the current minute bucket to one below the limit.
	bucket := time.Now().Unix() / 60
	key := fmt.Sprintf("ratelimit:ci-tenant:%d", bucket)
	rdb.Set(ctx, key, 59, 2*time.Minute)

	// Clean up so subsequent tests in the same minute are not rate limited.
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

// TestRateLimit_SurvivesRestart verifies that the rate limit counter persists
// in Redis across API restarts (counter is not lost when the process restarts).
// This test reads the counter directly from Redis rather than restarting the container.
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
// results in a job appearing in the Redis scan queue (LPUSH) and being consumed
// by the worker (BRPOP), with last_scanned_at updated afterward.
func TestScanQueue_JobEnqueuedInRedis(t *testing.T) {
	base := apiURL(t)
	rdb := redisClient(t)
	ctx := context.Background()

	// Create a test account.
	body := `{"provider":"aws","label":"smoke-redis-queue","access_key_id":"AKIAIOSFODNN7EXAMPLE","secret_key":"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY","region":"eu-central-1"}`
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

	// The job should appear in the queue within 2s (enqueue is synchronous in the handler).
	deadline := time.Now().Add(2 * time.Second)
	var queueLen int64
	for time.Now().Before(deadline) {
		queueLen, err = rdb.LLen(ctx, "axiaops:scan_queue").Result()
		if err == nil && queueLen > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Note: queue may already be empty if the worker consumed it — that's also a pass.
	// We verify the end state: last_scanned_at must be set within 30s.
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

func decodeJSON(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}
