// Package redis provides a Redis-backed implementation of queue.Queue.
// Jobs are enqueued with LPUSH and dequeued with BRPOP (blocking pop).
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const queueKey = "axiaops:scan_queue"

// ScanJob mirrors queue.ScanJob to avoid an import cycle.
type ScanJob struct {
	OrganizationID string    `json:"tenant_id"`
	AccountID      string    `json:"account_id"`
	EnqueuedAt     time.Time `json:"enqueued_at"`
	RequestID      string    `json:"request_id"`
}

// Queue is a Redis-backed scan job queue.
type Queue struct {
	rdb *redis.Client
}

// New creates a Redis queue client from the given URL.
func New(redisURL string) (*Queue, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, err
	}
	return &Queue{rdb: rdb}, nil
}

// Enqueue pushes a job to the left of the queue list.
func (q *Queue) Enqueue(ctx context.Context, job ScanJob) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("queue: marshal job: %w", err)
	}
	return q.rdb.LPush(ctx, queueKey, data).Err()
}

// Dequeue blocks until a job is available (BRPOP with no timeout).
func (q *Queue) Dequeue(ctx context.Context) (ScanJob, error) {
	result, err := q.rdb.BRPop(ctx, 0, queueKey).Result()
	if err != nil {
		return ScanJob{}, fmt.Errorf("queue: dequeue: %w", err)
	}
	var job ScanJob
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		return ScanJob{}, fmt.Errorf("queue: unmarshal job: %w", err)
	}
	return job, nil
}

// Close closes the underlying Redis connection.
func (q *Queue) Close() error {
	return q.rdb.Close()
}
