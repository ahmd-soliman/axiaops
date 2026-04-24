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
// These tests verify the full pipeline: costs → usage → zombie detection.
func TestE2E_BusinessScenarios(t *testing.T) {
	tests := []struct {
		scenario      string
		expectZombies int
		expectActive  int
		minSavings    float64
	}{
		{"startup", 3, 3, 50.0},       // Small account with EC2, RDS, Lambda, VPC
		{"enterprise", 10, 9, 400.0},  // Large account (CloudFront/Kinesis/S3 removed — use real AWS detection)
		{"all-zombies", 9, 0, 250.0},  // All resources idle (CloudFront/Kinesis/S3 removed)
		{"no-zombies", 0, 5, 0.0},     // Everything active across major services
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

			// Run zombie detection
			zombies := analyzer.Detect(records, usage, "test-account")
			summary := analyzer.Summarize(zombies)

			// Verify business expectations
			if summary.TotalZombies != tt.expectZombies {
				t.Errorf("expected %d zombies, got %d", tt.expectZombies, summary.TotalZombies)
			}

			activeResources := len(records) - summary.TotalZombies
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

	zombies := analyzer.Detect(records, usage, "test-account")

	// Group zombies by service
	zombiesByService := make(map[string][]model.ZombieResource)
	for _, g := range zombies {
		zombiesByService[g.Service] = append(zombiesByService[g.Service], g)
	}

	// Verify each service has expected zombie detection
	expectedServices := []string{"AmazonEC2", "AmazonRDS", "AWSLambda", "AmazonElasticLoadBalancing", "AmazonVPC"}
	for _, service := range expectedServices {
		if len(zombiesByService[service]) == 0 {
			t.Errorf("expected zombies for service %s, got none", service)
		}
	}
}
