package main

import (
	"context"
	"log/slog"
	"time"

	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
)

// startWorker starts a goroutine that dequeues scan jobs and executes them.
// It returns immediately. The goroutine stops when ctx is cancelled.
// When REDIS_URL is unset the queue's Dequeue blocks on ctx — so the worker
// exits cleanly on shutdown without processing any jobs (sync mode uses HTTP).
func startWorker(ctx context.Context, q queue.Queue, store storage.Store) {
	go func() {
		slog.Info("worker: started")
		for {
			job, err := q.Dequeue(ctx)
			if err != nil {
				if ctx.Err() != nil {
					slog.Info("worker: stopped")
					return
				}
				slog.Error("worker: dequeue error", "err", err)
				time.Sleep(time.Second) // back-off before retry
				continue
			}

			wait := time.Since(job.EnqueuedAt)
			slog.Info("worker: scan.dequeued",
				"account_id", job.AccountID,
				"tenant_id", job.TenantID,
				"wait_ms", wait.Milliseconds(),
				"request_id", job.RequestID,
			)

			scanCtx := storage.WithTenantID(ctx, job.TenantID)
			if err := runScan(scanCtx, store, job.AccountID); err != nil {
				slog.Error("worker: scan.failed", "account_id", job.AccountID, "err", err)
				_ = store.UpdateAccountStatus(context.Background(), job.AccountID, "error")
				continue
			}
			_ = store.UpdateAccountStatus(context.Background(), job.AccountID, "connected")
			slog.Info("worker: scan.completed", "account_id", job.AccountID)
		}
	}()
}
