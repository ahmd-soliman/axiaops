package analyzer_test

import (
	"testing"
	"time"

	"axiaops.io/ingestion/internal/analyzer"
	"axiaops.io/ingestion/internal/model"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func costRecord(service, resourceID string, amount float64) model.CostRecord {
	return model.CostRecord{
		Provider:    "aws",
		AccountID:   "000000000000",
		Service:     service,
		Region:      "eu-central-1",
		ResourceID:  resourceID,
		Amount:      amount,
		Currency:    "USD",
		PeriodStart: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:   time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		Tags:        map[string]string{"team": "platform", "env": "staging"},
		FetchedAt:   time.Now(),
	}
}

func usageRecord(resourceID, metric string, avg float64) analyzer.UsageRecord {
	return analyzer.UsageRecord{
		ResourceID: resourceID,
		Metric:     metric,
		Unit:       "Count",
		Avg:        avg,
		PeriodDays: 30,
	}
}

// ── Detect ────────────────────────────────────────────────────────────────────

func TestDetect_FlagsZeroUsage(t *testing.T) {
	costs := []model.CostRecord{
		costRecord("AmazonRDS", "db-stag-01", 210.00),
	}
	usage := []analyzer.UsageRecord{
		usageRecord("db-stag-01", "DatabaseConnections", 0),
	}

	zombies := analyzer.Detect(costs, usage, "test-account-id")

	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie, got %d", len(zombies))
	}
	g := zombies[0]
	if g.ResourceID != "db-stag-01" {
		t.Errorf("wrong resource ID: %s", g.ResourceID)
	}
	if g.MonthlyCost != 210.00 {
		t.Errorf("wrong cost: %f", g.MonthlyCost)
	}
	if g.Owner != "platform" {
		t.Errorf("expected owner platform, got %s", g.Owner)
	}
}

func TestDetect_SkipsActiveResource(t *testing.T) {
	costs := []model.CostRecord{
		costRecord("AmazonEC2", "i-active", 189.60),
	}
	usage := []analyzer.UsageRecord{
		// CPU well above 5% threshold
		usageRecord("i-active", "CPUUtilization", 62.4),
	}

	zombies := analyzer.Detect(costs, usage, "test-account-id")

	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for active resource, got %d", len(zombies))
	}
}

func TestDetect_FlagsEC2BelowThreshold(t *testing.T) {
	costs := []model.CostRecord{
		costRecord("AmazonEC2", "i-idle", 189.60),
	}
	usage := []analyzer.UsageRecord{
		// 1.1% CPU — below the 5% threshold
		usageRecord("i-idle", "CPUUtilization", 1.1),
	}

	zombies := analyzer.Detect(costs, usage, "test-account-id")

	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie for idle EC2, got %d", len(zombies))
	}
}

func TestDetect_SkipsUnknownService(t *testing.T) {
	// AmazonS3 has no detection rule in MVP
	costs := []model.CostRecord{
		costRecord("AmazonS3", "arn:aws:s3:::my-bucket", 134.10),
	}
	usage := []analyzer.UsageRecord{
		usageRecord("arn:aws:s3:::my-bucket", "NumberOfObjects", 0),
	}

	zombies := analyzer.Detect(costs, usage, "test-account-id")

	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies for service with no rule, got %d", len(zombies))
	}
}

func TestDetect_SkipsResourceWithNoUsageData(t *testing.T) {
	costs := []model.CostRecord{
		costRecord("AmazonRDS", "db-no-usage", 210.00),
	}
	// No matching usage record
	usage := []analyzer.UsageRecord{}

	zombies := analyzer.Detect(costs, usage, "test-account-id")

	if len(zombies) != 0 {
		t.Errorf("expected 0 zombies when usage data is missing, got %d", len(zombies))
	}
}

func TestDetect_OwnerFallback(t *testing.T) {
	c := costRecord("AmazonRDS", "db-no-team", 100.00)
	c.Tags = map[string]string{} // no team tag

	zombies := analyzer.Detect(
		[]model.CostRecord{c},
		[]analyzer.UsageRecord{usageRecord("db-no-team", "DatabaseConnections", 0)},
		"test-account-id",
	)

	if len(zombies) != 1 {
		t.Fatalf("expected 1 zombie, got %d", len(zombies))
	}
	if zombies[0].Owner != "unknown" {
		t.Errorf("expected owner 'unknown', got %s", zombies[0].Owner)
	}
}

func TestDetect_MultipleZombies(t *testing.T) {
	costs := []model.CostRecord{
		costRecord("AmazonRDS", "db-01", 210.00),
		costRecord("AWSLambda", "fn-old", 12.80),
		costRecord("AmazonEC2", "i-active", 1243.87),
	}
	usage := []analyzer.UsageRecord{
		usageRecord("db-01", "DatabaseConnections", 0),
		usageRecord("fn-old", "Invocations", 0),
		usageRecord("i-active", "CPUUtilization", 62.4), // active — not a zombie
	}

	zombies := analyzer.Detect(costs, usage, "test-account-id")

	if len(zombies) != 2 {
		t.Fatalf("expected 2 zombies, got %d", len(zombies))
	}
}

// ── Summarize ─────────────────────────────────────────────────────────────────

func TestSummarize_Empty(t *testing.T) {
	s := analyzer.Summarize(nil)

	if s.TotalZombies != 0 {
		t.Errorf("expected 0 zombies, got %d", s.TotalZombies)
	}
	if s.PotentialMonthlySave != 0 {
		t.Errorf("expected 0 savings, got %f", s.PotentialMonthlySave)
	}
}

func TestSummarize_AggregatesSavings(t *testing.T) {
	zombies := []model.ZombieResource{
		{Service: "AmazonRDS", MonthlyCost: 210.00, Currency: "USD"},
		{Service: "AmazonEC2", MonthlyCost: 189.60, Currency: "USD"},
		{Service: "AmazonEC2", MonthlyCost: 100.00, Currency: "USD"},
	}

	s := analyzer.Summarize(zombies)

	if s.TotalZombies != 3 {
		t.Errorf("expected 3 zombies, got %d", s.TotalZombies)
	}
	if s.PotentialMonthlySave != 499.60 {
		t.Errorf("expected savings 499.60, got %f", s.PotentialMonthlySave)
	}
	if s.Currency != "USD" {
		t.Errorf("expected USD, got %s", s.Currency)
	}
	if s.ByService["AmazonEC2"].Zombies != 2 {
		t.Errorf("expected 2 EC2 zombies, got %d", s.ByService["AmazonEC2"].Zombies)
	}
	if s.ByService["AmazonEC2"].Savings != 289.60 {
		t.Errorf("expected EC2 savings 289.60, got %f", s.ByService["AmazonEC2"].Savings)
	}
}
