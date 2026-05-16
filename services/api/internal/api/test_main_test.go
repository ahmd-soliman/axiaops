// Package api_test — TestMain for the api handler test suite.
//
// Pre-amendment, the scan-gate handler treated StateNotLoaded as a fall-through
// (DEV_MODE / SaaS), so handler tests that didn't explicitly load a license
// implicitly passed the gate. Post-amendment (docs/b1.6-amendment-feature-gating.md)
// StateNotLoaded blocks scans unless enforcement-bypass is set.
//
// The test binary establishes bypass=true as its package-wide default —
// that's the posture every handler test except the license-gate tests
// actually wants (they're testing handler behaviour, not license behaviour).
// License-gate tests explicitly clear bypass and restore it on cleanup.
package api_test

import (
	"os"
	"testing"

	"axiaops.io/shared/license"
)

func TestMain(m *testing.M) {
	license.SetEnforcementBypass()
	os.Exit(m.Run())
}
