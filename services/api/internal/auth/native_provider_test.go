package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"axiaops.io/api/internal/auth"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/model"
)

// membershipStub is a minimal MembershipLookup. Returns role+email for
// the requested user; other users get the zero MembershipDetails (treated
// as "no membership" by Authenticate).
type membershipStub struct {
	role   string
	email  string
	userID string
	err    error
}

func (m membershipStub) lookup(_ context.Context, _, userID string) (auth.MembershipDetails, error) {
	if m.err != nil {
		return auth.MembershipDetails{}, m.err
	}
	if userID == m.userID {
		return auth.MembershipDetails{Role: m.role, Email: m.email}, nil
	}
	return auth.MembershipDetails{}, nil
}

func newProviderTest(t *testing.T) (*auth.NativeProvider, *auth.Manager, *fakeStore) {
	t.Helper()
	store := newFakeStore()
	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })
	mgr := auth.NewManager(store, auth.NewSessionCache(mem), auth.Config{})
	ms := membershipStub{role: "owner", email: "user-1@example.com", userID: "user-1"}
	prov := auth.NewNativeProvider(mgr, ms.lookup)
	return prov, mgr, store
}

func mintAndCookie(t *testing.T, mgr *auth.Manager, userID, orgID string, mode model.AuthMode) (*http.Request, auth.MintResult) {
	t.Helper()
	mr, err := mgr.MintSession(context.Background(), auth.MintRequest{
		UserID: userID, OrganizationID: orgID, AuthMode: mode,
	})
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}
	r := httptest.NewRequest("GET", "/v1/zombies", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: mr.PlaintextToken})
	return r, mr
}

func TestNativeProviderHappyPath(t *testing.T) {
	t.Parallel()
	prov, mgr, _ := newProviderTest(t)
	r, mr := mintAndCookie(t, mgr, "user-1", "org-1", model.AuthModePassword)

	id, err := prov.Authenticate(r)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if id.UserID != "user-1" || id.OrganizationID != "org-1" {
		t.Errorf("identity = %+v; want user-1/org-1", id)
	}
	if id.Role != "owner" {
		t.Errorf("role = %q; want owner", id.Role)
	}
	if id.Email != "user-1@example.com" {
		t.Errorf("email = %q; want user-1@example.com", id.Email)
	}
	if id.AuthMode != "password" {
		t.Errorf("auth_mode = %q; want password", id.AuthMode)
	}
	if id.SessionID != mr.Session.ID {
		t.Errorf("session_id = %q; want %q", id.SessionID, mr.Session.ID)
	}
	if id.SessionTokenHash != mr.Session.SessionTokenHash {
		t.Error("session_token_hash should equal the persisted hash")
	}
}

func TestNativeProviderNoCookie(t *testing.T) {
	t.Parallel()
	prov, _, _ := newProviderTest(t)
	r := httptest.NewRequest("GET", "/v1/zombies", nil)
	if _, err := prov.Authenticate(r); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate w/o cookie = %v; want ErrUnauthenticated", err)
	}
}

func TestNativeProviderUnknownToken(t *testing.T) {
	t.Parallel()
	prov, _, _ := newProviderTest(t)
	r := httptest.NewRequest("GET", "/v1/zombies", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "totally-not-a-real-token"})
	if _, err := prov.Authenticate(r); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate w/ unknown token = %v; want ErrUnauthenticated", err)
	}
}

func TestNativeProviderRevokedSession(t *testing.T) {
	t.Parallel()
	prov, mgr, _ := newProviderTest(t)
	r, mr := mintAndCookie(t, mgr, "user-1", "org-1", model.AuthModePassword)
	if err := mgr.RevokeSession(context.Background(), mr.Session.ID, mr.Session.SessionTokenHash, auth.RevokeReasonLogout); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := prov.Authenticate(r); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate after revoke = %v; want ErrUnauthenticated", err)
	}
}

func TestNativeProviderUserHasNoMembership(t *testing.T) {
	// Defensive case: session row exists, but the user has been removed
	// from the org since the session was minted. MembershipLookup returns
	// zero details (empty role). Provider must reject — handlers
	// downstream rely on Role being set.
	t.Parallel()
	store := newFakeStore()
	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })
	mgr := auth.NewManager(store, auth.NewSessionCache(mem), auth.Config{})
	ms := membershipStub{role: "", userID: "user-1"} // empty role — no membership
	prov := auth.NewNativeProvider(mgr, ms.lookup)

	r, _ := mintAndCookie(t, mgr, "user-1", "org-1", model.AuthModePassword)
	if _, err := prov.Authenticate(r); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate w/o membership = %v; want ErrUnauthenticated", err)
	}
}

func TestNativeProviderMembershipLookupFailure(t *testing.T) {
	// MembershipLookup may fail (transient PG error). Provider must
	// reject — don't fail open if we can't resolve the role+email.
	t.Parallel()
	store := newFakeStore()
	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })
	mgr := auth.NewManager(store, auth.NewSessionCache(mem), auth.Config{})
	ms := membershipStub{err: errors.New("db unavailable")}
	prov := auth.NewNativeProvider(mgr, ms.lookup)

	r, _ := mintAndCookie(t, mgr, "user-1", "org-1", model.AuthModePassword)
	if _, err := prov.Authenticate(r); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate w/ membership lookup error = %v; want ErrUnauthenticated", err)
	}
}
