//go:build !production

package license_test

import (
	"testing"

	"axiaops.io/shared/license"
)

// TestVerifyAtBoot_DevModeLoadsFixture pins the B1.7 layer 4 (issue #75)
// posture end-to-end: DEV_MODE no longer short-circuits via enforcementBypass.
// Instead the embedded 100-year dev fixture is loaded through the same Load +
// CheckExpiry chain a real customer license travels — Snapshot is set with
// the fixture's claims, state is StateValid, IsScanAllowed flips via state
// (NOT via the bypass flag), and the fixture's specific claim shape (mint
// contract) is intact. Closes the dev/prod parity gap layers 1–3 left open.
//
// Why all the post-conditions live in one test: every assertion below is a
// regression-pin against the same change-surface. Splitting "fixture
// round-trips" from "VerifyAtBoot wires it" produces two near-duplicate
// tests that share fixture, setup, and most assertions; one test is
// clearer and a single-line failure points at exactly which contract slipped.
//
// Build-tagged !production because the embedded fixture and dev pubkey
// only exist in the default build (embed_production.go zeros both seams);
// production-tagged builds run TestEmbedProduction_DevFixtureNotCompiledIn
// instead, which proves the same fixture is rejected in the customer build.
func TestVerifyAtBoot_DevModeLoadsFixture(t *testing.T) {
	resetSnapshot(t)
	t.Setenv(license.EnvLicense, "")
	t.Setenv(license.EnvLicensePath, t.TempDir()+"/no-such-license.jwt")
	// EnvPubKeyPath must NOT point at a test key here — the dev fixture is
	// signed with the dev-only key (embed_dev.go) and verified through the
	// fallback branch in Load. t.Setenv-isolated tests already clean up on
	// completion; the explicit clear below is belt-and-braces against any
	// future test that sets this via plain os.Setenv (no cleanup).
	t.Setenv(license.EnvPubKeyPath, "")

	if err := license.VerifyAtBoot(true); err != nil {
		t.Fatalf("DEV_MODE returned error: %v", err)
	}
	snap := license.Snapshot()
	if snap == nil {
		t.Fatal("DEV_MODE should load the embedded dev fixture; Snapshot() is nil")
	}

	// Identity claims pinned to the mint contract — a re-mint that drops
	// or renames any of these is a behaviour change that should fail the
	// suite, not silently propagate.
	if snap.CustomerID != "axiaops-dev-fixture" {
		t.Errorf("CustomerID = %q, want axiaops-dev-fixture (dev fixture identity)", snap.CustomerID)
	}
	if snap.LicenseID == "" {
		t.Error("LicenseID empty — fixture mint contract requires non-empty license_id")
	}
	if snap.MaxOrganizations != 10 {
		t.Errorf("MaxOrganizations = %d, want 10 (fixture mint flag)", snap.MaxOrganizations)
	}

	// State + days_remaining: 100-year fixture should never trigger any
	// expiry-driven UX in any reasonable test clock; the threshold is
	// generous so a CI host with mild clock skew doesn't trip it.
	if state := license.SnapshotState(); state != license.StateValid {
		t.Errorf("SnapshotState() = %v, want StateValid (100-year fixture)", state)
	}
	if days := snap.DaysRemaining(); days < 30000 {
		t.Errorf("days_remaining = %d, want >= 30000 (100-year fixture should never trigger renewal banner)", days)
	}

	// IsScanAllowed must flow via state=Valid, NOT via enforcementBypass.
	// The bypass flag is reserved for SaaS reactivation; layer 4 closed
	// the legacy DEV_MODE-flips-bypass shortcut.
	if license.IsEnforcementBypassed() {
		t.Errorf("DEV_MODE must NOT flip enforcement-bypass post-layer-4 — scans fall through via state=valid")
	}
	if !license.IsScanAllowed() {
		t.Errorf("IsScanAllowed under DEV_MODE = false, want true via state=valid (scans must work in dev slots)")
	}
}
