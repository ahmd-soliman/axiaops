package entitlement_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"axiaops.io/shared/entitlement"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// fixed reference time so the table is deterministic (the package itself never
// reads the clock — now is always injected).
var ref = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

func ptr(t time.Time) *time.Time { return &t }

func TestIsScanAllowed(t *testing.T) {
	const grace = 21 * 24 * time.Hour

	cases := []struct {
		name string
		ent  model.Entitlement
		now  time.Time
		want bool
	}{
		{"trialing allowed", model.Entitlement{Status: model.StatusTrialing}, ref, true},
		{"active allowed", model.Entitlement{Status: model.StatusActive}, ref, true},
		{"canceled gated", model.Entitlement{Status: model.StatusCanceled}, ref, false},
		{"suspended gated", model.Entitlement{Status: model.StatusSuspended}, ref, false},
		{"unknown status gated (fail closed)", model.Entitlement{Status: "frobnicated"}, ref, false},
		{"empty status gated (fail closed)", model.Entitlement{}, ref, false},

		// past_due: allowed strictly inside CurrentPeriodEnd + grace.
		{
			"past_due within grace",
			model.Entitlement{Status: model.StatusPastDue, CurrentPeriodEnd: ptr(ref.Add(-10 * 24 * time.Hour))},
			ref, true,
		},
		{
			"past_due exactly at grace boundary (inclusive)",
			model.Entitlement{Status: model.StatusPastDue, CurrentPeriodEnd: ptr(ref.Add(-grace))},
			ref, true,
		},
		{
			"past_due one second past grace",
			model.Entitlement{Status: model.StatusPastDue, CurrentPeriodEnd: ptr(ref.Add(-grace - time.Second))},
			ref, false,
		},
		{
			"past_due with nil period end fails closed",
			model.Entitlement{Status: model.StatusPastDue, CurrentPeriodEnd: nil},
			ref, false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := entitlement.IsScanAllowed(tc.ent, tc.now, grace); got != tc.want {
				t.Fatalf("IsScanAllowed(%s) = %v, want %v", tc.ent.Status, got, tc.want)
			}
		})
	}
}

// stubResolver lets us drive IsScanAllowedForOrg without a DB.
type stubResolver struct {
	ent model.Entitlement
	err error
}

func (s stubResolver) GetEntitlement(context.Context, string) (model.Entitlement, error) {
	return s.ent, s.err
}

func TestIsScanAllowedForOrg(t *testing.T) {
	const grace = 21 * 24 * time.Hour
	ctx := context.Background()

	t.Run("active org allowed", func(t *testing.T) {
		ok, err := entitlement.IsScanAllowedForOrg(ctx, stubResolver{ent: model.Entitlement{Status: model.StatusActive}}, "org-1", ref, grace)
		if err != nil || !ok {
			t.Fatalf("got (%v, %v), want (true, nil)", ok, err)
		}
	})

	t.Run("missing row fails closed without error", func(t *testing.T) {
		ok, err := entitlement.IsScanAllowedForOrg(ctx, stubResolver{err: storage.ErrEntitlementNotFound}, "org-1", ref, grace)
		if err != nil {
			t.Fatalf("missing row should not surface an error, got %v", err)
		}
		if ok {
			t.Fatal("missing row must deny (fail closed)")
		}
	})

	t.Run("db error fails closed and surfaces", func(t *testing.T) {
		sentinel := errors.New("db down")
		ok, err := entitlement.IsScanAllowedForOrg(ctx, stubResolver{err: sentinel}, "org-1", ref, grace)
		if !errors.Is(err, sentinel) {
			t.Fatalf("want surfaced error %v, got %v", sentinel, err)
		}
		if ok {
			t.Fatal("db error must deny (fail closed)")
		}
	})

	t.Run("canceled org denied", func(t *testing.T) {
		ok, err := entitlement.IsScanAllowedForOrg(ctx, stubResolver{ent: model.Entitlement{Status: model.StatusCanceled}}, "org-1", ref, grace)
		if err != nil || ok {
			t.Fatalf("got (%v, %v), want (false, nil)", ok, err)
		}
	})
}
