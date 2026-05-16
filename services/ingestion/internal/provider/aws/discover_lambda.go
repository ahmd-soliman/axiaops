package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

func discoverLambda(ctx context.Context, cfg aws.Config) []string {
	client := lambda.NewFromConfig(cfg)
	var ids []string
	var nextMarker *string
	for {
		out, err := client.ListFunctions(ctx, &lambda.ListFunctionsInput{
			Marker: nextMarker,
		})
		if err != nil {
			slog.Warn("discover: Lambda ListFunctions", "error", err)
			return nil
		}
		for _, f := range out.Functions {
			if f.FunctionName != nil {
				ids = append(ids, aws.ToString(f.FunctionName))
			}
		}
		if out.NextMarker == nil {
			break
		}
		nextMarker = out.NextMarker
	}
	return ids
}
