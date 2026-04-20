package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"axiaops.io/shared/analyzer"
	"axiaops.io/shared/retry"
)

// serviceMetric maps an AWS service name to the CloudWatch namespace,
// dimension name, and metric name needed to fetch its usage.
type serviceMetric struct {
	namespace     string
	dimensionName string
	metricName    string
	unit          string
}

var serviceMetrics = map[string]serviceMetric{
	"AmazonEC2": {
		namespace:     "AWS/EC2",
		dimensionName: "InstanceId",
		metricName:    "CPUUtilization",
		unit:          "Percent",
	},
	"AmazonRDS": {
		namespace:     "AWS/RDS",
		dimensionName: "DBInstanceIdentifier",
		metricName:    "DatabaseConnections",
		unit:          "Count",
	},
	"AWSLambda": {
		namespace:     "AWS/Lambda",
		dimensionName: "FunctionName",
		metricName:    "Invocations",
		unit:          "Count",
	},
	"AmazonElasticLoadBalancing": {
		namespace:     "AWS/ApplicationELB",
		dimensionName: "LoadBalancer",
		metricName:    "RequestCount",
		unit:          "Count",
	},
	"AmazonVPC": {
		namespace:     "AWS/NATGateway",
		dimensionName: "NatGatewayId",
		metricName:    "BytesOutToDestination",
		unit:          "Bytes",
	},
	// Tier 2 — CloudWatch-based detection
	"AmazonElastiCache": {
		namespace:     "AWS/ElastiCache",
		dimensionName: "CacheClusterId",
		metricName:    "CurrConnections",
		unit:          "Count",
	},
	"AmazonES": {
		namespace:     "AWS/ES",
		dimensionName: "DomainName",
		metricName:    "SearchRate",
		unit:          "Count",
	},
	"AmazonRedshift": {
		namespace:     "AWS/Redshift",
		dimensionName: "ClusterIdentifier",
		metricName:    "DatabaseConnections",
		unit:          "Count",
	},
	"AmazonSageMaker": {
		namespace:     "AWS/SageMaker",
		dimensionName: "EndpointName",
		metricName:    "Invocations",
		unit:          "Count",
	},
	"AmazonDynamoDB": {
		namespace:     "AWS/DynamoDB",
		dimensionName: "TableName",
		metricName:    "ConsumedReadCapacityUnits",
		unit:          "Count",
	},
	// EKS: requires Container Insights enabled on the cluster.
	// Clusters without Container Insights will return no data and be skipped.
	"AmazonEKS": {
		namespace:     "ContainerInsights",
		dimensionName: "ClusterName",
		metricName:    "cluster_node_count",
		unit:          "Count",
	},
	// NOTE: CloudFront, Kinesis, and S3 use direct detection via Discover*
	// functions that handle their own CloudWatch queries. They are NOT in this map.
}

// FetchUsage queries CloudWatch for average usage metrics for each discovered
// resource. Returns one UsageRecord per resource.
func FetchUsage(ctx context.Context, cw CloudWatchAPI, resources []DiscoveredResource, start, end time.Time) ([]analyzer.UsageRecord, error) {
	periodSecs := int32(end.Sub(start).Seconds())
	if periodSecs < 60 {
		periodSecs = 60
	}

	var usage []analyzer.UsageRecord
	var errors []error

	for _, r := range resources {
		sm, ok := serviceMetrics[r.Service]
		if !ok {
			continue
		}

		var out *cloudwatch.GetMetricStatisticsOutput
		err := retry.Do(ctx, retry.DefaultConfig(), func() error {
			var err error
			out, err = cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
				Namespace:  aws.String(sm.namespace),
				MetricName: aws.String(sm.metricName),
				Dimensions: []types.Dimension{
					{
						Name:  aws.String(sm.dimensionName),
						Value: aws.String(r.ResourceID),
					},
				},
				StartTime:  aws.Time(start),
				EndTime:    aws.Time(end),
				Period:     aws.Int32(periodSecs),
				Statistics: []types.Statistic{types.StatisticAverage},
			})
			return err
		})
		if err != nil {
			// Log individual resource failure but continue processing others
			fmt.Printf("cloudwatch: GetMetricStatistics %s/%s failed: %v\n", r.Service, r.ResourceID, err)
			errors = append(errors, fmt.Errorf("resource %s/%s: %w", r.Service, r.ResourceID, err))
			continue
		}

		avg := 0.0
		if len(out.Datapoints) > 0 {
			avg = aws.ToFloat64(out.Datapoints[0].Average)
		}

		usage = append(usage, analyzer.UsageRecord{
			ResourceID: r.ResourceID,
			Metric:     sm.metricName,
			Unit:       sm.unit,
			Avg:        avg,
			PeriodDays: int(end.Sub(start).Hours() / 24),
		})
	}

	// Return partial results even if some resources failed
	if len(errors) > 0 && len(usage) == 0 {
		// All resources failed - return the first error
		return nil, errors[0]
	}
	
	return usage, nil
}
