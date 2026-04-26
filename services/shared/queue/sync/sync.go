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
	"time"
)

// ScanJob mirrors queue.ScanJob to avoid an import cycle.
type ScanJob struct {
	OrganizationID string    `json:"organization_id"`
	AccountID      string    `json:"account_id"`
	EnqueuedAt     time.Time `json:"enqueued_at"`
	RequestID      string    `json:"request_id"`
}

// Queue is a synchronous HTTP-based scan queue fallback.
type Queue struct {
	ingestionURL string
	client       *http.Client
}

// New creates a sync Queue that POSTs to the given ingestion service URL.
func New(ingestionURL string) *Queue {
	return &Queue{
		ingestionURL: ingestionURL,
		client:       &http.Client{Timeout: 30 * time.Second},
	}
}

// Enqueue fires a POST /scan to the ingestion service synchronously.
func (q *Queue) Enqueue(ctx context.Context, job ScanJob) error {
	body, err := json.Marshal(map[string]string{
		"account_id":      job.AccountID,
		"organization_id": job.OrganizationID,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.ingestionURL+"/scan", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
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
