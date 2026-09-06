package aws

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"

	"axiaops.io/shared/model"
)

// DiscoverWastefulLogGroups calls logs:DescribeLogGroups in each region present
// in the cost records and returns a ZombieResource for every log group that has
// no retention policy set (logs stored indefinitely). Empty log groups with a
// retention policy are harmless ($0 cost) and are not flagged. This is API-only
// — no CloudWatch metrics needed because DescribeLogGroups includes both
// retentionInDays and storedBytes.
func DiscoverWastefulLogGroups(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := discoveryRegions(records, awsClient.Region())
	accountID := awsClient.AccountID()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("cw-logs: load config", "region", region, "error", err)
			continue
		}
		client := cloudwatchlogs.NewFromConfig(cfg)
		rates := awsClient.Rates(region)

		var nextToken *string
		for {
			out, err := client.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
				NextToken: nextToken,
			})
			if err != nil {
				slog.Warn("cw-logs: DescribeLogGroups failed", "region", region, "error", err)
				break
			}

			for _, lg := range out.LogGroups {
				name := aws.ToString(lg.LogGroupName)
				if name == "" {
					continue
				}
				storedBytes := aws.ToInt64(lg.StoredBytes)
				storedGB := float64(storedBytes) / (1024 * 1024 * 1024)
				monthlyCost := storedGB * rates.CWLogsGBMonthly

				// Flag 1: no retention policy — logs stored forever.
				if lg.RetentionInDays == nil {
					zombies = append(zombies, model.ZombieResource{
						Provider:          "aws",
						AccountID:         accountID,
						InternalAccountID: internalAccountID,
						Service:           "AmazonCloudWatch",
						Region:            region,
						ResourceID:        name,
						Tags:              map[string]string{},
						MonthlyCost:       monthlyCost,
						Currency:          awsClient.Currency(),
						PeriodStart:       start,
						PeriodEnd:         end,
						UsageMetric:       "RetentionDays",
						UsageAvg:          0,
						UsageUnit:         "Days",
						Reason:            fmt.Sprintf("CloudWatch log group has no retention policy — %.1f GB stored indefinitely accumulating charges", storedGB),
						Owner:             "unknown",
					})
					slog.Info("cw-logs: no-retention log group flagged", "log_group", name, "stored_gb", fmt.Sprintf("%.1f", storedGB), "region", region)
				}
			}

			if out.NextToken == nil {
				break
			}
			nextToken = out.NextToken
		}
	}
	return zombies, nil
}
