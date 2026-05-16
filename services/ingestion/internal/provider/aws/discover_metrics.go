package aws

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cloudwatchsdk "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cloudwatchTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// newCloudWatchClient creates a CloudWatch client from an AWS config.
func newCloudWatchClient(cfg aws.Config) CloudWatchAPI {
	return cloudwatchsdk.NewFromConfig(cfg)
}

// getMetricAvg queries CloudWatch for a single metric/resource and returns the
// average value. Returns -1 if no datapoints are available (metric not configured).
func getMetricAvg(ctx context.Context, cw CloudWatchAPI, namespace, metricName, dimensionName, dimensionValue string, start, end time.Time, periodSecs int32, extraDimensions []cloudwatchTypes.Dimension) (float64, error) {
	dimensions := []cloudwatchTypes.Dimension{
		{Name: aws.String(dimensionName), Value: aws.String(dimensionValue)},
	}
	dimensions = append(dimensions, extraDimensions...)

	out, err := cw.GetMetricStatistics(ctx, &cloudwatchsdk.GetMetricStatisticsInput{
		Namespace:  aws.String(namespace),
		MetricName: aws.String(metricName),
		Dimensions: dimensions,
		StartTime:  aws.Time(start),
		EndTime:    aws.Time(end),
		Period:     aws.Int32(periodSecs),
		Statistics: []cloudwatchTypes.Statistic{cloudwatchTypes.StatisticSum},
	})
	if err != nil {
		return -1, err
	}

	if len(out.Datapoints) == 0 {
		return -1, nil
	}

	total := 0.0
	for _, dp := range out.Datapoints {
		total += aws.ToFloat64(dp.Sum)
	}
	return total, nil
}
