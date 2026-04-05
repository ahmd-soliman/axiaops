// Package filefixture implements a dev-only Provider that reads CostRecords
// directly from a local JSON file. Used when DEV_MODE=true to avoid needing
// real AWS credentials or a running LocalStack instance.
package filefixture

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"axiaops.io/shared/model"
)

// Client reads cost data from a local JSON fixture file.
type Client struct {
	path string
}

func New(path string) *Client {
	return &Client{path: path}
}

func (c *Client) Name() string { return "filefixture" }

// FetchCosts reads and returns all CostRecords from the fixture file.
// The start/end parameters are accepted to satisfy the Provider interface
// but are ignored — the fixture returns all records as-is.
func (c *Client) FetchCosts(_ context.Context, _, _ time.Time) ([]model.CostRecord, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, fmt.Errorf("filefixture: read file: %w", err)
	}

	var records []model.CostRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("filefixture: unmarshal: %w", err)
	}

	return records, nil
}
