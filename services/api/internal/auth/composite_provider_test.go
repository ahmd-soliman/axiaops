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
