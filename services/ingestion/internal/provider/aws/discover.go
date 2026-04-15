package aws

import (
	"context"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/rds"

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
func DiscoverResources(ctx context.Context, records []model.CostRecord) []DiscoveredResource {
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
		cfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion(k.region),
		)
		if err != nil {
			log.Printf("discover: load config for region %s: %v", k.region, err)
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
		log.Printf("discover: EC2 DescribeInstances: %v", err)
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
		log.Printf("discover: RDS DescribeDBInstances: %v", err)
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
		log.Printf("discover: Lambda ListFunctions: %v", err)
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
		log.Printf("discover: ELB DescribeLoadBalancers: %v", err)
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
		log.Printf("discover: EC2 DescribeNatGateways: %v", err)
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

// eipMonthlyCost is the AWS charge for one unattached Elastic IP per month
// ($0.005/hour × 24 × 30 = $3.60). Source: AWS EC2 pricing.
const eipMonthlyCost = 3.60

// DiscoverUnattachedEIPs calls ec2:DescribeAddresses in each region present in
// the cost records and returns a GhostResource for every Elastic IP that is not
// attached to a network interface. Unattached EIPs are always zombies — AWS
// charges for them regardless of usage, with no CloudWatch metric to consult.
func DiscoverUnattachedEIPs(ctx context.Context, records []model.CostRecord, awsClient *Client, start, end time.Time) ([]model.GhostResource, error) {
	// Collect unique real AWS regions from cost records (skipping Cost Explorer
	// pseudo-values like "global" or "NoRegion").
	regions := make(map[string]struct{})
	for _, r := range records {
		if r.Region != "" && isAWSRegion(r.Region) {
			regions[r.Region] = struct{}{}
		}
	}

	var ghosts []model.GhostResource
	accountID := awsClient.AccountID()

	for region := range regions {
		// Use the same AWS client configuration but for different region
		cfg, err := awsClient.configForRegion(ctx, region)
		if err != nil {
			log.Printf("eip: load config for region %s: %v", region, err)
			continue
		}

		client := ec2.NewFromConfig(cfg)
		out, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{})
		if err != nil {
			log.Printf("eip: DescribeAddresses in %s: %v", region, err)
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

			// Convert EC2 tag list to a plain map.
			tags := make(map[string]string, len(addr.Tags))
			for _, t := range addr.Tags {
				if t.Key != nil && t.Value != nil {
					tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
				}
			}

			ownerTeam := "unknown"
			if t, ok := tags["team"]; ok && t != "" {
				ownerTeam = t
			}

			ghosts = append(ghosts, model.GhostResource{
				Provider:    "aws",
				AccountID:   accountID,
				Service:     "AmazonVPC",
				Region:      region,
				ResourceID:  allocationID,
				Tags:        tags,
				MonthlyCost: eipMonthlyCost,
				Currency:    "USD",
				PeriodStart: start,
				PeriodEnd:   end,
				UsageMetric: "NetworkInterfaceAttachment",
				UsageAvg:    0,
				UsageUnit:   "Count",
				Reason:      "Elastic IP not attached to any resource — incurring $0.005/hour idle charge",
				Owner:       ownerTeam,
			})
			log.Printf("eip: unattached EIP %s in %s flagged as zombie", allocationID, region)
		}
	}

	return ghosts, nil
}
