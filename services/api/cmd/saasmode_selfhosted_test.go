//go:build selfhosted

package main

import (
	"testing"

	"axiaops.io/shared/license"
)

// Regression-pin for the self-hosted (opt-in) half of the saasmode build-tag
// seam (mirrors devmode_dev_test.go). Runs only under `-tags selfhosted`. If a
// stale edit left the license bypass active in the selfhosted build, this fails.
// Paired with saasmode_saas_test.go (the default build).

func TestSaasMode_SelfHosted_NoBypass(t *testing.T) {
	prev := license.IsEnforcementBypassed()
	t.Cleanup(func() {
		if prev {
			license.SetEnforcementBypass()
		} else {
			license.ClearEnforcementBypass()
		}
	})

	license.ClearEnforcementBypass()
	bypassLicenseForSaaS() // must be a no-op in the self-hosted build
	if license.IsEnforcementBypassed() {
		t.Fatal("self-hosted build must NOT bypass license enforcement — the license gate is the only gate")
	}
}

func TestSaasMode_SelfHosted_EntitlementGateNil(t *testing.T) {
	resolver, grace := entitlementGate(nil)
	if resolver != nil {
		t.Errorf("self-hosted entitlementGate resolver = %v, want nil (license gate stays in force)", resolver)
	}
	if grace != 0 {
		t.Errorf("self-hosted entitlementGate grace = %v, want 0", grace)
	}
}
