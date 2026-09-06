package aws

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"

	"axiaops.io/shared/model"
)

// ecrStaleImageThreshold is the minimum age for a tagged image to be
// considered stale. Only applies to images that are NOT the most recently
// pushed in their repository — the latest push is always kept regardless of age.
const ecrStaleImageThreshold = 90 * 24 * time.Hour

// DiscoverStaleECRImages calls ecr:DescribeRepositories and ecr:DescribeImages
// in each region present in the cost records. It flags repositories that contain
// untagged images or tagged images older than 90 days (excluding the most
// recently pushed image). Results are summarized per repository — one zombie per
// repo with total waste across all stale images.
func DiscoverStaleECRImages(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := discoveryRegions(records, awsClient.Region())
	accountID := awsClient.AccountID()
	now := time.Now().UTC()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("ecr: load config", "region", region, "error", err)
			continue
		}
		client := ecr.NewFromConfig(cfg)

		// 1. List all repositories in the region.
		var repos []string
		var repoNextToken *string
		for {
			repoOut, err := client.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
				NextToken: repoNextToken,
			})
			if err != nil {
				slog.Warn("ecr: DescribeRepositories failed", "region", region, "error", err)
				break
			}
			for _, r := range repoOut.Repositories {
				if r.RepositoryName != nil {
					repos = append(repos, aws.ToString(r.RepositoryName))
				}
			}
			if repoOut.NextToken == nil {
				break
			}
			repoNextToken = repoOut.NextToken
		}

		// 2. For each repository, describe images and find waste.
		//    DescribeImages is 1 call per repo (up to 1000 images/page).
		//    Cap at 500 repos to avoid throttling on very large accounts.
		const maxECRRepos = 500
		if len(repos) > maxECRRepos {
			slog.Warn("ecr: capping repository scan", "total_repos", len(repos), "scanning", maxECRRepos, "region", region)
			repos = repos[:maxECRRepos]
		}
		if len(repos) > 0 {
			slog.Info("ecr: scanning repositories", "count", len(repos), "region", region)
		}
		rates := awsClient.Rates(region)
		for _, repoName := range repos {
			// Fetch all images in this repository.
			var imgNextToken *string
			var allImages []ecrImageInfo

			for {
				imgOut, err := client.DescribeImages(ctx, &ecr.DescribeImagesInput{
					RepositoryName: aws.String(repoName),
					NextToken:      imgNextToken,
				})
				if err != nil {
					slog.Warn("ecr: DescribeImages failed", "repo", repoName, "region", region, "error", err)
					break
				}
				for _, img := range imgOut.ImageDetails {
					pushed := time.Time{}
					if img.ImagePushedAt != nil {
						pushed = *img.ImagePushedAt
					}
					allImages = append(allImages, ecrImageInfo{
						sizeBytes: aws.ToInt64(img.ImageSizeInBytes),
						pushedAt:  pushed,
						tagged:    len(img.ImageTags) > 0,
					})
				}
				if imgOut.NextToken == nil {
					break
				}
				imgNextToken = imgOut.NextToken
			}

			// Classify stale images using the extracted pure function.
			staleCount, staleSizeBytes := classifyECRImages(allImages, ecrStaleImageThreshold, now)

			if staleCount == 0 {
				continue
			}

			staleGB := float64(staleSizeBytes) / (1024 * 1024 * 1024)
			monthlyCost := staleGB * rates.ECRStorageGBMonthly

			zombies = append(zombies, model.ZombieResource{
				Provider:          "aws",
				AccountID:         accountID,
				InternalAccountID: internalAccountID,
				Service:           "AmazonECR",
				Region:            region,
				ResourceID:        repoName,
				Tags:              map[string]string{},
				MonthlyCost:       monthlyCost,
				Currency:          awsClient.Currency(),
				PeriodStart:       start,
				PeriodEnd:         end,
				UsageMetric:       "StaleImageCount",
				UsageAvg:          float64(staleCount),
				UsageUnit:         "Count",
				Reason:            fmt.Sprintf("ECR repository has %d untagged/stale images totaling %.1f GB — accumulating $%.2f/month in storage", staleCount, staleGB, monthlyCost),
				Owner:             "unknown",
			})
			slog.Info("ecr: stale images flagged", "repo", repoName, "stale_count", staleCount, "stale_gb", fmt.Sprintf("%.1f", staleGB), "region", region)
		}
	}
	return zombies, nil
}

// ecrImageInfo holds the metadata needed to classify an ECR image as stale.
type ecrImageInfo struct {
	sizeBytes int64
	pushedAt  time.Time
	tagged    bool
}

// classifyECRImages returns the count and total size of stale images in a repo.
// An image is stale if it is (a) untagged and not the latest push, or
// (b) tagged, older than threshold, and not the latest push.
func classifyECRImages(images []ecrImageInfo, threshold time.Duration, now time.Time) (staleCount int, staleSizeBytes int64) {
	if len(images) == 0 {
		return 0, 0
	}
	var latestPush time.Time
	for _, img := range images {
		if img.pushedAt.After(latestPush) {
			latestPush = img.pushedAt
		}
	}
	for _, img := range images {
		isLatest := !img.pushedAt.IsZero() && img.pushedAt.Equal(latestPush)
		if !img.tagged && !isLatest {
			staleCount++
			staleSizeBytes += img.sizeBytes
			continue
		}
		if img.tagged && !isLatest && !img.pushedAt.IsZero() && now.Sub(img.pushedAt) > threshold {
			staleCount++
			staleSizeBytes += img.sizeBytes
		}
	}
	return
}
