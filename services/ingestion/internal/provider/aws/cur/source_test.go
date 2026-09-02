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
