package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/authz"
)

type fakeRoleStore struct {
	role string
	err  error
}

func (f *fakeRoleStore) RoleOf(_ context.Context, _, _ string) (string, error) {
	return f.role, f.err
}

func ok200(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func TestRequire_AllowsWhenPermGranted(t *testing.T) {
	h := middleware.Require(
		authz.PermAccountsRead,
		&fakeRoleStore{role: "viewer"},
		http.HandlerFunc(ok200),
	)
	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	req = req.WithContext(ctxWithIdentity(req.Context(), "t-1", "u-1"))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestRequire_DeniesWhenPermNotGranted(t *testing.T) {
	h := middleware.Require(
		authz.PermAccountsDelete,
		&fakeRoleStore{role: "member"},
		http.HandlerFunc(ok200),
	)
	req := httptest.NewRequest(http.MethodDelete, "/v1/accounts/abc", nil)
	req = req.WithContext(ctxWithIdentity(req.Context(), "t-1", "u-1"))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestRequire_DeniesEmptyRole(t *testing.T) {
	h := middleware.Require(
		authz.PermAccountsRead,
		&fakeRoleStore{role: ""},
		http.HandlerFunc(ok200),
	)
	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	req = req.WithContext(ctxWithIdentity(req.Context(), "t-1", "u-1"))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("empty role must 403, got %d", rr.Code)
	}
}

func TestRequire_FailsClosedOnStoreError(t *testing.T) {
	h := middleware.Require(
		authz.PermAccountsRead,
		&fakeRoleStore{err: errors.New("db gone")},
		http.HandlerFunc(ok200),
	)
	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	req = req.WithContext(ctxWithIdentity(req.Context(), "t-1", "u-1"))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("store error must 403, got %d", rr.Code)
	}
}

func TestRequire_DeniesWhenIdentityMissing(t *testing.T) {
	h := middleware.Require(
		authz.PermAccountsRead,
		&fakeRoleStore{role: "owner"},
		http.HandlerFunc(ok200),
	)
	// No identity set on context.
	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing identity must 403, got %d", rr.Code)
	}
}

// ctxWithIdentity inserts the same context keys that Auth.Wrap / DevBypass
// would set in production. The keys are unexported in middleware, so we
// pipe through DevBypass to reach them.
func ctxWithIdentity(parent context.Context, tenantID, userID string) context.Context {
	r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(parent)
	var captured context.Context
	middleware.DevBypass(tenantID, userID, "", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = r.Context()
	})).ServeHTTP(httptest.NewRecorder(), r)
	return captured
}
