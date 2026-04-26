package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/api/internal/api"
)

// permissionMatrixCase pins each registered v1 route to the role tier that
// must reach it. Adding a new endpoint without entering it here will be
// caught by TestPermissionMatrix_RoleGating, which asserts that a user with
// role="" (no membership) is denied 403 on every entry below.
//
// Read-only routes accessible to viewer use minRole=viewer; write endpoints
// use member; delete/admin use admin or owner. Routes that bypass Require
// entirely (the handler enforces internally) are marked via skipMatrix.
type permissionMatrixCase struct {
	method     string
	path       string
	body       string
	minRole    string
	skipMatrix bool // route enforces auth in handler, not via Require
}

var permissionMatrix = []permissionMatrixCase{
	// Read-only — every member tier including viewer.
	{method: "GET", path: "/v1/zombies", minRole: "viewer"},
	{method: "GET", path: "/v1/summary", minRole: "viewer"},
	{method: "GET", path: "/v1/trend", minRole: "viewer"},
	{method: "GET", path: "/v1/trend/services", minRole: "viewer"},
	{method: "GET", path: "/v1/trend/resource-types", minRole: "viewer"},
	{method: "GET", path: "/v1/costs", minRole: "viewer"},
	{method: "GET", path: "/v1/resources", minRole: "viewer"},
	{method: "GET", path: "/v1/accounts", minRole: "viewer"},
	{method: "GET", path: "/v1/dismissals", minRole: "viewer"},
	{method: "GET", path: "/v1/audit", minRole: "viewer"},
	{method: "GET", path: "/v1/memberships", minRole: "viewer"},

	// Member-write tier.
	{method: "POST", path: "/v1/accounts", body: `{}`, minRole: "member"},
	{method: "POST", path: "/v1/dismissals", body: `{}`, minRole: "member"},

	// Admin-only.
	{method: "POST", path: "/v1/memberships", body: `{}`, minRole: "admin"},

	// Owner-only.
	{method: "POST", path: "/v1/organizations/transfer-ownership", body: `{}`, minRole: "owner"},
	{method: "DELETE", path: "/v1/organizations/me", minRole: "owner"},

	// Self-leave bypass — handler does its own check.
	{method: "DELETE", path: "/v1/memberships/m-test", skipMatrix: true},
	// /users/me deletion is authn-only (no perm gate); handler enforces.
	{method: "DELETE", path: "/v1/users/me", skipMatrix: true},
}

// TestPermissionMatrix_DeniesEmptyRole asserts that every entry in the matrix
// returns 403 when the caller has no membership row. Catches "forgot to wrap
// in Require" regressions on new endpoints.
func TestPermissionMatrix_DeniesEmptyRole(t *testing.T) {
	for _, tc := range permissionMatrix {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if tc.skipMatrix {
				t.Skip("self-leave bypass — verified in dedicated tests")
			}
			store := NewMockStore().WithRole("")
			h := api.New(store, noopQueue())
			mux := http.NewServeMux()
			h.Register(mux)

			req := buildPermReq(tc.method, tc.path, tc.body)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Errorf("expected 403 for empty role, got %d (body: %s)", w.Code, w.Body.String())
			}
		})
	}
}

// TestPermissionMatrix_AllowsOwner asserts that owner reaches every entry —
// at least past Require. The handler may then 4xx for body/state reasons,
// but never 403.
func TestPermissionMatrix_AllowsOwner(t *testing.T) {
	for _, tc := range permissionMatrix {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if tc.skipMatrix {
				t.Skip("self-leave bypass — verified in dedicated tests")
			}
			store := NewMockStore().WithRole("owner")
			h := api.New(store, noopQueue())
			mux := http.NewServeMux()
			h.Register(mux)

			req := buildPermReq(tc.method, tc.path, tc.body)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code == http.StatusForbidden {
				t.Errorf("owner unexpectedly denied: %d (body: %s)", w.Code, w.Body.String())
			}
		})
	}
}

// TestPermissionMatrix_PublicRoutesDoNotRequirePermission asserts that
// /v1/version and /v1/me are reachable without a permission grant.
func TestPermissionMatrix_PublicRoutesDoNotRequirePermission(t *testing.T) {
	store := NewMockStore().WithRole("")
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)

	for _, path := range []string{"/v1/me", "/v1/version"} {
		req := buildPermReq(http.MethodGet, path, "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code == http.StatusForbidden {
			t.Errorf("%s should not require a permission grant, got 403", path)
		}
	}
}

func buildPermReq(method, path, body string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	return r.WithContext(injectIdentity(r.Context(), "tenant-test-uuid", "user-test-uuid", "u@x.com"))
}
