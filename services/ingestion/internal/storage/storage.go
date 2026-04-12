// Package storage defines the Store interface for persisting cost records.
// PostgreSQL is the only storage implementation.
package storage

import (
	"context"

	"axiaops.io/ingestion/internal/model"
)

// Store persists and retrieves cost records, tenants, and users.
type Store interface {
	// Save inserts a batch of cost records, skipping duplicates.
	// Returns the number of records actually inserted.
	Save(ctx context.Context, records []model.CostRecord) (int64, error)

	// UpsertTenant creates a tenant on first login or returns the existing one.
	// Keyed on org_code — the Kinde organisation identifier.
	UpsertTenant(ctx context.Context, orgCode, name string) (model.Tenant, error)

	// UpsertUser creates a user on first login or updates last_seen.
	// Keyed on kinde_sub — the stable Kinde user identifier.
	UpsertUser(ctx context.Context, tenantID, kindeSub, email, name string) (model.User, error)

	// Close releases any resources held by the store.
	Close() error
}
