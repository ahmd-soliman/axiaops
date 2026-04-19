package main

import (
	"context"
	"testing"
	"time"

	"axiaops.io/ingestion/internal/provider/fake"
	"axiaops.io/shared/analyzer"
)

// TestFakeProvider_FullPipeline tests the complete ingestion pipeline using fake data.
// This test can run in CI without AWS credentials or Docker.
func TestFakeProvider_FullPipeline(t *testing.T) {
	scenarios := []string{"startup", "enterprise", "all-ghosts", "no-ghosts"}
	
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			p := fake.New(scenario)
			ctx := context.Background()
			
			// Simulate the ingestion pipeline
			start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
			end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
			
			// Step 1: Fetch costs
			records, err := p.FetchCosts(ctx, start, end)
			if err != nil {
				t.Fatalf("FetchCosts failed: %v", err)
			}
			
			// Step 2: Fetch usage
			usage, err := p.FetchUsage(ctx, records, start, end)
			if err != nil {
				t.Fatalf("FetchUsage failed: %v", err)
			}
			
			// Step 3: Detect ghosts
			ghosts := analyzer.Detect(records, usage, "test-account")
			
			// Step 4: Verify business logic
			summary := analyzer.Summarize(ghosts)
			
			// Basic sanity checks
			if len(records) == 0 {
				t.Error("expected cost records, got none")
			}
			
			if len(usage) == 0 {
				t.Error("expected usage records, got none")
			}
			
			// Scenario-specific assertions
			switch scenario {
			case "all-ghosts":
				if summary.TotalGhosts == 0 {
					t.Error("all-ghosts scenario should have ghosts")
				}
				if summary.TotalGhosts != len(records) {
					t.Errorf("all-ghosts: expected all %d resources to be ghosts, got %d", len(records), summary.TotalGhosts)
				}
			case "no-ghosts":
				if summary.TotalGhosts != 0 {
					t.Errorf("no-ghosts scenario should have 0 ghosts, got %d", summary.TotalGhosts)
				}
			default:
				// startup and enterprise should have a mix
				if summary.TotalGhosts == 0 || summary.TotalGhosts == len(records) {
					t.Errorf("%s scenario should have some (not all) ghosts", scenario)
				}
			}
			
			t.Logf("%s: %d records, %d ghosts, $%.2f potential savings", 
				scenario, len(records), summary.TotalGhosts, summary.PotentialMonthlySave)
		})
	}
}