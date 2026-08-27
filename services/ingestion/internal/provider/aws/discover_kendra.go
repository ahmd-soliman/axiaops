package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kendra"
)

func discoverKendra(ctx context.Context, cfg aws.Config) []string {
	client := kendra.NewFromConfig(cfg)
	var ids []string
	var nextToken *string

	for {
		out, err := client.ListIndices(ctx, &kendra.ListIndicesInput{
			NextToken: nextToken,
		})
		if err != nil {
			slog.Warn("discover: Kendra ListIndices", "error", err)
			return nil
		}
		for _, idx := range out.IndexConfigurationSummaryItems {
			if idx.Id != nil {
				ids = append(ids, aws.ToString(idx.Id))
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return ids
}
