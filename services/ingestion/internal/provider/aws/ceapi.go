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

	// GetCostAndUsageWithResources is the only Cost Explorer API that supports
	// grouping by RESOURCE_ID. Used by FetchResourceCosts. Constraints AWS
	// imposes that the regular GetCostAndUsage does not:
	//   - Granularity must be DAILY or HOURLY (not MONTHLY).
	//   - Filter is required.
	//   - Time window can only span the past 14 days.
	//   - The customer's account must have opted in to "hourly granularity and
	//     resource-level data" in Cost Explorer settings, otherwise the API
	//     returns DataUnavailableException.
	GetCostAndUsageWithResources(
		ctx context.Context,
		input *costexplorer.GetCostAndUsageWithResourcesInput,
		opts ...func(*costexplorer.Options),
	) (*costexplorer.GetCostAndUsageWithResourcesOutput, error)
}
