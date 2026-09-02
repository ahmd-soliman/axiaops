package aws

import (
	"context"
	"time"

	"axiaops.io/ingestion/internal/provider"
	"axiaops.io/shared/model"
)

// CostExplorerSource implements provider.BillingSource using the AWS Cost Explorer API.
type CostExplorerSource struct {
	client *Client
}

// NewCostExplorerSource wraps an existing aws.Client.
func NewCostExplorerSource(client *Client) *CostExplorerSource {
	return &CostExplorerSource{client: client}
}

func (s *CostExplorerSource) Name() string {
	return "aws" // The CE source is the default AWS provider for now, maintaining compatibility.
}

func (s *CostExplorerSource) FetchCosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error) {
	records, err := s.client.FetchCosts(ctx, start, end)
	if err != nil {
		return nil, err
	}
	for i := range records {
		records[i].CostBasis = model.CostBasisBilled
	}
	return records, nil
}

// FetchResourceCosts is CUR-only (see billing.go). CE never returns
// resource-level cost from this source, per the adopted scope.
func (s *CostExplorerSource) FetchResourceCosts(context.Context, time.Time, time.Time) ([]model.CostRecord, error) {
	return nil, provider.ErrResourceCostsUnsupported
}
