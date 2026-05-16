package fake

import (
	"fmt"
	"testing"
	"time"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
)

// TestLargeScale_10kRecords tests the pipeline with 10,000 generated records
// across 100 accounts. Run with: go test -v -run=TestLargeScale
func TestLargeScale_10kRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}

	numRecords := 10000
	numAccounts := 100

	t.Logf("Generating %d records across %d accounts...", numRecords, numAccounts)

	costs, usage := generateTestData(numRecords, numAccounts)

	t.Logf("Running detection on %d cost records...", len(costs))
	start := time.Now()

	zombies := analyzer.Detect(costs, usage, "test-account")
	summary := analyzer.Summarize(zombies)

	elapsed := time.Since(start)

	t.Logf("Detection completed in %v", elapsed)
	t.Logf("Total records: %d", len(costs))
	t.Logf("Zombies detected: %d (%.1f%%)", summary.TotalZombies, float64(summary.TotalZombies)/float64(len(costs))*100)
	t.Logf("Potential savings: $%.2f", summary.PotentialMonthlySave)

	if len(costs) != numRecords {
		t.Errorf("expected %d records, got %d", numRecords, len(costs))
	}
}

// TestLargeScale_100kRecords tests with 100,000 records (run explicitly)
func TestLargeScale_100kRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-scale test in short mode")
	}

	numRecords := 100000
	numAccounts := 500

	t.Logf("Generating %d records across %d accounts...", numRecords, numAccounts)

	costs, usage := generateTestData(numRecords, numAccounts)

	t.Logf("Running detection on %d cost records...", len(costs))
	start := time.Now()

	zombies := analyzer.Detect(costs, usage, "test-account")
	summary := analyzer.Summarize(zombies)

	elapsed := time.Since(start)

	t.Logf("Detection completed in %v", elapsed)
	t.Logf("Total records: %d", len(costs))
	t.Logf("Zombies detected: %d (%.1f%%)", summary.TotalZombies, float64(summary.TotalZombies)/float64(len(costs))*100)
	t.Logf("Potential savings: $%.2f", summary.PotentialMonthlySave)
	t.Logf("Throughput: %.0f records/sec", float64(len(costs))/elapsed.Seconds())
}

// generateTestData creates synthetic cost and usage records for scale testing
func generateTestData(numRecords, numAccounts int) ([]model.CostRecord, []analyzer.UsageRecord) {
	costs := make([]model.CostRecord, numRecords)
	usage := make([]analyzer.UsageRecord, numRecords)

	services := []struct {
		name   string
		cost   float64
		metric string
		idle   float64
		active float64
	}{
		{"AmazonEC2", 36.20, "CPUUtilization", 2.0, 45.0},
		{"AmazonRDS", 149.75, "DatabaseConnections", 0, 25},
		{"AWSLambda", 3.20, "Invocations", 0, 1000},
		{"AmazonElasticLoadBalancing", 18.50, "RequestCount", 0, 5000},
		{"AmazonVPC", 3.60, "BytesOutToDestination", 0, 1000000},
	}

	recordsPerAccount := numRecords / numAccounts
	if recordsPerAccount == 0 {
		recordsPerAccount = 1
	}

	idx := 0
	for acct := 0; acct < numAccounts && idx < numRecords; acct++ {
		accountID := fmt.Sprintf("fake-account-%04d", acct+1)

		for i := 0; i < recordsPerAccount && idx < numRecords; i++ {
			svc := services[idx%len(services)]
			resourceID := fmt.Sprintf("%s-resource-%d", svc.name, idx)

			// 30% are zombies
			isZombie := idx%10 < 3

			costs[idx] = model.CostRecord{
				Provider:    "aws",
				AccountID:   accountID,
				Service:     svc.name,
				ResourceID:  resourceID,
				Amount:      svc.cost,
				Currency:    "USD",
				PeriodStart: time.Now().AddDate(0, 0, -30),
				PeriodEnd:   time.Now(),
				FetchedAt:   time.Now(),
			}

			usageValue := svc.active
			if isZombie {
				usageValue = svc.idle
			}

			usage[idx] = analyzer.UsageRecord{
				ResourceID: resourceID,
				Metric:     svc.metric,
				Avg:        usageValue,
				PeriodDays: 30,
			}

			idx++
		}
	}

	return costs[:idx], usage[:idx]
}
