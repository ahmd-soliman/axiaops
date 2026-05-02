package sso_test

// permission_matrix_test.go — sticky-owner security pin (plan §5.2 / §5.5).
//
// AxiaOps treats `owner` as a role that originates ONLY from explicit human
// action: bootstrap (`POST /v1/auth/bootstrap`) or an ownership transfer
// (`POST /v1/organizations/transfer-ownership`). It must NEVER be assignable
// via SSO JIT — not via group claims, not via direct caller argument, not via
// a reconcile-path role overwrite. Every layer in the JIT pipeline guards
// this independently (defence-in-depth):
//
//   1. DB CHECK on `sso_group_mappings.role` rejects 'owner' at write time.
//      Covered by the migration test in services/shared/storage/postgres.
//   2. `rolePriority` map omits owner — `JITResolveRole` cannot return it
//      even if a malformed mapping survives layer 1.
//   3. `JITProvisionMembership` returns `ErrJITOwnerForbidden` for any
//      caller that bypasses the resolver and asks for owner directly.
//   4. `JITProvisionMembership` reconcile branch noops when the *existing*
//      membership is already owner — JIT must never demote an owner either,
//      regardless of what the resolver currently produces.
//
// This file pins layers 2–4 from the test side. If you are adding a new
// role-assignment path (a new admin endpoint, a SCIM webhook, anything that
// writes to `memberships.role`), add the equivalent assertion here. The
// whole point of one file named `permission_matrix_test.go` is so a
// future contributor finds it before they ship a path that bypasses the
// guards.

import (
	"context"
	"testing"

	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// trackingJITStore counts SaveMembership / UpdateMembershipRole calls so the
// owner-rejection cases can also assert that NO write happened. The
// existing jit_test.go owner-reject test passes a nil store and relies on
// the implicit nil-deref crash to prove no call was made; this version is
// explicit and survives any future refactor that returns early without a
// nil panic.
type trackingJITStore struct {
	saveCalled   bool
	saveErr      error
	getResult    model.Membership
	getErr       error
	updateCalled bool
	updatedRole  string
}

func (s *trackingJITStore) SaveMembership(_ context.Context, _ model.Membership) error {
	s.saveCalled = true
	return s.saveErr
}
func (s *trackingJITStore) GetMembershipByOrgUser(_ context.Context, _, _ string) (model.Membership, error) {
	return s.getResult, s.getErr
}
func (s *trackingJITStore) UpdateMembershipRole(_ context.Context, _, role string) error {
	s.updateCalled = true
	s.updatedRole = role
	return nil
}

// TestPermissionMatrix_OwnerNeverFromJIT enumerates every layer in the JIT
// pipeline where an owner role could leak in and proves each guard holds.
func TestPermissionMatrix_OwnerNeverFromJIT(t *testing.T) {
	t.Run("layer 2: resolver ignores owner mapping (DB CHECK is not the only line of defence)", func(t *testing.T) {
		mappings := []model.SSOGroupMapping{
			{GroupExternalID: "g-malformed", Role: "owner"},
		}
		got := sso.JITResolveRole(mappings, []string{"g-malformed"}, "viewer")
		if got == "owner" {
			t.Fatalf("JITResolveRole returned %q for an owner mapping; resolver layer leaked owner", got)
		}

		// Mixed input: an owner mapping AND a real admin mapping. Admin must
		// win; owner must not influence the priority race.
		mappings = append(mappings, model.SSOGroupMapping{GroupExternalID: "g-eng", Role: "admin"})
		got = sso.JITResolveRole(mappings, []string{"g-malformed", "g-eng"}, "viewer")
		if got != "admin" {
			t.Errorf("mixed owner+admin mappings: got %q want admin", got)
		}
	})

	t.Run("layer 3: provision rejects role=owner with ErrJITOwnerForbidden and writes nothing", func(t *testing.T) {
		store := &trackingJITStore{}
		outcome, err := sso.JITProvisionMembership(t.Context(), store, "org-1", "user-1", "owner")
		if err != sso.ErrJITOwnerForbidden {
			t.Fatalf("error = %v; want ErrJITOwnerForbidden", err)
		}
		if outcome != sso.JITOutcomeNoop {
			t.Errorf("outcome = %v; want JITOutcomeNoop", outcome)
		}
		if store.saveCalled {
			t.Error("SaveMembership called despite owner reject — guard wrote a row")
		}
		if store.updateCalled {
			t.Error("UpdateMembershipRole called despite owner reject — guard mutated a row")
		}
	})

	t.Run("layer 4: existing owner membership is sticky on reconcile (no demote)", func(t *testing.T) {
		// SSO resolves to admin (a non-owner role the resolver can produce).
		// The membership row already exists at owner. Reconcile must noop —
		// owner stickiness is independent of the provisioned_via value, so
		// even provisioned_via='jit' (the only provenance reconcile would
		// otherwise touch) must not flip owner.
		store := &trackingJITStore{
			saveErr: storage.ErrMembershipExists,
			getResult: model.Membership{
				ID:             "m-1",
				UserID:         "user-1",
				OrganizationID: "org-1",
				Role:           "owner",
				ProvisionedVia: model.ProvisionedViaJIT,
			},
		}
		outcome, err := sso.JITProvisionMembership(t.Context(), store, "org-1", "user-1", "admin")
		if err != nil {
			t.Fatalf("JITProvisionMembership: %v", err)
		}
		if outcome != sso.JITOutcomeNoop {
			t.Errorf("outcome = %v; want JITOutcomeNoop (owner is sticky)", outcome)
		}
		if store.updateCalled {
			t.Errorf("UpdateMembershipRole called with role=%q on an existing-owner row — JIT demoted an owner", store.updatedRole)
		}
	})
}
