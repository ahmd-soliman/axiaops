package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
)

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
