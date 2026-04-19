package aws

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"

	"axiaops.io/shared/model"
)

// DiscoveredResource holds a resource ID alongside its service and region,
// ready to be passed to CloudWatch for usage metric lookup.
type DiscoveredResource struct {
	Service    string
	Region     string
	ResourceID string
}

// DiscoverResources calls service-specific AWS APIs to list resource IDs for
// each service+region combination present in the cost records.
// Cost Explorer does not expose resource IDs via group-by — this bridges that gap.
// Uses the provided AWS client for consistent credential handling.
func DiscoverResources(ctx context.Context, awsClient *Client, records []model.CostRecord) []DiscoveredResource {
	// Build a set of unique service+region pairs from cost records.
	type key struct{ service, region string }
	pairs := make(map[key]struct{})
	for _, r := range records {
		if r.Region != "" && isAWSRegion(r.Region) {
			pairs[key{r.Service, r.Region}] = struct{}{}
		}
	}

	var discovered []DiscoveredResource

	for k := range pairs {
		cfg, err := awsClient.configForRegion(ctx, k.region)
		if err != nil {
			slog.Warn("discover: load config for region", "region", k.region, "error", err)
			continue
		}

		var ids []string
		switch k.service {
		case "AmazonEC2":
			ids = discoverEC2(ctx, cfg)
		case "AmazonRDS":
			ids = discoverRDS(ctx, cfg)
		case "AWSLambda":
			ids = discoverLambda(ctx, cfg)
		case "AmazonElasticLoadBalancing":
			ids = discoverELB(ctx, cfg)
		case "AmazonVPC":
			ids = discoverNATGateways(ctx, cfg)
		case "AmazonElastiCache":
			ids = discoverElastiCache(ctx, cfg)
		case "AmazonES":
			ids = discoverOpenSearch(ctx, cfg)
		case "AmazonRedshift":
			ids = discoverRedshift(ctx, cfg)
		case "AmazonSageMaker":
			ids = discoverSageMaker(ctx, cfg)
		case "AmazonDynamoDB":
			ids = discoverDynamoDB(ctx, cfg)
		case "AmazonEKS":
			ids = discoverEKS(ctx, cfg)
		default:
			continue
		}

		for _, id := range ids {
			discovered = append(discovered, DiscoveredResource{
				Service:    k.service,
				Region:     k.region,
				ResourceID: id,
			})
		}
	}

	return discovered
}

func discoverEC2(ctx context.Context, cfg aws.Config) []string {
	client := ec2.NewFromConfig(cfg)
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{})
	if err != nil {
		slog.Warn("discover: EC2 DescribeInstances", "error", err)
		return nil
	}
	var ids []string
	for _, r := range out.Reservations {
		for _, i := range r.Instances {
			if i.InstanceId != nil {
				ids = append(ids, aws.ToString(i.InstanceId))
			}
		}
	}
	return ids
}

func discoverRDS(ctx context.Context, cfg aws.Config) []string {
	client := rds.NewFromConfig(cfg)
	out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{})
	if err != nil {
		slog.Warn("discover: RDS DescribeDBInstances", "error", err)
		return nil
	}
	var ids []string
	for _, db := range out.DBInstances {
		if db.DBInstanceIdentifier != nil {
			ids = append(ids, aws.ToString(db.DBInstanceIdentifier))
		}
	}
	return ids
}

func discoverLambda(ctx context.Context, cfg aws.Config) []string {
	client := lambda.NewFromConfig(cfg)
	out, err := client.ListFunctions(ctx, &lambda.ListFunctionsInput{})
	if err != nil {
		slog.Warn("discover: Lambda ListFunctions", "error", err)
		return nil
	}
	var ids []string
	for _, f := range out.Functions {
		if f.FunctionName != nil {
			ids = append(ids, aws.ToString(f.FunctionName))
		}
	}
	return ids
}

func discoverELB(ctx context.Context, cfg aws.Config) []string {
	client := elasticloadbalancingv2.NewFromConfig(cfg)
	out, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	if err != nil {
		slog.Warn("discover: ELB DescribeLoadBalancers", "error", err)
		return nil
	}
	var ids []string
	for _, lb := range out.LoadBalancers {
		if lb.LoadBalancerArn != nil {
			// CloudWatch uses the ARN suffix as the LoadBalancer dimension value
			ids = append(ids, arnSuffix(aws.ToString(lb.LoadBalancerArn)))
		}
	}
	return ids
}

func discoverNATGateways(ctx context.Context, cfg aws.Config) []string {
	client := ec2.NewFromConfig(cfg)
	out, err := client.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{})
	if err != nil {
		slog.Warn("discover: EC2 DescribeNatGateways", "error", err)
		return nil
	}
	var ids []string
	for _, ng := range out.NatGateways {
		if ng.NatGatewayId != nil {
			ids = append(ids, aws.ToString(ng.NatGatewayId))
		}
	}
	return ids
}

// arnSuffix extracts the resource portion from an ARN for use as a CloudWatch
// dimension value. Example:
// arn:aws:elasticloadbalancing:eu-central-1:123:loadbalancer/app/my-lb/abc123
// → app/my-lb/abc123
func arnSuffix(arn string) string {
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == '/' {
			// find the second-to-last slash to get app/name/id
			for j := i - 1; j >= 0; j-- {
				if arn[j] == '/' {
					return arn[j+1:]
				}
			}
		}
	}
	return arn
}

// isAWSRegion reports whether s is a real AWS region identifier (e.g. "us-east-1")
// rather than a Cost Explorer pseudo-value like "global" or "NoRegion".
// Real AWS regions always end in a digit and contain at least two hyphens.
func isAWSRegion(s string) bool {
	if len(s) < 5 {
		return false
	}
	last := s[len(s)-1]
	if last < '0' || last > '9' {
		return false
	}
	hyphens := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			hyphens++
		}
	}
	return hyphens >= 2
}

// ── Shared helpers ────────────────────────────────────────────────────────────

// uniqueRegions extracts the set of real AWS regions present in cost records.
// Cost Explorer pseudo-values like "global" or "NoRegion" are excluded.
func uniqueRegions(records []model.CostRecord) map[string]struct{} {
	regions := make(map[string]struct{})
	for _, r := range records {
		if r.Region != "" && isAWSRegion(r.Region) {
			regions[r.Region] = struct{}{}
		}
	}
	return regions
}

// ec2TagsToMap converts an EC2 tag slice to a plain string map.
func ec2TagsToMap(tags []ec2types.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.Key != nil && t.Value != nil {
			m[aws.ToString(t.Key)] = aws.ToString(t.Value)
		}
	}
	return m
}

// ownerFromTags returns the value of the "team" tag, or "unknown" if absent.
func ownerFromTags(tags map[string]string) string {
	if t, ok := tags["team"]; ok && t != "" {
		return t
	}
	return "unknown"
}

// ── EIP ───────────────────────────────────────────────────────────────────────

// eipMonthlyCost is the AWS charge for one unattached Elastic IP per month
// ($0.005/hour × 24 × 30 = $3.60). Source: AWS EC2 pricing.
const eipMonthlyCost = 3.60

// DiscoverUnattachedEIPs calls ec2:DescribeAddresses in each region present in
// the cost records and returns a GhostResource for every Elastic IP that is not
// attached to a network interface. Unattached EIPs are always zombies — AWS
// charges for them regardless of usage, with no CloudWatch metric to consult.
// internalAccountID is the UUID from the accounts table, used for filtering.
func DiscoverUnattachedEIPs(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.GhostResource, error) {
	regions := uniqueRegions(records)
	accountID := awsClient.AccountID()
	var ghosts []model.GhostResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("eip: load config for region", "region", region, "error", err)
			continue
		}

		client := ec2.NewFromConfig(cfg)
		out, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{})
		if err != nil {
			slog.Warn("eip: DescribeAddresses", "region", region, "error", err)
			continue
		}

		for _, addr := range out.Addresses {
			// An EIP is a zombie when it has no attached network interface.
			if addr.NetworkInterfaceId != nil {
				continue
			}
			allocationID := aws.ToString(addr.AllocationId)
			if allocationID == "" {
				continue
			}

			tags := ec2TagsToMap(addr.Tags)
			ghosts = append(ghosts, model.GhostResource{
				Provider:          "aws",
				AccountID:         accountID,
				InternalAccountID: internalAccountID,
				Service:           "AmazonVPC",
				Region:            region,
				ResourceID:        allocationID,
				Tags:              tags,
				MonthlyCost:       eipMonthlyCost,
				Currency:          "USD",
				PeriodStart:       start,
				PeriodEnd:         end,
				UsageMetric:       "NetworkInterfaceAttachment",
				UsageAvg:          0,
				UsageUnit:         "Count",
				Reason:            "Elastic IP not attached to any resource — incurring $0.005/hour idle charge",
				Owner:             ownerFromTags(tags),
			})
			slog.Info("eip: unattached EIP flagged", "allocation_id", allocationID, "region", region)
		}
	}

	return ghosts, nil
}

// ── Unattached EBS volumes ────────────────────────────────────────────────────

// ebsVolumeMonthlyGBCost is the EBS gp3 storage cost per GB/month.
// Source: AWS EBS pricing (gp3 rate, most common type).
// Note: Actual costs vary by volume type (gp2: $0.10, io1/io2: $0.125+, st1/sc1: $0.025-0.045).
// Using gp3 as a conservative baseline — real savings may be 20-50% higher for premium volumes.
const ebsVolumeMonthlyGBCost = 0.08

// DiscoverUnattachedEBSVolumes calls ec2:DescribeVolumes in each region and
// returns a GhostResource for every volume whose state is "available" (i.e. not
// mounted to any instance). AWS charges for EBS storage regardless of whether
// the volume is attached, making these invisible but guaranteed waste.
func DiscoverUnattachedEBSVolumes(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.GhostResource, error) {
	regions := uniqueRegions(records)
	accountID := awsClient.AccountID()
	var ghosts []model.GhostResource

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

			for _, vol := range out.Volumes {
				volID := aws.ToString(vol.VolumeId)
				sizeGB := aws.ToInt32(vol.Size)
				volType := string(vol.VolumeType)
				monthlyCost := float64(sizeGB) * ebsVolumeMonthlyGBCost
				tags := ec2TagsToMap(vol.Tags)

				ghosts = append(ghosts, model.GhostResource{
					Provider:          "aws",
					AccountID:         accountID,
					InternalAccountID: internalAccountID,
					Service:           "AmazonEC2",
					Region:            region,
					ResourceID:        volID,
					Tags:              tags,
					MonthlyCost:       monthlyCost,
					Currency:          "USD",
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
	return ghosts, nil
}

// ── Orphaned EBS snapshots ────────────────────────────────────────────────────

// ebsSnapshotMonthlyGBCost is the EBS snapshot storage cost per GB/month.
// Source: AWS EBS pricing.
const ebsSnapshotMonthlyGBCost = 0.05

// DiscoverOrphanedEBSSnapshots calls ec2:DescribeSnapshots (filtered to account
// owner) and flags any snapshot whose source volume no longer exists AND that
// does not back a registered AMI. These snapshots accumulate silently at
// $0.05/GB-month and are safe to delete once both conditions are met.
func DiscoverOrphanedEBSSnapshots(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.GhostResource, error) {
	regions := uniqueRegions(records)
	accountID := awsClient.AccountID()
	var ghosts []model.GhostResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("ebs-snap: load config", "region", region, "error", err)
			continue
		}
		client := ec2.NewFromConfig(cfg)

		// 1. Build the set of volume IDs that currently exist in this region.
		existingVolumes := make(map[string]struct{})
		var volNextToken *string
		for {
			volOut, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
				NextToken: volNextToken,
			})
			if err != nil {
				slog.Warn("ebs-snap: DescribeVolumes failed", "region", region, "error", err)
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
		if len(existingVolumes) == 0 && err != nil {
			continue // DescribeVolumes failed, skip this region
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
				monthlyCost := float64(sizeGB) * ebsSnapshotMonthlyGBCost
				tags := ec2TagsToMap(snap.Tags)

				ghosts = append(ghosts, model.GhostResource{
					Provider:          "aws",
					AccountID:         accountID,
					InternalAccountID: internalAccountID,
					Service:           "AmazonEC2",
					Region:            region,
					ResourceID:        snapID,
					Tags:              tags,
					MonthlyCost:       monthlyCost,
					Currency:          "USD",
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
	return ghosts, nil
}

// ── Long-stopped EC2 instances ────────────────────────────────────────────────

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
func DiscoverLongStoppedInstances(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.GhostResource, error) {
	regions := uniqueRegions(records)
	accountID := awsClient.AccountID()
	now := time.Now().UTC()
	var ghosts []model.GhostResource

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
		allVolIDs := make([]string, 0)
		for _, c := range candidates {
			allVolIDs = append(allVolIDs, c.volumeIDs...)
		}
		volSizes := make(map[string]int32) // volumeID → sizeGB
		if len(allVolIDs) > 0 {
			volOut, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
				VolumeIds: allVolIDs,
			})
			if err != nil {
				slog.Warn("stopped-ec2: DescribeVolumes for attached volumes failed", "region", region, "error", err)
				// Continue with zero-cost estimate rather than skipping the check.
			} else {
				for _, v := range volOut.Volumes {
					if v.VolumeId != nil && v.Size != nil {
						volSizes[aws.ToString(v.VolumeId)] = aws.ToInt32(v.Size)
					}
				}
			}
		}

		for _, c := range candidates {
			totalGB := int32(0)
			for _, vid := range c.volumeIDs {
				totalGB += volSizes[vid]
			}
			monthlyCost := float64(totalGB) * ebsVolumeMonthlyGBCost
			daysStop := int(now.Sub(c.stoppedAt).Hours() / 24)

			ghosts = append(ghosts, model.GhostResource{
				Provider:          "aws",
				AccountID:         accountID,
				InternalAccountID: internalAccountID,
				Service:           "AmazonEC2",
				Region:            region,
				ResourceID:        c.instanceID,
				Tags:              c.tags,
				MonthlyCost:       monthlyCost,
				Currency:          "USD",
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
	return ghosts, nil
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

// ── Old AMIs + backing snapshots ──────────────────────────────────────────────

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
func DiscoverOldAMIs(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.GhostResource, error) {
	regions := uniqueRegions(records)
	accountID := awsClient.AccountID()
	now := time.Now().UTC()
	var ghosts []model.GhostResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("old-ami: load config", "region", region, "error", err)
			continue
		}
		client := ec2.NewFromConfig(cfg)

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
				monthlyCost := float64(totalSnapshotGB) * ebsSnapshotMonthlyGBCost
				ageDays := int(now.Sub(createdAt).Hours() / 24)

				tags := ec2TagsToMap(img.Tags)
				ghosts = append(ghosts, model.GhostResource{
					Provider:          "aws",
					AccountID:         accountID,
					InternalAccountID: internalAccountID,
					Service:           "AmazonEC2",
					Region:            region,
					ResourceID:        amiID,
					Tags:              tags,
					MonthlyCost:       monthlyCost,
					Currency:          "USD",
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
	return ghosts, nil
}

// ── Tier 2 Discovery Functions ───────────────────────────────────────────────

func discoverElastiCache(ctx context.Context, cfg aws.Config) []string {
	client := elasticache.NewFromConfig(cfg)
	out, err := client.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{})
	if err != nil {
		slog.Warn("discover: ElastiCache DescribeCacheClusters", "error", err)
		return nil
	}
	var ids []string
	for _, c := range out.CacheClusters {
		if c.CacheClusterId != nil {
			ids = append(ids, aws.ToString(c.CacheClusterId))
		}
	}
	return ids
}

func discoverOpenSearch(ctx context.Context, cfg aws.Config) []string {
	client := opensearch.NewFromConfig(cfg)
	out, err := client.ListDomainNames(ctx, &opensearch.ListDomainNamesInput{})
	if err != nil {
		slog.Warn("discover: OpenSearch ListDomainNames", "error", err)
		return nil
	}
	var ids []string
	for _, d := range out.DomainNames {
		if d.DomainName != nil {
			ids = append(ids, aws.ToString(d.DomainName))
		}
	}
	return ids
}

func discoverRedshift(ctx context.Context, cfg aws.Config) []string {
	client := redshift.NewFromConfig(cfg)
	out, err := client.DescribeClusters(ctx, &redshift.DescribeClustersInput{})
	if err != nil {
		slog.Warn("discover: Redshift DescribeClusters", "error", err)
		return nil
	}
	var ids []string
	for _, c := range out.Clusters {
		if c.ClusterIdentifier != nil {
			ids = append(ids, aws.ToString(c.ClusterIdentifier))
		}
	}
	return ids
}

func discoverSageMaker(ctx context.Context, cfg aws.Config) []string {
	client := sagemaker.NewFromConfig(cfg)
	out, err := client.ListEndpoints(ctx, &sagemaker.ListEndpointsInput{})
	if err != nil {
		slog.Warn("discover: SageMaker ListEndpoints", "error", err)
		return nil
	}
	var ids []string
	for _, e := range out.Endpoints {
		if e.EndpointName != nil {
			ids = append(ids, aws.ToString(e.EndpointName))
		}
	}
	return ids
}

func discoverDynamoDB(ctx context.Context, cfg aws.Config) []string {
	client := dynamodb.NewFromConfig(cfg)
	out, err := client.ListTables(ctx, &dynamodb.ListTablesInput{})
	if err != nil {
		slog.Warn("discover: DynamoDB ListTables", "error", err)
		return nil
	}
	return out.TableNames
}

func discoverEKS(ctx context.Context, cfg aws.Config) []string {
	client := eks.NewFromConfig(cfg)
	out, err := client.ListClusters(ctx, &eks.ListClustersInput{})
	if err != nil {
		slog.Warn("discover: EKS ListClusters", "error", err)
		return nil
	}
	return out.Clusters
}
