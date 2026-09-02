package cur

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"axiaops.io/shared/model"
)

// fetchAndParse runs the query and paginates through results, mapping them to CostRecord.
func (s *AthenaCURSource) fetchAndParse(ctx context.Context, sql string, start, end time.Time, queryType string) ([]model.CostRecord, error) {
	execID, err := s.runQuery(ctx, sql, queryType)
	if err != nil {
		return nil, err
	}

	var records []model.CostRecord
	now := time.Now().UTC()

	var nextToken *string
	isHeader := true
	for {
		page, err := s.client.GetQueryResults(ctx, &athena.GetQueryResultsInput{
			QueryExecutionId: aws.String(execID),
			NextToken:        nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("get query results: %w", err)
		}

		for _, row := range page.ResultSet.Rows {
			if isHeader {
				isHeader = false
				continue
			}
			
			d := row.Data
			if len(d) < 6 {
				continue // malformed row
			}

			accountID := *d[0].VarCharValue
			service := *d[1].VarCharValue
			region := ""
			if d[2].VarCharValue != nil {
				region = *d[2].VarCharValue
			}
			periodStartStr := *d[3].VarCharValue
			resourceID := ""
			if d[4].VarCharValue != nil {
				resourceID = *d[4].VarCharValue
			}
			costStr := *d[5].VarCharValue

			cost, _ := strconv.ParseFloat(costStr, 64)
			if cost == 0 {
				continue
			}

			periodStart, err := time.Parse("2006-01-02", periodStartStr)
			if err != nil {
				// fallback if dates are weird
				periodStart = start
			}

			records = append(records, model.CostRecord{
				Provider:    "aws",
				AccountID:   accountID,
				Service:     service, // needs mapping to KnownServices? No, CUR output usually matches CE for most services? We'll see.
				Region:      region,
				ResourceID:  resourceID,
				Amount:      cost,
				Currency:    "USD", // CUR is always USD natively
				PeriodStart: periodStart,
				PeriodEnd:   periodStart.Add(24 * time.Hour), // daily grain
				CostBasis:   model.CostBasisBilled,
				FetchedAt:   now,
			})
		}
		nextToken = page.NextToken
		if nextToken == nil {
			break
		}
	}
	return records, nil
}

func (s *AthenaCURSource) FetchCosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error) {
	sql := s.buildAmortizedSQL(start, end, false)
	return s.fetchAndParse(ctx, sql, start, end, "aggregate_costs")
}

func (s *AthenaCURSource) FetchResourceCosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error) {
	sql := s.buildAmortizedSQL(start, end, true)
	return s.fetchAndParse(ctx, sql, start, end, "resource_costs")
}
