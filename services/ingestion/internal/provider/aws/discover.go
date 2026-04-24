package aws

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cloudwatchsdk "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cloudwatchTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

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
		// NOTE: CloudFront, Kinesis, and S3 are handled by dedicated Discover*
		// functions that do their own CloudWatch queries. They are NOT discovered here.
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

func discoverRDS(ctx context.Context, cfg aws.Config) []string {
	client := rds.NewFromConfig(cfg)
	var ids []string
	var marker *string
	for {
		out, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
			Marker: marker,
		})
		if err != nil {
			slog.Warn("discover: RDS DescribeDBInstances", "error", err)
			return nil
		}
		for _, db := range out.DBInstances {
			if db.DBInstanceIdentifier != nil {
				ids = append(ids, aws.ToString(db.DBInstanceIdentifier))
			}
		}
		if out.Marker == nil {
			break
		}
		marker = out.Marker
	}
	return ids
}

func discoverLambda(ctx context.Context, cfg aws.Config) []string {
	client := lambda.NewFromConfig(cfg)
	var ids []string
	var nextMarker *string
	for {
		out, err := client.ListFunctions(ctx, &lambda.ListFunctionsInput{
			Marker: nextMarker,
		})
		if err != nil {
			slog.Warn("discover: Lambda ListFunctions", "error", err)
			return nil
		}
		for _, f := range out.Functions {
			if f.FunctionName != nil {
				ids = append(ids, aws.ToString(f.FunctionName))
			}
		}
		if out.NextMarker == nil {
			break
		}
		nextMarker = out.NextMarker
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
// the cost records and returns a ZombieResource for every Elastic IP that is not
// attached to a network interface. Unattached EIPs are always zombies — AWS
// charges for them regardless of usage, with no CloudWatch metric to consult.
// internalAccountID is the UUID from the accounts table, used for filtering.
func DiscoverUnattachedEIPs(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := uniqueRegions(records)
	accountID := awsClient.AccountID()
	var zombies []model.ZombieResource

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
			zombies = append(zombies, model.ZombieResource{
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

	return zombies, nil
}

// ── Unattached EBS volumes ────────────────────────────────────────────────────

// ebsVolumeMonthlyGBCost is the EBS gp3 storage cost per GB/month.
// Source: AWS EBS pricing (gp3 rate, most common type).
// Note: Actual costs vary by volume type (gp2: $0.10, io1/io2: $0.125+, st1/sc1: $0.025-0.045).
// Using gp3 as a conservative baseline — real savings may be 20-50% higher for premium volumes.
const ebsVolumeMonthlyGBCost = 0.08

// DiscoverUnattachedEBSVolumes calls ec2:DescribeVolumes in each region and
// returns a ZombieResource for every volume whose state is "available" (i.e. not
// mounted to any instance). AWS charges for EBS storage regardless of whether
// the volume is attached, making these invisible but guaranteed waste.
func DiscoverUnattachedEBSVolumes(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := uniqueRegions(records)
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

			for _, vol := range out.Volumes {
				volID := aws.ToString(vol.VolumeId)
				sizeGB := aws.ToInt32(vol.Size)
				volType := string(vol.VolumeType)
				monthlyCost := float64(sizeGB) * ebsVolumeMonthlyGBCost
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
	return zombies, nil
}

// ── Orphaned EBS snapshots ────────────────────────────────────────────────────

// ebsSnapshotMonthlyGBCost is the EBS snapshot storage cost per GB/month.
// Source: AWS EBS pricing.
const ebsSnapshotMonthlyGBCost = 0.05

// DiscoverOrphanedEBSSnapshots calls ec2:DescribeSnapshots (filtered to account
// owner) and flags any snapshot whose source volume no longer exists AND that
// does not back a registered AMI. These snapshots accumulate silently at
// $0.05/GB-month and are safe to delete once both conditions are met.
func DiscoverOrphanedEBSSnapshots(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := uniqueRegions(records)
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

				zombies = append(zombies, model.ZombieResource{
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
	return zombies, nil
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
func DiscoverLongStoppedInstances(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := uniqueRegions(records)
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
			monthlyCost := float64(totalGB) * ebsVolumeMonthlyGBCost
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
func DiscoverOldAMIs(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := uniqueRegions(records)
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
				zombies = append(zombies, model.ZombieResource{
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
	return zombies, nil
}

// ── Wasteful CloudWatch Log Groups ──────────────────────────────────────────

// cwLogStorageGBCost is the CloudWatch Logs storage cost per GB/month.
// Source: AWS CloudWatch pricing ($0.03/GB standard, but stored bytes are
// already compressed — effective rate is higher per raw GB).
const cwLogStorageGBCost = 0.03

// DiscoverWastefulLogGroups calls logs:DescribeLogGroups in each region present
// in the cost records and returns a ZombieResource for every log group that has
// no retention policy set (logs stored indefinitely). Empty log groups with a
// retention policy are harmless ($0 cost) and are not flagged. This is API-only
// — no CloudWatch metrics needed because DescribeLogGroups includes both
// retentionInDays and storedBytes.
func DiscoverWastefulLogGroups(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := uniqueRegions(records)
	accountID := awsClient.AccountID()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("cw-logs: load config", "region", region, "error", err)
			continue
		}
		client := cloudwatchlogs.NewFromConfig(cfg)

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
				monthlyCost := storedGB * cwLogStorageGBCost

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
						Currency:          "USD",
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

// ── Orphaned RDS snapshots ───────────────────────────────────────────────────

// rdsSnapshotMonthlyGBCost is the RDS snapshot storage cost per GB/month.
// Source: AWS RDS pricing.
const rdsSnapshotMonthlyGBCost = 0.095

// rdsSnapshotAgeThreshold is the minimum age for a manual RDS snapshot to be
// considered stale. Snapshots younger than this are left alone — they may be
// part of a recent migration or manual backup workflow.
const rdsSnapshotAgeThreshold = 30 * 24 * time.Hour

// DiscoverOrphanedRDSSnapshots calls rds:DescribeDBSnapshots (type=manual)
// cross-referenced with rds:DescribeDBInstances to find manual snapshots whose
// source DB instance no longer exists and that are older than 30 days. These
// accumulate silently at $0.095/GB-month. Automated snapshots are excluded —
// AWS manages their lifecycle via the retention setting.
func DiscoverOrphanedRDSSnapshots(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := uniqueRegions(records)
	accountID := awsClient.AccountID()
	now := time.Now().UTC()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("rds-snap: load config", "region", region, "error", err)
			continue
		}
		client := rds.NewFromConfig(cfg)

		// 1. Build the set of DB instance identifiers that currently exist.
		//    If this call fails we cannot safely distinguish "orphaned" from
		//    "source DB exists but DescribeDBInstances returned an error", so
		//    skip the entire region rather than producing false positives.
		existingDBs := make(map[string]struct{})
		var dbFetchFailed bool
		var dbMarker *string
		for {
			dbOut, err := client.DescribeDBInstances(ctx, &rds.DescribeDBInstancesInput{
				Marker: dbMarker,
			})
			if err != nil {
				slog.Warn("rds-snap: DescribeDBInstances failed", "region", region, "error", err)
				dbFetchFailed = true
				break
			}
			for _, db := range dbOut.DBInstances {
				if db.DBInstanceIdentifier != nil {
					existingDBs[aws.ToString(db.DBInstanceIdentifier)] = struct{}{}
				}
			}
			if dbOut.Marker == nil {
				break
			}
			dbMarker = dbOut.Marker
		}
		if dbFetchFailed {
			continue
		}

		// 2. Enumerate manual snapshots and flag orphans.
		var snapMarker *string
		for {
			snapOut, err := client.DescribeDBSnapshots(ctx, &rds.DescribeDBSnapshotsInput{
				SnapshotType: aws.String("manual"),
				Marker:       snapMarker,
			})
			if err != nil {
				slog.Warn("rds-snap: DescribeDBSnapshots failed", "region", region, "error", err)
				break
			}

			for _, snap := range snapOut.DBSnapshots {
				snapID := aws.ToString(snap.DBSnapshotIdentifier)
				dbID := aws.ToString(snap.DBInstanceIdentifier)

				_, sourceExists := existingDBs[dbID]
				if snap.SnapshotCreateTime == nil {
					continue
				}
				ageDays := isRDSSnapshotOrphaned(sourceExists, now.Sub(*snap.SnapshotCreateTime), rdsSnapshotAgeThreshold)
				if ageDays < 0 {
					continue
				}

				sizeGB := aws.ToInt32(snap.AllocatedStorage)
				monthlyCost := float64(sizeGB) * rdsSnapshotMonthlyGBCost

				zombies = append(zombies, model.ZombieResource{
					Provider:          "aws",
					AccountID:         accountID,
					InternalAccountID: internalAccountID,
					Service:           "AmazonRDS",
					Region:            region,
					ResourceID:        snapID,
					Tags:              map[string]string{},
					MonthlyCost:       monthlyCost,
					Currency:          "USD",
					PeriodStart:       start,
					PeriodEnd:         end,
					UsageMetric:       "SourceDBExists",
					UsageAvg:          float64(ageDays),
					UsageUnit:         "Days",
					Reason:            fmt.Sprintf("Manual RDS snapshot (%d GB, %d days old) is orphaned — source DB %q no longer exists, accumulating $%.2f/month in storage charges", sizeGB, ageDays, dbID, monthlyCost),
					Owner:             "unknown",
				})
				slog.Info("rds-snap: orphaned snapshot flagged", "snapshot_id", snapID, "source_db", dbID, "size_gb", sizeGB, "age_days", ageDays, "region", region)
			}

			if snapOut.Marker == nil {
				break
			}
			snapMarker = snapOut.Marker
		}
	}
	return zombies, nil
}

// ── Stale ECR images ──────────────────────────────────────��─────────────────

// ecrStorageMonthlyGBCost is the ECR storage cost per GB/month.
// Source: AWS ECR pricing.
const ecrStorageMonthlyGBCost = 0.10

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
	regions := uniqueRegions(records)
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
			monthlyCost := staleGB * ecrStorageMonthlyGBCost

			zombies = append(zombies, model.ZombieResource{
				Provider:          "aws",
				AccountID:         accountID,
				InternalAccountID: internalAccountID,
				Service:           "AmazonECR",
				Region:            region,
				ResourceID:        repoName,
				Tags:              map[string]string{},
				MonthlyCost:       monthlyCost,
				Currency:          "USD",
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

// ── Classification helpers (pure functions, unit-testable) ───────────────────

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

// isSecretUnused returns the number of days since last access if the secret
// should be flagged, or -1 if the secret is still in use.
func isSecretUnused(lastAccessed, created *time.Time, threshold time.Duration, now time.Time) int {
	if lastAccessed != nil {
		age := now.Sub(*lastAccessed)
		if age < threshold {
			return -1
		}
		return int(age.Hours() / 24)
	}
	if created != nil {
		age := now.Sub(*created)
		if age < threshold {
			return -1
		}
		return int(age.Hours() / 24)
	}
	return -1
}

// isRDSSnapshotOrphaned returns the age in days if the snapshot should be
// flagged, or -1 if it's not a zombie. A snapshot is orphaned when its source
// DB no longer exists and it's older than the threshold.
func isRDSSnapshotOrphaned(sourceDBExists bool, snapshotAge time.Duration, threshold time.Duration) int {
	if sourceDBExists {
		return -1
	}
	if snapshotAge < threshold {
		return -1
	}
	return int(snapshotAge.Hours() / 24)
}

// ── Tier 2 Discovery Functions ───────────────────────────────────────────────

func discoverElastiCache(ctx context.Context, cfg aws.Config) []string {
	client := elasticache.NewFromConfig(cfg)
	var ids []string
	var marker *string
	for {
		out, err := client.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
			Marker: marker,
		})
		if err != nil {
			slog.Warn("discover: ElastiCache DescribeCacheClusters", "error", err)
			return nil
		}
		for _, c := range out.CacheClusters {
			if c.CacheClusterId != nil {
				ids = append(ids, aws.ToString(c.CacheClusterId))
			}
		}
		if out.Marker == nil {
			break
		}
		marker = out.Marker
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
	var ids []string
	var marker *string
	for {
		out, err := client.DescribeClusters(ctx, &redshift.DescribeClustersInput{
			Marker: marker,
		})
		if err != nil {
			slog.Warn("discover: Redshift DescribeClusters", "error", err)
			return nil
		}
		for _, c := range out.Clusters {
			if c.ClusterIdentifier != nil {
				ids = append(ids, aws.ToString(c.ClusterIdentifier))
			}
		}
		if out.Marker == nil {
			break
		}
		marker = out.Marker
	}
	return ids
}

func discoverSageMaker(ctx context.Context, cfg aws.Config) []string {
	client := sagemaker.NewFromConfig(cfg)
	var ids []string
	var nextToken *string
	for {
		out, err := client.ListEndpoints(ctx, &sagemaker.ListEndpointsInput{
			NextToken: nextToken,
		})
		if err != nil {
			slog.Warn("discover: SageMaker ListEndpoints", "error", err)
			return nil
		}
		for _, e := range out.Endpoints {
			if e.EndpointName != nil {
				ids = append(ids, aws.ToString(e.EndpointName))
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return ids
}

func discoverDynamoDB(ctx context.Context, cfg aws.Config) []string {
	client := dynamodb.NewFromConfig(cfg)
	var ids []string
	var startTable *string
	for {
		out, err := client.ListTables(ctx, &dynamodb.ListTablesInput{
			ExclusiveStartTableName: startTable,
		})
		if err != nil {
			slog.Warn("discover: DynamoDB ListTables", "error", err)
			return nil
		}
		ids = append(ids, out.TableNames...)
		if out.LastEvaluatedTableName == nil {
			break
		}
		startTable = out.LastEvaluatedTableName
	}
	return ids
}

func discoverEKS(ctx context.Context, cfg aws.Config) []string {
	client := eks.NewFromConfig(cfg)
	var ids []string
	var nextToken *string
	for {
		out, err := client.ListClusters(ctx, &eks.ListClustersInput{
			NextToken: nextToken,
		})
		if err != nil {
			slog.Warn("discover: EKS ListClusters", "error", err)
			return nil
		}
		ids = append(ids, out.Clusters...)
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return ids
}

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

// ── Idle CloudFront distributions ───────────────────────────────────────────

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
			Currency:          "USD",
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

// ── Idle Kinesis data streams ───────────────────────────────────────────────

// kinesisShardHourlyCost is the provisioned-mode cost per shard-hour.
// Source: AWS Kinesis Data Streams pricing (us-east-1).
const kinesisShardHourlyCost = 0.015

// DiscoverIdleKinesisStreams lists Kinesis streams in all regions found in cost
// records and queries CloudWatch for IncomingRecords. Streams with zero incoming
// records are flagged, with cost estimated from shard count.
func DiscoverIdleKinesisStreams(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := uniqueRegions(records)
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
			monthlyCost := kinesisShardHourlyCost * 730 // 1 shard default
			desc, descErr := kinesisClient.DescribeStreamSummary(ctx, &kinesis.DescribeStreamSummaryInput{
				StreamName: aws.String(name),
			})
			if descErr == nil && desc.StreamDescriptionSummary != nil {
				shards := aws.ToInt32(desc.StreamDescriptionSummary.OpenShardCount)
				if shards > 0 {
					monthlyCost = float64(shards) * kinesisShardHourlyCost * 730
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
				Currency:          "USD",
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

// ── Idle S3 buckets ─────────────────────────────────────────────────────────

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
				Currency:          "USD",
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

// ── Shared helpers for Tier 1 CloudWatch-based detection ────────────────────

// newCloudWatchClient creates a CloudWatch client from an AWS config.
func newCloudWatchClient(cfg aws.Config) CloudWatchAPI {
	return cloudwatchsdk.NewFromConfig(cfg)
}

// getMetricAvg queries CloudWatch for a single metric/resource and returns the
// average value. Returns -1 if no datapoints are available (metric not configured).
func getMetricAvg(ctx context.Context, cw CloudWatchAPI, namespace, metricName, dimensionName, dimensionValue string, start, end time.Time, periodSecs int32, extraDimensions []cloudwatchTypes.Dimension) (float64, error) {
	dimensions := []cloudwatchTypes.Dimension{
		{Name: aws.String(dimensionName), Value: aws.String(dimensionValue)},
	}
	dimensions = append(dimensions, extraDimensions...)

	out, err := cw.GetMetricStatistics(ctx, &cloudwatchsdk.GetMetricStatisticsInput{
		Namespace:  aws.String(namespace),
		MetricName: aws.String(metricName),
		Dimensions: dimensions,
		StartTime:  aws.Time(start),
		EndTime:    aws.Time(end),
		Period:     aws.Int32(periodSecs),
		Statistics: []cloudwatchTypes.Statistic{cloudwatchTypes.StatisticSum},
	})
	if err != nil {
		return -1, err
	}

	if len(out.Datapoints) == 0 {
		return -1, nil
	}

	total := 0.0
	for _, dp := range out.Datapoints {
		total += aws.ToFloat64(dp.Sum)
	}
	return total, nil
}

// serviceCostFromRecords sums the monthly cost for a given service across all
// cost records. Used to estimate per-resource cost when CE doesn't provide
// resource-level breakdown.
func serviceCostFromRecords(records []model.CostRecord, service string) float64 {
	total := 0.0
	for _, r := range records {
		if r.Service == service {
			total += r.Amount
		}
	}
	return total
}

// ── Unused Secrets Manager secrets ──────────────────────────────────────────

// secretMonthlyCost is the AWS Secrets Manager charge per secret per month.
// Source: AWS Secrets Manager pricing.
const secretMonthlyCost = 0.40

// unusedSecretThreshold is the minimum time since a secret was last accessed
// before it is flagged as unused. 90 days is conservative — secrets accessed
// quarterly (e.g. rotation) are excluded.
const unusedSecretThreshold = 90 * 24 * time.Hour

// DiscoverUnusedSecrets calls secretsmanager:ListSecrets in each region present
// in the cost records and returns a ZombieResource for every secret whose
// LastAccessedDate is older than 90 days (or was never accessed). Secrets are
// billed at $0.40/month regardless of whether they are read, so forgotten
// secrets accumulate charges silently after the service that used them is torn down.
func DiscoverUnusedSecrets(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time, internalAccountID string) ([]model.ZombieResource, error) {
	regions := uniqueRegions(records)
	accountID := awsClient.AccountID()
	now := time.Now().UTC()
	var zombies []model.ZombieResource

	for region := range regions {
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			slog.Warn("secrets: load config", "region", region, "error", err)
			continue
		}
		client := secretsmanager.NewFromConfig(cfg)

		var nextToken *string
		for {
			out, err := client.ListSecrets(ctx, &secretsmanager.ListSecretsInput{
				NextToken: nextToken,
			})
			if err != nil {
				slog.Warn("secrets: ListSecrets failed", "region", region, "error", err)
				break
			}

			for _, s := range out.SecretList {
				name := aws.ToString(s.Name)
				if name == "" {
					continue
				}

				daysSinceAccess := isSecretUnused(s.LastAccessedDate, s.CreatedDate, unusedSecretThreshold, now)
				if daysSinceAccess < 0 {
					continue
				}

				zombies = append(zombies, model.ZombieResource{
					Provider:          "aws",
					AccountID:         accountID,
					InternalAccountID: internalAccountID,
					Service:           "AWSSecretsManager",
					Region:            region,
					ResourceID:        name,
					Tags:              map[string]string{},
					MonthlyCost:       secretMonthlyCost,
					Currency:          "USD",
					PeriodStart:       start,
					PeriodEnd:         end,
					UsageMetric:       "DaysSinceAccess",
					UsageAvg:          float64(daysSinceAccess),
					UsageUnit:         "Days",
					Reason:            fmt.Sprintf("Secret not accessed for %d days — still billing $0.40/month", daysSinceAccess),
					Owner:             "unknown",
				})
				slog.Info("secrets: unused secret flagged", "name", name, "days_since_access", daysSinceAccess, "region", region)
			}

			if out.NextToken == nil {
				break
			}
			nextToken = out.NextToken
		}
	}
	return zombies, nil
}
