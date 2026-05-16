// Package main — TestMain for the ingestion test suite.
//
// Pre-amendment, scheduler/worker tests that didn't load a license relied
// on IsScanAllowed's StateNotLoaded fall-through. Post-amendment
// (docs/b1.6-amendment-feature-gating.md) StateNotLoaded blocks scans
// unless enforcement-bypass is set. The test binary establishes bypass=true
// as its package-wide default; license-specific tests explicitly clear it
// (and restore on cleanup) when asserting real enforcement.
package main

import (
	"os"
	"testing"

	"axiaops.io/shared/license"
)

func TestMain(m *testing.M) {
	license.SetEnforcementBypass()
	os.Exit(m.Run())
}
