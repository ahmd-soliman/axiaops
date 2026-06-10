package api

import (
	"context"
	"log/slog"
	"time"

	"axiaops.io/shared/entitlement"
	"axiaops.io/shared/license"
)

// gateAllowsScan decides whether a scan may proceed for one organization, and
// returns the pre-built 403 JSON body to write when it may not.
//
// One handler, two behaviours, selected by the nil-able resolver (mirrors the
// Deps.EnforcementResolver precedent):
//
//   - resolver == nil → SELF-HOSTED: gate on the license JWT exactly as before
//     (byte-identical license_expired / license_not_loaded bodies via
//     scanGateBody in handler.go). This is the path cmd/main.go takes.
//   - resolver != nil → SAAS (cmd/api-saashosted): the license is bypassed at
//     boot (license.SetEnforcementBypass), so gate on per-tenant entitlement
//     instead. Fail-closed: a missing row or a lookup error denies the scan.
//
// See docs/saas-platform-admin-design.md §7.1.
func gateAllowsScan(ctx context.Context, resolver entitlement.Resolver, grace time.Duration, organizationID string) (allowed bool, body []byte) {
	if resolver == nil {
		// State read ONCE and passed to both the predicate and the body builder
		// — the wall-clock CheckExpiry in two reads could otherwise cross-classify
		// a request on the microsecond boundary (in_grace vs expired).
		licState := license.SnapshotState()
		if !license.IsScanAllowedForState(licState) {
			return false, scanGateBody(licState)
		}
		return true, nil
	}

	ok, err := entitlement.IsScanAllowedForOrg(ctx, resolver, organizationID, time.Now(), grace)
	if err != nil {
		// Fail closed on a lookup error — never run paid work on a DB outage.
		slog.Error("scanAccount: entitlement lookup failed", "organization_id", organizationID, "error", err)
		return false, []byte(`{"error":"entitlement_lookup_error"}`)
	}
	if !ok {
		return false, []byte(`{"error":"not_entitled"}`)
	}
	return true, nil
}
