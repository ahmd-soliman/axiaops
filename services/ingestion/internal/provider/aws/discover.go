package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

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
		case "AmazonECS":
			ids = discoverECS(ctx, cfg)
		case "AmazonDocDB":
			ids = discoverDocDB(ctx, cfg)
		case "AmazonMSK":
			ids = discoverMSK(ctx, cfg)
		case "AmazonBedrock":
			ids = discoverBedrock(ctx, cfg)
		case "AmazonKendra":
			ids = discoverKendra(ctx, cfg)
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

// discoveryRegions extracts the set of real AWS regions present in cost records,
// plus the account's own configured home region as a guaranteed floor.
// Cost Explorer pseudo-values like "global" or "NoRegion" are excluded.
//
// The floor exists for cold start: a freshly connected CUR account delivers
// no cost data for up to 24h, then month-to-date only for a few weeks (see
// axiaops-cur-migration-plan.md §2). Without it, an empty cost-record set
// means every Discover* function's region loop below runs zero times —
// nothing gets checked anywhere, even in the region the customer actually
// told us about at connect time. Once real cost data accumulates, this
// becomes a no-op (the account's region is already in the set) — it isn't a
// second filtering mode, just a lower bound on the first one, so a genuinely
// idle account can still surface zombies in its own region on day one.
//
// Known limitation, accepted as-is (not fixed): accountRegion is a single
// value (model.Account.Region), so this only guarantees cold-start coverage
// for the *one* region the customer typed in at connect time. A genuinely
// multi-region customer's other regions stay invisible to every Discover*
// rule until real cost data organically reveals them — potentially the
// first few weeks of a CUR account's life (see the cold-start note above).
// Two ways to close this were considered and deliberately not built:
//   - Let the customer list multiple regions at connect time (schema +
//     onboarding UI change).
//   - During cold start specifically (empty records), call
//     ec2:DescribeRegions once and float the account's *entire* enabled
//     region set instead of just one — self-narrows back to cost-driven
//     regions the moment real data exists, no new field or UI needed.
// Revisit if multi-region cold-start blind spots turn out to matter in
// practice; the second option is the cheaper fix if so.
func discoveryRegions(records []model.CostRecord, accountRegion string) map[string]struct{} {
	regions := make(map[string]struct{})
	for _, r := range records {
		if r.Region != "" && isAWSRegion(r.Region) {
			regions[r.Region] = struct{}{}
		}
	}
	if accountRegion != "" && isAWSRegion(accountRegion) {
		regions[accountRegion] = struct{}{}
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
