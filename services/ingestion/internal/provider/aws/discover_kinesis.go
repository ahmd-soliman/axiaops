package aws

import (
	"context"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"

	"axiaops.io/shared/model"
)

func discoverKinesis(ctx context.Context, cfg aws.Config) []string {
	client := kinesis.NewFromConfig(cfg)
	var ids []string
	var nextToken *string
	for {
		out, err := client.ListStreams(ctx, &kinesis.ListStreamsInput{
			NextToken: nextToken,
		})
		if err != nil {
			slog.Warn("discover: Kinesis ListStreams", "error", err)
			return nil
		}
		ids = append(ids, out.StreamNames...)
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return ids
}

// DiscoverIdleKinesisStreams lists Kinesis streams in all regions found in cost
// records and queries CloudWatch for IncomingRecords. Streams with zero incoming
// records are flagged, with cost estimated from shard count.
func DiscoverIdleKinesisStreams(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := discoveryRegions(records, awsClient.Region())
	accountID := awsClient.AccountID()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("kinesis: load config", "region", region, "error", err)
			continue
		}

		streamNames := discoverKinesis(ctx, cfg)
		if len(streamNames) == 0 {
			continue
		}

		cw := newCloudWatchClient(cfg)
		kinesisClient := kinesis.NewFromConfig(cfg)
		rates := awsClient.Rates(region)

		periodSecs := int32(end.Sub(start).Seconds())
		if periodSecs < 60 {
			periodSecs = 60
		}

		for _, name := range streamNames {
			avg, err := getMetricAvg(ctx, cw, "AWS/Kinesis", "IncomingRecords", "StreamName", name, start, end, periodSecs, nil)
			if err != nil {
				slog.Warn("kinesis: GetMetricStatistics", "stream", name, "region", region, "error", err)
				continue
			}

			if avg > 0 {
				continue
			}

			// Estimate cost from shard count.
			monthlyCost := rates.KinesisShardHourly * 730 // 1 shard default
			desc, descErr := kinesisClient.DescribeStreamSummary(ctx, &kinesis.DescribeStreamSummaryInput{
				StreamName: aws.String(name),
			})
			if descErr == nil && desc.StreamDescriptionSummary != nil {
				shards := aws.ToInt32(desc.StreamDescriptionSummary.OpenShardCount)
				if shards > 0 {
					monthlyCost = float64(shards) * rates.KinesisShardHourly * 730
				}
			}

			zombies = append(zombies, model.ZombieResource{
				Provider:          "aws",
				AccountID:         accountID,
				InternalAccountID: internalAccountID,
				Service:           "AmazonKinesis",
				Region:            region,
				ResourceID:        name,
				Tags:              map[string]string{},
				MonthlyCost:       monthlyCost,
				Currency:          awsClient.Currency(),
				PeriodStart:       start,
				PeriodEnd:         end,
				UsageMetric:       "IncomingRecords",
				UsageAvg:          0,
				UsageUnit:         "Count",
				Reason:            "Kinesis data stream has zero incoming records — likely unused",
				Owner:             "unknown",
			})
			slog.Info("kinesis: idle stream flagged", "stream", name, "region", region)
		}
	}

	return zombies, nil
}
