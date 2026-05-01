package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"axiaops.io/api/internal/auth"
)

// inMemoryProvider is a stub auth.Provider used purely to drive the
// composite logic. The test files for NativeProvider already cover the
// real native flow; here we only care about ordering + error collapsing.
type inMemoryProvider struct {
	id    auth.Identity
	err   error
	calls int
}

func (p *inMemoryProvider) Authenticate(*http.Request) (auth.Identity, error) {
	p.calls++
	return p.id, p.err
}

func TestCompositeFirstWins(t *testing.T) {
	t.Parallel()
	first := &inMemoryProvider{id: auth.Identity{UserID: "first"}}
	second := &inMemoryProvider{id: auth.Identity{UserID: "second"}}
	c := auth.NewCompositeProvider(first, second)

	id, err := c.Authenticate(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if id.UserID != "first" {
		t.Errorf("UserID = %q; want first (priority order)", id.UserID)
	}
	if first.calls != 1 || second.calls != 0 {
		t.Errorf("calls = (%d, %d); want (1, 0) — second must not run when first succeeds", first.calls, second.calls)
	}
}

func TestCompositeFallsThroughOnError(t *testing.T) {
	t.Parallel()
	first := &inMemoryProvider{err: auth.ErrUnauthenticated}
	second := &inMemoryProvider{id: auth.Identity{UserID: "second"}}
	c := auth.NewCompositeProvider(first, second)

	id, err := c.Authenticate(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if id.UserID != "second" {
		t.Errorf("UserID = %q; want second (fallthrough)", id.UserID)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Errorf("calls = (%d, %d); want (1, 1)", first.calls, second.calls)
	}
}

func TestCompositeAllErrorsCollapseTo401Sentinel(t *testing.T) {
	t.Parallel()
	first := &inMemoryProvider{err: auth.ErrUnauthenticated}
	second := &inMemoryProvider{err: errors.New("db down")}
	c := auth.NewCompositeProvider(first, second)

	_, err := c.Authenticate(httptest.NewRequest("GET", "/x", nil))
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate = %v; want ErrUnauthenticated (no internal-reason leak)", err)
	}
}

func TestCompositeRequiresAtLeastOneProvider(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("NewCompositeProvider() with no args must panic")
		}
	}()
	_ = auth.NewCompositeProvider()
}

// TestCompositeRollingDeployBothShapesAccepted nails the architect-S1
// acceptance: under AUTH_PROVIDER=both, the same replica must accept
// either a native session cookie OR a Kinde Bearer JWT without
// flapping. Drives two shaped requests through the same composite —
// the native-shaped request returns the native Identity; the
// kinde-shaped request falls through native and returns the kinde
// Identity. Both succeed; neither order is sticky.
func TestCompositeRollingDeployBothShapesAccepted(t *testing.T) {
	t.Parallel()
	// Stand-in providers that "accept" requests carrying their
	// shape's marker header. Real native/kinde providers parse
	// cookies / Bearer JWTs respectively; the composite logic is
	// shape-agnostic, so a header-shape stub is sufficient.
	nativeStub := shapedProvider{header: "X-Test-Cookie", id: auth.Identity{UserID: "u-native", AuthMode: "password"}}
	kindeStub := shapedProvider{header: "Authorization", id: auth.Identity{UserID: "u-kinde", AuthMode: "kinde"}}
	c := auth.NewCompositeProvider(nativeStub, kindeStub)

	// Cookie-shaped request → native wins.
	cookieReq := httptest.NewRequest("GET", "/x", nil)
	cookieReq.Header.Set("X-Test-Cookie", "fixture")
	id, err := c.Authenticate(cookieReq)
	if err != nil {
		t.Fatalf("cookie-shaped request: %v", err)
	}
	if id.UserID != "u-native" || id.AuthMode != "password" {
		t.Errorf("cookie-shaped → identity = %+v; want native", id)
	}

	// Bearer-shaped request, same composite instance → native fails,
	// kinde wins. No flapping; the ordering is deterministic per
	// request, not stateful across requests.
	bearerReq := httptest.NewRequest("GET", "/x", nil)
	bearerReq.Header.Set("Authorization", "Bearer fixture")
	id, err = c.Authenticate(bearerReq)
	if err != nil {
		t.Fatalf("bearer-shaped request: %v", err)
	}
	if id.UserID != "u-kinde" || id.AuthMode != "kinde" {
		t.Errorf("bearer-shaped → identity = %+v; want kinde", id)
	}
}

type shapedProvider struct {
	header string
	id     auth.Identity
}

func (p shapedProvider) Authenticate(r *http.Request) (auth.Identity, error) {
	if r.Header.Get(p.header) == "" {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	return p.id, nil
}
