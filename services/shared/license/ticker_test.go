package license_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"axiaops.io/shared/license"
	"axiaops.io/shared/observability"
)

// installLicenseFixture sets a *License directly via SetCurrent so tests can
// drive specific exp/grace combinations without going through Load. Cleanup
// resets the snapshot so tests don't leak state.
func installLicenseFixture(t *testing.T, exp time.Time, graceDays int) *license.License {
	t.Helper()
	lic := &license.License{
		LicenseID:        "lic_test",
		CustomerID:       "test-001",
		ExpiresAt:        exp,
		GracePeriodDays:  graceDays,
		MaxOrganizations: 5,
	}
	license.SetCurrent(lic)
	t.Cleanup(func() { license.SetCurrent(nil) })
	return lic
}

func TestTickOnce_NoTransitionWhenStateUnchanged(t *testing.T) {
	installLicenseFixture(t, time.Now().Add(365*24*time.Hour), 30)

	got := license.TickOnceForTest(license.StateValid)
	if got != license.StateValid {
		t.Errorf("tickOnce(valid → valid) = %v, want StateValid", got)
	}
}

func TestTickOnce_DetectsValidToInGrace(t *testing.T) {
	// License whose exp is in the past but within grace.
	installLicenseFixture(t, time.Now().Add(-2*24*time.Hour), 30)

	got := license.TickOnceForTest(license.StateValid)
	if got != license.StateInGrace {
		t.Errorf("tickOnce(valid → in_grace) = %v, want StateInGrace", got)
	}
}

func TestTickOnce_DetectsInGraceToExpired(t *testing.T) {
	// License past grace.
	installLicenseFixture(t, time.Now().Add(-60*24*time.Hour), 30)

	got := license.TickOnceForTest(license.StateInGrace)
	if got != license.StateExpired {
		t.Errorf("tickOnce(in_grace → expired) = %v, want StateExpired", got)
	}
}

func TestTickOnce_NoLicenseNoOp(t *testing.T) {
	license.SetCurrent(nil)
	t.Cleanup(func() { license.SetCurrent(nil) })

	// With no license, tickOnce should leave the prev state untouched.
	if got := license.TickOnceForTest(license.StateValid); got != license.StateValid {
		t.Errorf("tickOnce(no license, prev=valid) = %v, want StateValid (passthrough)", got)
	}
	if got := license.TickOnceForTest(license.StateInGrace); got != license.StateInGrace {
		t.Errorf("tickOnce(no license, prev=in_grace) = %v, want StateInGrace (passthrough)", got)
	}
}

func TestRunTicker_NoLicenseExitsImmediately(t *testing.T) {
	license.SetCurrent(nil)
	t.Cleanup(func() { license.SetCurrent(nil) })

	done := make(chan struct{})
	go func() {
		license.RunTicker(context.Background(), 10*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("RunTicker did not return when no license is loaded")
	}
}

// waitForTick polls until the LicenseDaysRemaining gauge moves away from
// the seeded sentinel, confirming the ticker fired. Reading the gauge is
// load-bearing here: only tickOnce writes it, so a positive value proves
// the loop ran. Reading SnapshotState would be a tautology — it computes
// against time.Now() and changes regardless of whether the ticker ticked.
func waitForTick(t *testing.T, deadline time.Duration) bool {
	t.Helper()
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		if testutil.ToFloat64(observability.Global.LicenseDaysRemaining) > 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestRunTicker_StopsOnContextCancel(t *testing.T) {
	installLicenseFixture(t, time.Now().Add(365*24*time.Hour), 30)
	// Seed the gauge negative so waitForTick can detect a ticker write.
	observability.Global.LicenseDaysRemaining.Set(-1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		license.RunTicker(ctx, 10*time.Millisecond)
		close(done)
	}()

	if !waitForTick(t, 500*time.Millisecond) {
		t.Fatal("ticker did not produce a tick within 500ms — cannot meaningfully test cancel")
	}
	cancel()

	select {
	case <-done:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("RunTicker did not stop within 500ms of ctx cancel")
	}
}

func TestRunTicker_FiresOnInterval(t *testing.T) {
	installLicenseFixture(t, time.Now().Add(365*24*time.Hour), 30)
	// Seed the gauge to a sentinel value the ticker overwrites on first tick.
	observability.Global.LicenseDaysRemaining.Set(-1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go license.RunTicker(ctx, 10*time.Millisecond)

	if !waitForTick(t, 500*time.Millisecond) {
		t.Fatal("ticker did not update LicenseDaysRemaining within 500ms — RunTicker loop not firing")
	}
}
