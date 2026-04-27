package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// STSAPI is the subset of the AWS Security Token Service client used by this
// package. Declaring it as an interface lets tests inject a mock instead of
// a real SDK client — the same pattern used by CostExplorerAPI and
// CloudWatchAPI.
type STSAPI interface {
	AssumeRole(
		ctx context.Context,
		input *sts.AssumeRoleInput,
		opts ...func(*sts.Options),
	) (*sts.AssumeRoleOutput, error)

	GetCallerIdentity(
		ctx context.Context,
		input *sts.GetCallerIdentityInput,
		opts ...func(*sts.Options),
	) (*sts.GetCallerIdentityOutput, error)
}
