// Package storage defines the Store interface for persisting cost records.
// PostgreSQL is the only storage implementation.
// without changing any other code.
package storage

import (
	"context"

	"axiaops.io/shared/model"
)

type ctxKey string

const tenantKey ctxKey = "tenant_id"

// WithTenantID returns a context carrying the given tenant ID.
// The PostgreSQL store reads this to set app.tenant_id for Row-Level Security.
func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantKey, tenantID)
}

// TenantIDFromCtx returns the tenant ID stored in the context, or "".
func TenantIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(tenantKey).(string)
	return v
}

// Store persists and retrieves cost records, tenants, and users.
type Store interface {
	// Save inserts a batch of cost records, skipping duplicates.
	// Returns the number of records actually inserted.
	Save(ctx context.Context, records []model.CostRecord) (int64, error)

	// SaveGhosts replaces all ghost records with the latest detection results.
	// Called by the ingestion job after each analysis run.
	// ctx must carry a tenant ID via WithTenantID when using PostgreSQL.
	SaveGhosts(ctx context.Context, ghosts []model.GhostResource) error

	// LoadGhosts returns ghost records for the tenant in ctx.
	// Called by the API service per request.
	// ctx must carry a tenant ID via WithTenantID when using PostgreSQL.
	LoadGhosts(ctx context.Context) ([]model.GhostResource, error)

	// UpsertTenant creates a tenant on first login or returns the existing one.
	// Keyed on org_code — the Kinde organisation identifier.
	UpsertTenant(ctx context.Context, orgCode, name string) (model.Tenant, error)

	// UpsertUser creates a user on first login or updates last_seen.
	// Keyed on kinde_sub — the stable Kinde user identifier.
	UpsertUser(ctx context.Context, tenantID, kindeSub, email, name string) (model.User, error)

	// SaveAccount inserts or replaces a connected cloud account for a tenant.
	SaveAccount(ctx context.Context, a model.Account) error

	// ListAccounts returns all connected accounts for the tenant in ctx.
	ListAccounts(ctx context.Context) ([]model.Account, error)

	// ListAllAccounts returns accounts for ALL tenants, bypassing row-level security.
	// Used internally by the scheduled scan scheduler to check all accounts across all tenants.
	// WARNING: This must only be called from trusted internal code (e.g., background jobs).
	// Never call with untrusted input. ctx.tenant_id is ignored if present.
	ListAllAccounts(ctx context.Context) ([]model.Account, error)

	// GetAccount returns a single account by ID for the tenant in ctx.
	GetAccount(ctx context.Context, id string) (model.Account, error)

	// DeleteAccount removes an account by ID for the tenant in ctx.
	DeleteAccount(ctx context.Context, id string) error

	// UpdateAccountStatus sets the status and last_scanned_at for an account.
	UpdateAccountStatus(ctx context.Context, id, status string) error

	// TryMarkAccountScanning sets status to scanning only if not already scanning.
	// Returns true when the row was updated; false when another scan is in progress.
	TryMarkAccountScanning(ctx context.Context, id string) (bool, error)

	// SaveResources replaces all resource records with the latest inventory.
	// Called by the ingestion job after each analysis run.
	// ctx must carry a tenant ID via WithTenantID when using PostgreSQL.
	SaveResources(ctx context.Context, resources []model.ResourceRecord) error

	// LoadResources returns all resource records for the tenant in ctx.
	// Called by the API service per request.
	// ctx must carry a tenant ID via WithTenantID when using PostgreSQL.
	LoadResources(ctx context.Context) ([]model.ResourceRecord, error)

	// SaveSnapshot writes a ghost snapshot after each ingestion scan.
	// Snapshots are never replaced — they accumulate to form the savings history.
	// ctx must carry a tenant ID via WithTenantID when using PostgreSQL.
	SaveSnapshot(ctx context.Context, snap model.GhostSnapshot) error

	// ListSnapshots returns ghost snapshots for the tenant in ctx, ordered
	// oldest-first. If accountID is non-empty, only snapshots for that account
	// are returned.
	ListSnapshots(ctx context.Context, accountID string) ([]model.GhostSnapshot, error)

	// Close releases any resources held by the store.
	Close() error
}
