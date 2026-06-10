// entitlement.go — SaaS per-tenant entitlement Store impl. Methods declared on
// *Store; satisfies storage.EntitlementStore (embedded into storage.Store).
// See docs/saas-platform-admin-design.md §7.2 / §8.
//
// Every method runs on s.adminPool with NO organization context — the
// `entitlements` table is system-scoped, has no RLS (migration 033), and is
// read cross-org / pre-auth. Mirrors staff.go's posture exactly.
//
// DORMANT SCAFFOLD (Phase 2A): no scan gate consults these yet.

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// entitlementSelectSQL is the column list shared by the by-org and list-all
// reads. Order matches scanEntitlement.
const entitlementSelectSQL = `
	SELECT organization_id, plan, status, max_accounts, features,
	       trial_ends_at, current_period_end,
	       COALESCE(billing_customer_ref, ''), COALESCE(billing_subscription_ref, ''),
	       created_at, updated_at
	FROM entitlements`

func scanEntitlement(row pgx.Row) (model.Entitlement, error) {
	var e model.Entitlement
	var status string
	err := row.Scan(
		&e.OrganizationID, &e.Plan, &status, &e.MaxAccounts, &e.Features,
		&e.TrialEndsAt, &e.CurrentPeriodEnd,
		&e.BillingCustomerRef, &e.BillingSubscriptionRef,
		&e.CreatedAt, &e.UpdatedAt,
	)
	e.Status = model.EntitlementStatus(status)
	return e, err
}

// GetEntitlement returns the org's entitlement. Returns
// storage.ErrEntitlementNotFound when absent.
func (s *Store) GetEntitlement(ctx context.Context, organizationID string) (model.Entitlement, error) {
	if organizationID == "" {
		return model.Entitlement{}, storage.ErrEntitlementNotFound
	}
	e, err := scanEntitlement(s.adminPool.QueryRow(ctx, entitlementSelectSQL+` WHERE organization_id = $1`, organizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Entitlement{}, storage.ErrEntitlementNotFound
	}
	if err != nil {
		return model.Entitlement{}, fmt.Errorf("postgres: get entitlement: %w", err)
	}
	return e, nil
}

// UpsertEntitlement inserts or updates the org's entitlement, keyed on the
// UNIQUE organization_id. Idempotent + order-tolerant for at-least-once billing
// webhooks. Refreshes updated_at on every write.
func (s *Store) UpsertEntitlement(ctx context.Context, e model.Entitlement) error {
	if e.OrganizationID == "" {
		return fmt.Errorf("postgres: upsert entitlement: organization_id required")
	}
	if !model.ValidEntitlementStatus(e.Status) {
		return fmt.Errorf("postgres: upsert entitlement: invalid status %q", e.Status)
	}
	plan := e.Plan
	if plan == "" {
		plan = "free"
	}
	maxAccounts := e.MaxAccounts
	if maxAccounts <= 0 {
		maxAccounts = 1
	}
	features := e.Features
	if features == nil {
		features = []string{}
	}
	var billingCustomerRef, billingSubscriptionRef *string
	if e.BillingCustomerRef != "" {
		billingCustomerRef = &e.BillingCustomerRef
	}
	if e.BillingSubscriptionRef != "" {
		billingSubscriptionRef = &e.BillingSubscriptionRef
	}

	_, err := s.adminPool.Exec(ctx, `
		INSERT INTO entitlements (
			organization_id, plan, status, max_accounts, features,
			trial_ends_at, current_period_end,
			billing_customer_ref, billing_subscription_ref, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT (organization_id) DO UPDATE SET
			plan                     = EXCLUDED.plan,
			status                   = EXCLUDED.status,
			max_accounts             = EXCLUDED.max_accounts,
			features                 = EXCLUDED.features,
			trial_ends_at            = EXCLUDED.trial_ends_at,
			current_period_end       = EXCLUDED.current_period_end,
			billing_customer_ref     = EXCLUDED.billing_customer_ref,
			billing_subscription_ref = EXCLUDED.billing_subscription_ref,
			updated_at               = NOW()`,
		e.OrganizationID, plan, string(e.Status), maxAccounts, features,
		e.TrialEndsAt, e.CurrentPeriodEnd,
		billingCustomerRef, billingSubscriptionRef,
	)
	if err != nil {
		// 23503 = foreign_key_violation: the organization_id doesn't exist.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return storage.ErrOrganizationNotFound
		}
		return fmt.Errorf("postgres: upsert entitlement: %w", err)
	}
	return nil
}

// ListAllEntitlements returns every entitlement, oldest-first. Admin pool, no
// org context — for the cross-org scheduler.
func (s *Store) ListAllEntitlements(ctx context.Context) ([]model.Entitlement, error) {
	rows, err := s.adminPool.Query(ctx, entitlementSelectSQL+` ORDER BY created_at ASC, organization_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list all entitlements: %w", err)
	}
	defer rows.Close()

	var ents []model.Entitlement
	for rows.Next() {
		e, err := scanEntitlement(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan entitlement: %w", err)
		}
		ents = append(ents, e)
	}
	return ents, rows.Err()
}
