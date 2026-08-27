package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
)

func discoverMSK(ctx context.Context, cfg aws.Config) []string {
	client := kafka.NewFromConfig(cfg)
	var ids []string
	var nextToken *string

	for {
		out, err := client.ListClustersV2(ctx, &kafka.ListClustersV2Input{
			NextToken: nextToken,
		})
		if err != nil {
			slog.Warn("discover: MSK ListClustersV2", "error", err)
			return nil
		}
		for _, c := range out.ClusterInfoList {
			if c.ClusterName != nil {
				ids = append(ids, aws.ToString(c.ClusterName))
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return ids
}
