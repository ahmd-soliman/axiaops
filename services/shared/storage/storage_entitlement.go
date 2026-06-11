// storage_entitlement.go — SaaS per-tenant entitlement Store additions. Split
// into its own file (like storage_staff.go) for a reviewable diff; composes
// into Store via Go's multi-file interface declarations.
//
// Every method here runs on the admin pool (RLS-bypass) with NO organization
// context — the `entitlements` table is system-scoped (migration 033), written
// only by AxiaOps and read cross-org / pre-auth. GetEntitlement takes the org
// id as an explicit parameter (NOT from WithOrganizationID), so the same method
// serves the org-scoped api handler and the cross-org ingestion worker /
// scheduler. See docs/saas-platform-admin-design.md §7.2 / §8.
//
// DORMANT SCAFFOLD (Phase 2A): present and tested, but no scan gate consults it
// until Phase 2B wires the default (SaaS) build's scan gates via the build-tag
// seam (services/{api,ingestion}/cmd/saasmode_saas.go) (design §7.1).

package storage

import (
	"context"
	"errors"

	"axiaops.io/shared/model"
)

// ErrEntitlementNotFound is returned by GetEntitlement when the org has no
// entitlement row. The entitlement gate treats this as a definitive fail-closed
// "deny" (not an error) — billing is the source of truth and every real tenant
// gets a row at signup, so absence means "not entitled" (design §7.2).
var ErrEntitlementNotFound = errors.New("storage: entitlement not found")

// EntitlementStore is the slice of Store dealing with SaaS per-tenant
// entitlement. Implementations live in storage/postgres/entitlement.go and run
// on the admin pool, org-less.
type EntitlementStore interface {
	// GetEntitlement returns the org's entitlement by organization_id. Returns
	// ErrEntitlementNotFound when no row matches. Admin pool, no org context.
	GetEntitlement(ctx context.Context, organizationID string) (model.Entitlement, error)

	// UpsertEntitlement inserts or updates the org's entitlement, keyed on the
	// UNIQUE organization_id (ON CONFLICT DO UPDATE) — idempotent and
	// order-tolerant for at-least-once billing webhooks. Refreshes updated_at.
	UpsertEntitlement(ctx context.Context, e model.Entitlement) error

	// ListAllEntitlements returns every entitlement, oldest-first. Admin pool,
	// no org context (mirrors ListAllAccounts) — for the cross-org scheduler to
	// batch-load entitlement state in one pass.
	ListAllEntitlements(ctx context.Context) ([]model.Entitlement, error)
}
