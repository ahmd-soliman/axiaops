package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
)

func discoverSageMaker(ctx context.Context, cfg aws.Config) []string {
	client := sagemaker.NewFromConfig(cfg)
	var ids []string
	var nextToken *string
	for {
		out, err := client.ListEndpoints(ctx, &sagemaker.ListEndpointsInput{
			NextToken: nextToken,
		})
		if err != nil {
			slog.Warn("discover: SageMaker ListEndpoints", "error", err)
			return nil
		}
		for _, e := range out.Endpoints {
			if e.EndpointName != nil {
				ids = append(ids, aws.ToString(e.EndpointName))
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return ids
}
