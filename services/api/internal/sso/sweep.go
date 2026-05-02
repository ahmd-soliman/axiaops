package sso

import (
	"context"
	"log/slog"
	"time"

	"axiaops.io/shared/storage"
)

// sweepInterval is the wake cadence for the SSO ticker.
// 24h matches the design doc — domain expiry is at 90-day granularity, and the
// SAML cert sunset (Phase C) is at 30-day granularity, so a sub-day cadence
// would just burn cycles.
const sweepInterval = 24 * time.Hour

// Sweeper runs the cross-org maintenance ticker. It bundles three
// responsibilities so cmd/main.go has one ticker to start, not three:
//
//  1. Stale-domain sweep — verified rows past expires_at flip to status='stale'
//     and stop routing logins (B2 in scope).
//  2. SAML cert sunset (Phase C, no-op in B2) — sso_connections.saml_previous_cert
//     past saml_previous_cert_expires_at gets cleared.
//  3. Assertion-replay GC (Phase C, no-op in B2) — sso_assertion_replay rows
//     past expires_at get deleted.
//
// In B2 slice 3 only #1 is wired; #2 and #3 land with the SAML SP slice.
type Sweeper struct {
	store    storage.Store
	interval time.Duration
	now      func() time.Time
}

// NewSweeper constructs a sweeper over the given store. interval defaults to
// 24h when zero — exposed so tests can drive a tighter loop.
func NewSweeper(store storage.Store, interval time.Duration) *Sweeper {
	if interval == 0 {
		interval = sweepInterval
	}
	return &Sweeper{store: store, interval: interval, now: time.Now}
}

// Run blocks until ctx is cancelled, sweeping at every tick (and once at start
// so the first sweep doesn't wait the full interval).
func (s *Sweeper) Run(ctx context.Context) {
	s.tick(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

// tick performs one round of sweep work. Any individual error is logged and
// we continue — a transient DB issue shouldn't take the ticker out.
func (s *Sweeper) tick(ctx context.Context) {
	now := s.now().UTC()
	if n, err := s.store.SweepStaleSSODomains(ctx, now); err != nil {
		slog.Error("sso: sweep stale domains failed", "error", err)
	} else if n > 0 {
		slog.Info("sso: sweep stale domains", "marked_stale", n)
	}
	// Phase C sweeps go here when the SAML SP slice lands:
	//   - SweepExpiredAssertionReplay
	//   - SweepExpiredPreviousCerts
}
