package aws

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"axiaops.io/shared/model"
)

// DiscoverUnattachedEBSVolumes calls ec2:DescribeVolumes in each region and
// returns a ZombieResource for every volume whose state is "available" (i.e. not
// mounted to any instance). AWS charges for EBS storage regardless of whether
// the volume is attached, making these invisible but guaranteed waste.
func DiscoverUnattachedEBSVolumes(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := discoveryRegions(records, awsClient.Region())
	accountID := awsClient.AccountID()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("ebs-vol: load config", "region", region, "error", err)
			continue
		}
		client := ec2.NewFromConfig(cfg)

		var nextToken *string
		for {
			out, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
				Filters: []ec2types.Filter{
					{Name: aws.String("status"), Values: []string{"available"}},
				},
				NextToken: nextToken,
			})
			if err != nil {
				slog.Warn("ebs-vol: DescribeVolumes failed", "region", region, "error", err)
				break
			}

			rates := awsClient.Rates(region)
			for _, vol := range out.Volumes {
				volID := aws.ToString(vol.VolumeId)
				sizeGB := aws.ToInt32(vol.Size)
				volType := string(vol.VolumeType)
				monthlyCost := float64(sizeGB) * rates.EBSVolumeGBMonthly
				tags := ec2TagsToMap(vol.Tags)

				zombies = append(zombies, model.ZombieResource{
					Provider:          "aws",
					AccountID:         accountID,
					InternalAccountID: internalAccountID,
					Service:           "AmazonEC2",
					Region:            region,
					ResourceID:        volID,
					Tags:              tags,
					MonthlyCost:       monthlyCost,
					Currency:          awsClient.Currency(),
					PeriodStart:       start,
					PeriodEnd:         end,
					UsageMetric:       "VolumeState",
					UsageAvg:          0,
					UsageUnit:         "State",
					Reason:            fmt.Sprintf("EBS volume (%d GB %s) is unattached — not mounted to any instance but still incurring storage charges", sizeGB, volType),
					Owner:             ownerFromTags(tags),
				})
				slog.Info("ebs-vol: unattached volume flagged", "volume_id", volID, "size_gb", sizeGB, "region", region)
			}

			if out.NextToken == nil {
				break
			}
			nextToken = out.NextToken
		}
	}
	return zombies, nil
}

// DiscoverOrphanedEBSSnapshots calls ec2:DescribeSnapshots (filtered to account
// owner) and flags any snapshot whose source volume no longer exists AND that
// does not back a registered AMI. These snapshots accumulate silently at
// $0.05/GB-month and are safe to delete once both conditions are met.
func DiscoverOrphanedEBSSnapshots(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := discoveryRegions(records, awsClient.Region())
	accountID := awsClient.AccountID()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("ebs-snap: load config", "region", region, "error", err)
			continue
		}
		client := ec2.NewFromConfig(cfg)

		// 1. Build the set of volume IDs that currently exist in this region.
		//    If this call fails we cannot safely distinguish "orphaned" from
		//    "source volume exists but DescribeVolumes returned an error", so
		//    skip the entire region rather than producing false positives.
		existingVolumes := make(map[string]struct{})
		var volumeFetchFailed bool
		var volNextToken *string
		for {
			volOut, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
				NextToken: volNextToken,
			})
			if err != nil {
				slog.Warn("ebs-snap: DescribeVolumes failed", "region", region, "error", err)
				volumeFetchFailed = true
				break
			}
			for _, v := range volOut.Volumes {
				if v.VolumeId != nil {
					existingVolumes[aws.ToString(v.VolumeId)] = struct{}{}
				}
			}
			if volOut.NextToken == nil {
				break
			}
			volNextToken = volOut.NextToken
		}
		if volumeFetchFailed {
			continue // Cannot safely determine orphan status without the volume list
		}

		// 2. Build the set of snapshot IDs referenced by registered AMIs.
		//    These are "in use" even though their source volume may be gone.
		amiSnapshots := make(map[string]struct{})
		var imgNextToken *string
		for {
			imgOut, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
				Owners:    []string{"self"},
				NextToken: imgNextToken,
			})
			if err != nil {
				slog.Warn("ebs-snap: DescribeImages failed", "region", region, "error", err)
				break
			}
			for _, img := range imgOut.Images {
				for _, bdm := range img.BlockDeviceMappings {
					if bdm.Ebs != nil && bdm.Ebs.SnapshotId != nil {
						amiSnapshots[aws.ToString(bdm.Ebs.SnapshotId)] = struct{}{}
					}
				}
			}
			if imgOut.NextToken == nil {
				break
			}
			imgNextToken = imgOut.NextToken
		}

		// 3. Enumerate all snapshots owned by this account and flag orphans.
		rates := awsClient.Rates(region)
		var snapNextToken *string
		for {
			snapOut, err := client.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
				OwnerIds:  []string{accountID},
				NextToken: snapNextToken,
			})
			if err != nil {
				slog.Warn("ebs-snap: DescribeSnapshots failed", "region", region, "error", err)
				break
			}

			for _, snap := range snapOut.Snapshots {
				snapID := aws.ToString(snap.SnapshotId)
				volID := aws.ToString(snap.VolumeId)

				// Source volume still exists — snapshot is not orphaned.
				if _, exists := existingVolumes[volID]; exists {
					continue
				}
				// Snapshot backs a registered AMI — handled by DiscoverOldAMIs.
				if _, backsAMI := amiSnapshots[snapID]; backsAMI {
					continue
				}

				sizeGB := aws.ToInt32(snap.VolumeSize)
				monthlyCost := float64(sizeGB) * rates.EBSSnapshotGBMonthly
				tags := ec2TagsToMap(snap.Tags)

				zombies = append(zombies, model.ZombieResource{
					Provider:          "aws",
					AccountID:         accountID,
					InternalAccountID: internalAccountID,
					Service:           "AmazonEC2",
					Region:            region,
					ResourceID:        snapID,
					Tags:              tags,
					MonthlyCost:       monthlyCost,
					Currency:          awsClient.Currency(),
					PeriodStart:       start,
					PeriodEnd:         end,
					UsageMetric:       "SourceVolumeExists",
					UsageAvg:          0,
					UsageUnit:         "Boolean",
					Reason:            fmt.Sprintf("EBS snapshot (%d GB) source volume %s no longer exists — orphaned storage accumulating charges", sizeGB, volID),
					Owner:             ownerFromTags(tags),
				})
				slog.Info("ebs-snap: orphaned snapshot flagged", "snapshot_id", snapID, "size_gb", sizeGB, "region", region)
			}

			if snapOut.NextToken == nil {
				break
			}
			snapNextToken = snapOut.NextToken
		}
	}
	return zombies, nil
}
