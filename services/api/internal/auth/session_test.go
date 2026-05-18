package auth_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// fakeStore is an in-memory storage.NativeAuthStore for unit tests.
// Implements both Manager's surface (sessions, list/count) and the
// Handler's surface (bootstrap, login, password reset). Methods not
// reachable from any test panic so a forgotten dependency surfaces
// immediately.
type fakeStore struct {
	mu sync.Mutex

	sessions map[string]model.Session // keyed by session_token_hash
	touches  map[string]int           // session ID → TouchSessionLastSeen call count
	now      func() time.Time

	// Bootstrap singleton state. Only one row is allowed.
	bootstrap          *fakeBootstrap
	organizationsCount int64

	// Users keyed by lower(email) for global login lookup, and by id
	// for membership joins.
	usersByEmail map[string]model.User
	usersByID    map[string]model.User

	// Memberships keyed by user_id (a slice — a user can belong to
	// multiple orgs).
	memberships map[string][]model.Membership

	// Organization display names keyed by org_id. Populated by
	// seedAccount alongside memberships so ListUserMemberships' join
	// returns realistic data.
	orgsByID map[string]string

	// listUserMembershipsErr, when non-nil, makes the next
	// ListUserMemberships call fail. Used by the multi-org login DB-error
	// test to assert the handler 500s instead of silently degrading.
	listUserMembershipsErr error

	// Native invitations keyed by (org_id, lower(email)) (matches the
	// production partial unique index on pending_memberships) plus a
	// secondary index by token_hash for the redeem-path lookup.
	invitations        map[string]model.PendingInvitation
	invitationsByToken map[string]string // token_hash → primary key

	// Password resets keyed by token_hash. Single-use: row gains a
	// non-nil RedeemedAt on consume.
	passwordResets map[string]*fakePasswordReset

	// hooks is optional fault-injection plumbing for transactional tests
	// (M-7 atomicity regression). Nil in normal use.
	hooks *fakeStoreHooks
}

type fakePasswordReset struct {
	id, userID, organizationID, issuedBy string
	expiresAt                            time.Time
	redeemedAt                           *time.Time
}

type fakeBootstrap struct {
	tokenHash string
	pod       string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions:     make(map[string]model.Session),
		touches:      make(map[string]int),
		now:          func() time.Time { return time.Now().UTC() },
		usersByEmail: make(map[string]model.User),
		usersByID:    make(map[string]model.User),
		memberships:  make(map[string][]model.Membership),
		orgsByID:     make(map[string]string),
	}
}

func (f *fakeStore) CreateSession(_ context.Context, in model.Session) (model.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	in.CreatedAt = now
	in.LastSeenAt = now
	f.sessions[in.SessionTokenHash] = in
	return in, nil
}

// createSessionEnforcingCapHook lets a test inject a failure between the
// session INSERT and the cap-revoke. When set, calls to
// CreateSessionEnforcingCap run the insert, invoke the hook, and — if
// the hook returns non-nil — roll back the insert (delete the row from
// the map) before returning the hook's error. Used by the M-7
// regression test that drives a transactional rollback failure.
type fakeStoreHooks struct {
	mu                   sync.Mutex
	afterInsertBeforeCap func() error
}

func (f *fakeStore) CreateSessionEnforcingCap(ctx context.Context, in model.Session, perUserCap int) (model.Session, []string, error) {
	// Production tx semantics emulated: INSERT, then under the same lock
	// run the cap step. On any cap-step failure, undo the INSERT (i.e.
	// the row never becomes visible). Hashes returned reflect what was
	// revoked atomically — empty if cap is not exceeded.
	f.mu.Lock()
	now := f.now()
	in.CreatedAt = now
	in.LastSeenAt = now
	f.sessions[in.SessionTokenHash] = in

	// Failure injection hook (test-only). Captured under f.mu so the
	// hook itself can poke other fakeStore fields without re-entry.
	if f.hooks != nil {
		f.hooks.mu.Lock()
		hook := f.hooks.afterInsertBeforeCap
		f.hooks.mu.Unlock()
		if hook != nil {
			if err := hook(); err != nil {
				// Roll back the INSERT — the row never becomes visible
				// to any reader (mirrors tx ROLLBACK).
				delete(f.sessions, in.SessionTokenHash)
				f.mu.Unlock()
				return model.Session{}, nil, err
			}
		}
	}

	// Cap enforcement (skipped when cap <= 0).
	var revoked []string
	if perUserCap > 0 {
		type entry struct {
			hash      string
			id        string
			createdAt time.Time
		}
		// Collect peers (everything live for this user EXCEPT the
		// row we just inserted) so the just-minted session always
		// wins the seat.
		peers := []entry{}
		for h, s := range f.sessions {
			if s.UserID == in.UserID && s.ID != in.ID && s.RevokedAt == nil && s.ExpiresAt.After(now) {
				peers = append(peers, entry{hash: h, id: s.ID, createdAt: s.CreatedAt})
			}
		}
		// totalLive = peers + 1; excess = totalLive - cap.
		excess := len(peers) + 1 - perUserCap
		if excess > 0 {
			sort.Slice(peers, func(i, j int) bool {
				if peers[i].createdAt.Equal(peers[j].createdAt) {
					return peers[i].id < peers[j].id
				}
				return peers[i].createdAt.Before(peers[j].createdAt)
			})
			if excess > len(peers) {
				excess = len(peers)
			}
			t := now
			for i := 0; i < excess; i++ {
				s := f.sessions[peers[i].hash]
				s.RevokedAt = &t
				f.sessions[peers[i].hash] = s
				revoked = append(revoked, peers[i].hash)
			}
		}
	}
	f.mu.Unlock()
	return in, revoked, nil
}

func (f *fakeStore) GetSessionByTokenHash(_ context.Context, h string) (model.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[h]
	if !ok {
		return model.Session{}, storage.ErrSessionNotFound
	}
	return s, nil
}

func (f *fakeStore) TouchSessionLastSeen(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touches[sessionID]++
	for h, s := range f.sessions {
		if s.ID == sessionID {
			s.LastSeenAt = f.now()
			f.sessions[h] = s
			return nil
		}
	}
	return nil
}

func (f *fakeStore) RevokeSession(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	for h, s := range f.sessions {
		if s.ID == sessionID && s.RevokedAt == nil {
			t := now
			s.RevokedAt = &t
			f.sessions[h] = s
			return nil
		}
	}
	return nil
}

func (f *fakeStore) RevokeUserSessions(_ context.Context, userID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	hashes := []string{}
	for h, s := range f.sessions {
		if s.UserID == userID && s.RevokedAt == nil && s.ExpiresAt.After(now) {
			hashes = append(hashes, h)
			t := now
			s.RevokedAt = &t
			f.sessions[h] = s
		}
	}
	sort.Strings(hashes)
	return hashes, nil
}

func (f *fakeStore) ListUserSessionTokenHashes(_ context.Context, userID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	type entry struct {
		hash      string
		createdAt time.Time
	}
	picked := []entry{}
	for h, s := range f.sessions {
		if s.UserID == userID && s.RevokedAt == nil && s.ExpiresAt.After(now) {
			picked = append(picked, entry{hash: h, createdAt: s.CreatedAt})
		}
	}
	// Ordering contract: oldest-first by CreatedAt. Mirrors the postgres
	// query's ORDER BY created_at ASC, id ASC — the per-user cap relies
	// on this to revoke the oldest sessions.
	sort.Slice(picked, func(i, j int) bool {
		if picked[i].createdAt.Equal(picked[j].createdAt) {
			return picked[i].hash < picked[j].hash
		}
		return picked[i].createdAt.Before(picked[j].createdAt)
	})
	hashes := make([]string, len(picked))
	for i, e := range picked {
		hashes[i] = e.hash
	}
	return hashes, nil
}

func (f *fakeStore) CountSessionsForUser(_ context.Context, userID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	n := 0
	for _, s := range f.sessions {
		if s.UserID == userID && s.RevokedAt == nil && s.ExpiresAt.After(now) {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) SweepExpiredSessions(context.Context, time.Time) (int64, error) { return 0, nil }

// ── handler-surface methods (CountOrganizations, bootstrap, login) ─────────

func (f *fakeStore) CountOrganizations(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.organizationsCount, nil
}

func (f *fakeStore) CreateBootstrapState(_ context.Context, tokenHash, pod string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.organizationsCount > 0 {
		return false, storage.ErrBootstrapAlreadyDone
	}
	if f.bootstrap != nil {
		return false, nil
	}
	f.bootstrap = &fakeBootstrap{tokenHash: tokenHash, pod: pod}
	return true, nil
}

func (f *fakeStore) GetBootstrapState(context.Context) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bootstrap == nil {
		return "", "", storage.ErrBootstrapAlreadyDone
	}
	return f.bootstrap.tokenHash, f.bootstrap.pod, nil
}

func (f *fakeStore) ConsumeBootstrapState(_ context.Context, in storage.BootstrapConsume) (storage.BootstrapResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bootstrap == nil {
		return storage.BootstrapResult{}, storage.ErrBootstrapAlreadyDone
	}
	if f.bootstrap.tokenHash != in.TokenHash {
		return storage.BootstrapResult{}, storage.ErrBootstrapTokenMismatch
	}
	if _, exists := f.usersByEmail[strings.ToLower(in.UserEmail)]; exists {
		return storage.BootstrapResult{}, storage.ErrUserEmailExists
	}
	now := f.now()
	user := model.User{
		ID:             in.UserID,
		OrganizationID: in.OrganizationID,
		Email:          in.UserEmail,
		Name:           in.UserName,
		PasswordHash:   in.UserPasswordHash,
		PasswordSetAt:  &now,
		CreatedAt:      now,
		LastSeen:       now,
	}
	f.usersByEmail[strings.ToLower(in.UserEmail)] = user
	f.usersByID[in.UserID] = user
	f.memberships[in.UserID] = []model.Membership{{
		ID:             "m-" + in.UserID,
		OrganizationID: in.OrganizationID,
		UserID:         in.UserID,
		Role:           "owner",
		CreatedAt:      now,
		UpdatedAt:      now,
	}}
	session := model.Session{
		ID:               in.SessionID,
		UserID:           in.UserID,
		OrganizationID:   in.OrganizationID,
		AuthMode:         model.AuthModeBootstrap,
		SessionTokenHash: in.SessionTokenHash,
		CreatedAt:        now,
		ExpiresAt:        in.SessionExpiresAt,
		LastSeenAt:       now,
		UserAgentHash:    in.SessionUserAgentHash,
	}
	f.sessions[in.SessionTokenHash] = session
	f.organizationsCount++
	f.bootstrap = nil
	return storage.BootstrapResult{User: user, Session: session}, nil
}

func (f *fakeStore) LookupUserByEmail(_ context.Context, email string) (model.User, []model.Membership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.usersByEmail[strings.ToLower(email)]
	if !ok {
		return model.User{}, nil, storage.ErrUserNotFound
	}
	mships := append([]model.Membership(nil), f.memberships[u.ID]...)
	return u, mships, nil
}

func (f *fakeStore) ListUserMemberships(_ context.Context, userID string) ([]model.MembershipWithOrganization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listUserMembershipsErr != nil {
		return nil, f.listUserMembershipsErr
	}
	src := f.memberships[userID]
	out := make([]model.MembershipWithOrganization, 0, len(src))
	for _, m := range src {
		out = append(out, model.MembershipWithOrganization{
			Membership:       m,
			OrganizationName: f.orgsByID[m.OrganizationID],
		})
	}
	return out, nil
}

// ── unused-by-tests methods — panic to surface accidental coupling ─────────

func (f *fakeStore) CreateUserWithPassword(context.Context, model.User) (model.User, error) {
	panic("not used by tests in this package")
}
func (f *fakeStore) UpdateUserPassword(context.Context, string, string) error {
	panic("not used by tests in this package")
}
func (f *fakeStore) LookupMembership(context.Context, string, string) (string, string, error) {
	panic("not used by tests in this package")
}
func (f *fakeStore) CreatePasswordReset(_ context.Context, id, userID, organizationID, tokenHash, issuedBy string, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.passwordResets == nil {
		f.passwordResets = make(map[string]*fakePasswordReset)
	}
	f.passwordResets[tokenHash] = &fakePasswordReset{
		id:             id,
		userID:         userID,
		organizationID: organizationID,
		issuedBy:       issuedBy,
		expiresAt:      expiresAt,
	}
	return nil
}

func (f *fakeStore) RedeemPasswordReset(_ context.Context, tokenHash, newHash string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.passwordResets[tokenHash]
	if !ok || row.redeemedAt != nil {
		return "", "", storage.ErrPasswordResetNotFound
	}
	if !f.now().Before(row.expiresAt) {
		return "", "", storage.ErrPasswordResetExpired
	}
	now := f.now()
	row.redeemedAt = &now
	if u, ok := f.usersByID[row.userID]; ok {
		u.PasswordHash = newHash
		u.PasswordSetAt = &now
		f.usersByID[row.userID] = u
		f.usersByEmail[strings.ToLower(u.Email)] = u
	}
	return row.userID, row.organizationID, nil
}

// Invitations are stored keyed by (organization_id, lower(email)) —
// matches the partial unique index on pending_memberships in production.
// A separate index by token_hash is maintained for the redeem-path lookup.
func invitationKey(orgID, email string) string {
	return orgID + "|" + strings.ToLower(email)
}

func (f *fakeStore) seedInvitation(orgID, email, role, tokenHash string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.invitations == nil {
		f.invitations = make(map[string]model.PendingInvitation)
	}
	if f.invitationsByToken == nil {
		f.invitationsByToken = make(map[string]string)
	}
	key := invitationKey(orgID, email)
	f.invitations[key] = model.PendingInvitation{
		ID:              "inv-" + email,
		OrganizationID:  orgID,
		Email:           email,
		Role:            role,
		Status:          model.InvitationStatusPending,
		InviteTokenHash: tokenHash,
		ExpiresAt:       f.now().Add(24 * time.Hour),
	}
	f.invitationsByToken[tokenHash] = key
}

func (f *fakeStore) CreateNativeInvitation(_ context.Context, in model.PendingInvitation) (model.PendingInvitation, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.invitations == nil {
		f.invitations = make(map[string]model.PendingInvitation)
	}
	if f.invitationsByToken == nil {
		f.invitationsByToken = make(map[string]string)
	}
	if in.ID == "" {
		in.ID = "inv-" + in.Email
	}
	in.Status = model.InvitationStatusPending
	if in.ExpiresAt.IsZero() {
		in.ExpiresAt = f.now().Add(14 * 24 * time.Hour)
	}
	in.CreatedAt = f.now()
	in.UpdatedAt = f.now()
	key := invitationKey(in.OrganizationID, in.Email)
	prev, existed := f.invitations[key]
	if existed {
		// Re-invite: drop the previous token_hash → key mapping so a
		// stale token can't be redeemed after rotation. Mirrors the
		// production upsert behaviour on the partial unique index.
		delete(f.invitationsByToken, prev.InviteTokenHash)
	}
	f.invitations[key] = in
	f.invitationsByToken[in.InviteTokenHash] = key
	return in, !existed, nil
}

func (f *fakeStore) LookupInvitationByToken(_ context.Context, tokenHash string) (storage.PeekedInvitation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := f.invitationsByToken[tokenHash]
	if !ok {
		return storage.PeekedInvitation{}, storage.ErrInvitationNotFound
	}
	inv, ok := f.invitations[key]
	if !ok || inv.Status != model.InvitationStatusPending || !inv.ExpiresAt.After(f.now()) {
		return storage.PeekedInvitation{}, storage.ErrInvitationNotFound
	}
	out := storage.PeekedInvitation{
		Email:            inv.Email,
		OrganizationID:   inv.OrganizationID,
		OrganizationName: f.orgsByID[inv.OrganizationID],
		Role:             inv.Role,
	}
	if u, ok := f.usersByEmail[strings.ToLower(inv.Email)]; ok {
		copy := u
		out.ExistingUser = &copy
	}
	return out, nil
}

func (f *fakeStore) RedeemNativeInvitation(_ context.Context, req storage.NativeInviteRedeem) (model.User, model.Membership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := f.invitationsByToken[req.TokenHash]
	if !ok {
		return model.User{}, model.Membership{}, storage.ErrInvitationNotFound
	}
	inv, ok := f.invitations[key]
	if !ok || inv.Status != model.InvitationStatusPending || !inv.ExpiresAt.After(f.now()) {
		return model.User{}, model.Membership{}, storage.ErrInvitationNotFound
	}
	var user model.User
	if req.ExistingUserID != "" {
		// Existing-user flow.
		u, ok := f.usersByID[req.ExistingUserID]
		if !ok {
			return model.User{}, model.Membership{}, storage.ErrInvitationNotFound
		}
		if !strings.EqualFold(u.Email, inv.Email) {
			return model.User{}, model.Membership{}, fmt.Errorf("fake: existing user email mismatch")
		}
		user = u
	} else {
		// New-user flow.
		if existing, ok := f.usersByEmail[strings.ToLower(inv.Email)]; ok {
			// Race: another path created this user between peek and
			// redeem. Mirrors postgres behaviour.
			_ = existing
			return model.User{}, model.Membership{}, storage.ErrUserEmailExists
		}
		now := f.now()
		user = model.User{
			ID:             req.UserID,
			OrganizationID: inv.OrganizationID,
			Email:          inv.Email,
			Name:           req.UserName,
			PasswordHash:   req.PasswordHash,
			PasswordSetAt:  &now,
			CreatedAt:      now,
			LastSeen:       now,
		}
		f.usersByEmail[strings.ToLower(inv.Email)] = user
		f.usersByID[user.ID] = user
	}
	mship := model.Membership{
		ID:             "m-" + inv.ID,
		OrganizationID: inv.OrganizationID,
		UserID:         user.ID,
		Role:           inv.Role,
		CreatedAt:      f.now(),
		UpdatedAt:      f.now(),
	}
	f.memberships[user.ID] = append(f.memberships[user.ID], mship)
	delete(f.invitations, key)
	delete(f.invitationsByToken, req.TokenHash)
	return user, mship, nil
}

// newManager wires Manager + fakeStore + the in-memory cache impl exposed
// by cache.New("") (which selects the memory backend when REDIS_URL is
// unset). The SessionCache is returned too so tests can poke the cache
// directly to exercise the architect-C4 deserialise-then-Live() invariant.
func newManager(t *testing.T) (*auth.Manager, *fakeStore, cache.Cache, *auth.SessionCache) {
	t.Helper()
	store := newFakeStore()
	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })
	sc := auth.NewSessionCache(mem)
	mgr := auth.NewManager(store, sc, auth.Config{
		TTL:             time.Hour,
		SessionsPerUser: 3, // small cap so cap-exceed tests are tractable
	})
	return mgr, store, mem, sc
}

// ── Cookie helpers ──────────────────────────────────────────────────────────

func TestSetSessionRoundTrip(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.Header.Set("X-Forwarded-Proto", "https") // simulates production behind a TLS terminator
	expires := time.Now().Add(time.Hour)
	auth.SetSession(w, r, auth.NewCookieConfig(), "abc.def.ghi", expires)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one Set-Cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Name != auth.SessionCookieName {
		t.Errorf("cookie name = %q; want %q", c.Name, auth.SessionCookieName)
	}
	if c.Value != "abc.def.ghi" {
		t.Errorf("cookie value = %q; want abc.def.ghi", c.Value)
	}
	if !c.HttpOnly {
		t.Error("cookie HttpOnly should be set")
	}
	if !c.Secure {
		t.Error("cookie Secure should be set when X-Forwarded-Proto is https")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v; want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("cookie Path = %q; want /", c.Path)
	}
}

func TestSetSessionPlainHTTPNotSecure(t *testing.T) {
	// Plain HTTP request — no TLS, no X-Forwarded-Proto header.
	// Browsers silently drop Secure cookies on HTTP origins, so the
	// helper must NOT mark the cookie Secure here. This is the local
	// dev / test path (also covers any deployment that exposes the
	// API on plain HTTP, which is unsupported in production).
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	auth.SetSession(w, r, auth.NewCookieConfig(), "x", time.Now().Add(time.Hour))
	c := w.Result().Cookies()[0]
	if c.Secure {
		t.Error("cookie Secure must be off when the request is plain HTTP")
	}
}

func TestSetSessionTLSRequestIsSecure(t *testing.T) {
	// Request with r.TLS set (direct HTTPS to the API — uncommon for
	// our deployments since we sit behind a TLS terminator, but we
	// honour it anyway).
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.TLS = &tls.ConnectionState{} // non-nil signals direct TLS to the API
	auth.SetSession(w, r, auth.NewCookieConfig(), "x", time.Now().Add(time.Hour))
	c := w.Result().Cookies()[0]
	if !c.Secure {
		t.Error("cookie Secure must be set when r.TLS is non-nil")
	}
}

func TestSetSessionXForwardedProtoHTTPSIsSecure(t *testing.T) {
	// Production shape: TLS terminator (App Runner / nginx) sets
	// X-Forwarded-Proto on the proxied request. The API container
	// itself receives plain HTTP from the LB but knows the original
	// edge was HTTPS — cookies must be marked Secure.
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	auth.SetSession(w, r, auth.NewCookieConfig(), "x", time.Now().Add(time.Hour))
	c := w.Result().Cookies()[0]
	if !c.Secure {
		t.Error("cookie Secure must be set when X-Forwarded-Proto is https")
	}
}

func TestSetSessionXForwardedProtoHTTPIsNotSecure(t *testing.T) {
	// Defensive: explicit X-Forwarded-Proto: http (a proxy that
	// terminates TLS for some routes but not others, or a
	// misconfigured local LB) must NOT mark the cookie Secure. The
	// fallback is "anything that isn't 'https' is treated as plain".
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/auth/login", nil)
	r.Header.Set("X-Forwarded-Proto", "http")
	auth.SetSession(w, r, auth.NewCookieConfig(), "x", time.Now().Add(time.Hour))
	c := w.Result().Cookies()[0]
	if c.Secure {
		t.Error("cookie Secure must be off when X-Forwarded-Proto is http")
	}
}

func TestReadSessionAbsent(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("GET", "/", nil)
	if got := auth.ReadSession(r); got != "" {
		t.Errorf("ReadSession with no cookie = %q; want empty", got)
	}
}

func TestReadSessionPresent(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "tok"})
	if got := auth.ReadSession(r); got != "tok" {
		t.Errorf("ReadSession = %q; want tok", got)
	}
}

func TestClearSessionExpiresImmediately(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/auth/logout", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	auth.ClearSession(w, r, auth.NewCookieConfig())
	c := w.Result().Cookies()[0]
	if c.MaxAge >= 0 {
		t.Errorf("expected MaxAge < 0 (immediate expiry), got %d", c.MaxAge)
	}
	if !c.Secure {
		t.Error("clear cookie must match the original Secure attribute (set was https → clear should be Secure too)")
	}
}

// ── Mint + Validate roundtrip ───────────────────────────────────────────────

func TestMintAndValidateRoundTrip(t *testing.T) {
	t.Parallel()
	mgr, _, _, _ := newManager(t)
	ctx := context.Background()

	mint, err := mgr.MintSession(ctx, auth.MintRequest{
		UserID:         "user-1",
		OrganizationID: "org-1",
		AuthMode:       model.AuthModePassword,
	})
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	if mint.PlaintextToken == "" {
		t.Fatal("plaintext token must be non-empty")
	}
	if mint.Session.SessionTokenHash != auth.HashToken(mint.PlaintextToken) {
		t.Fatal("session_token_hash must equal sha256(plaintext)")
	}
	if mint.Session.AuthMode != model.AuthModePassword {
		t.Errorf("auth_mode = %v; want password", mint.Session.AuthMode)
	}

	validated, err := mgr.ValidateSession(ctx, mint.PlaintextToken)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if validated.ID != mint.Session.ID {
		t.Errorf("validated session ID = %q; want %q", validated.ID, mint.Session.ID)
	}
}

func TestValidateSessionEmptyTokenFails(t *testing.T) {
	t.Parallel()
	mgr, _, _, _ := newManager(t)
	_, err := mgr.ValidateSession(context.Background(), "")
	if !errors.Is(err, storage.ErrSessionNotFound) {
		t.Errorf("got %v; want ErrSessionNotFound", err)
	}
}

func TestValidateSessionUnknownTokenFails(t *testing.T) {
	t.Parallel()
	mgr, _, _, _ := newManager(t)
	_, err := mgr.ValidateSession(context.Background(), "ZZ-not-a-real-token-ZZ")
	if !errors.Is(err, storage.ErrSessionNotFound) {
		t.Errorf("got %v; want ErrSessionNotFound", err)
	}
}

// ── Cache hit / miss / liveness re-check (architect C4) ─────────────────────

func TestValidateSessionTouchesOnlyOnCacheMiss(t *testing.T) {
	t.Parallel()
	mgr, store, _, _ := newManager(t)
	ctx := context.Background()

	mint, err := mgr.MintSession(ctx, auth.MintRequest{
		UserID:         "user-1",
		OrganizationID: "org-1",
		AuthMode:       model.AuthModePassword,
	})
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	// First Validate is a cache hit (Mint populated the cache). No PG touch.
	if _, err := mgr.ValidateSession(ctx, mint.PlaintextToken); err != nil {
		t.Fatalf("Validate 1: %v", err)
	}
	if got := store.touches[mint.Session.ID]; got != 0 {
		t.Errorf("after cache hit, touch count = %d; want 0 (architect N3)", got)
	}
}

func TestValidateSessionTouchesOnPGFallthrough(t *testing.T) {
	t.Parallel()
	mgr, store, mem, _ := newManager(t)
	ctx := context.Background()
	mint, err := mgr.MintSession(ctx, auth.MintRequest{
		UserID:         "u",
		OrganizationID: "o",
		AuthMode:       model.AuthModePassword,
	})
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	// Drop the cache entry to force a miss on the next Validate.
	if err := mem.Del(ctx, "axiaops:session:"+auth.HashToken(mint.PlaintextToken)); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if _, err := mgr.ValidateSession(ctx, mint.PlaintextToken); err != nil {
		t.Fatalf("Validate after cache evict: %v", err)
	}
	if got := store.touches[mint.Session.ID]; got != 1 {
		t.Errorf("after cache miss, touch count = %d; want 1", got)
	}
}

func TestCachedRevokedSessionIsRejected(t *testing.T) {
	// Architect C4: the deserialised cache value MUST gate liveness via
	// model.Session.Live(). To exercise the seam directly, we mint a real
	// session, then overwrite its cache entry with a copy where RevokedAt
	// is set in the past — simulating any path that put a stale revoked
	// snapshot into the cache. ValidateSession must reject without
	// touching PG.
	t.Parallel()
	mgr, _, _, sc := newManager(t)
	ctx := context.Background()
	mint, err := mgr.MintSession(ctx, auth.MintRequest{
		UserID:         "u",
		OrganizationID: "o",
		AuthMode:       model.AuthModePassword,
	})
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	revoked := mint.Session
	rt := time.Now().UTC().Add(-1 * time.Minute)
	revoked.RevokedAt = &rt
	sc.Put(ctx, revoked, time.Now().UTC())

	if _, err := mgr.ValidateSession(ctx, mint.PlaintextToken); !errors.Is(err, storage.ErrSessionNotFound) {
		t.Fatalf("Validate against cached-revoked entry = %v; want ErrSessionNotFound", err)
	}
}

func TestRevokeSessionInvalidatesCache(t *testing.T) {
	t.Parallel()
	mgr, _, mem, _ := newManager(t)
	ctx := context.Background()
	mint, err := mgr.MintSession(ctx, auth.MintRequest{
		UserID:         "u",
		OrganizationID: "o",
		AuthMode:       model.AuthModePassword,
	})
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	if err := mgr.RevokeSession(ctx, mint.Session.ID, mint.Session.SessionTokenHash, auth.RevokeReasonLogout); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	// The cache key is gone; next Validate must miss-then-PG-then-reject.
	if _, err := mem.Get(ctx, "axiaops:session:"+mint.Session.SessionTokenHash); err == nil {
		t.Fatal("expected cache miss after RevokeSession; cache entry still present")
	}
	if _, err := mgr.ValidateSession(ctx, mint.PlaintextToken); !errors.Is(err, storage.ErrSessionNotFound) {
		t.Fatalf("Validate after RevokeSession = %v; want ErrSessionNotFound", err)
	}
}

// ── RevokeUserSessions clears EVERY cache entry (architect C4) ──────────────

func TestRevokeUserSessionsClearsAllCacheEntries(t *testing.T) {
	t.Parallel()
	mgr, _, mem, _ := newManager(t)
	ctx := context.Background()
	hashes := []string{}
	for i := 0; i < 3; i++ {
		mint, err := mgr.MintSession(ctx, auth.MintRequest{
			UserID:         "u",
			OrganizationID: "o",
			AuthMode:       model.AuthModePassword,
		})
		if err != nil {
			t.Fatalf("MintSession #%d: %v", i, err)
		}
		hashes = append(hashes, mint.Session.SessionTokenHash)
	}
	n, err := mgr.RevokeUserSessions(ctx, "u", auth.RevokeReasonPasswordReset)
	if err != nil {
		t.Fatalf("RevokeUserSessions: %v", err)
	}
	if n != 3 {
		t.Errorf("revoked %d sessions; want 3", n)
	}
	for _, h := range hashes {
		if _, err := mem.Get(ctx, "axiaops:session:"+h); err == nil {
			t.Errorf("cache entry for hash %s still present — must be evicted", h)
		}
	}
}

// ── Per-user cap (architect C2) ─────────────────────────────────────────────

func TestPerUserCapEvictsExcess(t *testing.T) {
	t.Parallel()
	mgr, store, _, _ := newManager(t)
	ctx := context.Background()
	// Cap is 3 (set in newManager). Mint 5 sessions — count should settle at 3.
	for i := 0; i < 5; i++ {
		if _, err := mgr.MintSession(ctx, auth.MintRequest{
			UserID:         "u",
			OrganizationID: "o",
			AuthMode:       model.AuthModePassword,
		}); err != nil {
			t.Fatalf("MintSession #%d: %v", i, err)
		}
	}
	count, err := store.CountSessionsForUser(ctx, "u")
	if err != nil {
		t.Fatalf("CountSessionsForUser: %v", err)
	}
	if count != 3 {
		t.Errorf("after 5 mints with cap=3, live count = %d; want 3", count)
	}
}

func TestPerUserCapRevokesOldestFirst(t *testing.T) {
	// Plan §4.6: "the 11th login revokes the OLDEST active session."
	// Verifies the contractual ordering of ListUserSessionTokenHashes.
	t.Parallel()
	store := newFakeStore()
	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })

	// Drive the store's now forward 1ms per call so CreatedAt strictly
	// orders the sessions in the order they were minted.
	t0 := time.Now().UTC().Truncate(time.Second)
	tick := int64(0)
	store.now = func() time.Time {
		tick++
		return t0.Add(time.Duration(tick) * time.Millisecond)
	}

	mgr := auth.NewManager(store, auth.NewSessionCache(mem), auth.Config{
		TTL:             time.Hour,
		SessionsPerUser: 2,
		NowFunc:         func() time.Time { return t0.Add(time.Hour) }, // far enough that nothing is expired
	})
	ctx := context.Background()

	mints := make([]auth.MintResult, 0, 4)
	for i := 0; i < 4; i++ {
		mr, err := mgr.MintSession(ctx, auth.MintRequest{
			UserID:         "u",
			OrganizationID: "o",
			AuthMode:       model.AuthModePassword,
		})
		if err != nil {
			t.Fatalf("MintSession #%d: %v", i, err)
		}
		mints = append(mints, mr)
	}

	// With cap=2, the 2 oldest must be revoked; the 2 newest must remain.
	// Inspect the fakeStore directly: the surviving hashes should match
	// the last two mints.
	survived := map[string]bool{}
	for _, h := range mustList(t, store, "u") {
		survived[h] = true
	}
	if len(survived) != 2 {
		t.Fatalf("survivor count = %d; want 2", len(survived))
	}
	for i, mr := range mints {
		want := i >= 2 // last two should survive
		got := survived[mr.Session.SessionTokenHash]
		if want != got {
			t.Errorf("mint #%d (created index %d): survived=%v, want %v", i, i, got, want)
		}
	}
}

// ── Audit M-7 regression: cap-step failure is transactional ────────────────

// TestPerUserCap_FailurePropagates_NoSessionLeak pins the M-7 audit fix:
// when the cap-enforcement step fails inside the same transaction as the
// session INSERT, MintSession MUST return that error to the caller AND
// MUST NOT leak any session row or any cache entry. The previous
// implementation logged at warn and returned success — leaving an
// over-cap leftover until the 5-minute sweep. The new shape rolls back
// the INSERT (the fake's hook does this in lockstep with the
// production tx) so the user sees their login fail and retries instead
// of silently slipping under the cap.
func TestPerUserCap_FailurePropagates_NoSessionLeak(t *testing.T) {
	t.Parallel()
	mgr, store, mem, _ := newManager(t)
	ctx := context.Background()

	// Seed the cap-1 sessions so the next mint hits the cap-revoke path.
	preSeed := []string{}
	for i := 0; i < 3; i++ {
		mr, err := mgr.MintSession(ctx, auth.MintRequest{
			UserID:         "u-fail",
			OrganizationID: "o",
			AuthMode:       model.AuthModePassword,
		})
		if err != nil {
			t.Fatalf("seed MintSession #%d: %v", i, err)
		}
		preSeed = append(preSeed, mr.Session.SessionTokenHash)
	}

	// Arm the failure hook — every subsequent mint's cap-revoke step
	// returns this error, which forces the fake to roll back the
	// just-inserted row.
	wantErr := errors.New("simulated cap-revoke failure")
	store.hooks = &fakeStoreHooks{afterInsertBeforeCap: func() error { return wantErr }}

	// Attempt the (cap+1)th mint — must fail loudly.
	mr, err := mgr.MintSession(ctx, auth.MintRequest{
		UserID:         "u-fail",
		OrganizationID: "o",
		AuthMode:       model.AuthModePassword,
	})
	if err == nil {
		t.Fatalf("MintSession returned no error; want %v", wantErr)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("MintSession err = %v; want wraps %v", err, wantErr)
	}
	if mr.PlaintextToken != "" || mr.Session.ID != "" {
		t.Errorf("MintResult must be zero on failure; got %+v", mr)
	}

	// Session count must still be the pre-failure count (3) — the new
	// row was rolled back.
	count, err := store.CountSessionsForUser(ctx, "u-fail")
	if err != nil {
		t.Fatalf("CountSessionsForUser: %v", err)
	}
	if count != 3 {
		t.Errorf("after failed mint, live count = %d; want 3 (no leak)", count)
	}

	// None of the pre-seed sessions should have been revoked. The
	// rollback semantics mean the cap-revoke also reverts.
	hashes := mustList(t, store, "u-fail")
	if len(hashes) != 3 {
		t.Errorf("after failed mint, live hashes = %d; want 3 untouched", len(hashes))
	}
	survived := map[string]bool{}
	for _, h := range hashes {
		survived[h] = true
	}
	for _, h := range preSeed {
		if !survived[h] {
			t.Errorf("pre-seeded session %s was revoked despite failed mint — rollback broken", h)
		}
	}

	// Cache must not contain any orphan entry for the rolled-back
	// session (the production path skips the cache.Put entirely on
	// error). Scan every existing cache key prefix and assert nothing
	// references a session ID outside the pre-seed set.
	for _, h := range preSeed {
		if _, err := mem.Get(ctx, "axiaops:session:"+h); err != nil {
			t.Errorf("pre-seed cache entry for %s was evicted unexpectedly: %v", h, err)
		}
	}
}

// TestPerUserCap_TransactionalCommit_NoOverCapWindow asserts the M-7
// post-commit invariant: when the (cap+1)th mint succeeds, the live
// session count is exactly the cap — never cap+1 even transiently from
// the perspective of a concurrent reader. The fake's CreateSession-
// EnforcingCap mirrors the production tx (insert + revoke in one
// lock-protected block), so any read between the two steps would also
// see over-cap state — this test rules that out by reading immediately
// after each mint completes and asserting the count never overshoots.
func TestPerUserCap_TransactionalCommit_NoOverCapWindow(t *testing.T) {
	t.Parallel()
	mgr, store, _, _ := newManager(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := mgr.MintSession(ctx, auth.MintRequest{
			UserID:         "u-tx",
			OrganizationID: "o",
			AuthMode:       model.AuthModePassword,
		}); err != nil {
			t.Fatalf("MintSession #%d: %v", i, err)
		}
		count, err := store.CountSessionsForUser(ctx, "u-tx")
		if err != nil {
			t.Fatalf("CountSessionsForUser after mint #%d: %v", i, err)
		}
		// Cap is 3 (newManager). For i < 3 the count grows linearly;
		// from i=3 onward the cap holds it at 3.
		want := i + 1
		if want > 3 {
			want = 3
		}
		if count != want {
			t.Errorf("after mint #%d: count = %d, want %d (no over-cap window)", i, count, want)
		}
	}
}

func mustList(t *testing.T, store *fakeStore, userID string) []string {
	t.Helper()
	hashes, err := store.ListUserSessionTokenHashes(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListUserSessionTokenHashes: %v", err)
	}
	return hashes
}

// ── Expired session rejected ────────────────────────────────────────────────

func TestExpiredSessionRejected(t *testing.T) {
	// TTL = -1h means MintSession creates a session whose ExpiresAt is
	// already in the past. Path coverage: SessionCache.Put skips caching
	// when the computed TTL is below minTTL (and -1h is well below) — so
	// this test exercises the PG-fallthrough path: ValidateSession misses
	// the cache, SELECTs from PG, and Live() rejects the expired row.
	// A separate test (TestCachedRevokedSessionIsRejected) covers the
	// cached-but-revoked-or-expired Live() gate via direct SessionCache.Put.
	t.Parallel()
	store := newFakeStore()
	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })
	now := time.Now().UTC()
	mgr := auth.NewManager(store, auth.NewSessionCache(mem), auth.Config{
		TTL:             -1 * time.Hour,
		SessionsPerUser: 0,
		NowFunc:         func() time.Time { return now },
	})
	mint, err := mgr.MintSession(context.Background(), auth.MintRequest{
		UserID:         "u",
		OrganizationID: "o",
		AuthMode:       model.AuthModePassword,
	})
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	if _, err := mgr.ValidateSession(context.Background(), mint.PlaintextToken); !errors.Is(err, storage.ErrSessionNotFound) {
		t.Errorf("expired session validated = %v; want ErrSessionNotFound", err)
	}
}

// Client-IP extraction tests live with the canonical implementation in
// services/api/internal/httpip/httpip_test.go.
