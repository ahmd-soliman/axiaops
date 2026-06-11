//go:build selfhosted

package main

import (
	"testing"

	"axiaops.io/shared/license"
)

// Regression-pin for the self-hosted (opt-in) half of the saasmode build-tag
// seam (mirrors devmode_dev_test.go). Runs only under `-tags selfhosted`. Fails
// if a stale edit left the license bypass active in the selfhosted ingestion
// build.

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
	bypassLicenseForSaaS() // no-op in the self-hosted build
	if license.IsEnforcementBypassed() {
		t.Fatal("self-hosted ingestion build must NOT bypass license enforcement")
	}
}

func TestSaasMode_SelfHosted_EntitlementGateNil(t *testing.T) {
	resolver, grace := entitlementGate(nil)
	if resolver != nil {
		t.Errorf("self-hosted entitlementGate resolver = %v, want nil", resolver)
	}
	if grace != 0 {
		t.Errorf("self-hosted entitlementGate grace = %v, want 0", grace)
	}
}
