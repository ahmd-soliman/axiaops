package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"axiaops.io/shared/model"
)

// TestEntitlement_AutoCreate_UpsertOrganization proves the first-login / SSO-JIT
// chokepoint (UpsertOrganization) auto-grants a default 'internal'/'active'
// entitlement so the default (SaaS) fail-closed scan-gate lets the new org scan
// without billing.
func TestEntitlement_AutoCreate_UpsertOrganization(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	org, err := s.UpsertOrganization(ctx, "auto-"+uuid.New().String()[:8], "Acme")
	if err != nil {
		t.Fatalf("UpsertOrganization: %v", err)
	}

	got, err := s.GetEntitlement(ctx, org.ID)
	if err != nil {
		t.Fatalf("GetEntitlement: want auto-granted row, got %v", err)
	}
	if got.Plan != "internal" {
		t.Fatalf("plan = %q, want internal", got.Plan)
	}
	if got.Status != model.StatusActive {
		t.Fatalf("status = %q, want active", got.Status)
	}
	if got.MaxAccounts != 1000 {
		t.Fatalf("max_accounts = %d, want 1000", got.MaxAccounts)
	}
}

// TestEntitlement_AutoCreate_EnsureOrganization proves the DEV_MODE-startup
// chokepoint (EnsureOrganization) also auto-grants the default entitlement.
func TestEntitlement_AutoCreate_EnsureOrganization(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id := uuid.New().String()
	if err := s.EnsureOrganization(ctx, id, "ensure-"+uuid.New().String()[:8], "Dev Org"); err != nil {
		t.Fatalf("EnsureOrganization: %v", err)
	}

	got, err := s.GetEntitlement(ctx, id)
	if err != nil {
		t.Fatalf("GetEntitlement: want auto-granted row, got %v", err)
	}
	if got.Plan != "internal" || got.Status != model.StatusActive || got.MaxAccounts != 1000 {
		t.Fatalf("auto-granted entitlement = %+v, want internal/active/1000", got)
	}
}

// TestEntitlement_AutoCreate_Idempotent proves calling the chokepoint twice does
// not error and does not clobber a billing-set (non-internal) row that was
// written between calls — the ON CONFLICT DO NOTHING guard lets a real plan win.
func TestEntitlement_AutoCreate_Idempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	orgCode := "idem-" + uuid.New().String()[:8]
	org, err := s.UpsertOrganization(ctx, orgCode, "Acme")
	if err != nil {
		t.Fatalf("UpsertOrganization: %v", err)
	}

	// Simulate billing assigning a real plan.
	if err := s.UpsertEntitlement(ctx, model.Entitlement{
		OrganizationID: org.ID,
		Plan:           "pro",
		Status:         model.StatusActive,
		MaxAccounts:    5,
	}); err != nil {
		t.Fatalf("UpsertEntitlement pro: %v", err)
	}

	// Call the chokepoint again (idempotent org upsert) — must not error and must
	// NOT overwrite the 'pro' row back to 'internal'.
	if _, err := s.UpsertOrganization(ctx, orgCode, "Acme"); err != nil {
		t.Fatalf("UpsertOrganization (second): %v", err)
	}

	got, err := s.GetEntitlement(ctx, org.ID)
	if err != nil {
		t.Fatalf("GetEntitlement: %v", err)
	}
	if got.Plan != "pro" {
		t.Fatalf("plan = %q, want pro (billing-set row clobbered by auto-entitle)", got.Plan)
	}
	if got.MaxAccounts != 5 {
		t.Fatalf("max_accounts = %d, want 5 (billing-set row clobbered)", got.MaxAccounts)
	}
}
