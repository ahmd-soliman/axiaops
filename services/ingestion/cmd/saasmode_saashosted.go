//go:build saashosted

package main

import (
	"os"
	"strconv"
	"time"

	"axiaops.io/shared/entitlement"
	"axiaops.io/shared/license"
	"axiaops.io/shared/storage"
)

// SaaS build (`-tags saashosted`, paired with `production` for shipping). This
// is "license removal in SaaS mode" (design §7.1): the license JWT is bypassed
// at boot and per-tenant entitlement gates scans instead. This file is compiled
// ONLY into saashosted builds, so the bypass can never be reached in a
// self-hosted/customer binary — the same compile-time guarantee the production
// tag gives DEV_MODE.

// bypassLicenseForSaaS flips the license enforcement bypass so license.IsScanAllowed*
// always returns true; the entitlement gate then owns the decision. Must run
// before license.VerifyAtBoot (the caller in main() does so).
func bypassLicenseForSaaS() { license.SetEnforcementBypass() }

// entitlementGate returns the store as the entitlement Resolver plus the grace
// window, so the three scan gates consult per-tenant entitlement.
func entitlementGate(store storage.Store) (entitlement.Resolver, time.Duration) {
	return store, readEntitlementGrace()
}

// readEntitlementGrace reads ENTITLEMENT_GRACE_DAYS (default
// entitlement.DefaultGraceDays) as the past_due grace window.
func readEntitlementGrace() time.Duration {
	days := entitlement.DefaultGraceDays
	// n > 0 only: 0/negatives fall back to the default, consistent with the api
	// side (saasmode_saashosted.go) — past_due always has a non-zero window.
	if v := os.Getenv("ENTITLEMENT_GRACE_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}
	return time.Duration(days) * 24 * time.Hour
}
