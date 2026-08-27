package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

func discoverECS(ctx context.Context, cfg aws.Config) []string {
	client := ecs.NewFromConfig(cfg)
	var ids []string

	var nextToken *string
	for {
		out, err := client.ListClusters(ctx, &ecs.ListClustersInput{
			NextToken: nextToken,
		})
		if err != nil {
			slog.Warn("discover: ECS ListClusters", "error", err)
			return nil
		}

		for _, clusterArn := range out.ClusterArns {
			var serviceNextToken *string
			for {
				svcOut, err := client.ListServices(ctx, &ecs.ListServicesInput{
					Cluster:   aws.String(clusterArn),
					NextToken: serviceNextToken,
				})
				if err != nil {
					slog.Warn("discover: ECS ListServices", "cluster", clusterArn, "error", err)
					break
				}
				for _, svcArn := range svcOut.ServiceArns {
					ids = append(ids, svcArn)
				}
				if svcOut.NextToken == nil {
					break
				}
				serviceNextToken = svcOut.NextToken
			}
		}

		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return ids
}
