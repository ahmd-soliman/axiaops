//go:build production

package license_test

import (
	"os"
	"testing"

	"axiaops.io/shared/license"
)

// TestEmbedProduction_DevFixtureNotCompiledIn is the structural counterpart
// to embed_dev_test.go: in `-tags production` builds the dev pubkey and the
// fixture JWT are zeroed, so the dev-key fallback in Load() is unreachable
// and a leaked dev fixture cannot authenticate against a customer-shipping
// binary. This pins the layer-4 half of B1.7's "leaked dev fixture is
// useless against customers" claim.
//
// We test by attempting to load the dev fixture as if it were a real
// license: under `!production` it would succeed via the dev-key fallback;
// under `production` it must fail because (a) the embedded prod pubkey
// doesn't match the dev signature and (b) the dev fallback key is nil.
//
// The fixture content lives outside this file (the embed dir contains
// fixture-dev.jwt) and is read off disk so the production-tagged build
// never compiles it in — exactly the property under test.
func TestEmbedProduction_DevFixtureNotCompiledIn(t *testing.T) {
	resetSnapshot(t)
	raw, err := os.ReadFile("fixture-dev.jwt")
	if err != nil {
		t.Fatalf("read fixture-dev.jwt for cross-tag verification: %v", err)
	}
	t.Setenv(license.EnvLicense, string(raw))
	t.Setenv(license.EnvLicensePath, "")

	if _, err := license.Load(); err == nil {
		t.Fatal("production build accepted the dev fixture; expected signature error — layer 4 dev/prod key isolation is broken")
	}
}

// TestEmbedProduction_DevModeWithoutFixtureSoftFails — the production-tagged
// devmode_production.go hard-wires devModeEnabled() to false, so VerifyAtBoot
// never receives devMode=true in a real customer build. This test belt-and-
// braces the contract anyway: even if some future regression coerces the
// flag to true at the call site, VerifyAtBoot(true) must NOT panic, must NOT
// return an error, and must leave Snapshot nil with IsScanAllowed=false so
// the scan-gate keeps blocking. Anything else would re-introduce a runtime
// bypass channel.
func TestEmbedProduction_DevModeWithoutFixtureSoftFails(t *testing.T) {
	resetSnapshot(t)
	t.Setenv(license.EnvLicense, "")
	t.Setenv(license.EnvLicensePath, t.TempDir()+"/no-such-license.jwt")

	if err := license.VerifyAtBoot(true); err != nil {
		t.Fatalf("production-tagged VerifyAtBoot(devMode=true) returned %v, want nil (soft-fail to scan-gate)", err)
	}
	if license.Snapshot() != nil {
		t.Error("production-tagged DEV_MODE must NOT set Snapshot — no fixture is compiled in")
	}
	if license.IsEnforcementBypassed() {
		t.Error("production-tagged DEV_MODE must NOT flip enforcement-bypass — that would re-open the layer-3 bypass channel")
	}
	if license.IsScanAllowed() {
		t.Error("production-tagged DEV_MODE must leave IsScanAllowed=false (scan-gate blocks via license_not_loaded)")
	}
}
