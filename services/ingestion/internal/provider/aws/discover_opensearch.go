package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
)

func discoverOpenSearch(ctx context.Context, cfg aws.Config) []string {
	client := opensearch.NewFromConfig(cfg)
	out, err := client.ListDomainNames(ctx, &opensearch.ListDomainNamesInput{})
	if err != nil {
		slog.Warn("discover: OpenSearch ListDomainNames", "error", err)
		return nil
	}
	var ids []string
	for _, d := range out.DomainNames {
		if d.DomainName != nil {
			ids = append(ids, aws.ToString(d.DomainName))
		}
	}
	return ids
}
