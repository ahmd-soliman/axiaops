package aws

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cloudwatchTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"axiaops.io/shared/model"
)

// discoverS3BucketsByRegion lists all S3 buckets and groups them by their
// actual region via GetBucketLocation. This ensures CloudWatch metric queries
// target the correct region. Buckets whose location cannot be determined are
// skipped with a warning.
func discoverS3BucketsByRegion(ctx context.Context, cfg aws.Config) map[string][]string {
	client := s3.NewFromConfig(cfg)

	// 1. List all buckets (global operation).
	var allBuckets []string
	var contToken *string
	for {
		out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{
			ContinuationToken: contToken,
		})
		if err != nil {
			slog.Warn("discover: S3 ListBuckets", "error", err)
			return nil
		}
		for _, b := range out.Buckets {
			if b.Name != nil {
				allBuckets = append(allBuckets, aws.ToString(b.Name))
			}
		}
		if out.ContinuationToken == nil {
			break
		}
		contToken = out.ContinuationToken
	}

	// 2. Determine each bucket's region.
	byRegion := make(map[string][]string)
	for _, name := range allBuckets {
		locOut, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{
			Bucket: aws.String(name),
		})
		if err != nil {
			slog.Warn("discover: S3 GetBucketLocation", "bucket", name, "error", err)
			continue
		}
		// AWS returns "" for us-east-1 (the default).
		region := string(locOut.LocationConstraint)
		if region == "" {
			region = "us-east-1"
		}
		byRegion[region] = append(byRegion[region], name)
	}
	return byRegion
}

// DiscoverIdleS3Buckets lists all S3 buckets and queries CloudWatch for the
// AllRequests metric (requires S3 request metrics to be enabled on the bucket).
// Buckets with zero requests are flagged. Buckets without request metrics
// configured are skipped (no CloudWatch data available).
func DiscoverIdleS3Buckets(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	cfg, err := awsClient.configForRegion(ctx, "us-east-1")
	if err != nil {
		return nil, fmt.Errorf("s3: load config: %w", err)
	}

	bucketsByRegion := discoverS3BucketsByRegion(ctx, cfg)
	if len(bucketsByRegion) == 0 {
		return nil, nil
	}

	// Estimate per-bucket cost from aggregate CE data.
	totalS3Cost := serviceCostFromRecords(records, "AmazonS3")
	totalBuckets := 0
	for _, buckets := range bucketsByRegion {
		totalBuckets += len(buckets)
	}
	perBucketCost := 0.0
	if totalBuckets > 0 && totalS3Cost > 0 {
		perBucketCost = totalS3Cost / float64(totalBuckets)
	}

	accountID := awsClient.AccountID()
	var zombies []model.ZombieResource

	for region, buckets := range bucketsByRegion {
		regionCfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("s3: load config for region", "region", region, "error", err)
			continue
		}
		cw := newCloudWatchClient(regionCfg)

		periodSecs := int32(end.Sub(start).Seconds())
		if periodSecs < 60 {
			periodSecs = 60
		}

		for _, bucketName := range buckets {
			// S3 metrics require FilterId dimension. "EntireBucket" is the default
			// filter ID when request metrics are enabled without a custom filter.
			avg, err := getMetricAvg(ctx, cw, "AWS/S3", "AllRequests", "BucketName", bucketName, start, end, periodSecs,
				[]cloudwatchTypes.Dimension{{Name: aws.String("FilterId"), Value: aws.String("EntireBucket")}})
			if err != nil {
				// No request metrics configured — skip silently.
				continue
			}

			// If CloudWatch returned no datapoints, the bucket likely doesn't have
			// request metrics enabled. Skip rather than false-positive.
			// (getMetricAvg returns -1 for no datapoints)
			if avg < 0 {
				continue
			}

			if avg > 0 {
				continue
			}

			zombies = append(zombies, model.ZombieResource{
				Provider:          "aws",
				AccountID:         accountID,
				InternalAccountID: internalAccountID,
				Service:           "AmazonS3",
				Region:            region,
				ResourceID:        bucketName,
				Tags:              map[string]string{},
				MonthlyCost:       perBucketCost,
				Currency:          awsClient.Currency(),
				PeriodStart:       start,
				PeriodEnd:         end,
				UsageMetric:       "AllRequests",
				UsageAvg:          0,
				UsageUnit:         "Count",
				Reason:            "S3 bucket has zero requests — likely abandoned (requires request metrics enabled)",
				Owner:             "unknown",
			})
			slog.Info("s3: idle bucket flagged", "bucket", bucketName, "region", region)
		}
	}

	return zombies, nil
}

// DiscoverIncompleteMultipartUploads iterates over all buckets and flags aborted
// or uncompleted multipart uploads older than 7 days that consume storage costs.
func DiscoverIncompleteMultipartUploads(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	cfg, err := awsClient.configForRegion(ctx, "us-east-1")
	if err != nil {
		return nil, fmt.Errorf("s3_multipart: load config: %w", err)
	}

	bucketsByRegion := discoverS3BucketsByRegion(ctx, cfg)
	if len(bucketsByRegion) == 0 {
		return nil, nil
	}

	accountID := awsClient.AccountID()
	cutoff := time.Now().AddDate(0, 0, -7)
	var zombies []model.ZombieResource

	for region, buckets := range bucketsByRegion {
		regionCfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("s3_multipart: load config for region", "region", region, "error", err)
			continue
		}
		client := s3.NewFromConfig(regionCfg)

		for _, bucketName := range buckets {
			out, err := client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
				Bucket: aws.String(bucketName),
			})
			if err != nil {
				slog.Warn("s3_multipart: ListMultipartUploads", "bucket", bucketName, "error", err)
				continue
			}

			staleUploads := 0
			for _, upload := range out.Uploads {
				if upload.Initiated != nil && upload.Initiated.Before(cutoff) {
					staleUploads++
				}
			}

			if staleUploads > 0 {
				z := model.ZombieResource{
					InternalAccountID: internalAccountID,
					AccountID:         accountID,
					Provider:          "aws",
					Service:           "AmazonS3",
					ResourceType:      "s3_multipart",
					ResourceID:        bucketName + "/incomplete-uploads",
					Region:            region,
					Tags:              map[string]string{"Bucket": bucketName},
					MonthlyCost:       10.00, // Nominal estimate
					Currency:          awsClient.Currency(),
					PeriodStart:       start,
					PeriodEnd:         end,
					UsageMetric:       "MultipartUploads",
					UsageAvg:          float64(staleUploads),
					UsageUnit:         "Count",
					Reason:            fmt.Sprintf("S3 bucket has %d incomplete multipart upload(s) older than 7 days", staleUploads),
					Owner:             "unknown",
				}
				zombies = append(zombies, z)
				slog.Info("s3_multipart: incomplete upload flagged", "bucket", bucketName, "count", staleUploads)
			}
		}
	}

	return zombies, nil
}
