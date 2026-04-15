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
type ScanJob struct {
	TenantID   string    `json:"tenant_id"`
	AccountID  string    `json:"account_id"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	RequestID  string    `json:"request_id"`
}

// Queue is the unified scan job queue interface.
type Queue interface {
	Enqueue(ctx context.Context, job ScanJob) error
	Dequeue(ctx context.Context) (ScanJob, error)
	Close() error
}

// redisAdapter wraps the Redis queue, converting between queue.ScanJob and redisqueue.ScanJob.
type redisAdapter struct{ q *redisqueue.Queue }

func (a *redisAdapter) Enqueue(ctx context.Context, job ScanJob) error {
	return a.q.Enqueue(ctx, redisqueue.ScanJob(job))
}
func (a *redisAdapter) Dequeue(ctx context.Context) (ScanJob, error) {
	j, err := a.q.Dequeue(ctx)
	return ScanJob(j), err
}
func (a *redisAdapter) Close() error { return a.q.Close() }

// syncAdapter wraps the sync queue, converting between queue.ScanJob and syncqueue.ScanJob.
type syncAdapter struct{ q *syncqueue.Queue }

func (a *syncAdapter) Enqueue(ctx context.Context, job ScanJob) error {
	return a.q.Enqueue(ctx, syncqueue.ScanJob(job))
}
func (a *syncAdapter) Dequeue(ctx context.Context) (ScanJob, error) {
	j, err := a.q.Dequeue(ctx)
	return ScanJob(j), err
}
func (a *syncAdapter) Close() error { return a.q.Close() }

// New returns a Redis-backed Queue when redisURL is non-empty,
// otherwise returns a synchronous HTTP fallback Queue.
// ingestionURL is only used by the sync fallback.
func New(redisURL, ingestionURL string) Queue {
	if redisURL != "" {
		q, err := redisqueue.New(redisURL)
		if err == nil {
			slog.Info("queue: backend selected", "backend", "redis")
			return &redisAdapter{q}
		}
		slog.Warn("queue: failed to connect to Redis, falling back to sync", "err", err)
	}
	slog.Info("queue: backend selected", "backend", "sync")
	return &syncAdapter{syncqueue.New(ingestionURL)}
}
