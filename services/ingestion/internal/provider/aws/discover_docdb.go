package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/docdb"
)

func discoverDocDB(ctx context.Context, cfg aws.Config) []string {
	client := docdb.NewFromConfig(cfg)
	var ids []string
	var marker *string

	for {
		out, err := client.DescribeDBClusters(ctx, &docdb.DescribeDBClustersInput{
			Marker: marker,
		})
		if err != nil {
			slog.Warn("discover: DocDB DescribeDBClusters", "error", err)
			return nil
		}
		for _, c := range out.DBClusters {
			if c.DBClusterIdentifier != nil {
				ids = append(ids, aws.ToString(c.DBClusterIdentifier))
			}
		}
		if out.Marker == nil {
			break
		}
		marker = out.Marker
	}
	return ids
}
