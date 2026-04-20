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
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cloudwatchsdk "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/model"
	"axiaops.io/shared/retry"
)

const dateLayout = "2006-01-02"

// Client fetches costs from the AWS Cost Explorer API and usage from CloudWatch.
type Client struct {
	accountID string
	cfg       aws.Config
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
		cfg:       cfg,
		ce:        costexplorer.NewFromConfig(cfg),
		cw:        cloudwatchsdk.NewFromConfig(cfg),
	}, nil
}

// NewWithStaticCredentials builds a Client using the given access key (e.g. per-tenant scan)
// without mutating process-wide environment variables.
func NewWithStaticCredentials(ctx context.Context, accessKeyID, secretAccessKey, region string) (*Client, error) {
	if region == "" {
		region = "eu-central-1"
	}
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")),
		config.WithRegion(region),
	)
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
		cfg:       cfg,
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
	discovered := DiscoverResources(ctx, c, records)
	log.Printf("discover: found %d resources across %d cost records", len(discovered), len(records))
	return FetchUsage(ctx, c.cw, discovered, start, end)
}

func (c *Client) Name() string      { return "aws" }
func (c *Client) AccountID() string { return c.accountID }

// configForRegion creates a new AWS config for the specified region using the same credentials
func (c *Client) configForRegion(ctx context.Context, region string) (aws.Config, error) {
	cfg := c.cfg.Copy()
	cfg.Region = region
	return cfg, nil
}

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
		var page *costexplorer.GetCostAndUsageOutput
		err := retry.Do(ctx, retry.DefaultConfig(), func() error {
			var err error
			page, err = c.ce.GetCostAndUsage(ctx, input)
			return err
		})
		
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
				ceService := group.Keys[0]
				region := group.Keys[1]

				metric := group.Metrics["UnblendedCost"]
				amount, _ := strconv.ParseFloat(aws.ToString(metric.Amount), 64)

				records = append(records, model.CostRecord{
					Provider:    "aws",
					AccountID:   c.accountID,
					Service:     normalizeService(ceService),
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

// resourceLevelServices lists AWS services that reliably return resource-level
// IDs from Cost Explorer when grouped by RESOURCE_ID dimension.
var resourceLevelServices = []string{
	"Amazon Elastic Compute Cloud - Compute",
	"Amazon Relational Database Service",
	"AWS Lambda",
	"Amazon Elastic Load Balancing",
	"Amazon Virtual Private Cloud",
	"Amazon ElastiCache",
	"Amazon OpenSearch Service",
	"Amazon Redshift",
	"Amazon SageMaker",
	"Amazon DynamoDB",
	"Amazon Elastic Kubernetes Service",
}

// ceServiceToInternal maps Cost Explorer service names (as returned with
// RESOURCE_ID grouping) to the internal service names used in serviceRules.
var ceServiceToInternal = map[string]string{
	"Amazon Elastic Compute Cloud - Compute": "AmazonEC2",
	"Amazon Relational Database Service":     "AmazonRDS",
	"AWS Lambda":                             "AWSLambda",
	"Amazon Elastic Load Balancing":          "AmazonElasticLoadBalancing",
	"Amazon Virtual Private Cloud":           "AmazonVPC",
	"Amazon ElastiCache":                     "AmazonElastiCache",
	"Amazon OpenSearch Service":              "AmazonES",
	"Amazon Redshift":                        "AmazonRedshift",
	"Amazon SageMaker":                       "AmazonSageMaker",
	"Amazon DynamoDB":                        "AmazonDynamoDB",
	"Amazon Elastic Kubernetes Service":      "AmazonEKS",
	"Amazon Cost Explorer":                   "AWSCostExplorer",
	"Amazon CloudWatch":                      "AmazonCloudWatch",
	"Amazon Simple Storage Service":          "AmazonS3",
	"AWS Glue":                               "AWSGlue",
	"Amazon Simple Notification Service":     "AmazonSNS",
	"Amazon Simple Queue Service":            "AmazonSQS",
	"AWS Secrets Manager":                    "AWSSecretsManager",
	"AWS Key Management Service":             "AWSKms",
	"Amazon Glacier":                         "AmazonGlacier",
	"AWS CloudFormation":                     "AWSCloudFormation",
}

// normalizeService maps Cost Explorer service names to internal names.
// Unknown services are logged as warnings and returned as-is.
func normalizeService(ceService string) string {
	if mapped, ok := ceServiceToInternal[ceService]; ok {
		return mapped
	}
	slog.Warn("unknown AWS service from Cost Explorer", "service", ceService)
	return ceService
}

// FetchResourceCosts calls GetCostAndUsage grouped by SERVICE and RESOURCE_ID
// to get per-resource cost data. This is only reliable for a subset of services.
// Records returned have ResourceID populated, enabling Detect() to join with usage.
func (c *Client) FetchResourceCosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error) {
	// Build OR filter for supported services.
	filter := &types.Expression{
		Dimensions: &types.DimensionValues{
			Key:    types.DimensionService,
			Values: resourceLevelServices,
		},
	}

	input := &costexplorer.GetCostAndUsageInput{
		TimePeriod: &types.DateInterval{
			Start: aws.String(start.Format(dateLayout)),
			End:   aws.String(end.Format(dateLayout)),
		},
		Granularity: types.GranularityMonthly,
		Metrics:     []string{"UnblendedCost"},
		Filter:      filter,
		GroupBy: []types.GroupDefinition{
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("SERVICE")},
			{Type: types.GroupDefinitionTypeDimension, Key: aws.String("RESOURCE_ID")},
		},
	}

	var records []model.CostRecord

	for {
		var page *costexplorer.GetCostAndUsageOutput
		err := retry.Do(ctx, retry.DefaultConfig(), func() error {
			var err error
			page, err = c.ce.GetCostAndUsage(ctx, input)
			return err
		})

		if err != nil {
			// Non-fatal: resource-level data is supplemental.
			slog.Warn("aws: FetchResourceCosts failed, continuing without resource-level costs", "error", err)
			return nil, nil
		}

		for _, result := range page.ResultsByTime {
			periodStart, _ := time.Parse(dateLayout, aws.ToString(result.TimePeriod.Start))
			periodEnd, _ := time.Parse(dateLayout, aws.ToString(result.TimePeriod.End))

			for _, group := range result.Groups {
				ceService := group.Keys[0]
				resourceID := group.Keys[1]

				if resourceID == "" || resourceID == "NoResourceId" {
					continue
				}

				metric := group.Metrics["UnblendedCost"]
				amount, _ := strconv.ParseFloat(aws.ToString(metric.Amount), 64)
				if amount <= 0 {
					continue
				}

				// Extract region and short resource ID from ARN if possible.
				region, shortID := parseResourceID(resourceID)

				records = append(records, model.CostRecord{
					Provider:    "aws",
					AccountID:   c.accountID,
					Service:     normalizeService(ceService),
					Region:      region,
					ResourceID:  shortID,
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

	slog.Info("aws: fetched resource-level costs", "count", len(records))
	return records, nil
}

// parseResourceID extracts the region and short resource ID from an ARN or
// raw resource identifier returned by Cost Explorer.
// For ARNs like "arn:aws:ec2:us-east-1:123456789:instance/i-0abc123" it returns
// ("us-east-1", "i-0abc123"). For non-ARN values it returns ("", rawID).
func parseResourceID(raw string) (region, resourceID string) {
	if !strings.HasPrefix(raw, "arn:") {
		return "", raw
	}
	// ARN format: arn:partition:service:region:account:resource-type/resource-id
	parts := strings.SplitN(raw, ":", 8)
	if len(parts) < 6 {
		return "", raw
	}
	region = parts[3]

	// Resource ID is everything after the last "/" or ":" in the resource part.
	resource := strings.Join(parts[5:], ":")
	if idx := strings.LastIndex(resource, "/"); idx >= 0 {
		resourceID = resource[idx+1:]
	} else if idx := strings.LastIndex(resource, ":"); idx >= 0 {
		resourceID = resource[idx+1:]
	} else {
		resourceID = resource
	}

	if resourceID == "" {
		resourceID = raw
	}
	return region, resourceID
}
