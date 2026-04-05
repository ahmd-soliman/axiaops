package aws

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/rds"

	"axiaops.io/ingestion/internal/model"
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
		if r.Region != "" {
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
