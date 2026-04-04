package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
)

// CostExplorerAPI is the subset of the AWS Cost Explorer client used by this
// package. Declaring it as an interface allows tests to inject a mock instead
// of a real SDK client.
type CostExplorerAPI interface {
	GetCostAndUsage(
		ctx context.Context,
		input *costexplorer.GetCostAndUsageInput,
		opts ...func(*costexplorer.Options),
	) (*costexplorer.GetCostAndUsageOutput, error)
}
