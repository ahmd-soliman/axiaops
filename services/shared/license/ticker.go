package license

import (
	"context"
	"log/slog"
	"time"

	"axiaops.io/shared/observability"
)

// DefaultTickerInterval is the production cadence: hourly classification
// re-runs. Sub-minute granularity buys nothing — license expiry transitions
// happen on day boundaries — and burns wakeups; multi-hour cadence would
// delay the scan-gate flip past contractual expiry by too long. 1h is the
// chosen trade-off (plan §4.9.2a).
const DefaultTickerInterval = time.Hour

// RunTicker re-classifies the boot-time license periodically, updates the
// Prometheus gauges so long-running binaries advance with the wall clock,
// and emits a structured slog line on every state transition.
//
// Critically, this NEVER calls os.Exit — mid-flight transitions are
// observability events; slice 5's scan-gate is what actually blocks
// behaviour. Industry-aligned with GitLab EE / Atlassian DC / HashiCorp
// Enterprise: license expiry mid-flight does not crash a healthy serving
// process.
//
// Returns when ctx is cancelled. Designed to run as a goroutine launched
// from cmd/main.go after VerifyAtBoot. No-op when no license is loaded
// (DEV_MODE / SaaS binary) — the ticker exits immediately and the launching
// goroutine is reaped.
func RunTicker(ctx context.Context, interval time.Duration) {
	if Snapshot() == nil {
		slog.Info("license: ticker not started (no license loaded)")
		return
	}
	if interval <= 0 {
		interval = DefaultTickerInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	last := SnapshotState()
	slog.Info("license: ticker started", "interval", interval, "initial_state", last)

	for {
		select {
		case <-ctx.Done():
			slog.Info("license: ticker stopped")
			return
		case <-ticker.C:
			last = tickOnce(last)
		}
	}
}

// tickOnce re-classifies the boot-time license, updates the gauges, and
// emits a transition slog line when the state has changed. Returns the
// post-tick state so the caller maintains the rolling window.
//
// Extracted from RunTicker so tests can drive transitions deterministically
// (manipulate the *License directly + call tickOnce) without spinning real
// timers or faking time.Now.
func tickOnce(prev State) State {
	lic := Snapshot()
	if lic == nil {
		// Defensive: SetCurrent(nil) mid-run would land here. Don't touch
		// gauges — slice 3 left them at the boot-time values; clearing them
		// on a transient nil would defeat dashboards.
		return prev
	}
	current := CheckExpiry(lic)
	days := lic.DaysRemaining()

	observability.Global.LicenseDaysRemaining.Set(float64(days))
	// Reset before re-setting — GaugeVec.WithLabelValues().Set(1) does not
	// zero sibling label-sets, and a stale state="valid"=1 alongside the
	// new state="in_grace"=1 would defeat the in-grace alert. See the field
	// comment on LicenseStateInfo in services/shared/observability/metrics.go.
	observability.Global.LicenseStateInfo.Reset()
	observability.Global.LicenseStateInfo.WithLabelValues(current.String(), lic.CustomerID).Set(1)

	if current != prev {
		slog.Warn("license: state transition",
			"from", prev,
			"to", current,
			"license_id", lic.LicenseID,
			"customer_id", lic.CustomerID,
			"days_remaining", days,
		)
	}
	return current
}
