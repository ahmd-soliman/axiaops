package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
)

func discoverElastiCache(ctx context.Context, cfg aws.Config) []string {
	client := elasticache.NewFromConfig(cfg)
	var ids []string
	var marker *string
	for {
		out, err := client.DescribeCacheClusters(ctx, &elasticache.DescribeCacheClustersInput{
			Marker: marker,
		})
		if err != nil {
			slog.Warn("discover: ElastiCache DescribeCacheClusters", "error", err)
			return nil
		}
		for _, c := range out.CacheClusters {
			if c.CacheClusterId != nil {
				ids = append(ids, aws.ToString(c.CacheClusterId))
			}
		}
		if out.Marker == nil {
			break
		}
		marker = out.Marker
	}
	return ids
}
