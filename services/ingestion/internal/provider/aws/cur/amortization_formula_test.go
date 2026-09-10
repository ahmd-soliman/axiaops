package cur

import (
	"math"
	"strings"
	"testing"
	"time"
)

// amortizedCostForLineItem mirrors the CASE expression in buildAmortizedSQL
// (query.go) column for column, so the expected arithmetic for each
// line_item_line_item_type can be pinned down and exercised in a fast, $0
// Go test -- no AWS account, no Athena, no RI/SP purchase.
//
// This is tier 1 of #54's validation: it proves the *formula* is right.
// It does NOT execute the real SQL string, so a typo or swapped column
// introduced only in query.go's SQL text would not be caught here -- that
// requires running the actual generated SQL against a live or synthetic
// Athena table (tier 2). TestBuildAmortizedSQL_CoversSameLineItemTypesAsAmortizedCostForLineItem
// below guards against the two drifting apart on *which* types are
// special-cased, but not against the SQL's arithmetic itself diverging from
// this Go mirror.
//
// Keep this in sync with query.go's CASE expression by hand -- if you
// change one, change the other.
type curLineItem struct {
	LineItemType string

	// Savings Plan columns
	SavingsPlanEffectiveCost   float64
	SavingsPlanTotalCommitment float64
	SavingsPlanUsedCommitment  float64

	// Reserved Instance columns
	ReservationEffectiveCost             float64
	ReservationUnusedAmortizedUpfrontFee float64
	ReservationUnusedRecurringFee        float64

	// Fallback for everything without a commitment (Usage, Credit, Refund,
	// and the zeroed-out Fee/SavingsPlan*/RIFee branches' raw list price).
	UnblendedCost float64
}

func amortizedCostForLineItem(r curLineItem) float64 {
	switch r.LineItemType {
	case "SavingsPlanCoveredUsage":
		return r.SavingsPlanEffectiveCost
	case "SavingsPlanRecurringFee":
		return r.SavingsPlanTotalCommitment - r.SavingsPlanUsedCommitment
	case "SavingsPlanNegation":
		return 0
	case "SavingsPlanUpfrontFee":
		return 0
	case "DiscountedUsage":
		return r.ReservationEffectiveCost
	case "RIFee":
		return r.ReservationUnusedAmortizedUpfrontFee + r.ReservationUnusedRecurringFee
	case "Fee":
		return 0
	default:
		return r.UnblendedCost
	}
}

func TestAmortizedCostForLineItem(t *testing.T) {
	tests := []struct {
		name string
		row  curLineItem
		want float64
	}{
		{
			name: "SavingsPlanCoveredUsage passes through the SP effective cost unchanged",
			row: curLineItem{
				LineItemType:             "SavingsPlanCoveredUsage",
				SavingsPlanEffectiveCost: 2.40,
				UnblendedCost:            6.00, // list price before SP discount -- must NOT be used
			},
			want: 2.40,
		},
		{
			name: "SavingsPlanRecurringFee is the unused portion of the SP commitment for the period",
			row: curLineItem{
				LineItemType:               "SavingsPlanRecurringFee",
				SavingsPlanTotalCommitment: 1.00,
				SavingsPlanUsedCommitment:  0.80,
			},
			want: 0.20, // $0.80 of the $1.00 commitment was actually used -- $0.20 wasted
		},
		{
			name: "SavingsPlanNegation is zeroed -- the real cost was already counted via CoveredUsage/RecurringFee",
			row: curLineItem{
				LineItemType:  "SavingsPlanNegation",
				UnblendedCost: -2.40, // AWS reports negations with a raw negative unblended cost
			},
			want: 0,
		},
		{
			name: "SavingsPlanUpfrontFee is zeroed -- already amortized into SavingsPlanCoveredUsage's effective cost",
			row: curLineItem{
				LineItemType:  "SavingsPlanUpfrontFee",
				UnblendedCost: 100.00, // the raw one-time upfront payment
			},
			want: 0,
		},
		{
			name: "DiscountedUsage passes through the RI effective cost unchanged",
			row: curLineItem{
				LineItemType:             "DiscountedUsage",
				ReservationEffectiveCost: 1.80,
				UnblendedCost:            4.50, // list price before RI discount -- must NOT be used
			},
			want: 1.80,
		},
		{
			name: "RIFee sums the unused upfront and unused recurring portions of the reservation",
			row: curLineItem{
				LineItemType:                         "RIFee",
				ReservationUnusedAmortizedUpfrontFee: 0.15,
				ReservationUnusedRecurringFee:        0.05,
			},
			want: 0.20,
		},
		{
			name: "Fee is zeroed -- generic one-time fees aren't attributable to a resource's amortized cost",
			row: curLineItem{
				LineItemType:  "Fee",
				UnblendedCost: 5.00,
			},
			want: 0,
		},
		{
			name: "Usage falls through to the raw unblended cost via ELSE -- no commitment involved",
			row: curLineItem{
				LineItemType:  "Usage",
				UnblendedCost: 3.60,
			},
			want: 3.60,
		},
		{
			name: "Credit nets out via ELSE -- AWS reports credits as a negative unblended cost",
			row: curLineItem{
				LineItemType:  "Credit",
				UnblendedCost: -1.00,
			},
			want: -1.00,
		},
		{
			name: "Refund nets out via ELSE, same shape as Credit",
			row: curLineItem{
				LineItemType:  "Refund",
				UnblendedCost: -0.50,
			},
			want: -0.50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := amortizedCostForLineItem(tt.row)
			// Epsilon comparison: float64 subtraction (e.g. 1.00 - 0.80)
			// doesn't land on an exact binary representation of 0.20.
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("amortizedCostForLineItem(%+v) = %v, want %v", tt.row, got, tt.want)
			}
		})
	}
}

// Guards against the Go mirror above and the real SQL drifting apart on
// *which* line item types are special-cased (e.g. a new branch added to one
// but not the other). It does not check that the arithmetic inside each
// branch matches -- only that the same set of types is handled specially.
func TestBuildAmortizedSQL_CoversSameLineItemTypesAsAmortizedCostForLineItem(t *testing.T) {
	s := NewAthenaCURSource(nil, "db", "tbl", "wg", "s3://res")
	start, _ := time.Parse("2006-01-02", "2026-09-01")
	end, _ := time.Parse("2006-01-02", "2026-09-03")
	sql := s.buildAmortizedSQL(start, end, false)

	specialCased := []string{
		"SavingsPlanCoveredUsage",
		"SavingsPlanRecurringFee",
		"SavingsPlanNegation",
		"SavingsPlanUpfrontFee",
		"DiscountedUsage",
		"RIFee",
		"Fee",
	}
	for _, lit := range specialCased {
		want := "'" + lit + "'"
		if !strings.Contains(sql, want) {
			t.Errorf("expected buildAmortizedSQL to special-case %s, got: %s", want, sql)
		}
	}
}
