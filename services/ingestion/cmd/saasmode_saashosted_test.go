//go:build saashosted

package main

import (
	"testing"
	"time"

	"axiaops.io/shared/license"
)

// Regression-pin for the SaaS half of the saasmode build-tag seam (mirrors
// devmode_production_test.go). Runs only under `-tags saashosted`.

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
		t.Fatal("saashosted ingestion build must bypass license enforcement so entitlement is the gate")
	}
}

func TestSaasMode_SaaS_GracePositive(t *testing.T) {
	t.Setenv("ENTITLEMENT_GRACE_DAYS", "")
	_, grace := entitlementGate(nil)
	if grace != 21*24*time.Hour {
		t.Errorf("default grace = %v, want 21d", grace)
	}
}
