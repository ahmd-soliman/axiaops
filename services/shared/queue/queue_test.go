package queue_test

import (
	"context"
	"os"
	"testing"
	"time"

	"axiaops.io/shared/queue"
	redisqueue "axiaops.io/shared/queue/redis"
	syncqueue "axiaops.io/shared/queue/sync"
)

var testJob = queue.ScanJob{
	OrganizationID: "tenant-1",
	AccountID:      "account-1",
	EnqueuedAt:     time.Now().UTC().Truncate(time.Second),
	RequestID:      "req-1",
}

// suite runs the shared enqueue/dequeue test against any Queue implementation.
func suite(t *testing.T, q queue.Queue) {
	t.Helper()
	ctx := context.Background()

	if err := q.Enqueue(ctx, testJob); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	deqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	got, err := q.Dequeue(deqCtx)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got.OrganizationID != testJob.OrganizationID || got.AccountID != testJob.AccountID {
		t.Errorf("got %+v, want %+v", got, testJob)
	}
}

func TestRedisQueue(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set — skipping Redis queue tests")
	}
	rq, err := redisqueue.New(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = rq.Close() }()

	q := queue.New(url, "")
	defer func() { _ = q.Close() }()
	suite(t, q)
}

func TestSyncQueue_DequeueRespectsContextCancel(t *testing.T) {
	sq := syncqueue.New("http://localhost:9999") // unreachable — Dequeue should block then cancel
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := sq.Dequeue(ctx)
	if err == nil {
		t.Fatal("expected context cancellation error from sync Dequeue")
	}
}
