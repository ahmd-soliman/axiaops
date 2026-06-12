package entitlement_test

import (
	"context"
	"testing"
	"time"

	"axiaops.io/shared/entitlement"
	"axiaops.io/shared/model"
)

// memWriter is an in-memory EntitlementWriter keyed by org id — mirrors the
// upsert (ON CONFLICT organization_id) semantics so idempotency/order-tolerance
// are exercised without a DB.
type memWriter struct {
	rows  map[string]model.Entitlement
	calls int
}

func newMemWriter() *memWriter { return &memWriter{rows: map[string]model.Entitlement{}} }

func (w *memWriter) UpsertEntitlement(_ context.Context, e model.Entitlement) error {
	w.calls++
	w.rows[e.OrganizationID] = e
	return nil
}

func TestApplyBillingEvent_Defaults(t *testing.T) {
	w := newMemWriter()
	err := entitlement.ApplyBillingEvent(context.Background(), w, entitlement.BillingEvent{
		OrganizationID: "org-1",
		Status:         model.StatusActive,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := w.rows["org-1"]
	if got.Plan != "free" {
		t.Errorf("plan default = %q, want free", got.Plan)
	}
	if got.MaxAccounts != 1 {
		t.Errorf("max_accounts default = %d, want 1", got.MaxAccounts)
	}
	if got.Features == nil {
		t.Error("features should default to non-nil empty slice")
	}
}

func TestApplyBillingEvent_Validation(t *testing.T) {
	w := newMemWriter()
	if err := entitlement.ApplyBillingEvent(context.Background(), w, entitlement.BillingEvent{Status: model.StatusActive}); err == nil {
		t.Error("missing org id should error")
	}
	if err := entitlement.ApplyBillingEvent(context.Background(), w, entitlement.BillingEvent{OrganizationID: "org-1", Status: "bogus"}); err == nil {
		t.Error("invalid status should error")
	}
	if w.calls != 0 {
		t.Errorf("invalid events must not write, got %d writes", w.calls)
	}
}

func TestApplyBillingEvent_IdempotentAndOrderTolerant(t *testing.T) {
	w := newMemWriter()
	ctx := context.Background()
	period := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	evt := entitlement.BillingEvent{
		OrganizationID:   "org-1",
		Plan:             "pro",
		Status:           model.StatusActive,
		MaxAccounts:      10,
		CurrentPeriodEnd: &period,
	}
	// Re-deliver the same event: converges to one row.
	for i := 0; i < 3; i++ {
		if err := entitlement.ApplyBillingEvent(ctx, w, evt); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
	if len(w.rows) != 1 {
		t.Fatalf("idempotent re-delivery should yield 1 row, got %d", len(w.rows))
	}
	if got := w.rows["org-1"]; got.Status != model.StatusActive || got.MaxAccounts != 10 {
		t.Fatalf("converged row wrong: %+v", got)
	}

	// A later cancellation overwrites (last-write wins on the keyed upsert).
	if err := entitlement.ApplyBillingEvent(ctx, w, entitlement.BillingEvent{OrganizationID: "org-1", Status: model.StatusCanceled}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := w.rows["org-1"]; got.Status != model.StatusCanceled {
		t.Fatalf("after cancel, status = %q, want canceled", got.Status)
	}
}
