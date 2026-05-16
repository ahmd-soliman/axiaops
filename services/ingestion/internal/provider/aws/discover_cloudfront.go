package aws

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cloudwatchTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"axiaops.io/shared/model"
)

func discoverCloudFront(ctx context.Context, cfg aws.Config) []string {
	// CloudFront is a global service — ListDistributions works from any region
	// and returns the same results. The globalDone guard in DiscoverResources
	// ensures this only runs once per scan.
	client := cloudfront.NewFromConfig(cfg)
	var ids []string
	var marker *string
	for {
		out, err := client.ListDistributions(ctx, &cloudfront.ListDistributionsInput{
			Marker: marker,
		})
		if err != nil {
			slog.Warn("discover: CloudFront ListDistributions", "error", err)
			return nil
		}
		if out.DistributionList != nil {
			for _, d := range out.DistributionList.Items {
				if d.Id != nil {
					ids = append(ids, aws.ToString(d.Id))
				}
			}
			if out.DistributionList.IsTruncated != nil && *out.DistributionList.IsTruncated && out.DistributionList.NextMarker != nil {
				marker = out.DistributionList.NextMarker
			} else {
				break
			}
		} else {
			break
		}
	}
	return ids
}

// DiscoverIdleCloudFrontDistributions lists all CloudFront distributions and
// queries CloudWatch for the Requests metric. Distributions with zero requests
// over the lookback period are flagged as idle.
func DiscoverIdleCloudFrontDistributions(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	cfg, err := awsClient.configForRegion(ctx, "us-east-1")
	if err != nil {
		return nil, fmt.Errorf("cloudfront: load config: %w", err)
	}

	ids := discoverCloudFront(ctx, cfg)
	if len(ids) == 0 {
		return nil, nil
	}

	// Estimate per-distribution cost from aggregate CE data.
	totalCFCost := serviceCostFromRecords(records, "AmazonCloudFront")
	perDistCost := 0.0
	if len(ids) > 0 && totalCFCost > 0 {
		perDistCost = totalCFCost / float64(len(ids))
	}

	// CloudFront metrics live in us-east-1 with Region=Global dimension.
	cwCfg, err := awsClient.configForRegion(ctx, "us-east-1")
	if err != nil {
		return nil, fmt.Errorf("cloudfront: cw config: %w", err)
	}
	cw := newCloudWatchClient(cwCfg)

	periodSecs := int32(end.Sub(start).Seconds())
	if periodSecs < 60 {
		periodSecs = 60
	}

	accountID := awsClient.AccountID()
	var zombies []model.ZombieResource

	for _, distID := range ids {
		avg, err := getMetricAvg(ctx, cw, "AWS/CloudFront", "Requests", "DistributionId", distID, start, end, periodSecs,
			[]cloudwatchTypes.Dimension{{Name: aws.String("Region"), Value: aws.String("Global")}})
		if err != nil {
			slog.Warn("cloudfront: GetMetricStatistics", "distribution_id", distID, "error", err)
			continue
		}

		if avg > 0 {
			continue
		}

		zombies = append(zombies, model.ZombieResource{
			Provider:          "aws",
			AccountID:         accountID,
			InternalAccountID: internalAccountID,
			Service:           "AmazonCloudFront",
			Region:            "global",
			ResourceID:        distID,
			Tags:              map[string]string{},
			MonthlyCost:       perDistCost,
			Currency:          awsClient.Currency(),
			PeriodStart:       start,
			PeriodEnd:         end,
			UsageMetric:       "Requests",
			UsageAvg:          0,
			UsageUnit:         "Count",
			Reason:            "CloudFront distribution has zero requests — likely abandoned",
			Owner:             "unknown",
		})
		slog.Info("cloudfront: idle distribution flagged", "distribution_id", distID)
	}

	return zombies, nil
}
