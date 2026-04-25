package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
)

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
