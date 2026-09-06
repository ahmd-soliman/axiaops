// Package sync provides a synchronous HTTP fallback implementation of queue.Queue.
// Enqueue fires a POST /scan to the ingestion service directly.
// Dequeue blocks forever (never returns) — the worker loop is a no-op in sync mode.
package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"axiaops.io/shared/httpauth"
)

// ScanJob mirrors queue.ScanJob to avoid an import cycle. Field order must
// stay aligned with the sibling structs in queue/queue.go and queue/redis/redis.go —
// the parent package uses keyed conversion (see queue.toSync) but consistency
// helps anyone reading the three side by side.
type ScanJob struct {
	OrganizationID string    `json:"organization_id"`
	AccountID      string    `json:"account_id"`
	EnqueuedAt     time.Time `json:"enqueued_at"`
	RequestID      string    `json:"request_id"`
	Timestamp      int64     `json:"timestamp,omitempty"`
	Signature      string    `json:"signature,omitempty"`
}

// Queue is a synchronous HTTP-based scan queue fallback.
type Queue struct {
	ingestionURL string
	client       *http.Client
	// secret signs outbound POST /scan requests via the C-1 HMAC scheme.
	// nil ⇒ DEV_MODE; the constructor skips the header set and the
	// receiving ingestion middleware is in passthrough.
	secret []byte
}

// clientTimeout bounds every Enqueue call. For this backend, Enqueue *is* the
// scan — a blocking POST /scan that only returns once ingestion finishes —
// not a lightweight queue push, so this has to be long enough for a real
// scan rather than a normal HTTP round trip. It was previously 30s, which
// fired on real accounts with enough resources to discover well before a
// legitimate scan could finish, producing a false scan.enqueue_failed even
// though ingestion completed the scan successfully moments later.
//
// This can't simply be removed in favor of relying on the caller's context:
// scanAccount's goroutine (services/api/internal/api/handler.go) does supply
// one (scanEnqueueTimeout, kept equal to this value below), but ingestion's
// own scanScheduledAccounts sweep (cmd/main.go) calls Enqueue with
// context.Background() — no deadline at all. That sweep loops over every
// overdue account making one blocking Enqueue call at a time; an unbounded
// client would let one hung account stall every account behind it in that
// loop indefinitely. A generous, bounded client timeout is the backstop for
// that path; scanAccount's own context still governs the HTTP-triggered path.
const clientTimeout = 15 * time.Minute

// New creates a sync Queue that POSTs to the given ingestion service URL.
// secret is the shared HMAC secret loaded from INGESTION_SHARED_SECRET; pass
// nil in DEV_MODE.
func New(ingestionURL string, secret []byte) *Queue {
	return &Queue{
		ingestionURL: ingestionURL,
		client:       &http.Client{Timeout: clientTimeout},
		secret:       secret,
	}
}

// Enqueue fires a POST /scan to the ingestion service synchronously, signing
// the request when a shared secret is configured. The body shape matches what
// the receiver decodes — only account_id + organization_id are needed at the
// HTTP hop; envelope fields (Timestamp, Signature, RequestID, EnqueuedAt) are
// the Redis path's concern.
func (q *Queue) Enqueue(ctx context.Context, job ScanJob) error {
	body, err := json.Marshal(map[string]string{
		"account_id":      job.AccountID,
		"organization_id": job.OrganizationID,
	})
	if err != nil {
		return err
	}
	const path = "/scan"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.ingestionURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if len(q.secret) > 0 {
		ts := time.Now()
		sig := httpauth.Sign(q.secret, ts, http.MethodPost, path, body)
		req.Header.Set(httpauth.HeaderTimestamp, strconv.FormatInt(ts.Unix(), 10))
		req.Header.Set(httpauth.HeaderSignature, sig)
	}
	resp, err := q.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("queue: sync enqueue: ingestion returned %d", resp.StatusCode)
	}
	return nil
}

// Dequeue blocks forever — in sync mode the worker loop is a no-op.
func (q *Queue) Dequeue(ctx context.Context) (ScanJob, error) {
	<-ctx.Done()
	return ScanJob{}, ctx.Err()
}

// Close is a no-op for the sync queue.
func (q *Queue) Close() error { return nil }
