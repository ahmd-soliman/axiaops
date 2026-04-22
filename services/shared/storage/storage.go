// Package storage defines the Store interface for persisting cost records.
// PostgreSQL is the only storage implementation.
// without changing any other code.
package storage

import (
	"context"
	"errors"
	"time"

	"axiaops.io/shared/model"
)

// ErrAlreadyDismissed is returned when a ghost resource already has an active
// dismissal or snooze.  Callers should surface this as HTTP 409 Conflict.
var ErrAlreadyDismissed = errors.New("storage: resource already has an active dismissal")

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

// CostFilter specifies criteria for listing cost records.
type CostFilter struct {
	InternalAccountID string // optional: filter by internal_account_id (system account ID)
	AWSAccountID      string // optional: filter by account_id (AWS account ID) — for backward compatibility with old records
	Service           string // optional: filter by service name
	Days              int    // optional: lookback window in days (default: 30)
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

	// EnsureTenant creates a tenant with a caller-supplied id if no row with
	// that id exists yet. Unlike UpsertTenant, the id is pinned (not a UUID)
	// and the row is never modified on conflict. Used by dev mode at startup
	// to guarantee a known-id tenant row for FK references.
	EnsureTenant(ctx context.Context, id, orgCode, name string) error

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

	// ListCostRecords returns cost records for the tenant in ctx, filtered by account, service, and time window.
	// Records with amount > 0 are returned, ordered by period_start (newest first) then amount (largest first).
	// If filter.Days is 0 or negative, defaults to 30 days.
	// ctx must carry a tenant ID via WithTenantID when using PostgreSQL.
	ListCostRecords(ctx context.Context, filter CostFilter) ([]model.CostRecord, error)

	// SaveSnapshot writes a ghost snapshot after each ingestion scan.
	// Snapshots are never replaced — they accumulate to form the savings history.
	// ctx must carry a tenant ID via WithTenantID when using PostgreSQL.
	SaveSnapshot(ctx context.Context, snap model.GhostSnapshot) error

	// ListSnapshots returns ghost snapshots for the tenant in ctx, ordered
	// oldest-first. If accountID is non-empty, only snapshots for that account
	// are returned.
	ListSnapshots(ctx context.Context, accountID string) ([]model.GhostSnapshot, error)

	// SaveSnapshotServices writes per-service breakdown rows for a snapshot.
	SaveSnapshotServices(ctx context.Context, services []model.SnapshotService) error

	// ListSnapshotsByService returns ghost snapshots filtered by service,
	// ordered oldest-first. Each snapshot's cost/count reflects only the given
	// service. If resourceType is non-empty, further filters to that sub-type;
	// otherwise aggregates all resource types for the service.
	// If accountID is non-empty, also filters by account.
	ListSnapshotsByService(ctx context.Context, service, resourceType, accountID string) ([]model.GhostSnapshot, error)

	// ListTrendServices returns the distinct services that have snapshot data
	// for the tenant, useful for populating filter UI.
	ListTrendServices(ctx context.Context) ([]string, error)

	// ListTrendResourceTypes returns distinct resource types for a given service
	// that have snapshot data for the tenant.
	ListTrendResourceTypes(ctx context.Context, service string) ([]string, error)

	// DeleteOldCostRecords removes cost records older than the given cutoff for all tenants.
	// Returns the number of rows deleted.
	DeleteOldCostRecords(ctx context.Context, cutoff time.Time) (int64, error)

	// DismissGhost records a dismiss or snooze action for a ghost resource.
	// Returns the new dismissal ID.
	// Returns ErrAlreadyDismissed if an active dismissal already exists for the fingerprint.
	DismissGhost(ctx context.Context, d model.DismissAction) (int64, error)

	// RevokeDismissal soft-deletes an active dismissal (sets revoked_at / revoked_by).
	// Returns an error if the dismissal does not exist or is already revoked.
	RevokeDismissal(ctx context.Context, id int64, revokedBy string) error

	// ListActiveDismissals returns all active (non-revoked, non-expired) dismissals
	// for the tenant in ctx.  If accountID is non-empty, only that account is returned.
	ListActiveDismissals(ctx context.Context, accountID string) ([]model.DismissAction, error)

	// ExpireSnoozes marks snoozed records whose snoozed_until has passed as revoked.
	// This is a cross-tenant operation called by the background maintenance worker.
	// Returns the number of records expired.
	ExpireSnoozes(ctx context.Context) (int64, error)

	// Close releases any resources held by the store.
	Close() error
}
