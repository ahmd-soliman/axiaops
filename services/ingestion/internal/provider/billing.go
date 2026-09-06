package provider

import (
	"context"
	"errors"
	"time"

	"axiaops.io/shared/model"
)

// BillingSource produces cost records for one connected account. Exactly one
// implementation is active per account — CE and CUR are mutually exclusive,
// never layered.
type BillingSource interface {
	// Name identifies the source in logs and metrics:
	// "aws-cost-explorer" | "aws-cur-athena".
	Name() string

	// FetchCosts returns service+region granularity for the window.
	FetchCosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error)

	// FetchResourceCosts returns resource granularity. The CE implementation
	// returns ErrResourceCostsUnsupported; only CUR implements it.
	FetchResourceCosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error)
}

// ErrResourceCostsUnsupported signals that this source cannot produce
// resource-level costs at all. Callers degrade to rates.yml estimates and mark
// the records CostBasisListPrice. Distinct from a transient failure, which
// must fail the scan.
var ErrResourceCostsUnsupported = errors.New("billing source has no resource-level data")
