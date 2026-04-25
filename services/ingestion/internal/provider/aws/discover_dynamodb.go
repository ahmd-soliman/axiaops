package aws

import (
	"context"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

func discoverDynamoDB(ctx context.Context, cfg aws.Config) []string {
	client := dynamodb.NewFromConfig(cfg)
	var ids []string
	var startTable *string
	for {
		out, err := client.ListTables(ctx, &dynamodb.ListTablesInput{
			ExclusiveStartTableName: startTable,
		})
		if err != nil {
			slog.Warn("discover: DynamoDB ListTables", "error", err)
			return nil
		}
		ids = append(ids, out.TableNames...)
		if out.LastEvaluatedTableName == nil {
			break
		}
		startTable = out.LastEvaluatedTableName
	}
	return ids
}
