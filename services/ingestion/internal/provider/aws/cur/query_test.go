package cur

import (
	"strings"
	"testing"
	"time"
)

// buildAmortizedSQL's billing_period condition must list every month the
// [start, end) window spans, not just the first and last -- a range
// crossing more than one boundary (e.g. a widened DAYS_BACK) used to drop
// every month in between silently. See query.go's periodCondition comment.

func TestBuildAmortizedSQL_SingleMonth(t *testing.T) {
	s := NewAthenaCURSource(nil, "db", "tbl", "wg", "s3://res")

	start, _ := time.Parse("2006-01-02", "2026-09-01")
	end, _ := time.Parse("2006-01-02", "2026-09-03")

	sql := s.buildAmortizedSQL(start, end, false)

	if !strings.Contains(sql, "billing_period IN ('2026-09')") {
		t.Errorf("expected single-month IN clause for 2026-09, got: %s", sql)
	}
}

func TestBuildAmortizedSQL_MultiMonthRangeIncludesEveryMonthInBetween(t *testing.T) {
	s := NewAthenaCURSource(nil, "db", "tbl", "wg", "s3://res")

	// Mirrors a DAYS_BACK=180 scan run on 2026-09-03 -- start lands in
	// March, end in September. Every month April-August must still appear;
	// the pre-fix version only emitted March and September.
	end, _ := time.Parse("2006-01-02", "2026-09-03")
	start := end.AddDate(0, 0, -180)

	sql := s.buildAmortizedSQL(start, end, false)

	for _, month := range []string{"2026-03", "2026-04", "2026-05", "2026-06", "2026-07", "2026-08", "2026-09"} {
		want := "'" + month + "'"
		if !strings.Contains(sql, want) {
			t.Errorf("expected %s in billing_period IN clause, got: %s", want, sql)
		}
	}
}

func TestBuildAmortizedSQL_TwoAdjacentMonths(t *testing.T) {
	s := NewAthenaCURSource(nil, "db", "tbl", "wg", "s3://res")

	// The shape the old two-value OR was actually built for: a 30-day
	// DAYS_BACK crossing exactly one month boundary.
	start, _ := time.Parse("2006-01-02", "2026-08-20")
	end, _ := time.Parse("2006-01-02", "2026-09-03")

	sql := s.buildAmortizedSQL(start, end, false)

	if !strings.Contains(sql, "billing_period IN ('2026-08', '2026-09')") {
		t.Errorf("expected IN clause covering both August and September, got: %s", sql)
	}
}

func TestBuildTaxSQL_FiltersToTaxLineItemsOnly(t *testing.T) {
	s := NewAthenaCURSource(nil, "db", "tbl", "wg", "s3://res")

	start, _ := time.Parse("2006-01-02", "2026-09-01")
	end, _ := time.Parse("2006-01-02", "2026-09-03")

	sql := s.buildTaxSQL(start, end)

	if !strings.Contains(sql, "line_item_line_item_type = 'Tax'") {
		t.Errorf("expected filter on Tax line items, got: %s", sql)
	}
	if !strings.Contains(sql, "'Tax' AS service") {
		t.Errorf("expected literal 'Tax' service column, got: %s", sql)
	}
	if !strings.Contains(sql, "billing_period IN ('2026-09')") {
		t.Errorf("expected billing_period condition to be reused, got: %s", sql)
	}
	if strings.Contains(sql, "line_item_resource_id") {
		t.Errorf("tax query should not break down by resource, got: %s", sql)
	}
}
