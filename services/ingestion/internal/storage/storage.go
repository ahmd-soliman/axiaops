// Package storage defines the Store interface for persisting cost records.
// Implementations can swap between SQLite (dev) and PostgreSQL (production)
// without changing any other code.
package storage

import (
	"context"

	"axiaops.io/ingestion/internal/model"
)

// Store persists and retrieves cost records.
type Store interface {
	// Save inserts a batch of cost records, skipping duplicates.
	// Returns the number of records actually inserted.
	Save(ctx context.Context, records []model.CostRecord) (int64, error)

	// Close releases any resources held by the store.
	Close() error
}
