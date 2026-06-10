//go:build saashosted

package main

import (
	"testing"
	"time"

	"axiaops.io/shared/license"
)

// Regression-pin for the SaaS half of the saasmode build-tag seam (mirrors
// devmode_production_test.go). Runs only under `-tags saashosted`. Asserts the
// bypass IS flipped and entitlementGate yields a non-zero grace.

func TestSaasMode_SaaS_Bypass(t *testing.T) {
	prev := license.IsEnforcementBypassed()
	t.Cleanup(func() {
		if prev {
			license.SetEnforcementBypass()
		} else {
			license.ClearEnforcementBypass()
		}
	})

	license.ClearEnforcementBypass()
	bypassLicenseForSaaS()
	if !license.IsEnforcementBypassed() {
		t.Fatal("saashosted build must bypass license enforcement so entitlement is the gate")
	}
}

func TestSaasMode_SaaS_GracePositive(t *testing.T) {
	t.Setenv("ENTITLEMENT_GRACE_DAYS", "") // exercise the default
	_, grace := entitlementGate(nil)
	if grace <= 0 {
		t.Fatalf("saashosted entitlementGate grace = %v, want positive (default 21d)", grace)
	}
	if want := 21 * 24 * time.Hour; grace != want {
		t.Errorf("default grace = %v, want %v", grace, want)
	}
}
