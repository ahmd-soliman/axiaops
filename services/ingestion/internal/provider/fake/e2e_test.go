package fake_test

import (
	"context"
	"testing"
	"time"

	"axiaops.io/ingestion/internal/provider/fake"
	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
)

// TestE2E_BusinessScenarios tests the fake provider with realistic business scenarios.
// These tests verify the full pipeline: costs → usage → ghost detection.
func TestE2E_BusinessScenarios(t *testing.T) {
	tests := []struct {
		scenario      string
		expectGhosts  int
		expectActive  int
		minSavings    float64
	}{
		{"startup", 2, 2, 40.0},      // Small account, some waste
		{"enterprise", 6, 6, 300.0},  // Large account, significant waste
		{"all-ghosts", 3, 0, 200.0},  // Everything idle
		{"no-ghosts", 0, 2, 0.0},     // Everything active
	}

	for _, tt := range tests {
		t.Run(tt.scenario, func(t *testing.T) {
			p := fake.New(tt.scenario)
			start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
			end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)

			// Fetch costs and usage
			records, err := p.FetchCosts(context.Background(), start, end)
			if err != nil {
				t.Fatalf("FetchCosts failed: %v", err)
			}

			usage, err := p.FetchUsage(context.Background(), records, start, end)
			if err != nil {
				t.Fatalf("FetchUsage failed: %v", err)
			}

			// Run ghost detection
			ghosts := analyzer.Detect(records, usage, "test-account")
			summary := analyzer.Summarize(ghosts)

			// Verify business expectations
			if summary.TotalGhosts != tt.expectGhosts {
				t.Errorf("expected %d ghosts, got %d", tt.expectGhosts, summary.TotalGhosts)
			}

			activeResources := len(records) - summary.TotalGhosts
			if activeResources != tt.expectActive {
				t.Errorf("expected %d active resources, got %d", tt.expectActive, activeResources)
			}

			if summary.PotentialMonthlySave < tt.minSavings {
				t.Errorf("expected >= $%.2f savings, got $%.2f", tt.minSavings, summary.PotentialMonthlySave)
			}

			// Verify all records have required fields
			for _, r := range records {
				if r.Provider == "" || r.Service == "" || r.Amount <= 0 {
					t.Errorf("invalid record: %+v", r)
				}
			}
		})
	}
}

// TestE2E_DetectionRules verifies that each service type is detected correctly.
func TestE2E_DetectionRules(t *testing.T) {
	p := fake.New("enterprise")
	records, _ := p.FetchCosts(context.Background(), time.Now(), time.Now())
	usage, _ := p.FetchUsage(context.Background(), records, time.Now(), time.Now())
	
	ghosts := analyzer.Detect(records, usage, "test-account")
	
	// Group ghosts by service
	ghostsByService := make(map[string][]model.GhostResource)
	for _, g := range ghosts {
		ghostsByService[g.Service] = append(ghostsByService[g.Service], g)
	}
	
	// Verify each service has expected ghost detection
	expectedServices := []string{"AmazonEC2", "AmazonRDS", "AWSLambda", "AmazonElasticLoadBalancing", "AmazonVPC"}
	for _, service := range expectedServices {
		if len(ghostsByService[service]) == 0 {
			t.Errorf("expected ghosts for service %s, got none", service)
		}
	}
}