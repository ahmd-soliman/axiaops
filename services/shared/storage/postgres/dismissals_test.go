package postgres_test

import (
	"testing"

	"axiaops.io/shared/model"
)

// TestListActiveDismissals_JoinsLastKnownCost confirms ListActiveDismissals
// surfaces the monthly_cost / currency from the matching zombie_records row
// via LEFT JOIN. Resource present in the latest scan → cost populated.
func TestListActiveDismissals_JoinsLastKnownCost(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	mustSaveTestAccount(t, s, ctx, org.ID, "test-account-id")

	z := zombieResource("AmazonEC2", 42.50)
	if err := s.SaveZombies(ctx, []model.ZombieResource{z}); err != nil {
		t.Fatalf("SaveZombies: %v", err)
	}

	if _, err := s.DismissZombie(ctx, model.DismissAction{
		AccountID:   z.InternalAccountID,
		Provider:    z.Provider,
		Service:     z.Service,
		Region:      z.Region,
		ResourceID:  z.ResourceID,
		Action:      "dismiss",
		Reason:      "intentional",
		DismissedBy: "test@axiaops.local",
	}); err != nil {
		t.Fatalf("DismissZombie: %v", err)
	}

	dismissals, err := s.ListActiveDismissals(ctx, "")
	if err != nil {
		t.Fatalf("ListActiveDismissals: %v", err)
	}
	if len(dismissals) != 1 {
		t.Fatalf("expected 1 dismissal, got %d", len(dismissals))
	}
	got := dismissals[0]
	if got.MonthlyCost == nil {
		t.Fatal("expected MonthlyCost to be set, got nil")
	}
	if *got.MonthlyCost != 42.50 {
		t.Errorf("MonthlyCost: got %v, want 42.50", *got.MonthlyCost)
	}
	if got.Currency != "USD" {
		t.Errorf("Currency: got %q, want %q", got.Currency, "USD")
	}
}

// TestListActiveDismissals_OrphanedDismissalReturnsNilCost confirms that when
// the underlying zombie_records row no longer exists (resource deleted upstream,
// or a subsequent scan dropped it), the LEFT JOIN miss surfaces as nil
// MonthlyCost — exactly the gap the GitLab issue describes. This is the case
// the frontend's costsById workaround could not cover.
func TestListActiveDismissals_OrphanedDismissalReturnsNilCost(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	mustSaveTestAccount(t, s, ctx, org.ID, "test-account-id")

	z := zombieResource("AmazonEC2", 42.50)
	if err := s.SaveZombies(ctx, []model.ZombieResource{z}); err != nil {
		t.Fatalf("SaveZombies: %v", err)
	}
	if _, err := s.DismissZombie(ctx, model.DismissAction{
		AccountID:   z.InternalAccountID,
		Provider:    z.Provider,
		Service:     z.Service,
		Region:      z.Region,
		ResourceID:  z.ResourceID,
		Action:      "dismiss",
		Reason:      "intentional",
		DismissedBy: "test@axiaops.local",
	}); err != nil {
		t.Fatalf("DismissZombie: %v", err)
	}

	// Re-save zombies for the SAME internal account with a different
	// resource_id. SaveZombies replaces wholesale per (org, internal_account),
	// so the original resource_id row disappears — mirrors the "resource
	// deleted upstream, next scan no longer finds it" pathway.
	replacement := zombieResource("AmazonEC2", 99.99)
	replacement.ResourceID = "res-zombie-replacement"
	if err := s.SaveZombies(ctx, []model.ZombieResource{replacement}); err != nil {
		t.Fatalf("SaveZombies (replacement): %v", err)
	}

	dismissals, err := s.ListActiveDismissals(ctx, "")
	if err != nil {
		t.Fatalf("ListActiveDismissals: %v", err)
	}
	if len(dismissals) != 1 {
		t.Fatalf("expected 1 dismissal, got %d", len(dismissals))
	}
	if dismissals[0].MonthlyCost != nil {
		t.Errorf("expected nil MonthlyCost on orphaned dismissal, got %v", *dismissals[0].MonthlyCost)
	}
	if dismissals[0].Currency != "" {
		t.Errorf("expected empty Currency on orphaned dismissal, got %q", dismissals[0].Currency)
	}
}
