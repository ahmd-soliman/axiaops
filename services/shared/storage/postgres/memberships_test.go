package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
	"axiaops.io/shared/storage/postgres"
)

// Membership Store tests. These exercise the higher-level methods that the
// authz middleware and membership handlers depend on. RLS is asserted by
// running every test against the app pool (see DATABASE_URL guard).

func seedUser(t *testing.T, s *postgres.Store, organizationID, email string) model.User {
	t.Helper()
	u, err := s.UpsertUser(context.Background(), organizationID, "kinde-"+uuid.NewString(), email, email)
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	return u
}

func TestRoleOf_NoMembershipReturnsEmpty(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	u := seedUser(t, s, org.ID, "noone@x.com")

	role, err := s.RoleOf(ctx, org.ID, u.ID)
	if err != nil {
		t.Fatalf("RoleOf: %v", err)
	}
	if role != "" {
		t.Fatalf("expected empty role, got %q", role)
	}
}

func TestSaveMembership_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	u := seedUser(t, s, org.ID, "alice@x.com")

	if err := s.SaveMembership(ctx, model.Membership{
		OrganizationID: org.ID,
		UserID:         u.ID,
		Role:           "admin",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	role, err := s.RoleOf(ctx, org.ID, u.ID)
	if err != nil {
		t.Fatalf("RoleOf: %v", err)
	}
	if role != "admin" {
		t.Fatalf("expected admin, got %q", role)
	}
}

func TestSaveMembership_DuplicateReturnsExists(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	u := seedUser(t, s, org.ID, "dup@x.com")

	if err := s.SaveMembership(ctx, model.Membership{
		OrganizationID: org.ID, UserID: u.ID, Role: "viewer",
	}); err != nil {
		t.Fatalf("first SaveMembership: %v", err)
	}
	err := s.SaveMembership(ctx, model.Membership{
		OrganizationID: org.ID, UserID: u.ID, Role: "admin",
	})
	if !errors.Is(err, storage.ErrMembershipExists) {
		t.Fatalf("expected ErrMembershipExists, got %v", err)
	}
}

func TestUpdateMembershipRole_LastOwnerGuard(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	owner := seedUser(t, s, org.ID, "owner@x.com")

	id := uuid.NewString()
	if err := s.SaveMembership(ctx, model.Membership{
		ID: id, OrganizationID: org.ID, UserID: owner.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	err := s.UpdateMembershipRole(ctx, id, "admin")
	if !errors.Is(err, storage.ErrLastOwner) {
		t.Fatalf("expected ErrLastOwner, got %v", err)
	}
}

func TestUpdateMembershipRole_AllowsRoleChangeBetweenNonOwners(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	user := seedUser(t, s, org.ID, "viewer@x.com")

	id := uuid.NewString()
	if err := s.SaveMembership(ctx, model.Membership{
		ID: id, OrganizationID: org.ID, UserID: user.ID, Role: "viewer",
	}); err != nil {
		t.Fatalf("seed viewer: %v", err)
	}

	if err := s.UpdateMembershipRole(ctx, id, "member"); err != nil {
		t.Fatalf("UpdateMembershipRole: %v", err)
	}

	role, err := s.RoleOf(ctx, org.ID, user.ID)
	if err != nil {
		t.Fatalf("RoleOf: %v", err)
	}
	if role != "member" {
		t.Fatalf("expected member, got %q", role)
	}
}

func TestDeleteMembership_LastOwnerGuard(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	owner := seedUser(t, s, org.ID, "lastowner@x.com")

	id := uuid.NewString()
	if err := s.SaveMembership(ctx, model.Membership{
		ID: id, OrganizationID: org.ID, UserID: owner.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	err := s.DeleteMembership(ctx, id)
	if !errors.Is(err, storage.ErrLastOwner) {
		t.Fatalf("expected ErrLastOwner, got %v", err)
	}
}

func TestTransferOwnership_Atomic(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	owner := seedUser(t, s, org.ID, "from@x.com")
	target := seedUser(t, s, org.ID, "to@x.com")

	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: owner.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: target.ID, Role: "admin",
	}); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	if err := s.TransferOwnership(ctx, target.ID); err != nil {
		t.Fatalf("TransferOwnership: %v", err)
	}

	ownerRole, err := s.RoleOf(ctx, org.ID, owner.ID)
	if err != nil {
		t.Fatalf("RoleOf old owner: %v", err)
	}
	if ownerRole != "admin" {
		t.Fatalf("expected old owner -> admin, got %q", ownerRole)
	}
	targetRole, err := s.RoleOf(ctx, org.ID, target.ID)
	if err != nil {
		t.Fatalf("RoleOf new owner: %v", err)
	}
	if targetRole != "owner" {
		t.Fatalf("expected new owner -> owner, got %q", targetRole)
	}
}

func TestTransferOwnership_TargetNotInOrganization(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

	err := s.TransferOwnership(ctx, "u-does-not-exist")
	if !errors.Is(err, storage.ErrMembershipNotFound) {
		t.Fatalf("expected ErrMembershipNotFound, got %v", err)
	}
}

func TestEnsureFirstMembership_OnlyFirstWins(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	first := seedUser(t, s, org.ID, "first@x.com")
	second := seedUser(t, s, org.ID, "second@x.com")

	ok, err := s.EnsureFirstMembership(ctx, org.ID, first.ID)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !ok {
		t.Fatalf("expected first call to insert")
	}

	ok, err = s.EnsureFirstMembership(ctx, org.ID, second.ID)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if ok {
		t.Fatalf("expected second call to be a no-op")
	}

	role, err := s.RoleOf(ctx, org.ID, first.ID)
	if err != nil {
		t.Fatalf("RoleOf: %v", err)
	}
	if role != "owner" {
		t.Fatalf("first user should be owner, got %q", role)
	}

	role, err = s.RoleOf(ctx, org.ID, second.ID)
	if err != nil {
		t.Fatalf("RoleOf second: %v", err)
	}
	if role != "" {
		t.Fatalf("second user should have no membership, got %q", role)
	}
}

func TestRoleOf_OrganizationIsolation(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("requires DATABASE_URL (app user) for RLS")
	}
	s := newTestStore(t)
	ctxA, orgA := newOrgCtx(t, s)
	ctxB, orgB := newOrgCtx(t, s)

	userA := seedUser(t, s, orgA.ID, "a@x.com")
	userB := seedUser(t, s, orgB.ID, "b@x.com")

	if err := s.SaveMembership(ctxA, model.Membership{
		OrganizationID: orgA.ID, UserID: userA.ID, Role: "admin",
	}); err != nil {
		t.Fatalf("save A: %v", err)
	}
	if err := s.SaveMembership(ctxB, model.Membership{
		OrganizationID: orgB.ID, UserID: userB.ID, Role: "viewer",
	}); err != nil {
		t.Fatalf("save B: %v", err)
	}

	// Looking up A's user from B's organization must return empty (RLS).
	role, err := s.RoleOf(ctxB, orgB.ID, userA.ID)
	if err != nil {
		t.Fatalf("RoleOf cross-organization: %v", err)
	}
	if role != "" {
		t.Fatalf("expected empty role for cross-organization lookup, got %q", role)
	}
}

func TestListMemberships_JoinsUserEmail(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	u := seedUser(t, s, org.ID, "list@x.com")

	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: u.ID, Role: "member",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	rows, err := s.ListMemberships(ctx)
	if err != nil {
		t.Fatalf("ListMemberships: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Email != "list@x.com" {
		t.Fatalf("expected email join, got %q", rows[0].Email)
	}
}

func TestGetUserByEmail_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx, _ := newOrgCtx(t, s)

	_, err := s.GetUserByEmail(ctx, "missing@x.com")
	if !errors.Is(err, storage.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestGetUserByEmail_CaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	seedUser(t, s, org.ID, "Mixed@Example.com")

	u, err := s.GetUserByEmail(ctx, "mixed@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if u.Email != "Mixed@Example.com" {
		t.Fatalf("got %q", u.Email)
	}
}

// Bootstrap-path regression: EnsureDevMembership and EnsureFirstMembership
// must work when the Store is opened via postgres.New (no separate owner URL),
// i.e. adminPool == pool and the app role's RLS is enforced. The original
// implementation used adminPool without a transaction, so it silently relied
// on the test harness setting both DATABASE_URL and MIGRATION_DATABASE_URL.
// In dev mode the API runs with only DATABASE_URL set and the INSERT was
// rejected by the WITH CHECK clause.

func TestEnsureDevMembership_WithoutOwnerPool(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("requires DATABASE_URL (app user) for RLS")
	}

	// Seed the organization + user via the raw owner connection so the test
	// doesn't depend on a Store that has the owner pool.
	conn := setup(t)
	organizationID, userAID, _ := newOrganizationWithUsers(t, conn)

	// Open a Store WITHOUT a separate owner pool — adminPool falls back to pool.
	s, err := postgres.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.EnsureDevMembership(context.Background(), organizationID, userAID, "owner"); err != nil {
		t.Fatalf("EnsureDevMembership without owner pool: %v", err)
	}

	role, err := s.RoleOf(context.Background(), organizationID, userAID)
	if err != nil {
		t.Fatalf("RoleOf: %v", err)
	}
	if role != "owner" {
		t.Errorf("expected owner role, got %q", role)
	}
}

func TestEnsureFirstMembership_WithoutOwnerPool(t *testing.T) {
	if !rlsEnforced() {
		t.Skip("requires DATABASE_URL (app user) for RLS")
	}

	conn := setup(t)
	organizationID, userAID, _ := newOrganizationWithUsers(t, conn)

	s, err := postgres.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ok, err := s.EnsureFirstMembership(context.Background(), organizationID, userAID)
	if err != nil {
		t.Fatalf("EnsureFirstMembership without owner pool: %v", err)
	}
	if !ok {
		t.Fatalf("expected first call to insert, got ok=false")
	}

	role, err := s.RoleOf(context.Background(), organizationID, userAID)
	if err != nil {
		t.Fatalf("RoleOf: %v", err)
	}
	if role != "owner" {
		t.Errorf("expected owner role, got %q", role)
	}
}
