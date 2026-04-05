// Package storage defines the Store interface for persisting cost records.
// Implementations can swap between SQLite (dev) and PostgreSQL (production)
// without changing any other code.
package storage

import (
	"context"

	"axiaops.io/shared/model"
)

// Store persists and retrieves cost records, tenants, and users.
type Store interface {
	// Save inserts a batch of cost records, skipping duplicates.
	// Returns the number of records actually inserted.
	Save(ctx context.Context, records []model.CostRecord) (int64, error)

	// SaveGhosts replaces all ghost records with the latest detection results.
	// Called by the ingestion job after each analysis run.
	SaveGhosts(ctx context.Context, ghosts []model.GhostResource) error

	// LoadGhosts returns all ghost records from the last ingestion run.
	// Called by the API service on startup and on demand.
	LoadGhosts(ctx context.Context) ([]model.GhostResource, error)

	// UpsertTenant creates a tenant on first login or returns the existing one.
	// Keyed on org_code — the Kinde organisation identifier.
	UpsertTenant(ctx context.Context, orgCode, name string) (model.Tenant, error)

	// UpsertUser creates a user on first login or updates last_seen.
	// Keyed on kinde_sub — the stable Kinde user identifier.
	UpsertUser(ctx context.Context, tenantID, kindeSub, email, name string) (model.User, error)

	// Close releases any resources held by the store.
	Close() error
}
