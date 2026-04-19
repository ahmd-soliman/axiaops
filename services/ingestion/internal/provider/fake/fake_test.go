package fake_test

import (
	"context"
	"testing"
	"time"

	"axiaops.io/ingestion/internal/provider/fake"
)

var (
	start = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end   = time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
)

func TestNew_UnknownScenario_FallsBackToStartup(t *testing.T) {
	p := fake.New("nonexistent")
	records, err := p.FetchCosts(context.Background(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("expected records from startup fallback, got none")
	}
}

func TestNew_EmptyScenario_FallsBackToStartup(t *testing.T) {
	p := fake.New("")
	records, err := p.FetchCosts(context.Background(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("expected records from startup fallback, got none")
	}
}

func TestFetchCosts_SetsTimestamps(t *testing.T) {
	p := fake.New("startup")
	records, err := p.FetchCosts(context.Background(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, r := range records {
		if !r.PeriodStart.Equal(start) {
			t.Errorf("expected PeriodStart %v, got %v", start, r.PeriodStart)
		}
		if !r.PeriodEnd.Equal(end) {
			t.Errorf("expected PeriodEnd %v, got %v", end, r.PeriodEnd)
		}
		if r.FetchedAt.IsZero() {
			t.Error("expected FetchedAt to be set")
		}
	}
}

func TestScenarios(t *testing.T) {
	tests := []struct {
		scenario   string
		minRecords int
		minUsage   int
	}{
		{"startup", 4, 4},
		{"enterprise", 12, 12},
		{"all-ghosts", 3, 3},
		{"no-ghosts", 2, 2},
	}

	for _, tt := range tests {
		t.Run(tt.scenario, func(t *testing.T) {
			p := fake.New(tt.scenario)

			records, err := p.FetchCosts(context.Background(), start, end)
			if err != nil {
				t.Fatalf("FetchCosts error: %v", err)
			}
			if len(records) < tt.minRecords {
				t.Errorf("expected >= %d records, got %d", tt.minRecords, len(records))
			}

			usage, err := p.FetchUsage(context.Background(), records, start, end)
			if err != nil {
				t.Fatalf("FetchUsage error: %v", err)
			}
			if len(usage) < tt.minUsage {
				t.Errorf("expected >= %d usage records, got %d", tt.minUsage, len(usage))
			}

			// Every cost record must have provider and service set.
			for _, r := range records {
				if r.Provider == "" {
					t.Errorf("record missing provider: %+v", r)
				}
				if r.Service == "" {
					t.Errorf("record missing service: %+v", r)
				}
				if r.Amount <= 0 {
					t.Errorf("record has non-positive amount: %+v", r)
				}
			}
		})
	}
}

func TestAllGhosts_AllUsageIsZero(t *testing.T) {
	p := fake.New("all-ghosts")
	records, _ := p.FetchCosts(context.Background(), start, end)
	usage, _ := p.FetchUsage(context.Background(), records, start, end)

	for _, u := range usage {
		if u.Avg != 0.0 {
			t.Errorf("all-ghosts: expected zero usage for %s, got %f", u.ResourceID, u.Avg)
		}
	}
}

func TestNoGhosts_AllUsageAboveThreshold(t *testing.T) {
	p := fake.New("no-ghosts")
	records, _ := p.FetchCosts(context.Background(), start, end)
	usage, _ := p.FetchUsage(context.Background(), records, start, end)

	for _, u := range usage {
		if u.Avg <= 0 {
			t.Errorf("no-ghosts: expected positive usage for %s, got %f", u.ResourceID, u.Avg)
		}
	}
}

func TestScenarioNames_ReturnsAllFour(t *testing.T) {
	names := fake.ScenarioNames()
	if len(names) < 4 {
		t.Errorf("expected at least 4 scenarios, got %d: %v", len(names), names)
	}
}
