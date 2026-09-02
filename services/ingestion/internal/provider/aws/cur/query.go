package cur

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"github.com/aws/aws-sdk-go-v2/service/athena/types"
	)

// runQuery executes the SQL and polls for completion, returning the execution ID.
func (s *AthenaCURSource) runQuery(ctx context.Context, sql string, queryType string) (string, error) {
	startTime := time.Now()
	out, err := s.client.StartQueryExecution(ctx, &athena.StartQueryExecutionInput{
		QueryString:         aws.String(sql),
		QueryExecutionContext: &types.QueryExecutionContext{Database: aws.String(s.database)},
		ResultConfiguration: &types.ResultConfiguration{OutputLocation: aws.String(s.resultsS3)},
		WorkGroup:           aws.String(s.workgroup),
	})
	if err != nil {
		return "", fmt.Errorf("start query: %w", err)
	}

	execID := *out.QueryExecutionId
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Best-effort cancel the query on the backend.
			_, _ = s.client.StopQueryExecution(context.Background(), &athena.StopQueryExecutionInput{
				QueryExecutionId: aws.String(execID),
			})
			return "", ctx.Err()
		case <-ticker.C:
			status, err := s.client.GetQueryExecution(ctx, &athena.GetQueryExecutionInput{
				QueryExecutionId: aws.String(execID),
			})
			if err != nil {
				return "", fmt.Errorf("get query execution: %w", err)
			}
			state := status.QueryExecution.Status.State
			switch state {
			case types.QueryExecutionStateSucceeded:
				duration := time.Since(startTime).Seconds()
				queryDurationSeconds.WithLabelValues(queryType).Observe(duration)
				if status.QueryExecution.Statistics != nil && status.QueryExecution.Statistics.DataScannedInBytes != nil {
					bytesScannedTotal.WithLabelValues(queryType).Add(float64(*status.QueryExecution.Statistics.DataScannedInBytes))
				}
				return execID, nil
			case types.QueryExecutionStateFailed:
				queryErrorsTotal.WithLabelValues(queryType).Inc()
				return "", fmt.Errorf("%w: %s", ErrQueryFailed, *status.QueryExecution.Status.StateChangeReason)
			case types.QueryExecutionStateCancelled:
				return "", ErrQueryCanceled
			}
			// Queued or Running, keep polling
		}
	}
}

// buildAmortizedSQL constructs the CUDOS-compatible amortization query.
// It uses billing_period as a partition key (formatted YYYY-MM) for pruning.
func (s *AthenaCURSource) buildAmortizedSQL(start, end time.Time, resourceLevel bool) string {
	// Format dates for SQL injection (safely parameterized usually, but Athena doesn't fully support
	// parameterized queries for everything nicely in v2 SDK without ExecutionParameters, 
	// we will inject safely formatted strings).
	
	// Ensure we span the correct billing periods. If start/end cross months, we need to query multiple partitions.
	// For simplicity, we just use a basic string here for now. A robust implementation would generate
	// (billing_period = '2026-08' OR billing_period = '2026-09')
	
	billingPeriodStart := start.Format("2006-01")
	billingPeriodEnd := end.Add(-24 * time.Hour).Format("2006-01") // exclusive end boundary usually
	
	periodCondition := fmt.Sprintf("billing_period = '%s'", billingPeriodStart)
	if billingPeriodStart != billingPeriodEnd {
		periodCondition = fmt.Sprintf("(billing_period = '%s' OR billing_period = '%s')", billingPeriodStart, billingPeriodEnd)
	}

	resourceField := "''"
	groupBy := "1, 2, 3, 4"
	if resourceLevel {
		resourceField = "line_item_resource_id"
		groupBy = "1, 2, 3, 4, 5"
	}

	return fmt.Sprintf(`
SELECT
  line_item_usage_account_id AS account_id,
  line_item_product_code AS service,
  product_region_code AS region,
  DATE(line_item_usage_start_date) AS period_start,
  %s AS resource_id,
  SUM(CASE line_item_line_item_type
    WHEN 'SavingsPlanCoveredUsage' THEN savings_plan_savings_plan_effective_cost
    WHEN 'SavingsPlanRecurringFee' THEN savings_plan_total_commitment_to_date - savings_plan_used_commitment
    WHEN 'SavingsPlanNegation'     THEN 0
    WHEN 'SavingsPlanUpfrontFee'   THEN 0
    WHEN 'DiscountedUsage'         THEN reservation_effective_cost
    WHEN 'RIFee'                   THEN reservation_unused_amortized_upfront_fee_for_billing_period + reservation_unused_recurring_fee
    WHEN 'Fee'                     THEN 0
    ELSE line_item_unblended_cost
  END) AS amortized_cost
FROM "%s"."%s"
WHERE %s
  AND line_item_usage_start_date >= TIMESTAMP '%s'
  AND line_item_usage_start_date < TIMESTAMP '%s'
  AND line_item_line_item_type NOT IN ('Tax', 'Credit', 'Refund')
GROUP BY %s
HAVING SUM(CASE line_item_line_item_type
    WHEN 'SavingsPlanCoveredUsage' THEN savings_plan_savings_plan_effective_cost
    WHEN 'SavingsPlanRecurringFee' THEN savings_plan_total_commitment_to_date - savings_plan_used_commitment
    WHEN 'SavingsPlanNegation'     THEN 0
    WHEN 'SavingsPlanUpfrontFee'   THEN 0
    WHEN 'DiscountedUsage'         THEN reservation_effective_cost
    WHEN 'RIFee'                   THEN reservation_unused_amortized_upfront_fee_for_billing_period + reservation_unused_recurring_fee
    WHEN 'Fee'                     THEN 0
    ELSE line_item_unblended_cost
  END) > 0.000001`, 
		resourceField, 
		s.database, 
		s.table, 
		periodCondition, 
		start.Format("2006-01-02 15:04:05"), 
		end.Format("2006-01-02 15:04:05"),
		groupBy)
}
