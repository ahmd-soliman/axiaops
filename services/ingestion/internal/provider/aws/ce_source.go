package aws

import (
	"context"
	"time"

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

func (s *CostExplorerSource) FetchResourceCosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error) {
	// For CE, resource costs are genuinely billed.
	records, err := s.client.FetchResourceCosts(ctx, start, end)
	if err != nil {
		return nil, err
	}
	for i := range records {
		records[i].CostBasis = model.CostBasisBilled
	}
	return records, nil
}
