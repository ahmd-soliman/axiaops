package main

import (
	"context"
	"log/slog"
	"time"

	"axiaops.io/shared/entitlement"
	"axiaops.io/shared/license"
)

// gateAllowsScan decides whether a scan may proceed for one organization at one
// of the three ingestion gate sites (POST /scan, the worker, the scheduler).
//
// One code path, two behaviours, selected by the nil-able resolver (mirrors the
// api side):
//
//   - resolver == nil → SELF-HOSTED: gate on the license JWT exactly as before;
//     code is one of license_expired / license_not_loaded / license_inactive.
//   - resolver != nil → SAAS (the default build): the license is bypassed
//     at boot, so gate on per-tenant entitlement. Fail-closed: a missing row or a
//     lookup error denies; code is not_entitled / entitlement_lookup_error.
//
// Returns (allowed, code). On the allowed path code is "". Callers use code as
// the POST /scan 403 error body and as the worker/scheduler skip-reason log
// field. See docs/saas-platform-admin-design.md §7.1.
func gateAllowsScan(ctx context.Context, resolver entitlement.Resolver, grace time.Duration, organizationID string) (allowed bool, code string) {
	if resolver == nil {
		licState := license.SnapshotState()
		if !license.IsScanAllowedForState(licState) {
			return false, licenseErrCode(licState)
		}
		return true, ""
	}

	ok, err := entitlement.IsScanAllowedForOrg(ctx, resolver, organizationID, time.Now(), grace)
	if err != nil {
		// Fail closed on a lookup error — never run paid work on a DB outage.
		slog.Error("ingestion scan-gate: entitlement lookup failed", "organization_id", organizationID, "error", err)
		return false, "entitlement_lookup_error"
	}
	if !ok {
		return false, "not_entitled"
	}
	return true, ""
}

// licenseErrCode maps a blocking license state to its scan-gate error code. The
// default branch is the regression guard the amendment doc describes: a future
// blocking state (StateRevoked, …) lands here with a generic license_inactive
// AND a slog.Warn so the unhandled state surfaces in logs rather than shipping
// the wrong code silently.
func licenseErrCode(s license.State) string {
	switch s {
	case license.StateExpired:
		return "license_expired"
	case license.StateNotLoaded:
		return "license_not_loaded"
	default:
		slog.Warn("ingestion scan-gate: unhandled license state", "state", s.String())
		return "license_inactive"
	}
}
