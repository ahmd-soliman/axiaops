// Package entitlement is the SaaS analogue of the self-hosted license package:
// it answers "may this org run a paid scan?" from billing-driven entitlement
// state instead of a signed license JWT. See
// docs/saas-platform-admin-design.md §7.2.
//
// WIRED IN SAAS BUILDS (Phase 2B). In a `-tags saashosted` build the api +
// ingestion roots call license.SetEnforcementBypass() at boot (the license JWT
// goes dormant) and thread a Resolver into the scan gates, which then call
// IsScanAllowedForOrg here instead of license.IsScanAllowed. In the default
// (self-hosted) build the resolver is nil and the gates keep the license
// predicate — this package is inert there. The selection is the compile-time
// `saashosted` build tag (services/{api,ingestion}/cmd/saasmode_*.go), so the
// bypass can never be reached in a self-hosted/customer binary. See
// docs/saas-platform-admin-design.md §7.1.
//
// Mirrors the license package's seam shape on purpose:
//   - IsScanAllowed is the pure policy predicate (cf. license.IsScanAllowedForState).
//   - IsScanAllowedForOrg is the convenience wrapper the scan-gate sites call
//     (cf. license.IsScanAllowed), looking the row up via a Resolver.
package entitlement

import (
	"context"
	"errors"
	"time"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// DefaultGraceDays is the past_due grace window in days, applied past
// CurrentPeriodEnd before scans are gated. Mirrors the license grace_period
// philosophy. Overridable per deployment via the composition root (the future
// ENTITLEMENT_GRACE_DAYS env var) — passed explicitly into the predicate so
// this package stays free of env reads and trivially testable.
const DefaultGraceDays = 21

// Resolver looks up one org's entitlement. The concrete implementation is the
// storage.Store (storage.EntitlementStore slice), which reads the system-scoped
// `entitlements` table on the admin pool — so this works identically for the
// org-scoped api handler and the cross-org ingestion worker/scheduler, none of
// which need an RLS org-context for the lookup.
type Resolver interface {
	GetEntitlement(ctx context.Context, organizationID string) (model.Entitlement, error)
}

// IsScanAllowed is the pure policy predicate — the single seam that decides
// whether an entitlement permits new paid work (scans). No I/O, no clock of its
// own (now is injected), so it is exhaustively table-testable.
//
// Policy (design §7.2):
//   - trialing, active        → allowed.
//   - past_due                → allowed only inside the grace window, i.e.
//     now <= CurrentPeriodEnd + grace. With no CurrentPeriodEnd there is no
//     anchor for the window, so we FAIL CLOSED (deny) rather than grant an
//     unbounded grace.
//   - canceled, suspended     → gated.
//   - anything else (unknown) → gated (fail closed).
//
// Reads/dashboard are NOT gated by this — only new scans. Lapsed tenants keep
// their existing data (graceful degradation), exactly like the self-hosted
// license posture.
func IsScanAllowed(e model.Entitlement, now time.Time, grace time.Duration) bool {
	switch e.Status {
	case model.StatusTrialing, model.StatusActive:
		return true
	case model.StatusPastDue:
		if e.CurrentPeriodEnd == nil {
			return false // no anchor for the grace window → fail closed
		}
		return !now.After(e.CurrentPeriodEnd.Add(grace))
	case model.StatusCanceled, model.StatusSuspended:
		return false
	default:
		return false
	}
}

// IsScanAllowedForOrg looks up the org's entitlement and applies the predicate.
// It is the wrapper the scan-gate sites call (api scanAccount + the 3 ingestion
// sites) in saashosted builds.
//
// Fail-closed by construction (the posture chosen for SaaS — billing is the
// source of truth, every real tenant gets a row at signup, so absence means
// "not entitled", design §7.2):
//   - No row (storage.ErrEntitlementNotFound) → (false, nil): a definitive,
//     non-error "deny" the caller can skip on quietly.
//   - Any other lookup error (DB down, etc.)  → (false, err): also deny, but
//     surfaced so the caller logs it — never run a paid scan on an outage.
func IsScanAllowedForOrg(ctx context.Context, r Resolver, organizationID string, now time.Time, grace time.Duration) (bool, error) {
	ent, err := r.GetEntitlement(ctx, organizationID)
	if err != nil {
		if errors.Is(err, storage.ErrEntitlementNotFound) {
			return false, nil
		}
		return false, err
	}
	return IsScanAllowed(ent, now, grace), nil
}
