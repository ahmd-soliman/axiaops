package entitlement

import (
	"context"
	"fmt"
	"time"

	"axiaops.io/shared/model"
)

// billing.go is the seam where a billing provider's events become entitlement
// rows. REAL provider integration (Stripe webhook parsing, signature
// verification, the cmd/billing-webhook HTTP handler) is explicitly OUT OF
// SCOPE here (design §2.2 — "billing-provider mechanics are a separate doc;
// here we only define the entitlement state billing must drive"). This file
// defines the provider-agnostic projection boundary so that later work only has
// to decode Stripe → BillingEvent → ApplyBillingEvent and never touches the
// gate or the table shape.
//
// Today the only callers are the unit tests and cmd/entitlement-seed (manual
// row writes for dev-1). ApplyBillingEvent is the single write path so those
// callers and the future webhook all share one idempotent projection.

// Writer is the minimal persistence seam ApplyBillingEvent needs. The
// storage.Store (storage.EntitlementStore slice) satisfies it structurally;
// keeping it as a local one-method interface lets the projection be unit-tested
// with an in-memory stub and keeps this file decoupled from the storage impl.
type Writer interface {
	UpsertEntitlement(ctx context.Context, e model.Entitlement) error
}

// BillingEvent is the provider-agnostic shape a billing webhook (or an admin /
// seed action) projects onto an org's entitlement. It carries only what the
// gate and the table need — never raw provider payloads. A future Stripe
// adapter decodes a `customer.subscription.updated` (etc.) into this struct.
type BillingEvent struct {
	OrganizationID         string
	Plan                   string // "free" | "pro" | "enterprise"
	Status                 model.EntitlementStatus
	MaxAccounts            int
	Features               []string
	TrialEndsAt            *time.Time
	CurrentPeriodEnd       *time.Time
	BillingCustomerRef     string
	BillingSubscriptionRef string
}

// ApplyBillingEvent validates evt and upserts the org's entitlement. It is
// idempotent and order-tolerant by construction: the upsert is keyed on
// organization_id (migration 033's UNIQUE), so re-delivering the same event
// converges to the same row and a later event simply overwrites — the standard
// posture for at-least-once webhook delivery (design §A.2). The caller (webhook
// handler / seed) owns dedup-by-event-id if it wants stricter replay rejection;
// this projection only guarantees a consistent final row.
func ApplyBillingEvent(ctx context.Context, w Writer, evt BillingEvent) error {
	if evt.OrganizationID == "" {
		return fmt.Errorf("entitlement: apply billing event: organization_id required")
	}
	if !model.ValidEntitlementStatus(evt.Status) {
		return fmt.Errorf("entitlement: apply billing event: invalid status %q", evt.Status)
	}
	plan := evt.Plan
	if plan == "" {
		plan = "free"
	}
	maxAccounts := evt.MaxAccounts
	if maxAccounts <= 0 {
		maxAccounts = 1
	}
	features := evt.Features
	if features == nil {
		features = []string{}
	}
	ent := model.Entitlement{
		OrganizationID:         evt.OrganizationID,
		Plan:                   plan,
		Status:                 evt.Status,
		MaxAccounts:            maxAccounts,
		Features:               features,
		TrialEndsAt:            evt.TrialEndsAt,
		CurrentPeriodEnd:       evt.CurrentPeriodEnd,
		BillingCustomerRef:     evt.BillingCustomerRef,
		BillingSubscriptionRef: evt.BillingSubscriptionRef,
	}
	if err := w.UpsertEntitlement(ctx, ent); err != nil {
		return fmt.Errorf("entitlement: apply billing event: %w", err)
	}
	return nil
}
