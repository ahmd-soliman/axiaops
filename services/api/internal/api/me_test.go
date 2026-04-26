package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"axiaops.io/api/internal/api"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/queue"
)

// meRequest builds a request with full identity (tenant_id, user_id, email)
// via DevBypass — the unexported context keys can only be set through that
// path. DevBypass populates the context for any non-public path; we feed it
// the actual request so the caller's method and path are honoured.
func meRequest(method, path string) *http.Request {
	src := httptest.NewRequest(method, path, nil)
	var captured *http.Request
	middleware.DevBypass("tenant-me", "user-me", "me@example.com", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = r
	})).ServeHTTP(httptest.NewRecorder(), src)
	return captured
}

func newQueueShim() queue.Queue { return noopQueue() }

func TestGetMe_ReturnsRoleAndPermissions(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	h := api.New(store, newQueueShim())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodGet, "/v1/me"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp struct {
		UserID         string   `json:"user_id"`
		OrganizationID string   `json:"organization_id"`
		Email          string   `json:"email"`
		Role           string   `json:"role"`
		Permissions    []string `json:"permissions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.UserID != "user-me" {
		t.Errorf("user_id=%q", resp.UserID)
	}
	if resp.OrganizationID != "tenant-me" {
		t.Errorf("tenant_id=%q", resp.OrganizationID)
	}
	if resp.Email != "me@example.com" {
		t.Errorf("email=%q", resp.Email)
	}
	if resp.Role != "admin" {
		t.Errorf("role=%q", resp.Role)
	}
	if len(resp.Permissions) == 0 {
		t.Error("permissions empty for admin")
	}
}

func TestGetMe_NoMembershipReturnsEmptyRole(t *testing.T) {
	store := NewMockStore().WithRole("")
	h := api.New(store, newQueueShim())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodGet, "/v1/me"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (route is authn-only), got %d", w.Code)
	}

	var resp struct {
		Role        string   `json:"role"`
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Role != "" {
		t.Errorf("role=%q, want empty", resp.Role)
	}
	if len(resp.Permissions) != 0 {
		t.Errorf("permissions=%v, want empty for missing role", resp.Permissions)
	}
}
