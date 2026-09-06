package cur

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"github.com/aws/aws-sdk-go-v2/service/athena/types"
	"axiaops.io/shared/model"
)

type mockAthena struct {
	AthenaAPI
	execID string
	rows   [][]string
}

func (m *mockAthena) StartQueryExecution(ctx context.Context, params *athena.StartQueryExecutionInput, optFns ...func(*athena.Options)) (*athena.StartQueryExecutionOutput, error) {
	return &athena.StartQueryExecutionOutput{
		QueryExecutionId: aws.String(m.execID),
	}, nil
}

func (m *mockAthena) GetQueryExecution(ctx context.Context, params *athena.GetQueryExecutionInput, optFns ...func(*athena.Options)) (*athena.GetQueryExecutionOutput, error) {
	return &athena.GetQueryExecutionOutput{
		QueryExecution: &types.QueryExecution{
			Status: &types.QueryExecutionStatus{
				State: types.QueryExecutionStateSucceeded,
			},
			Statistics: &types.QueryExecutionStatistics{
				DataScannedInBytes: aws.Int64(1024),
			},
		},
	}, nil
}

func (m *mockAthena) GetQueryResults(ctx context.Context, params *athena.GetQueryResultsInput, optFns ...func(*athena.Options)) (*athena.GetQueryResultsOutput, error) {
	var parsedRows []types.Row
	
	// Add header
	header := types.Row{Data: []types.Datum{
		{VarCharValue: aws.String("account_id")},
		{VarCharValue: aws.String("service")},
		{VarCharValue: aws.String("region")},
		{VarCharValue: aws.String("period_start")},
		{VarCharValue: aws.String("resource_id")},
		{VarCharValue: aws.String("amortized_cost")},
	}}
	parsedRows = append(parsedRows, header)

	// Add data
	for _, r := range m.rows {
		row := types.Row{Data: []types.Datum{
			{VarCharValue: aws.String(r[0])},
			{VarCharValue: aws.String(r[1])},
			{VarCharValue: aws.String(r[2])},
			{VarCharValue: aws.String(r[3])},
			{VarCharValue: aws.String(r[4])},
			{VarCharValue: aws.String(r[5])},
		}}
		parsedRows = append(parsedRows, row)
	}

	return &athena.GetQueryResultsOutput{
		ResultSet: &types.ResultSet{
			Rows: parsedRows,
		},
	}, nil
}

func TestFetchResourceCosts(t *testing.T) {
	mock := &mockAthena{
		execID: "query-123",
		rows: [][]string{
			{"111122223333", "AmazonEC2", "us-east-1", "2026-08-01", "i-0abcd1234efgh5678", "1.25"},
			{"111122223333", "AmazonS3", "us-west-2", "2026-08-01", "my-bucket", "0.50"},
		},
	}

	source := NewAthenaCURSource(mock, "db", "tbl", "wg", "s3://res")
	// Make poll very fast for test
	source.pollInterval = time.Millisecond

	start, _ := time.Parse("2006-01-02", "2026-08-01")
	end := start.Add(24 * time.Hour)

	costs, err := source.FetchResourceCosts(context.Background(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(costs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(costs))
	}

	c1 := costs[0]
	if c1.Service != "AmazonEC2" || c1.Amount != 1.25 || c1.CostBasis != model.CostBasisBilled {
		t.Errorf("record 1 parsed incorrectly: %+v", c1)
	}
	
	c2 := costs[1]
	if c2.ResourceID != "my-bucket" || c2.Amount != 0.50 {
		t.Errorf("record 2 parsed incorrectly: %+v", c2)
	}
}

// TestFetchResourceCosts_SkipsBlankResourceID guards against re-introducing
// the upsert-key collision bug: RI/SP fee line items (RIFee,
// SavingsPlanRecurringFee) carry no resource attribution and come back with
// resource_id="", which is the same sentinel FetchCosts uses for its own
// aggregate row (service, region, resource_id="", period_start). Saving a
// resource-level row here would silently overwrite that correct aggregate
// with just the fee's isolated amount.
func TestFetchResourceCosts_SkipsBlankResourceID(t *testing.T) {
	mock := &mockAthena{
		execID: "query-456",
		rows: [][]string{
			{"111122223333", "AmazonRDS", "us-east-1", "2026-08-05", "db-1", "5.00"},
			{"111122223333", "AmazonRDS", "us-east-1", "2026-08-05", "", "1.50"},
		},
	}

	source := NewAthenaCURSource(mock, "db", "tbl", "wg", "s3://res")
	source.pollInterval = time.Millisecond

	start, _ := time.Parse("2006-01-02", "2026-08-05")
	end := start.Add(24 * time.Hour)

	costs, err := source.FetchResourceCosts(context.Background(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(costs) != 1 {
		t.Fatalf("expected 1 record (blank resource_id dropped), got %d: %+v", len(costs), costs)
	}
	if costs[0].ResourceID != "db-1" || costs[0].Amount != 5.00 {
		t.Errorf("expected surviving record db-1/5.00, got %+v", costs[0])
	}
}

// TestFetchCosts_KeepsBlankResourceID is the mirror check: the aggregate
// (non-resource-level) query never populates resource_id at all — every row
// legitimately uses the "" sentinel — so FetchCosts must not apply the same
// skip, or every aggregate row would be dropped.
func TestFetchCosts_KeepsBlankResourceID(t *testing.T) {
	mock := &mockAthena{
		execID: "query-789",
		rows: [][]string{
			{"111122223333", "AmazonRDS", "us-east-1", "2026-08-05", "", "6.50"},
		},
	}

	source := NewAthenaCURSource(mock, "db", "tbl", "wg", "s3://res")
	source.pollInterval = time.Millisecond

	start, _ := time.Parse("2006-01-02", "2026-08-05")
	end := start.Add(24 * time.Hour)

	costs, err := source.FetchCosts(context.Background(), start, end)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(costs) != 1 {
		t.Fatalf("expected 1 aggregate record, got %d: %+v", len(costs), costs)
	}
	if costs[0].Amount != 6.50 {
		t.Errorf("expected amount 6.50, got %+v", costs[0])
	}
}
