package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// LookupMembership is the hot-path helper used by the native auth provider
// — every authenticated request hits it. The integration test pins the
// SQL behaviour: empty inputs are no-ops, present rows return role+email
// in one call, missing rows return ("","") with no error.

func TestLookupMembership_ReturnsRoleAndEmail(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	u := seedUser(t, s, org.ID, "alice@example.com")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: u.ID, Role: "admin",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	role, email, err := s.LookupMembership(context.Background(), org.ID, u.ID)
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "admin" {
		t.Errorf("role = %q; want admin", role)
	}
	if email != "alice@example.com" {
		t.Errorf("email = %q; want alice@example.com", email)
	}
}

func TestLookupMembership_NoRowReturnsEmpty(t *testing.T) {
	s := newTestStore(t)
	_, org := newOrgCtx(t, s)

	role, email, err := s.LookupMembership(context.Background(), org.ID, "non-existent-user")
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "" || email != "" {
		t.Errorf("missing membership returned (%q, %q); want empty", role, email)
	}
}

func TestLookupMembership_EmptyInputsAreNoop(t *testing.T) {
	s := newTestStore(t)
	role, email, err := s.LookupMembership(context.Background(), "", "")
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "" || email != "" {
		t.Errorf("empty inputs returned (%q, %q); want empty", role, email)
	}
}

func TestLookupMembership_UserExistsWithoutMembership(t *testing.T) {
	// User row exists but membership is missing — could happen after
	// admin removes a member while their session is still cached. The
	// SELECT JOIN returns no rows; LookupMembership returns ("", "", nil).
	// Provider treats that as "no membership" and rejects.
	s := newTestStore(t)
	_, org := newOrgCtx(t, s)
	u := seedUser(t, s, org.ID, "orphan@example.com")
	// Deliberately skip SaveMembership — user exists, no membership.
	role, email, err := s.LookupMembership(context.Background(), org.ID, u.ID)
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "" || email != "" {
		t.Errorf("user without membership returned (%q, %q); want empty", role, email)
	}
}

// LookupUserByEmail backs POST /v1/auth/login. Verifies the global
// (RLS-bypassing) lookup behaviour: case-insensitive email match,
// memberships across orgs, and ErrUserNotFound on miss.

func TestLookupUserByEmail_HappyPath(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	u := seedUser(t, s, org.ID, "alice@example.com")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: u.ID, Role: "member",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	got, mships, err := s.LookupUserByEmail(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("LookupUserByEmail: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("user.ID = %q; want %q", got.ID, u.ID)
	}
	if len(mships) != 1 || mships[0].Role != "member" {
		t.Errorf("memberships = %+v; want one with role=member", mships)
	}
}

func TestLookupUserByEmail_CaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	_, org := newOrgCtx(t, s)
	seedUser(t, s, org.ID, "Mixed@Example.com")

	got, _, err := s.LookupUserByEmail(context.Background(), "mixed@example.com")
	if err != nil {
		t.Fatalf("LookupUserByEmail: %v", err)
	}
	if got.Email != "Mixed@Example.com" {
		t.Errorf("expected canonical-case email round-trip, got %q", got.Email)
	}
}

func TestLookupUserByEmail_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.LookupUserByEmail(context.Background(), "ghost@example.com")
	if !errors.Is(err, storage.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestLookupUserByEmail_EmptyInput(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.LookupUserByEmail(context.Background(), "")
	if !errors.Is(err, storage.ErrUserNotFound) {
		t.Fatalf("empty email should return ErrUserNotFound, got %v", err)
	}
}

// TestCreateBootstrapState_MultiReplicaRace simulates the architect-C5
// scenario: N replicas pointing at the same fresh DB simultaneously
// trying to mint the install token. The pg_advisory_xact_lock + ON
// CONFLICT DO NOTHING combination must guarantee exactly one winner.
//
// Without the advisory lock, the COUNT(*) FROM organizations check
// followed by INSERT INTO bootstrap_state could TOCTOU: two replicas
// each see zero orgs, both attempt the insert, ON CONFLICT eats the
// loser — not catastrophic but means two replicas log "I won" without
// actually winning. The advisory lock serialises the count + insert
// pair so the loser sees the row already exists and returns won=false.
func TestCreateBootstrapState_MultiReplicaRace(t *testing.T) {
	s := newTestStore(t)
	const replicas = 5
	type result struct {
		won bool
		err error
		pod string
	}
	results := make([]result, replicas)
	var wg sync.WaitGroup
	wg.Add(replicas)
	for i := 0; i < replicas; i++ {
		go func(idx int) {
			defer wg.Done()
			tokenHash := "fixture-hash-" + uuid.NewString()
			pod := "pod-" + uuid.NewString()
			won, err := s.CreateBootstrapState(context.Background(), tokenHash, pod)
			results[idx] = result{won: won, err: err, pod: pod}
		}(i)
	}
	wg.Wait()

	winners := 0
	for _, r := range results {
		if r.err != nil {
			t.Errorf("replica returned error: %v", r.err)
		}
		if r.won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("expected exactly 1 winner across %d replicas, got %d", replicas, winners)
	}

	// Singleton row exists with the winner's data.
	hash, pod, err := s.GetBootstrapState(context.Background())
	if err != nil {
		t.Fatalf("GetBootstrapState after race: %v", err)
	}
	if hash == "" || pod == "" {
		t.Errorf("singleton row missing fields; hash=%q pod=%q", hash, pod)
	}
}

// TestCreateBootstrapState_RefusesWhenOrgExists asserts the second
// guard in CreateBootstrapState — once any organization exists the
// install is past bootstrap and a subsequent CreateBootstrapState
// returns ErrBootstrapAlreadyDone regardless of the row state.
func TestCreateBootstrapState_RefusesWhenOrgExists(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.UpsertOrganization(context.Background(), "test-existing-org", "Existing"); err != nil {
		t.Fatalf("UpsertOrganization: %v", err)
	}
	_, err := s.CreateBootstrapState(context.Background(), "fixture-hash", "test-pod")
	if !errors.Is(err, storage.ErrBootstrapAlreadyDone) {
		t.Fatalf("expected ErrBootstrapAlreadyDone, got %v", err)
	}
}

// TestPasswordResetsTable_NoRLS asserts architect-C1 for the
// password_resets table. RedeemPasswordReset must work with a bare
// context — same reasoning as sessions: the redeem flow has no auth
// context yet (the row itself supplies the org). RLS on this table
// would lock out every legitimate reset.
func TestPasswordResetsTable_NoRLS(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	user := seedUser(t, s, org.ID, "reset-norls@example.com")

	tokenHash := "no-rls-reset-fixture"
	if err := s.CreatePasswordReset(
		ctx,
		"reset-id-fixture", user.ID, org.ID,
		tokenHash, user.ID,
		time.Now().Add(time.Hour),
	); err != nil {
		t.Fatalf("CreatePasswordReset: %v", err)
	}

	// Bare context — no app.organization_id. Must succeed: the redeem
	// flow lives below the auth layer, the row carries its own org id.
	uid, oid, err := s.RedeemPasswordReset(context.Background(), tokenHash, "new-hash-fixture")
	if err != nil {
		t.Fatalf("RedeemPasswordReset without org context: %v (RLS regression?)", err)
	}
	if uid != user.ID {
		t.Errorf("got user_id %q; want %q", uid, user.ID)
	}
	if oid != org.ID {
		t.Errorf("got organization_id %q; want %q", oid, org.ID)
	}
}

// TestSessionsTable_NoRLS asserts the architect-C1 invariant: lookup
// by token_hash works WITHOUT setting app.organization_id on the
// connection. If a future migration accidentally enables RLS on the
// sessions table, this test fails — the lookup is what *establishes*
// the org context, so RLS would block the very query that resolves
// the user's organization.
func TestSessionsTable_NoRLS(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	user := seedUser(t, s, org.ID, "norls@example.com")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: user.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	sess, err := s.CreateSession(context.Background(), model.Session{
		ID:               uuid.NewString(),
		UserID:           user.ID,
		OrganizationID:   org.ID,
		AuthMode:         model.AuthModePassword,
		SessionTokenHash: "no-rls-fixture-hash",
		ExpiresAt:        time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Bare context — no app.organization_id, no WithOrganizationID.
	// Must succeed: lookup-precedes-org-context is the load-bearing
	// invariant from migration 021.
	got, err := s.GetSessionByTokenHash(context.Background(), sess.SessionTokenHash)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash without org context: %v (RLS regression?)", err)
	}
	if got.ID != sess.ID {
		t.Errorf("got session id %q; want %q", got.ID, sess.ID)
	}
}

// ── LookupInvitationByToken (B1.5 slice 6.5) ───────────────────────────────

// TestLookupInvitationByToken_HappyPath_NewUser pins the SQL contract for
// the peek when the invited email is brand new. ExistingUser is nil — the
// redeem handler will route through the new-user flow.
func TestLookupInvitationByToken_HappyPath_NewUser(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	inviter := seedUser(t, s, org.ID, "owner@example.com")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: inviter.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	tokenHash := "hash-newuser-fixture"
	if _, _, err := s.CreateNativeInvitation(ctx, model.PendingInvitation{
		ID:              uuid.NewString(),
		OrganizationID:  org.ID,
		Email:           "newcomer@example.com",
		Role:            "member",
		InvitedByUserID: inviter.ID,
		InvitedByEmail:  inviter.Email,
		ExpiresAt:       time.Now().Add(24 * time.Hour),
		InviteTokenHash: tokenHash,
	}); err != nil {
		t.Fatalf("CreateNativeInvitation: %v", err)
	}

	peek, err := s.LookupInvitationByToken(context.Background(), tokenHash)
	if err != nil {
		t.Fatalf("LookupInvitationByToken: %v", err)
	}
	if peek.Email != "newcomer@example.com" {
		t.Errorf("Email = %q; want newcomer@example.com", peek.Email)
	}
	if peek.OrganizationID != org.ID {
		t.Errorf("OrganizationID = %q; want %q", peek.OrganizationID, org.ID)
	}
	if peek.OrganizationName == "" {
		t.Error("OrganizationName empty; expected joined value")
	}
	if peek.Role != "member" {
		t.Errorf("Role = %q; want member", peek.Role)
	}
	if peek.ExistingUser != nil {
		t.Errorf("ExistingUser = %+v; want nil for fresh email", peek.ExistingUser)
	}
}

// TestLookupInvitationByToken_HappyPath_ExistingUser is the B1.5 critical
// path: an email that already maps to a user in another organisation
// returns ExistingUser populated. Confirms the lookup is GLOBAL across
// orgs (the previous per-org SQL would have missed this).
func TestLookupInvitationByToken_HappyPath_ExistingUser(t *testing.T) {
	s := newTestStore(t)
	// Seed the user in org A.
	ctxA, orgA := newOrgCtx(t, s)
	alice := seedUser(t, s, orgA.ID, "alice@example.com")
	if err := s.SaveMembership(ctxA, model.Membership{
		ID: uuid.NewString(), OrganizationID: orgA.ID, UserID: alice.ID, Role: "admin",
	}); err != nil {
		t.Fatalf("SaveMembership orgA: %v", err)
	}

	// Create org B and an invitation for the same email.
	ctxB, orgB := newOrgCtx(t, s)
	inviter := seedUser(t, s, orgB.ID, "owner-b@example.com")
	tokenHash := "hash-existinguser-fixture"
	if _, _, err := s.CreateNativeInvitation(ctxB, model.PendingInvitation{
		ID:              uuid.NewString(),
		OrganizationID:  orgB.ID,
		Email:           "alice@example.com",
		Role:            "viewer",
		InvitedByUserID: inviter.ID,
		InvitedByEmail:  inviter.Email,
		ExpiresAt:       time.Now().Add(24 * time.Hour),
		InviteTokenHash: tokenHash,
	}); err != nil {
		t.Fatalf("CreateNativeInvitation: %v", err)
	}

	peek, err := s.LookupInvitationByToken(context.Background(), tokenHash)
	if err != nil {
		t.Fatalf("LookupInvitationByToken: %v", err)
	}
	if peek.ExistingUser == nil {
		t.Fatal("ExistingUser is nil; the global email lookup missed alice (this is the B1.5 bug fix regression test)")
	}
	if peek.ExistingUser.ID != alice.ID {
		t.Errorf("ExistingUser.ID = %q; want %q", peek.ExistingUser.ID, alice.ID)
	}
	if peek.OrganizationID != orgB.ID {
		t.Errorf("OrganizationID = %q; want orgB %q", peek.OrganizationID, orgB.ID)
	}
}

// TestLookupInvitationByToken_ExpiredToken returns ErrInvitationNotFound.
func TestLookupInvitationByToken_ExpiredToken(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	inviter := seedUser(t, s, org.ID, "owner@example.com")
	tokenHash := "hash-expired-fixture"
	// Insert with an expires_at in the past via direct SQL (CreateNativeInvitation
	// would reject; we want the row present-but-expired).
	if _, _, err := s.CreateNativeInvitation(ctx, model.PendingInvitation{
		ID:              uuid.NewString(),
		OrganizationID:  org.ID,
		Email:           "expired@example.com",
		Role:            "member",
		InvitedByUserID: inviter.ID,
		InvitedByEmail:  inviter.Email,
		ExpiresAt:       time.Now().Add(time.Hour),
		InviteTokenHash: tokenHash,
	}); err != nil {
		t.Fatalf("CreateNativeInvitation: %v", err)
	}
	// Force-expire via a direct UPDATE.
	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(context.Background(),
		`UPDATE axiaops.pending_memberships SET expires_at = NOW() - INTERVAL '1 hour' WHERE invite_token_hash = $1`,
		tokenHash,
	); err != nil {
		t.Fatalf("force-expire: %v", err)
	}

	_, err := s.LookupInvitationByToken(context.Background(), tokenHash)
	if !errors.Is(err, storage.ErrInvitationNotFound) {
		t.Errorf("err = %v; want ErrInvitationNotFound for expired token", err)
	}
}

// TestLookupInvitationByToken_UnknownToken returns ErrInvitationNotFound.
func TestLookupInvitationByToken_UnknownToken(t *testing.T) {
	s := newTestStore(t)
	_, err := s.LookupInvitationByToken(context.Background(), "non-existent-token-hash")
	if !errors.Is(err, storage.ErrInvitationNotFound) {
		t.Errorf("err = %v; want ErrInvitationNotFound", err)
	}
}

// TestLookupInvitationByToken_EmptyHash short-circuits without hitting PG.
func TestLookupInvitationByToken_EmptyHash(t *testing.T) {
	s := newTestStore(t)
	_, err := s.LookupInvitationByToken(context.Background(), "")
	if !errors.Is(err, storage.ErrInvitationNotFound) {
		t.Errorf("err = %v; want ErrInvitationNotFound for empty hash", err)
	}
}
