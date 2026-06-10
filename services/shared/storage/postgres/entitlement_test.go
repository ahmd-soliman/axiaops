package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// TestEntitlement_UpsertGetRoundtrip proves migration 033 applied and the
// system-scoped CRUD works with NO organization context — the gate reads it
// cross-org / pre-auth, so a bare context.Background() (no app.organization_id,
// no WithOrganizationID) must succeed. Mirrors the staff-table posture.
func TestEntitlement_UpsertGetRoundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	org, err := s.UpsertOrganization(ctx, "ent-"+uuid.New().String()[:8], "Acme")
	if err != nil {
		t.Fatalf("UpsertOrganization: %v", err)
	}

	// Missing row → ErrEntitlementNotFound (the fail-closed signal).
	if _, err := s.GetEntitlement(ctx, org.ID); !errors.Is(err, storage.ErrEntitlementNotFound) {
		t.Fatalf("GetEntitlement before insert: want ErrEntitlementNotFound, got %v", err)
	}

	period := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	in := model.Entitlement{
		OrganizationID:     org.ID,
		Plan:               "pro",
		Status:             model.StatusActive,
		MaxAccounts:        10,
		Features:           []string{"base"},
		CurrentPeriodEnd:   &period,
		BillingCustomerRef: "cus_test123",
	}
	if err := s.UpsertEntitlement(ctx, in); err != nil {
		t.Fatalf("UpsertEntitlement insert: %v", err)
	}

	got, err := s.GetEntitlement(ctx, org.ID)
	if err != nil {
		t.Fatalf("GetEntitlement: %v", err)
	}
	if got.Plan != "pro" || got.Status != model.StatusActive || got.MaxAccounts != 10 {
		t.Fatalf("unexpected row: %+v", got)
	}
	if got.CurrentPeriodEnd == nil || !got.CurrentPeriodEnd.Equal(period) {
		t.Fatalf("current_period_end = %v, want %v", got.CurrentPeriodEnd, period)
	}
	if got.BillingCustomerRef != "cus_test123" {
		t.Fatalf("billing_customer_ref = %q", got.BillingCustomerRef)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", got)
	}

	// Upsert again (ON CONFLICT organization_id) → idempotent update, one row.
	in.Status = model.StatusCanceled
	in.MaxAccounts = 1
	if err := s.UpsertEntitlement(ctx, in); err != nil {
		t.Fatalf("UpsertEntitlement update: %v", err)
	}
	got2, err := s.GetEntitlement(ctx, org.ID)
	if err != nil {
		t.Fatalf("GetEntitlement after update: %v", err)
	}
	if got2.Status != model.StatusCanceled || got2.MaxAccounts != 1 {
		t.Fatalf("update not applied: %+v", got2)
	}
}

// TestEntitlement_UpsertUnknownOrg proves the FK is enforced — a row for a
// non-existent org collapses to ErrOrganizationNotFound (not a raw pg error).
func TestEntitlement_UpsertUnknownOrg(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	err := s.UpsertEntitlement(ctx, model.Entitlement{
		OrganizationID: "org-does-not-exist",
		Status:         model.StatusActive,
	})
	if !errors.Is(err, storage.ErrOrganizationNotFound) {
		t.Fatalf("want ErrOrganizationNotFound, got %v", err)
	}
}

// TestEntitlement_ListAll proves the cross-org enumeration the scheduler relies
// on returns every org's row on a bare context.
func TestEntitlement_ListAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	orgA, err := s.UpsertOrganization(ctx, "ent-a-"+uuid.New().String()[:8], "Acme")
	if err != nil {
		t.Fatalf("UpsertOrganization A: %v", err)
	}
	orgB, err := s.UpsertOrganization(ctx, "ent-b-"+uuid.New().String()[:8], "Globex")
	if err != nil {
		t.Fatalf("UpsertOrganization B: %v", err)
	}
	for _, id := range []string{orgA.ID, orgB.ID} {
		if err := s.UpsertEntitlement(ctx, model.Entitlement{OrganizationID: id, Status: model.StatusTrialing}); err != nil {
			t.Fatalf("UpsertEntitlement %s: %v", id, err)
		}
	}

	all, err := s.ListAllEntitlements(ctx)
	if err != nil {
		t.Fatalf("ListAllEntitlements: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range all {
		seen[e.OrganizationID] = true
	}
	if !seen[orgA.ID] || !seen[orgB.ID] {
		t.Fatalf("ListAllEntitlements missing seeded orgs: %+v", seen)
	}
}
