// Package queue provides an async scan job queue for AxiaOps services.
// When REDIS_URL is set, a Redis-backed implementation is used (LPUSH/BRPOP).
// When unset, a synchronous HTTP fallback is used so local dev works without Redis.
package queue

import (
	"context"
	"log/slog"
	"time"

	redisqueue "axiaops.io/shared/queue/redis"
	syncqueue "axiaops.io/shared/queue/sync"
)

// ScanJob represents a single scan request enqueued by the API.
//
// Timestamp and Signature are populated by the Redis backend at Enqueue
// time (envelope-signing) so a worker that
// dequeues the job can verify the envelope was minted by an authorised
// caller. They are JSON-omitted when empty so the sync queue's HTTP
// payload stays terse (its signing surface is the HTTP headers, not the
// envelope).
type ScanJob struct {
	OrganizationID string    `json:"organization_id"`
	AccountID      string    `json:"account_id"`
	EnqueuedAt     time.Time `json:"enqueued_at"`
	RequestID      string    `json:"request_id"`
	Timestamp      int64     `json:"timestamp,omitempty"`
	Signature      string    `json:"signature,omitempty"`
}

// Queue is the unified scan job queue interface.
type Queue interface {
	Enqueue(ctx context.Context, job ScanJob) error
	Dequeue(ctx context.Context) (ScanJob, error)
	Close() error
}

// toRedis / fromRedis use keyed field conversion so a future field addition
// to either struct surfaces as a named-field compile error rather than a
// confusing positional one.
func toRedis(j ScanJob) redisqueue.ScanJob {
	return redisqueue.ScanJob{
		OrganizationID: j.OrganizationID,
		AccountID:      j.AccountID,
		EnqueuedAt:     j.EnqueuedAt,
		RequestID:      j.RequestID,
		Timestamp:      j.Timestamp,
		Signature:      j.Signature,
	}
}

func fromRedis(j redisqueue.ScanJob) ScanJob {
	return ScanJob{
		OrganizationID: j.OrganizationID,
		AccountID:      j.AccountID,
		EnqueuedAt:     j.EnqueuedAt,
		RequestID:      j.RequestID,
		Timestamp:      j.Timestamp,
		Signature:      j.Signature,
	}
}

func toSync(j ScanJob) syncqueue.ScanJob {
	return syncqueue.ScanJob{
		OrganizationID: j.OrganizationID,
		AccountID:      j.AccountID,
		EnqueuedAt:     j.EnqueuedAt,
		RequestID:      j.RequestID,
		Timestamp:      j.Timestamp,
		Signature:      j.Signature,
	}
}

func fromSync(j syncqueue.ScanJob) ScanJob {
	return ScanJob{
		OrganizationID: j.OrganizationID,
		AccountID:      j.AccountID,
		EnqueuedAt:     j.EnqueuedAt,
		RequestID:      j.RequestID,
		Timestamp:      j.Timestamp,
		Signature:      j.Signature,
	}
}

// redisAdapter wraps the Redis queue, converting between queue.ScanJob and redisqueue.ScanJob.
type redisAdapter struct{ q *redisqueue.Queue }

func (a *redisAdapter) Enqueue(ctx context.Context, job ScanJob) error {
	return a.q.Enqueue(ctx, toRedis(job))
}
func (a *redisAdapter) Dequeue(ctx context.Context) (ScanJob, error) {
	j, err := a.q.Dequeue(ctx)
	return fromRedis(j), err
}
func (a *redisAdapter) Close() error { return a.q.Close() }

// syncAdapter wraps the sync queue, converting between queue.ScanJob and syncqueue.ScanJob.
type syncAdapter struct{ q *syncqueue.Queue }

func (a *syncAdapter) Enqueue(ctx context.Context, job ScanJob) error {
	return a.q.Enqueue(ctx, toSync(job))
}
func (a *syncAdapter) Dequeue(ctx context.Context) (ScanJob, error) {
	j, err := a.q.Dequeue(ctx)
	return fromSync(j), err
}
func (a *syncAdapter) Close() error { return a.q.Close() }

// New returns a Redis-backed Queue when redisURL is non-empty,
// otherwise returns a synchronous HTTP fallback Queue.
// ingestionURL is only used by the sync fallback.
//
// secret is the shared HMAC secret threaded into both backends:
//   - Redis backend signs the envelope before LPUSH (C-1 envelope auth).
//   - Sync backend signs the outbound HTTP request (C-1 wire auth).
//
// nil secret == DEV_MODE; both backends fall through to unsigned operation.
func New(redisURL, ingestionURL string, secret []byte) Queue {
	if redisURL != "" {
		q, err := redisqueue.New(redisURL, secret)
		if err == nil {
			slog.Info("queue: backend selected", "backend", "redis")
			return &redisAdapter{q}
		}
		slog.Warn("queue: failed to connect to Redis, falling back to sync", "err", err)
	}
	slog.Info("queue: backend selected", "backend", "sync")
	return &syncAdapter{syncqueue.New(ingestionURL, secret)}
}
