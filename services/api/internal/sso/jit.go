package sso

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"axiaops.io/shared/authz"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// JITMembershipStore is the minimal Store surface JITProvisionMembership
// touches. Narrowing the parameter type from storage.Store keeps the JIT
// path testable without standing up a full mock-of-everything.
// storage.Store satisfies this interface — production callers pass it
// unchanged.
type JITMembershipStore interface {
	SaveMembership(ctx context.Context, m model.Membership) error
	GetMembershipByOrgUser(ctx context.Context, organizationID, userID string) (model.Membership, error)
	UpdateMembershipRole(ctx context.Context, id, newRole string) error
}

// rolePriority assigns a strict total order to JIT-assignable roles. Higher
// number wins when a user matches multiple group mappings.
//
// Owner is intentionally absent — it is NEVER assignable via JIT. The owner
// property is sticky (sole-owner constraint, ownership-transfer flow) and
// must originate from a deliberate human action, not a group claim.
//
// The DB CHECK constraint on sso_group_mappings.role enforces this at write
// time too: only admin/member/viewer can be inserted as a mapping target.
var rolePriority = map[string]int{
	string(authz.RoleViewer): 1,
	string(authz.RoleMember): 2,
	string(authz.RoleAdmin):  3,
}

// JITResolveRole picks the highest-priority role from the mappings whose
// group_external_id matches one of the user's groups. Returns defaultRole if
// no mapping matches (or if mappings is empty). Never returns owner.
//
// Matching is case-sensitive on group_external_id — Entra GUIDs are
// case-sensitive. Admins paste their inputs and are responsible for case.
//
// Tie-break order: admin > member > viewer (per architect S6 / plan §5.2).
// Ties between two mappings to the same role are not errors — they're
// degenerate inputs and the resolver returns that role.
func JITResolveRole(mappings []model.SSOGroupMapping, userGroups []string, defaultRole string) string {
	if defaultRole == "" {
		defaultRole = string(authz.RoleViewer)
	}
	if len(mappings) == 0 || len(userGroups) == 0 {
		return defaultRole
	}

	// Build a set for O(1) membership tests on the user's groups.
	groupSet := make(map[string]struct{}, len(userGroups))
	for _, g := range userGroups {
		groupSet[g] = struct{}{}
	}

	bestRole := defaultRole
	bestPriority := rolePriority[defaultRole] // unknown roles → 0; default beats nothing
	for _, m := range mappings {
		if _, ok := groupSet[m.GroupExternalID]; !ok {
			continue
		}
		// rolePriority lookup is 0 for owner (absent) — owner mappings can't
		// have been written to the DB anyway thanks to the CHECK constraint,
		// but defending in depth here means a malformed row from a future
		// schema-evolution bug can't escalate someone to owner.
		p, ok := rolePriority[m.Role]
		if !ok {
			continue
		}
		if p > bestPriority {
			bestPriority = p
			bestRole = m.Role
		}
	}
	return bestRole
}

// ErrJITOwnerForbidden is returned when a caller tries to JIT-provision an
// owner — a defence-in-depth check the JIT-provision path should never trip
// (the resolver caps at admin), but worth surfacing if someone bypasses it.
var ErrJITOwnerForbidden = errors.New("sso: JIT cannot provision owner role; ownership originates from bootstrap or transfer only")

// JITOutcome describes which path JITProvisionMembership took. Returned so
// the caller can write the right audit row — first-time provision and
// re-login role change are different events (`sso_jit_provisioned` vs
// `sso_jit_role_updated` per design §10.3).
type JITOutcome int

const (
	// JITOutcomeNoop — membership already exists at the requested role.
	// Re-login with unchanged role; not audited (would otherwise log every
	// SSO login as a JIT event).
	JITOutcomeNoop JITOutcome = iota
	// JITOutcomeCreated — first-time JIT provision. Audit
	// `sso_jit_provisioned`.
	JITOutcomeCreated
	// JITOutcomeUpdated — existing membership role reconciled to match
	// current group claims. Audit `sso_jit_role_updated`.
	JITOutcomeUpdated
)

// JITProvisionMembership inserts a (user, organization, role) membership in
// one transaction and marks it as provisioned_via='jit'. Idempotent on the
// (organization_id, user_id) unique constraint — re-call after a successful
// JIT login is a no-op if the membership already exists with the same role,
// or an UpdateMembershipRole if the role differs (group claim changed).
//
// In B2 slice 3 (this skeleton), this returns ErrJITNotImplemented because
// the OIDC RP that triggers it doesn't exist yet. The OIDC RP slice fills
// this in by sourcing user.id (post UpsertUser) and the resolved role.
//
// The seam exists now so the OIDC RP slice doesn't need to define a new
// helper — and the test in permission_matrix_test.go already asserts the
// owner-rejection guard, which is the security-critical part.
func JITProvisionMembership(ctx context.Context, store JITMembershipStore, organizationID, userID, role string) (JITOutcome, error) {
	if role == string(authz.RoleOwner) {
		return JITOutcomeNoop, ErrJITOwnerForbidden
	}
	if _, ok := rolePriority[role]; !ok {
		return JITOutcomeNoop, fmt.Errorf("sso: JIT provision: unknown role %q", role)
	}
	if organizationID == "" || userID == "" {
		return JITOutcomeNoop, fmt.Errorf("sso: JIT provision: organization_id and user_id required")
	}

	// Try to insert; if a membership already exists with this role, the call
	// is idempotent and we no-op. If it exists with a different role, update.
	m := model.Membership{
		ID:             uuid.New().String(),
		UserID:         userID,
		OrganizationID: organizationID,
		Role:           role,
		ProvisionedVia: model.ProvisionedViaJIT,
	}
	err := store.SaveMembership(ctx, m)
	switch {
	case err == nil:
		return JITOutcomeCreated, nil
	case errors.Is(err, storage.ErrMembershipExists):
		// Existing membership; reconcile role. Single O(1) lookup via the
		// admin-pool helper (no list walk, no race window between the
		// uniqueness conflict and a follow-up ListMemberships).
		existing, lookupErr := store.GetMembershipByOrgUser(ctx, organizationID, userID)
		if lookupErr != nil {
			return JITOutcomeNoop, fmt.Errorf("sso: JIT provision: lookup existing membership: %w", lookupErr)
		}
		// Sticky owner: JIT must NEVER demote an owner. Mappings can't even
		// resolve to owner (rolePriority excludes it), but an existing owner
		// membership stays untouched here too.
		if existing.Role == string(authz.RoleOwner) || existing.Role == role {
			return JITOutcomeNoop, nil
		}
		// Provenance guard: only reconcile roles on memberships JIT itself
		// placed. An admin-placed row (provisioned_via='manual' or
		// 'invitation') reflects an explicit role choice the customer
		// admin made — JIT-on-relogin must not silently overwrite it
		// even if the user's group claims now resolve to something
		// different. Closes the race between POST /v1/auth/invitations/redeem
		// (B1.5 cross-org flow) and the SSO callback's invite-redeem step:
		// the loser of the FOR-UPDATE on pending_memberships sees
		// (false, nil) from RedeemPendingInvitation, falls through here,
		// and previously would update the just-inserted invitation-role
		// membership to the SSO-resolved role.
		//
		// 'scim' rows are SCIM-managed (Phase E) and similarly off-limits
		// to JIT. 'legacy' rows are pre-B2 backfills with unrecoverable
		// provenance — defensive skip; better to leave a possibly-wrong
		// role than to overwrite something we can't reason about.
		if existing.ProvisionedVia != model.ProvisionedViaJIT {
			return JITOutcomeNoop, nil
		}
		if err := store.UpdateMembershipRole(ctx, existing.ID, role); err != nil {
			return JITOutcomeNoop, fmt.Errorf("sso: JIT provision: update role: %w", err)
		}
		return JITOutcomeUpdated, nil
	default:
		return JITOutcomeNoop, fmt.Errorf("sso: JIT provision: %w", err)
	}
}
