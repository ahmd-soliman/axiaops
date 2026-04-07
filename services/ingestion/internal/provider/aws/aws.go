// Package aws implements the Provider interface for Amazon Web Services.
// It uses the AWS Cost Explorer API (GetCostAndUsage) to retrieve daily costs
// grouped by service and region, and normalizes them into model.CostRecord.
// Credentials are loaded automatically from the environment or ~/.aws/credentials.
package aws

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	cloudwatchsdk "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
)

const dateLayout = "2006-01-02"

// Client fetches costs from the AWS Cost Explorer API and usage from CloudWatch.
type Client struct {
	accountID string
	ce        CostExplorerAPI
	cw        CloudWatchAPI
}

// New loads AWS credentials from the environment or ~/.aws/credentials and
// resolves the account ID automatically via sts:GetCallerIdentity.
func New(ctx context.Context) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("aws: load config: %w", err)
	}
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("aws: GetCallerIdentity: %w", err)
	}
	accountID := aws.ToString(out.Account)
	log.Printf("aws: resolved account ID %s", accountID)
	return &Client{
		accountID: accountID,
		ce:        costexplorer.NewFromConfig(cfg),
		cw:        cloudwatchsdk.NewFromConfig(cfg),
	}, nil
}

// NewWithClient creates a Client with custom API implementations.
// Used in tests to inject mocks.
func NewWithClient(accountID string, ce CostExplorerAPI, cw CloudWatchAPI) *Client {
	return &Client{accountID: accountID, ce: ce, cw: cw}
}

// FetchUsage discovers resources via service APIs then queries CloudWatch
// for usage metrics for each discovered resource.
func (c *Client) FetchUsage(ctx context.Context, records []model.CostRecord, start, end time.Time) ([]analyzer.UsageRecord, error) {
	discovered := DiscoverResources(ctx, records)
	log.Printf("discover: found %d resources across %d cost records", len(discovered), len(records))
	return FetchUsage(ctx, c.cw, discovered, start, end)
}

func (c *Client) Name() string      { return "aws" }
func (c *Client) AccountID() string { return c.accountID }

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
			var unavail *types.DataUnavailableException
			if errors.As(err, &unavail) {
				return nil, fmt.Errorf(
					"aws: Cost Explorer data is not yet available for this account — " +
						"it can take up to 24 hours after first enabling Cost Explorer before data appears: %w", err)
			}
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
