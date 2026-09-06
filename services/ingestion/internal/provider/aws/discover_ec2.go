package aws

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"axiaops.io/shared/model"
)

func discoverEC2(ctx context.Context, cfg aws.Config) []string {
	client := ec2.NewFromConfig(cfg)
	var ids []string
	var nextToken *string
	for {
		out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			NextToken: nextToken,
		})
		if err != nil {
			slog.Warn("discover: EC2 DescribeInstances", "error", err)
			return nil
		}
		for _, r := range out.Reservations {
			for _, i := range r.Instances {
				if i.InstanceId != nil {
					ids = append(ids, aws.ToString(i.InstanceId))
				}
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return ids
}

// stoppedInstanceThreshold is the minimum time an instance must be stopped
// before it is flagged as a zombie. Stopped instances incur no compute charges
// but their attached EBS volumes still bill continuously.
const stoppedInstanceThreshold = 30 * 24 * time.Hour

// stoppedAtRe matches the stop timestamp embedded in EC2 StateTransitionReason.
// Example value: "User initiated (2024-01-15 14:30:45 GMT)"
var stoppedAtRe = regexp.MustCompile(`\((\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} GMT)\)`)

// DiscoverLongStoppedInstances calls ec2:DescribeInstances filtered to stopped
// state and flags any instance that has been stopped for longer than
// stoppedInstanceThreshold (30 days). The monthly cost is estimated from the
// total size of the instance's attached EBS volumes at $0.08/GB-month.
func DiscoverLongStoppedInstances(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := discoveryRegions(records, awsClient.Region())
	accountID := awsClient.AccountID()
	now := time.Now().UTC()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("stopped-ec2: load config", "region", region, "error", err)
			continue
		}
		client := ec2.NewFromConfig(cfg)

		// First pass: identify long-stopped instances and collect their volume IDs.
		type candidate struct {
			instanceID string
			stoppedAt  time.Time
			volumeIDs  []string
			tags       map[string]string
		}
		var candidates []candidate

		var instNextToken *string
		for {
			out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				Filters: []ec2types.Filter{
					{Name: aws.String("instance-state-name"), Values: []string{"stopped"}},
				},
				NextToken: instNextToken,
			})
			if err != nil {
				slog.Warn("stopped-ec2: DescribeInstances failed", "region", region, "error", err)
				break
			}

			for _, r := range out.Reservations {
				for _, i := range r.Instances {
					if i.InstanceId == nil {
						continue
					}
					stoppedAt, ok := parseStopTime(aws.ToString(i.StateTransitionReason))
					if !ok {
						continue // stop time unknown — skip rather than guess
					}
					if now.Sub(stoppedAt) < stoppedInstanceThreshold {
						continue // stopped recently, not a zombie yet
					}

					var volIDs []string
					for _, bdm := range i.BlockDeviceMappings {
						if bdm.Ebs != nil && bdm.Ebs.VolumeId != nil {
							volIDs = append(volIDs, aws.ToString(bdm.Ebs.VolumeId))
						}
					}
					tags := ec2TagsToMap(i.Tags)
					candidates = append(candidates, candidate{
						instanceID: aws.ToString(i.InstanceId),
						stoppedAt:  stoppedAt,
						volumeIDs:  volIDs,
						tags:       tags,
					})
				}
			}

			if out.NextToken == nil {
				break
			}
			instNextToken = out.NextToken
		}

		if len(candidates) == 0 {
			continue
		}

		// Second pass: batch-fetch volume sizes for accurate cost estimation.
		// DescribeVolumes accepts at most 200 IDs per call, so we chunk.
		allVolIDs := make([]string, 0)
		for _, c := range candidates {
			allVolIDs = append(allVolIDs, c.volumeIDs...)
		}
		volSizes := make(map[string]int32) // volumeID → sizeGB
		var volumeSizeFetchFailed bool
		const describeVolBatch = 200
		for i := 0; i < len(allVolIDs); i += describeVolBatch {
			end := i + describeVolBatch
			if end > len(allVolIDs) {
				end = len(allVolIDs)
			}
			volOut, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
				VolumeIds: allVolIDs[i:end],
			})
			if err != nil {
				slog.Warn("stopped-ec2: DescribeVolumes for attached volumes failed — skipping cost estimate for affected instances", "region", region, "error", err)
				volumeSizeFetchFailed = true
				break
			}
			for _, v := range volOut.Volumes {
				if v.VolumeId != nil && v.Size != nil {
					volSizes[aws.ToString(v.VolumeId)] = aws.ToInt32(v.Size)
				}
			}
		}

		rates := awsClient.Rates(region)
		for _, c := range candidates {
			totalGB := int32(0)
			for _, vid := range c.volumeIDs {
				totalGB += volSizes[vid]
			}
			// Skip if volume sizes are unknown and the instance has attached volumes —
			// reporting $0 cost would be misleading and erode trust in the findings.
			if volumeSizeFetchFailed && len(c.volumeIDs) > 0 {
				slog.Warn("stopped-ec2: skipping instance — volume sizes unknown", "instance_id", c.instanceID)
				continue
			}
			monthlyCost := float64(totalGB) * rates.EBSVolumeGBMonthly
			daysStop := int(now.Sub(c.stoppedAt).Hours() / 24)

			zombies = append(zombies, model.ZombieResource{
				Provider:          "aws",
				AccountID:         accountID,
				InternalAccountID: internalAccountID,
				Service:           "AmazonEC2",
				Region:            region,
				ResourceID:        c.instanceID,
				Tags:              c.tags,
				MonthlyCost:       monthlyCost,
				Currency:          awsClient.Currency(),
				PeriodStart:       start,
				PeriodEnd:         end,
				UsageMetric:       "DaysStopped",
				UsageAvg:          float64(daysStop),
				UsageUnit:         "Days",
				Reason:            fmt.Sprintf("EC2 instance stopped for %d days — attached EBS storage (%d GB) continues to bill at no compute benefit", daysStop, totalGB),
				Owner:             ownerFromTags(c.tags),
			})
			slog.Info("stopped-ec2: long-stopped instance flagged", "instance_id", c.instanceID, "days_stopped", daysStop, "region", region)
		}
	}
	return zombies, nil
}

// parseStopTime extracts the stop timestamp from an EC2 StateTransitionReason.
// Expected format: "User initiated (2024-01-15 14:30:45 GMT)"
func parseStopTime(reason string) (time.Time, bool) {
	m := stoppedAtRe.FindStringSubmatch(reason)
	if len(m) < 2 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02 15:04:05 MST", m[1])
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// oldAMIThreshold is the minimum age for an unused AMI to be considered stale.
const oldAMIThreshold = 90 * 24 * time.Hour

// DiscoverOldAMIs calls ec2:DescribeImages (owner=self) cross-referenced with
// ec2:DescribeInstances to determine which AMIs are still in use. Any AMI older
// than oldAMIThreshold (90 days) that is not referenced by any instance is
// flagged as a zombie. Cost is estimated from the total size of the AMI's
// backing EBS snapshots at $0.05/GB-month.
//
// Note: snapshots that back these AMIs are intentionally excluded from
// DiscoverOrphanedEBSSnapshots to avoid double-counting.
func DiscoverOldAMIs(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := discoveryRegions(records, awsClient.Region())
	accountID := awsClient.AccountID()
	now := time.Now().UTC()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("old-ami: load config", "region", region, "error", err)
			continue
		}
		client := ec2.NewFromConfig(cfg)
		rates := awsClient.Rates(region)

		// Collect the set of AMI IDs currently referenced by any instance
		// (running, stopped, or otherwise) in this region.
		inUseAMIs := make(map[string]struct{})
		var instNextToken *string
		for {
			instOut, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				NextToken: instNextToken,
			})
			if err != nil {
				slog.Warn("old-ami: DescribeInstances failed", "region", region, "error", err)
				break
			}
			for _, r := range instOut.Reservations {
				for _, i := range r.Instances {
					if i.ImageId != nil {
						inUseAMIs[aws.ToString(i.ImageId)] = struct{}{}
					}
				}
			}
			if instOut.NextToken == nil {
				break
			}
			instNextToken = instOut.NextToken
		}

		// Enumerate all AMIs owned by this account.
		var imgNextToken *string
		for {
			imgOut, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
				Owners:    []string{"self"},
				NextToken: imgNextToken,
			})
			if err != nil {
				slog.Warn("old-ami: DescribeImages failed", "region", region, "error", err)
				break
			}

			for _, img := range imgOut.Images {
				amiID := aws.ToString(img.ImageId)

				// Still referenced by at least one instance — not a zombie.
				if _, used := inUseAMIs[amiID]; used {
					continue
				}

				// Parse AMI creation date (AWS returns ISO 8601 / RFC 3339).
				createdAt, err := time.Parse(time.RFC3339, aws.ToString(img.CreationDate))
				if err != nil {
					// Fallback: AWS sometimes omits the timezone suffix.
					createdAt, err = time.Parse("2006-01-02T15:04:05.000Z", aws.ToString(img.CreationDate))
					if err != nil {
						slog.Warn("old-ami: could not parse creation date", "ami_id", amiID, "date", aws.ToString(img.CreationDate))
						continue
					}
				}
				if now.Sub(createdAt) < oldAMIThreshold {
					continue // AMI is recent enough
				}

				// Sum the size of all EBS snapshots that back this AMI.
				totalSnapshotGB := int32(0)
				for _, bdm := range img.BlockDeviceMappings {
					if bdm.Ebs != nil && bdm.Ebs.VolumeSize != nil {
						totalSnapshotGB += aws.ToInt32(bdm.Ebs.VolumeSize)
					}
				}
				monthlyCost := float64(totalSnapshotGB) * rates.EBSSnapshotGBMonthly
				ageDays := int(now.Sub(createdAt).Hours() / 24)

				tags := ec2TagsToMap(img.Tags)
				zombies = append(zombies, model.ZombieResource{
					Provider:          "aws",
					AccountID:         accountID,
					InternalAccountID: internalAccountID,
					Service:           "AmazonEC2",
					Region:            region,
					ResourceID:        amiID,
					Tags:              tags,
					MonthlyCost:       monthlyCost,
					Currency:          awsClient.Currency(),
					PeriodStart:       start,
					PeriodEnd:         end,
					UsageMetric:       "DaysSinceCreation",
					UsageAvg:          float64(ageDays),
					UsageUnit:         "Days",
					Reason:            fmt.Sprintf("AMI is %d days old and not referenced by any instance — backing snapshots (%d GB) accumulate storage charges", ageDays, totalSnapshotGB),
					Owner:             ownerFromTags(tags),
				})
				slog.Info("old-ami: unused AMI flagged", "ami_id", amiID, "age_days", ageDays, "snapshot_gb", totalSnapshotGB, "region", region)
			}

			if imgOut.NextToken == nil {
				break
			}
			imgNextToken = imgOut.NextToken
		}
	}
	return zombies, nil
}
