package main

import (
	"context"
	"log/slog"
	"time"

	"axiaops.io/shared/circuitbreaker"
	"axiaops.io/shared/errors"
	"axiaops.io/shared/queue"
	"axiaops.io/shared/storage"
)

// startWorker starts a goroutine that dequeues scan jobs and executes them.
// It returns immediately. The goroutine stops when ctx is cancelled.
// When REDIS_URL is unset the queue's Dequeue blocks on ctx — so the worker
// exits cleanly on shutdown without processing any jobs (sync mode uses HTTP).
func startWorker(ctx context.Context, q queue.Queue, store storage.Store) {
	// Create circuit breaker for scan operations
	cb := circuitbreaker.New(circuitbreaker.DefaultConfig())

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
				"organization_id", job.OrganizationID,
				"wait_ms", wait.Milliseconds(),
				"request_id", job.RequestID,
				"circuit_breaker_state", cb.State().String(),
			)

			scanCtx := storage.WithOrganizationID(ctx, job.OrganizationID)
			statusCtx := storage.WithOrganizationID(context.Background(), job.OrganizationID)

			// Execute scan with circuit breaker protection and timeout
			scanTimeout := 10 * time.Minute // Configurable timeout for scan operations
			scanCtxWithTimeout, cancel := context.WithTimeout(scanCtx, scanTimeout)
			defer cancel()

			err = cb.Execute(scanCtxWithTimeout, func() error {
				return runScan(scanCtxWithTimeout, store, job.AccountID)
			})

			if err != nil {
				catErr := errors.Categorize(err, "worker_scan")

				// Check for timeout specifically
				isTimeout := scanCtxWithTimeout.Err() == context.DeadlineExceeded

				slog.Error("worker: scan.failed",
					"account_id", job.AccountID,
					"err", err,
					"category", catErr.Category,
					"timeout", isTimeout,
					"circuit_breaker_state", cb.State().String(),
				)

				// Update account status based on error type
				status := "error"
				if cb.State() == circuitbreaker.StateOpen {
					status = "circuit_breaker_open"
				} else if isTimeout {
					status = "scan_timeout"
				}
				_ = store.UpdateAccountStatus(statusCtx, job.AccountID, status)
				continue
			}

			_ = store.UpdateAccountStatus(statusCtx, job.AccountID, "connected")
			slog.Info("worker: scan.completed",
				"account_id", job.AccountID,
				"circuit_breaker_state", cb.State().String(),
			)
		}
	}()
}
