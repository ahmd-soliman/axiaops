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
// at boot and the scan endpoint gates on per-tenant entitlement instead. /v1/version
// collapses to {state:"managed"} (handler.licenseSummary branches on
// license.IsEnforcementBypassed). Compiled ONLY into saashosted builds, so the
// bypass can never be reached in a self-hosted/customer binary.

// bypassLicenseForSaaS flips the license enforcement bypass so license.IsScanAllowed*
// always returns true; the entitlement gate then owns the scan decision. Must
// run before license.VerifyAtBoot (the caller in main() does so).
func bypassLicenseForSaaS() { license.SetEnforcementBypass() }

// entitlementGate returns the store as the entitlement Resolver plus the grace
// window, which ComposeServer wires into the handler's scan gate.
func entitlementGate(store storage.Store) (entitlement.Resolver, time.Duration) {
	return store, readEntitlementGrace()
}

// readEntitlementGrace reads ENTITLEMENT_GRACE_DAYS (default
// entitlement.DefaultGraceDays) as the past_due grace window.
func readEntitlementGrace() time.Duration {
	days := entitlement.DefaultGraceDays
	// n > 0 only: 0 (and negatives) fall back to the default, matching the api
	// handler's WithEntitlementResolver (grace <= 0 → default). "No grace at all"
	// is deliberately NOT an operator-settable value — past_due should always
	// have a non-zero window, consistent across api + ingestion.
	if v := os.Getenv("ENTITLEMENT_GRACE_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}
	return time.Duration(days) * 24 * time.Hour
}
