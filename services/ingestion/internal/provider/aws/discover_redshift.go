package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/redshift"
)

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
