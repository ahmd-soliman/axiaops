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
	scenarios := []string{"startup", "enterprise", "all-zombies", "no-zombies"}

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

			// Step 3: Detect zombies
			zombies := analyzer.Detect(records, usage, "test-account")

			// Step 4: Verify business logic
			summary := analyzer.Summarize(zombies)

			// Basic sanity checks
			if len(records) == 0 {
				t.Error("expected cost records, got none")
			}

			if len(usage) == 0 {
				t.Error("expected usage records, got none")
			}

			// Scenario-specific assertions
			switch scenario {
			case "all-zombies":
				if summary.TotalZombies == 0 {
					t.Error("all-zombies scenario should have zombies")
				}
				// CloudFront/Kinesis/S3 removed from fake provider test data since they require
				// real AWS APIs for detection. All 9 remaining resources should be zombies.
				if summary.TotalZombies != len(records) {
					t.Errorf("all-zombies: expected all %d resources to be zombies, got %d", len(records), summary.TotalZombies)
				}
			case "no-zombies":
				if summary.TotalZombies != 0 {
					t.Errorf("no-zombies scenario should have 0 zombies, got %d", summary.TotalZombies)
				}
			default:
				// startup and enterprise should have a mix
				if summary.TotalZombies == 0 || summary.TotalZombies == len(records) {
					t.Errorf("%s scenario should have some (not all) zombies", scenario)
				}
			}

			t.Logf("%s: %d records, %d zombies, $%.2f potential savings",
				scenario, len(records), summary.TotalZombies, summary.PotentialMonthlySave)
		})
	}
}
