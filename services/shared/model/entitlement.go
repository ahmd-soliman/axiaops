package model

import "time"

// EntitlementStatus is the billing-driven lifecycle state of an org's SaaS
// entitlement — the analogue of the self-hosted license State. Billing (Stripe
// or the chosen provider) is the source of truth; webhooks project onto this
// via the entitlement package's ApplyBillingEvent seam. See
// docs/saas-platform-admin-design.md §7.2.
type EntitlementStatus string

const (
	// StatusTrialing — in a free trial; scans allowed.
	StatusTrialing EntitlementStatus = "trialing"
	// StatusActive — paid and current; scans allowed.
	StatusActive EntitlementStatus = "active"
	// StatusPastDue — payment failed but inside the grace window; scans allowed
	// until current_period_end + ENTITLEMENT_GRACE_DAYS, then gated. Mirrors the
	// license in_grace philosophy (b1.6-amendment): degrade gracefully, don't
	// hard-cut the moment a card expires.
	StatusPastDue EntitlementStatus = "past_due"
	// StatusCanceled — subscription ended; scans gated. Existing data stays
	// readable (graceful degradation — you stop doing new paid work, you don't
	// hide the customer's data; design §7.2).
	StatusCanceled EntitlementStatus = "canceled"
	// StatusSuspended — administratively halted (abuse, non-payment past grace);
	// scans gated.
	StatusSuspended EntitlementStatus = "suspended"
)

// ValidEntitlementStatus reports whether s is one of the §7.2 statuses. Mirrors
// the migration 033 CHECK constraint — keep the two in lockstep.
func ValidEntitlementStatus(s EntitlementStatus) bool {
	switch s {
	case StatusTrialing, StatusActive, StatusPastDue, StatusCanceled, StatusSuspended:
		return true
	default:
		return false
	}
}

// Entitlement is one org's SaaS plan + billing state — one row per organization
// in the system-scoped `entitlements` table (migration 033). It is the SaaS
// replacement for the per-customer license JWT: under SaaS, AxiaOps owns the
// database the tenant lives in, so entitlement is just a row AxiaOps controls
// directly rather than a signed token shipped across a trust boundary
// (design §7.1). There is no customer-facing "license" — the tenant sees plan +
// usage; staff see the derived state, never a token.
//
// The past_due grace window is NOT a field here — it is derived at read time
// from CurrentPeriodEnd + the deployment's grace days (see the entitlement
// package), mirroring how license grace is exp + grace_period_days.
type Entitlement struct {
	OrganizationID string
	Plan           string // "free" | "pro" | "enterprise"
	Status         EntitlementStatus
	MaxAccounts    int
	Features       []string
	TrialEndsAt    *time.Time // nil outside a trial
	// CurrentPeriodEnd is the end of the current paid billing period. nil when
	// never billed (e.g. a bare trial). It anchors the past_due grace window.
	CurrentPeriodEnd *time.Time
	// BillingCustomerRef / BillingSubscriptionRef are opaque provider handles
	// (e.g. Stripe ids) the future webhook seam populates. Never shown to the
	// tenant; visible only to staff with the billing role (design §7.5).
	BillingCustomerRef     string
	BillingSubscriptionRef string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}
