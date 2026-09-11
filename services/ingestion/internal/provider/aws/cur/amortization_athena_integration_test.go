//go:build athena_integration

package cur

// Tier 2 of #54's validation path (tier 1 -- amortization_formula_test.go --
// proves the formula itself is correct via a Go mirror, at $0, with no AWS
// account). This test proves the REAL SQL in buildAmortizedSQL produces the
// same numbers as that already-verified formula, by running it for real
// through Athena against a small synthetic CUR-shaped table -- no RI/SP
// purchase required, no real customer billing data required.
//
// Reuses the same fixture values already pinned in
// TestAmortizedCostForLineItem, one isolated group per line_item_line_item_type
// branch (a distinct fake line_item_product_code per case keeps each group's
// SUM unambiguous), plus one case amortization_formula_test.go structurally
// cannot cover: the real SQL's `HAVING SUM(...) > 0.000001` clause, which
// drops any (account, service, region, day) group whose net amortized cost
// rounds to zero. fetchAndParse's own `cost <= 0` guard (fetch.go) does the
// same thing a second time at the Go layer -- this test proves the SQL-level
// filter behaves as intended, not just the Go-level backstop.
//
// Not run by `go test ./...` -- needs a real AWS account, S3 bucket, and
// Athena access, which breaks this repo's "no real network calls in unit
// tests" convention every other test here follows. Opt in explicitly:
//
//	AXIAOPS_CUR_TEST_BUCKET=my-test-bucket \
//	  go test -tags athena_integration ./internal/provider/aws/cur/... -run AthenaIntegration -v
//
// Cost: a handful of KB of test data, a handful of Athena queries. Athena
// bills per byte scanned -- this is fractions of a cent per run, not a
// meaningful cost concern. The table and its S3 objects are created under a
// per-run UUID prefix and torn down (best-effort) when the test finishes,
// success or failure.
//
// AXIAOPS_CUR_TEST_BUCKET (required) -- S3 bucket for the synthetic CSV and
// Athena query results. Must already exist; this test does not create it.
// AXIAOPS_CUR_TEST_WORKGROUP (optional, default "primary") -- Athena
// workgroup to run queries in.
// AXIAOPS_CUR_TEST_DATABASE (optional, default "axiaops_amortization_test")
// -- Glue database for the synthetic table. Created via `CREATE DATABASE IF
// NOT EXISTS` if it doesn't already exist.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

// One row per test case. Field names mirror curLineItem in
// amortization_formula_test.go; productCode is this test's isolation key --
// each case gets its own fake service code so its group's SUM is
// unambiguous (real CUR data would never see these product codes).
type athenaTestRow struct {
	productCode       string
	lineItemType      string
	spEffectiveCost   float64
	spTotalCommitment float64
	spUsedCommitment  float64
	riEffectiveCost   float64
	riUnusedUpfront   float64
	riUnusedRecurring float64
	unblendedCost     float64
}

func TestFetchCosts_AthenaIntegration_MatchesFormulaFixtures(t *testing.T) {
	bucket := os.Getenv("AXIAOPS_CUR_TEST_BUCKET")
	if bucket == "" {
		t.Skip("AXIAOPS_CUR_TEST_BUCKET not set -- skipping Athena integration test (see file header for how to run it)")
	}
	workgroup := os.Getenv("AXIAOPS_CUR_TEST_WORKGROUP")
	if workgroup == "" {
		workgroup = "primary"
	}
	database := os.Getenv("AXIAOPS_CUR_TEST_DATABASE")
	if database == "" {
		database = "axiaops_amortization_test"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		t.Fatalf("load AWS config: %v", err)
	}
	s3Client := s3.NewFromConfig(cfg)
	athenaClient := athena.NewFromConfig(cfg)

	runID := uuid.NewString()
	table := "amortization_test_" + strings.ReplaceAll(runID, "-", "_")
	dataPrefix := fmt.Sprintf("axiaops-athena-integration-test/%s/", runID)
	dataKey := dataPrefix + "data.csv"
	resultsS3 := fmt.Sprintf("s3://%s/axiaops-athena-integration-test-results/%s/", bucket, runID)

	// The account/day/region every row shares -- FetchCosts groups by
	// (account, service, region, day), so productCode alone (not these) is
	// what keeps each test case's group isolated.
	const testAccountID = "999999999999999"
	const testRegion = "us-east-1"
	const testDay = "2026-01-15"
	const testBillingPeriod = "2026-01"

	rows := []athenaTestRow{
		{productCode: "TestSPCoveredUsage", lineItemType: "SavingsPlanCoveredUsage", spEffectiveCost: 2.40, unblendedCost: 6.00},
		{productCode: "TestSPRecurringFee", lineItemType: "SavingsPlanRecurringFee", spTotalCommitment: 1.00, spUsedCommitment: 0.80},
		{productCode: "TestDiscountedUsage", lineItemType: "DiscountedUsage", riEffectiveCost: 1.80, unblendedCost: 4.50},
		{productCode: "TestRIFee", lineItemType: "RIFee", riUnusedUpfront: 0.15, riUnusedRecurring: 0.05},
		{productCode: "TestUsageFallback", lineItemType: "Usage", unblendedCost: 3.60},
		// Both rows in this group are zero-contributing branches (Fee,
		// SavingsPlanNegation always map to 0 regardless of their raw
		// unblended_cost) -- the group's SUM lands at exactly 0, which the
		// real SQL's HAVING clause must exclude entirely from the result
		// set. amortization_formula_test.go tests each of these branches
		// individually returns 0; it cannot test that a GROUP summing to
		// zero is dropped, since that's an aggregate-level behavior with no
		// equivalent in the per-row Go mirror.
		{productCode: "TestZeroGroupDropped", lineItemType: "Fee", unblendedCost: 5.00},
		{productCode: "TestZeroGroupDropped", lineItemType: "SavingsPlanNegation", unblendedCost: -2.40},
	}

	want := map[string]float64{
		"TestSPCoveredUsage":  2.40,
		"TestSPRecurringFee":  0.20,
		"TestDiscountedUsage": 1.80,
		"TestRIFee":           0.20,
		"TestUsageFallback":   3.60,
		// TestZeroGroupDropped is deliberately absent from `want` -- it
		// must not appear in the results at all.
	}

	csv := buildAthenaTestCSV(rows, testAccountID, testRegion, testDay, testBillingPeriod)

	if _, err := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(dataKey),
		Body:   strings.NewReader(csv),
	}); err != nil {
		t.Fatalf("upload synthetic CUR CSV: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = s3Client.DeleteObject(cleanupCtx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(dataKey)})
	})

	// setupSource is a throwaway AthenaCURSource just for issuing DDL
	// (CREATE DATABASE/TABLE, DROP TABLE) via the same runQuery/poll
	// machinery FetchCosts itself uses -- table/database don't matter for
	// these since the DDL statements name them explicitly.
	setupSource := NewAthenaCURSource(athenaClient, database, table, workgroup, resultsS3)

	if _, err := setupSource.runQuery(ctx, fmt.Sprintf(`CREATE DATABASE IF NOT EXISTS %s`, database), "test_setup"); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	createTableSQL := fmt.Sprintf(`
CREATE EXTERNAL TABLE %s.%s (
  line_item_usage_account_id string,
  line_item_product_code string,
  product_region_code string,
  line_item_usage_start_date timestamp,
  line_item_resource_id string,
  line_item_line_item_type string,
  billing_period string,
  savings_plan_savings_plan_effective_cost double,
  savings_plan_total_commitment_to_date double,
  savings_plan_used_commitment double,
  reservation_effective_cost double,
  reservation_unused_amortized_upfront_fee_for_billing_period double,
  reservation_unused_recurring_fee double,
  line_item_unblended_cost double
)
ROW FORMAT DELIMITED
FIELDS TERMINATED BY ','
STORED AS TEXTFILE
LOCATION 's3://%s/%s'
TBLPROPERTIES ('skip.header.line.count'='1')`,
		database, table, bucket, dataPrefix)

	if _, err := setupSource.runQuery(ctx, createTableSQL, "test_setup"); err != nil {
		t.Fatalf("create synthetic CUR table: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = setupSource.runQuery(cleanupCtx, fmt.Sprintf(`DROP TABLE IF EXISTS %s.%s`, database, table), "test_cleanup")
	})

	// The real subject under test: FetchCosts -> buildAmortizedSQL ->
	// fetchAndParse, the exact path production ingestion uses.
	source := NewAthenaCURSource(athenaClient, database, table, workgroup, resultsS3)
	start, _ := time.Parse("2006-01-02", testDay)
	end := start.Add(24 * time.Hour)

	records, err := source.FetchCosts(ctx, start, end)
	if err != nil {
		t.Fatalf("FetchCosts against synthetic table: %v", err)
	}

	got := map[string]float64{}
	for _, r := range records {
		got[r.Service] = r.Amount
	}

	for productCode, wantAmount := range want {
		gotAmount, ok := got[productCode]
		if !ok {
			t.Errorf("%s: expected a CostRecord with amount %.2f, got none", productCode, wantAmount)
			continue
		}
		if diff := gotAmount - wantAmount; diff > 1e-6 || diff < -1e-6 {
			t.Errorf("%s: amortized_cost = %.6f, want %.6f", productCode, gotAmount, wantAmount)
		}
	}

	if amount, ok := got["TestZeroGroupDropped"]; ok {
		t.Errorf("TestZeroGroupDropped: expected this zero-sum group to be dropped by the real SQL's HAVING clause, got a CostRecord with amount %.6f", amount)
	}
}

// buildAthenaTestCSV renders rows in the exact column order the CREATE
// EXTERNAL TABLE statement above declares. No quoting needed -- every field
// here is a plain identifier or number, never contains a comma.
func buildAthenaTestCSV(rows []athenaTestRow, accountID, region, day, billingPeriod string) string {
	var b strings.Builder
	b.WriteString("account_id,product_code,region,usage_start_date,resource_id,line_item_type,billing_period,sp_effective_cost,sp_total_commitment,sp_used_commitment,ri_effective_cost,ri_unused_upfront,ri_unused_recurring,unblended_cost\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s,%s,%s,%s 00:00:00.000,,%s,%s,%.6f,%.6f,%.6f,%.6f,%.6f,%.6f,%.6f\n",
			accountID, r.productCode, region, day, r.lineItemType, billingPeriod,
			r.spEffectiveCost, r.spTotalCommitment, r.spUsedCommitment,
			r.riEffectiveCost, r.riUnusedUpfront, r.riUnusedRecurring, r.unblendedCost)
	}
	return b.String()
}
