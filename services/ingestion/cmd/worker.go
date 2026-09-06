package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"axiaops.io/shared/circuitbreaker"
	scanerrors "axiaops.io/shared/errors"
	"axiaops.io/shared/httpauth"
	"axiaops.io/shared/observability"
	"axiaops.io/shared/queue"
	redisqueue "axiaops.io/shared/queue/redis"
	"axiaops.io/shared/storage"
)

// startWorker starts a goroutine that dequeues scan jobs and executes them.
// It returns immediately. The goroutine stops when ctx is cancelled.
// When REDIS_URL is unset the queue's Dequeue blocks on ctx — so the worker
// exits cleanly on shutdown without processing any jobs (sync mode uses HTTP).
//
// secrets / maxSkew / softEnforce mirror the HTTP-side wiring so the worker
// can verify the queue envelope signature (C-1 §4.4). secrets is the
// rotation slot list; envelope verification uses the first non-nil slot
// (envelope signing is asymmetric in practice — the api signs with current,
// ingestion verifies with the slot that matches at the moment of dequeue).
func startWorker(
	ctx context.Context,
	q queue.Queue,
	store storage.Store,
	secrets [][]byte,
	maxSkew time.Duration,
	softEnforce bool,
) {
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

			// Envelope verification BEFORE any field on the job is trusted or
			// echoed into logs. The audit C-3 lesson is "never log fields from
			// an untrusted envelope before the signature is verified" — a
			// malicious LPUSH could otherwise pollute the operator's logs
			// with attacker-controlled organization_id / account_id values.
			//
			// Failures: distinct log line that does NOT echo job fields,
			// increment counter, do NOT count toward the circuit breaker
			// (envelope failures are categorically different from scan
			// failures), and `continue` the loop. See plan §4.4.
			//
			// In softEnforce mode, log + count but proceed to runScan — the
			// transition window during which legitimate jobs may arrive
			// unsigned. Hard-enforce drops them.
			redisJob := toRedisJob(job)
			if vErr := verifyEnvelopeMultiSecret(secrets, maxSkew, redisJob); vErr != nil {
				reason := envelopeReason(vErr)
				observability.RecordEnvelopeRejection(reason)
				if softEnforce {
					slog.Debug("worker: envelope soft-enforce", "reason", reason)
				} else {
					slog.Warn("worker: scan.rejected_invalid_envelope",
						"reason", reason,
					)
					continue
				}
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

			// Mark the account as scanning the moment the worker picks up
			// the job — same pattern the api-side POST /v1/accounts/{id}/scan
			// handler uses. Sets status='scanning' AND last_scanned_at=NOW()
			// in one update via TryMarkAccountScanning. Without this, a slow
			// scan (AWS SDK retries on bad creds, network throttling, the
			// 10-minute scanTimeout below) starves any caller polling
			// last_scanned_at — including the integration test
			// `TestScheduledAutoScan_ZeroInterval` which expected the column
			// to advance within 30s. statusCtx (context.Background based) is
			// used here so a parent-ctx cancel during shutdown doesn't roll
			// back the scanning marker.
			if _, err := store.TryMarkAccountScanning(statusCtx, job.AccountID); err != nil {
				slog.Warn("worker: try mark scanning failed",
					"account_id", job.AccountID,
					"err", err,
				)
			}

			// Execute scan with circuit breaker protection and timeout
			scanTimeout := 10 * time.Minute // Configurable timeout for scan operations
			scanCtxWithTimeout, cancel := context.WithTimeout(scanCtx, scanTimeout)
			defer cancel()

			err = cb.Execute(scanCtxWithTimeout, func() error {
				return runScan(scanCtxWithTimeout, store, job.AccountID)
			})

			if err != nil {
				catErr := scanerrors.Categorize(err, "worker_scan")

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

			// runScan already sets the account's final status (connected, or
			// left pending a CUR account's first delivery) via
			// finalizeAccountStatus.
			slog.Info("worker: scan.completed",
				"account_id", job.AccountID,
				"circuit_breaker_state", cb.State().String(),
			)
		}
	}()
}

// toRedisJob lifts a queue.ScanJob into the redisqueue.ScanJob shape so
// VerifyEnvelope can re-canonicalise. Fields are mirrored 1:1.
func toRedisJob(j queue.ScanJob) redisqueue.ScanJob {
	return redisqueue.ScanJob{
		OrganizationID: j.OrganizationID,
		AccountID:      j.AccountID,
		EnqueuedAt:     j.EnqueuedAt,
		RequestID:      j.RequestID,
		Timestamp:      j.Timestamp,
		Signature:      j.Signature,
	}
}

// verifyEnvelopeMultiSecret walks all configured secret slots and returns nil
// on the first match. Mirrors the HTTP-side MultiSecretMiddleware so the
// rotation playbook works the same way on both surfaces — during a rotation
// the api may sign with current OR next, and the worker must accept either.
//
// Iterates the full slot list on success AND failure so timing cannot leak
// which slot matched (same reasoning as the HTTP middleware).
//
// When secrets is empty (DEV_MODE) the underlying VerifyEnvelope returns nil
// for any input; we short-circuit early to keep the iteration count zero.
func verifyEnvelopeMultiSecret(secrets [][]byte, maxSkew time.Duration, job redisqueue.ScanJob) error {
	if len(secrets) == 0 {
		return redisqueue.VerifyEnvelope(nil, maxSkew, time.Now, job)
	}
	var matched bool
	var lastErr error
	for _, s := range secrets {
		if len(s) == 0 {
			continue
		}
		if err := redisqueue.VerifyEnvelope(s, maxSkew, time.Now, job); err == nil {
			matched = true
		} else {
			lastErr = err
		}
	}
	if matched {
		return nil
	}
	return lastErr
}

// envelopeReason maps the httpauth sentinel into a low-cardinality metric
// label. Mirrors the HTTP-path mapping in httpauth.reasonLabel (kept private
// over there because it's only used by the middleware).
func envelopeReason(err error) string {
	switch {
	case errors.Is(err, httpauth.ErrMissingSignature):
		return "missing_signature"
	case errors.Is(err, httpauth.ErrMalformedSignature):
		return "malformed"
	case errors.Is(err, httpauth.ErrTimestampSkew):
		return "timestamp_skew"
	case errors.Is(err, httpauth.ErrSignatureMismatch):
		return "signature_mismatch"
	default:
		return "unknown"
	}
}
