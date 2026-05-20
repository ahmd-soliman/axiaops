package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// LookupMembership is the hot-path helper used by the native auth provider
// — every authenticated request hits it. The integration test pins the
// SQL behaviour: empty inputs are no-ops, present rows return role+email+name
// in one call, missing rows return ("","","") with no error.

func TestLookupMembership_ReturnsRoleEmailAndName(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	// Distinct email and name so a regression that accidentally swaps the
	// two columns in the SELECT projection — or returns email twice — is
	// caught by these assertions rather than passing on equal values.
	u := seedUserWithName(t, s, org.ID, "alice@example.com", "Alice Engineer")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: u.ID, Role: "admin",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	role, email, name, err := s.LookupMembership(context.Background(), org.ID, u.ID)
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "admin" {
		t.Errorf("role = %q; want admin", role)
	}
	if email != "alice@example.com" {
		t.Errorf("email = %q; want alice@example.com", email)
	}
	if name != "Alice Engineer" {
		t.Errorf("name = %q; want Alice Engineer", name)
	}
}

func TestLookupMembership_NoRowReturnsEmpty(t *testing.T) {
	s := newTestStore(t)
	_, org := newOrgCtx(t, s)

	role, email, name, err := s.LookupMembership(context.Background(), org.ID, "non-existent-user")
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "" || email != "" || name != "" {
		t.Errorf("missing membership returned (%q, %q, %q); want empty", role, email, name)
	}
}

func TestLookupMembership_EmptyInputsAreNoop(t *testing.T) {
	s := newTestStore(t)
	role, email, name, err := s.LookupMembership(context.Background(), "", "")
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "" || email != "" || name != "" {
		t.Errorf("empty inputs returned (%q, %q, %q); want empty", role, email, name)
	}
}

func TestLookupMembership_UserExistsWithoutMembership(t *testing.T) {
	// User row exists but membership is missing — could happen after
	// admin removes a member while their session is still cached. The
	// SELECT JOIN returns no rows; LookupMembership returns ("", "", "", nil).
	// Provider treats that as "no membership" and rejects.
	s := newTestStore(t)
	_, org := newOrgCtx(t, s)
	u := seedUser(t, s, org.ID, "orphan@example.com")
	// Deliberately skip SaveMembership — user exists, no membership.
	role, email, name, err := s.LookupMembership(context.Background(), org.ID, u.ID)
	if err != nil {
		t.Fatalf("LookupMembership: %v", err)
	}
	if role != "" || email != "" || name != "" {
		t.Errorf("user without membership returned (%q, %q, %q); want empty", role, email, name)
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

// UpdateUserName pins the CTE-based atomic-read-and-write that backs the
// PATCH /v1/users/me handler. The contract: on success the *prior* value
// is returned (so the caller can audit {old_name, new_name}); on a missing
// userID, ErrUserNotFound is returned distinctly from a generic DB error.

func TestUpdateUserName_ReturnsPriorAndUpdates(t *testing.T) {
	s := newTestStore(t)
	_, org := newOrgCtx(t, s)
	u := seedUserWithName(t, s, org.ID, "rename@example.com", "Initial Name")

	old, err := s.UpdateUserName(context.Background(), u.ID, "Updated Name")
	if err != nil {
		t.Fatalf("UpdateUserName: %v", err)
	}
	if old != "Initial Name" {
		t.Errorf("old = %q; want %q", old, "Initial Name")
	}

	// Round-trip the row to confirm the new value is persisted.
	got, err := s.GetUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Name != "Updated Name" {
		t.Errorf("persisted name = %q; want %q", got.Name, "Updated Name")
	}
}

func TestUpdateUserName_EmptyNewNameAllowed(t *testing.T) {
	// Empty-string newName is "unset" — the dashboard falls back to email
	// when name is empty. The store must not reject it (validation is the
	// handler's job).
	s := newTestStore(t)
	_, org := newOrgCtx(t, s)
	u := seedUserWithName(t, s, org.ID, "unset@example.com", "Will Be Unset")

	old, err := s.UpdateUserName(context.Background(), u.ID, "")
	if err != nil {
		t.Fatalf("UpdateUserName: %v", err)
	}
	if old != "Will Be Unset" {
		t.Errorf("old = %q; want %q", old, "Will Be Unset")
	}
	got, _ := s.GetUserByID(context.Background(), u.ID)
	if got.Name != "" {
		t.Errorf("persisted name = %q; want empty", got.Name)
	}
}

func TestUpdateUserName_MissingUserReturnsNotFound(t *testing.T) {
	// The CTE evaluates to zero rows when the id doesn't match — pgx.ErrNoRows
	// must be mapped to storage.ErrUserNotFound so the handler can return
	// 404 instead of 500.
	s := newTestStore(t)
	_, err := s.UpdateUserName(context.Background(), uuid.NewString(), "doesn't matter")
	if !errors.Is(err, storage.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestUpdateUserName_EmptyUserIDIsError(t *testing.T) {
	// Bare guard: empty userID is a programmer error, not a runtime case.
	// Should fail fast, not run an unbounded SQL.
	s := newTestStore(t)
	_, err := s.UpdateUserName(context.Background(), "", "anything")
	if err == nil {
		t.Fatal("expected error on empty userID; got nil")
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

// ── CreateSessionEnforcingCap (M-7 audit, 2026-05-09) ─────────────────────

// TestCreateSessionEnforcingCap_NoOpUnderCap covers the trivial branch:
// when the user has fewer than `perUserCap` live sessions, the new row is
// inserted, nothing is revoked, the returned hash slice is empty.
func TestCreateSessionEnforcingCap_NoOpUnderCap(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	user := seedUser(t, s, org.ID, "undercap@example.com")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: user.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	got, revoked, err := s.CreateSessionEnforcingCap(context.Background(), model.Session{
		ID:               uuid.NewString(),
		UserID:           user.ID,
		OrganizationID:   org.ID,
		AuthMode:         model.AuthModePassword,
		SessionTokenHash: "undercap-hash-1",
		ExpiresAt:        time.Now().Add(24 * time.Hour),
	}, 5)
	if err != nil {
		t.Fatalf("CreateSessionEnforcingCap: %v", err)
	}
	if got.ID == "" {
		t.Error("returned session has empty ID")
	}
	if len(revoked) != 0 {
		t.Errorf("revoked = %v; want empty under cap", revoked)
	}
}

// TestCreateSessionEnforcingCap_DisabledCapIsNoop covers `perUserCap = 0`:
// the cap is disabled, the user can stack arbitrarily many sessions.
func TestCreateSessionEnforcingCap_DisabledCapIsNoop(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	user := seedUser(t, s, org.ID, "nocap@example.com")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: user.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	for i := 0; i < 6; i++ {
		_, revoked, err := s.CreateSessionEnforcingCap(context.Background(), model.Session{
			ID:               uuid.NewString(),
			UserID:           user.ID,
			OrganizationID:   org.ID,
			AuthMode:         model.AuthModePassword,
			SessionTokenHash: fmt.Sprintf("nocap-hash-%d", i),
			ExpiresAt:        time.Now().Add(24 * time.Hour),
		}, 0)
		if err != nil {
			t.Fatalf("CreateSessionEnforcingCap #%d: %v", i, err)
		}
		if len(revoked) != 0 {
			t.Errorf("mint #%d revoked = %v; want empty (cap disabled)", i, revoked)
		}
	}
	count, err := s.CountSessionsForUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CountSessionsForUser: %v", err)
	}
	if count != 6 {
		t.Errorf("count = %d; want 6 (cap=0 disables enforcement)", count)
	}
}

// TestCreateSessionEnforcingCap_AtomicCommit_ExactlyCapVisible is the
// audit M-7 integration test: when the cap is hit and the insert + revoke
// both succeed in one transaction, the post-commit count is exactly the
// cap — never cap+1 transiently visible to any reader. We drive this by
// pre-seeding `cap` live sessions, then minting one more via the new
// transactional method, and asserting the count is `cap` and the new
// session is the survivor while the OLDEST pre-seed is the revoked row.
func TestCreateSessionEnforcingCap_AtomicCommit_ExactlyCapVisible(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	user := seedUser(t, s, org.ID, "atcap@example.com")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: user.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	const cap = 3
	// Seed exactly `cap` live sessions, each with a deliberately-different
	// created_at so the oldest-first ordering is unambiguous. We push
	// created_at backwards via direct SQL because CreateSession stamps
	// NOW() and three same-tick rows could tie on created_at.
	preHashes := []string{}
	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()
	now := time.Now().UTC()
	for i := 0; i < cap; i++ {
		id := uuid.NewString()
		hash := fmt.Sprintf("atcap-pre-hash-%d", i)
		sess, err := s.CreateSession(context.Background(), model.Session{
			ID:               id,
			UserID:           user.ID,
			OrganizationID:   org.ID,
			AuthMode:         model.AuthModePassword,
			SessionTokenHash: hash,
			ExpiresAt:        now.Add(24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("seed CreateSession #%d: %v", i, err)
		}
		// Push the created_at backwards by (cap - i) minutes so the
		// first-seeded row is the oldest, second is in the middle, etc.
		if _, err := conn.Exec(context.Background(),
			`UPDATE axiaops.sessions SET created_at = $1 WHERE id = $2`,
			now.Add(-time.Duration(cap-i)*time.Minute), sess.ID,
		); err != nil {
			t.Fatalf("backdate seed #%d: %v", i, err)
		}
		preHashes = append(preHashes, sess.SessionTokenHash)
	}

	// Mint one more — the cap-revoke path must kick in.
	newID := uuid.NewString()
	newHash := "atcap-new-hash"
	saved, revoked, err := s.CreateSessionEnforcingCap(context.Background(), model.Session{
		ID:               newID,
		UserID:           user.ID,
		OrganizationID:   org.ID,
		AuthMode:         model.AuthModePassword,
		SessionTokenHash: newHash,
		ExpiresAt:        now.Add(24 * time.Hour),
	}, cap)
	if err != nil {
		t.Fatalf("CreateSessionEnforcingCap over cap: %v", err)
	}
	if saved.ID != newID {
		t.Errorf("returned session ID = %q; want %q", saved.ID, newID)
	}
	if len(revoked) != 1 {
		t.Fatalf("revoked count = %d; want 1 (exactly the oldest peer)", len(revoked))
	}
	if revoked[0] != preHashes[0] {
		t.Errorf("revoked[0] = %q; want oldest peer hash %q", revoked[0], preHashes[0])
	}

	// Atomicity assertion: post-commit count is exactly `cap`. If the
	// previous best-effort shape had failed the revoke step (silent
	// log+continue), the count would be cap+1 here.
	count, err := s.CountSessionsForUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CountSessionsForUser: %v", err)
	}
	if count != cap {
		t.Errorf("post-commit count = %d; want exactly %d (no over-cap window)", count, cap)
	}

	// The newest session must be among the survivors.
	live, err := s.ListUserSessionTokenHashes(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ListUserSessionTokenHashes: %v", err)
	}
	survived := map[string]bool{}
	for _, h := range live {
		survived[h] = true
	}
	if !survived[newHash] {
		t.Error("brand-new session was revoked; the newest must always win the seat")
	}
	if survived[preHashes[0]] {
		t.Error("oldest pre-seed survived; it should be the one revoked")
	}
}

// TestCreateSessionEnforcingCap_RevokeColumnSet asserts the revoked-row's
// `revoked_at` column is stamped (not NULL). Defends against a regression
// where the UPDATE filter (`AND revoked_at IS NULL`) accidentally drops
// the row from the UPDATE result set but `revoked_at` stays NULL — the
// `axiaops_session_revocations_total` counter would over-count and the
// next ValidateSession of that token would still pass Live().
func TestCreateSessionEnforcingCap_RevokeColumnSet(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	user := seedUser(t, s, org.ID, "revokedat@example.com")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: user.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	// One peer + a backdate so we know which row gets evicted.
	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()
	peerHash := "revokedat-peer-hash"
	peer, err := s.CreateSession(context.Background(), model.Session{
		ID:               uuid.NewString(),
		UserID:           user.ID,
		OrganizationID:   org.ID,
		AuthMode:         model.AuthModePassword,
		SessionTokenHash: peerHash,
		ExpiresAt:        time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("seed CreateSession: %v", err)
	}
	if _, err := conn.Exec(context.Background(),
		`UPDATE axiaops.sessions SET created_at = NOW() - INTERVAL '1 hour' WHERE id = $1`,
		peer.ID,
	); err != nil {
		t.Fatalf("backdate peer: %v", err)
	}

	if _, _, err := s.CreateSessionEnforcingCap(context.Background(), model.Session{
		ID:               uuid.NewString(),
		UserID:           user.ID,
		OrganizationID:   org.ID,
		AuthMode:         model.AuthModePassword,
		SessionTokenHash: "revokedat-new-hash",
		ExpiresAt:        time.Now().Add(24 * time.Hour),
	}, 1); err != nil {
		t.Fatalf("CreateSessionEnforcingCap: %v", err)
	}

	// Read the peer row back — revoked_at must now be non-null.
	var revokedAt *time.Time
	if err := conn.QueryRow(context.Background(),
		`SELECT revoked_at FROM axiaops.sessions WHERE id = $1`, peer.ID,
	).Scan(&revokedAt); err != nil {
		t.Fatalf("SELECT revoked_at: %v", err)
	}
	if revokedAt == nil {
		t.Error("peer row revoked_at is NULL; UPDATE didn't stamp it (regression — Live() would still pass)")
	}
}

// TestCreateSessionEnforcingCap_ConcurrentMints_NeverOverCap pins the per-user
// advisory-lock fix for the concurrent-mint race. The scenario fires when the
// user is AT CAP-1 and two mints arrive in parallel:
//
//   - Each tx INSERTs its new row, then SELECT COUNT excludes its own id.
//   - Under READ COMMITTED neither tx sees the other's still-uncommitted row.
//   - Both compute liveOthers = cap-1, excess = cap-1 + 1 - cap = 0.
//   - Both COMMIT without revoking — leaving the user at cap+1 until the
//     5-minute sweep ticker.
//
// The pg_advisory_xact_lock keyed on user_id serialises mints for the same
// user so the second tx observes the first's committed row and computes
// excess = 1. Without the lock this test sees count = cap+1 on at least one
// scheduler ordering; with it, count is always exactly cap.
func TestCreateSessionEnforcingCap_ConcurrentMints_NeverOverCap(t *testing.T) {
	s := newTestStore(t)
	ctx, org := newOrgCtx(t, s)
	user := seedUser(t, s, org.ID, "concurrent-mint@example.com")
	if err := s.SaveMembership(ctx, model.Membership{
		ID: uuid.NewString(), OrganizationID: org.ID, UserID: user.ID, Role: "owner",
	}); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	const cap = 3
	// Pre-seed cap-1 live sessions. Each lock-free concurrent mint would
	// see excess=0 in this state and decline to revoke.
	conn := connectTestDB(t)
	defer func() { _ = conn.Close(context.Background()) }()
	now := time.Now().UTC()
	for i := 0; i < cap-1; i++ {
		sess, err := s.CreateSession(context.Background(), model.Session{
			ID:               uuid.NewString(),
			UserID:           user.ID,
			OrganizationID:   org.ID,
			AuthMode:         model.AuthModePassword,
			SessionTokenHash: fmt.Sprintf("concurrent-pre-hash-%d", i),
			ExpiresAt:        now.Add(24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("seed CreateSession #%d: %v", i, err)
		}
		// Backdate so oldest-first ordering is unambiguous if the lock
		// fix routes the second mint into the revoke branch.
		if _, err := conn.Exec(context.Background(),
			`UPDATE axiaops.sessions SET created_at = $1 WHERE id = $2`,
			now.Add(-time.Duration(cap-i)*time.Minute), sess.ID,
		); err != nil {
			t.Fatalf("backdate seed #%d: %v", i, err)
		}
	}

	const parallel = 2
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, parallel)
	revokedCounts := make([]int, parallel)
	for i := 0; i < parallel; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, revoked, err := s.CreateSessionEnforcingCap(context.Background(), model.Session{
				ID:               uuid.NewString(),
				UserID:           user.ID,
				OrganizationID:   org.ID,
				AuthMode:         model.AuthModePassword,
				SessionTokenHash: fmt.Sprintf("concurrent-new-hash-%d", i),
				ExpiresAt:        now.Add(24 * time.Hour),
			}, cap)
			errs[i] = err
			revokedCounts[i] = len(revoked)
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("parallel mint #%d: %v", i, err)
		}
	}

	count, err := s.CountSessionsForUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("CountSessionsForUser: %v", err)
	}
	if count != cap {
		t.Fatalf("post-parallel-mint count = %d; want exactly %d (advisory lock must serialise mints so the second one revokes the oldest peer)",
			count, cap)
	}
	// Sanity: across both mints, exactly one revoke happened. (cap-1
	// peers + 2 new inserts - 1 revoke = cap.)
	totalRevoked := revokedCounts[0] + revokedCounts[1]
	if totalRevoked != 1 {
		t.Errorf("total revoked across parallel mints = %d; want 1 (one mint revokes one oldest peer; the other is a no-op)",
			totalRevoked)
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
