package serverbuild

// tickers.go — background goroutines started via StartTickers. Lifted from
// cmd/main.go so the composition root has one entry point for "long-lived
// observability + cleanup work" and the smoke test can opt out by passing a
// zero-value TickerOptions.

import (
	"context"
	"log/slog"
	"time"

	"axiaops.io/shared/license"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
)

// stuckScanInterval is the cadence for the recovery ticker. 5 minutes
// matches the cmd/main.go behaviour pre-extraction.
const stuckScanInterval = 5 * time.Minute

// sessionSweepInterval — hourly cleanup of expired/revoked sessions older
// than 7 days. 7 days is past any session TTL (max 24h), so any such row
// is provably dead.
const sessionSweepInterval = time.Hour
const sessionSweepRetentionDays = 7

// runStuckScanTicker resets accounts left in `scanning` for longer than
// timeout. Opens its own pool because postgres.ResetStuckScans takes a URL,
// not an existing connection — historically convenient because the action
// is owner-level and brief.
func runStuckScanTicker(ctx context.Context, migrationURL string, timeout time.Duration) {
	t := time.NewTicker(stuckScanInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := postgres.ResetStuckScans(context.Background(), migrationURL, timeout)
			if err != nil {
				slog.Warn("scan-recovery: failed to reset stuck scans", "error", err)
				continue
			}
			if n > 0 {
				slog.Warn("scan-recovery: reset stuck scanning accounts", "count", n)
			}
		}
	}
}

// runLicenseTicker re-classifies the loaded license at the package's
// configured cadence. No-op under DEV_MODE (no license loaded) — the
// package short-circuits internally. Process is never killed by this
// ticker; transitions are observability events only.
func runLicenseTicker(ctx context.Context) {
	license.RunTicker(ctx, license.DefaultTickerInterval)
}

// runSessionSweepTicker hard-deletes sessions older than the retention
// window. Bounds growth of the sessions table without affecting active
// users.
func runSessionSweepTicker(ctx context.Context, store storage.Store) {
	t := time.NewTicker(sessionSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cutoff := time.Now().UTC().Add(-time.Duration(sessionSweepRetentionDays) * 24 * time.Hour)
			n, err := store.SweepExpiredSessions(context.Background(), cutoff)
			if err != nil {
				slog.Warn("session-sweep: failed", "error", err)
				continue
			}
			if n > 0 {
				slog.Info("session-sweep: deleted expired/revoked sessions", "count", n)
			}
		}
	}
}
