package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"axiaops.io/shared/analyzer"
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
}

// FetchUsage queries CloudWatch for average usage metrics for each discovered
// resource. Returns one UsageRecord per resource.
func FetchUsage(ctx context.Context, cw CloudWatchAPI, resources []DiscoveredResource, start, end time.Time) ([]analyzer.UsageRecord, error) {
	periodSecs := int32(end.Sub(start).Seconds())
	if periodSecs < 60 {
		periodSecs = 60
	}

	var usage []analyzer.UsageRecord

	for _, r := range resources {
		sm, ok := serviceMetrics[r.Service]
		if !ok {
			continue
		}

		out, err := cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
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
		if err != nil {
			return nil, fmt.Errorf("cloudwatch: GetMetricStatistics %s/%s: %w", r.Service, r.ResourceID, err)
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

	return usage, nil
}
