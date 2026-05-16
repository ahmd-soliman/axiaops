// Package provider defines the Provider interface that every cloud adapter must
// implement. Adding a new cloud provider (GCP, Azure, etc.) means creating a
// new package that satisfies this interface — no changes needed elsewhere.
package provider

import (
	"context"
	"time"

	"axiaops.io/shared/model"
)

// Provider is the interface every cloud provider adapter must implement.
type Provider interface {
	// Name returns the provider identifier (e.g. "aws", "gcp", "azure").
	Name() string

	// FetchCosts retrieves cost records for the given time range.
	FetchCosts(ctx context.Context, start, end time.Time) ([]model.CostRecord, error)
}
