// Package aws implements the Provider interface for Amazon Web Services.
// It uses the AWS Cost Explorer API (GetCostAndUsage) to retrieve daily costs
// grouped by service and region, and normalizes them into model.CostRecord.
// Credentials are loaded automatically from the environment or ~/.aws/credentials.
package aws

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"axiaops.io/ingestion/internal/model"
)

const dateLayout = "2006-01-02"

// Client fetches costs from the AWS Cost Explorer API.
type Client struct {
	accountID string
	ce        *costexplorer.Client
}

// New loads AWS credentials from the environment (AWS_ACCESS_KEY_ID,
// AWS_SECRET_ACCESS_KEY, AWS_REGION) or from ~/.aws/credentials.
func New(ctx context.Context, accountID string) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("aws: load config: %w", err)
	}
	return &Client{
		accountID: accountID,
		ce:        costexplorer.NewFromConfig(cfg),
	}, nil
}

func (c *Client) Name() string { return "aws" }

// FetchCosts calls GetCostAndUsage with daily granularity, grouped by
// SERVICE and REGION, and normalizes each result into a CostRecord.
func (c *Client) FetchCosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error) {
	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format(dateLayout)),
			End:   aws.String(end.Format(dateLayout)),
		},
		Granularity: types.GranularityDaily,
		Metrics:     []string{"UnblendedCost"},
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("SERVICE")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("REGION")},
		},
	}

	var records []model.CostRecord

	for {
		page, err := c.ce.GetCostAndUsage(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("aws: GetCostAndUsage: %w", err)
		}

		for _, result := range page.ResultsByTime {
			periodStart, _ := time.Parse(dateLayout, aws.ToString(result.TimePeriod.Start))
			periodEnd, _ := time.Parse(dateLayout, aws.ToString(result.TimePeriod.End))

			for _, group := range result.Groups {
				service := group.Keys[0]
				region := group.Keys[1]

				metric := group.Metrics["UnblendedCost"]
				amount, _ := strconv.ParseFloat(aws.ToString(metric.Amount), 64)

				records = append(records, model.CostRecord{
					Provider:    "aws",
					AccountID:   c.accountID,
					Service:     service,
					Region:      region,
					Amount:      amount,
					Currency:    aws.ToString(metric.Unit),
					PeriodStart: periodStart,
					PeriodEnd:   periodEnd,
					FetchedAt:   time.Now().UTC(),
				})
			}
		}

		if page.NextPageToken == nil {
			break
		}
		input.NextPageToken = page.NextPageToken
	}

	return records, nil
}
