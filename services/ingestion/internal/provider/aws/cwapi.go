package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
)

// CloudWatchAPI is the subset of the AWS CloudWatch client used by this
// package. Declaring it as an interface allows tests to inject a mock instead
// of a real SDK client.
type CloudWatchAPI interface {
	GetMetricStatistics(
		ctx context.Context,
		input *cloudwatch.GetMetricStatisticsInput,
		opts ...func(*cloudwatch.Options),
	) (*cloudwatch.GetMetricStatisticsOutput, error)
}
