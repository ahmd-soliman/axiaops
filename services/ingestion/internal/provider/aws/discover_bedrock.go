package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
)

func discoverBedrock(ctx context.Context, cfg aws.Config) []string {
	client := bedrock.NewFromConfig(cfg)
	var ids []string
	var nextToken *string

	for {
		out, err := client.ListProvisionedModelThroughputs(ctx, &bedrock.ListProvisionedModelThroughputsInput{
			NextToken: nextToken,
		})
		if err != nil {
			slog.Warn("discover: Bedrock ListProvisionedModelThroughputs", "error", err)
			return nil
		}
		for _, p := range out.ProvisionedModelSummaries {
			if p.ProvisionedModelName != nil {
				ids = append(ids, aws.ToString(p.ProvisionedModelName))
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return ids
}
