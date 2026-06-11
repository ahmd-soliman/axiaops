//go:build !selfhosted

package main

import (
	"testing"
	"time"

	"axiaops.io/shared/license"
)

// Regression-pin for the SaaS half of the saasmode build-tag seam (mirrors
// devmode_production_test.go). Runs in the DEFAULT build (any build without
// `-tags selfhosted`).

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
		t.Fatal("default (SaaS) ingestion build must bypass license enforcement so entitlement is the gate")
	}
}

func TestSaasMode_SaaS_GracePositive(t *testing.T) {
	t.Setenv("ENTITLEMENT_GRACE_DAYS", "")
	_, grace := entitlementGate(nil)
	if grace != 21*24*time.Hour {
		t.Errorf("default grace = %v, want 21d", grace)
	}
}
