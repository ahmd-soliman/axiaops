package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/model"
)

// delHandler builds a Handler around the given store and returns a mux ready
// to serve. Same shape as memHandler in memberships_test.go.
func delHandler(store *MockStore) *http.ServeMux {
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

// ── DELETE /v1/users/me ─────────────────────────────────────────────────────

func TestDeleteCurrentUser_LastOwner_409(t *testing.T) {
	// user-me is the sole owner of tenant-me — deletion must be refused.
	store := NewMockStore().
		WithRole("owner").
		WithMemberships([]model.MembershipWithUser{
			{Membership: model.Membership{ID: "m-1", TenantID: "tenant-me", UserID: "user-me", Role: "owner",
				CreatedAt: time.Now(), UpdatedAt: time.Now()}},
		}).
		WithUsers([]model.User{
			{ID: "user-me", TenantID: "tenant-me", Email: "me@example.com"},
		})

	mux := delHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodDelete, "/v1/users/me"))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestDeleteCurrentUser_OwnerWithCoOwner_204(t *testing.T) {
	store := NewMockStore().
		WithRole("owner").
		WithMemberships([]model.MembershipWithUser{
			{Membership: model.Membership{ID: "m-1", TenantID: "tenant-me", UserID: "user-me", Role: "owner",
				CreatedAt: time.Now(), UpdatedAt: time.Now()}},
			{Membership: model.Membership{ID: "m-2", TenantID: "tenant-me", UserID: "user-other", Role: "owner",
				CreatedAt: time.Now(), UpdatedAt: time.Now()}},
		}).
		WithUsers([]model.User{
			{ID: "user-me", TenantID: "tenant-me", Email: "me@example.com"},
			{ID: "user-other", TenantID: "tenant-me", Email: "other@example.com"},
		})

	mux := delHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodDelete, "/v1/users/me"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body: %s)", w.Code, w.Body.String())
	}
	// User row gone.
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, u := range store.users {
		if u.ID == "user-me" {
			t.Errorf("user-me should have been deleted")
		}
	}
}

func TestDeleteCurrentUser_NonOwner_204(t *testing.T) {
	store := NewMockStore().
		WithRole("member").
		WithMemberships([]model.MembershipWithUser{
			{Membership: model.Membership{ID: "m-1", TenantID: "tenant-me", UserID: "user-me", Role: "member",
				CreatedAt: time.Now(), UpdatedAt: time.Now()}},
		}).
		WithUsers([]model.User{
			{ID: "user-me", TenantID: "tenant-me", Email: "me@example.com"},
		})

	mux := delHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodDelete, "/v1/users/me"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestDeleteCurrentUser_NoIdentity_403(t *testing.T) {
	store := NewMockStore().WithRole("owner")
	mux := delHandler(store)

	// Plain request — no DevBypass, no identity on the context.
	req := httptest.NewRequest(http.MethodDelete, "/v1/users/me", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without identity, got %d", w.Code)
	}
}

// ── DELETE /v1/tenants/me ───────────────────────────────────────────────────

func TestDeleteCurrentTenant_Owner_204(t *testing.T) {
	store := NewMockStore().
		WithRole("owner").
		WithUsers([]model.User{
			{ID: "user-me", TenantID: "tenant-me", Email: "me@example.com"},
		})

	mux := delHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodDelete, "/v1/tenants/me"))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestDeleteCurrentTenant_NonOwnerRoles_403(t *testing.T) {
	for _, role := range []string{"admin", "member", "viewer", ""} {
		t.Run("role="+role, func(t *testing.T) {
			store := NewMockStore().WithRole(role)
			mux := delHandler(store)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, meRequest(http.MethodDelete, "/v1/tenants/me"))

			if w.Code != http.StatusForbidden {
				t.Errorf("role=%q: expected 403, got %d", role, w.Code)
			}
		})
	}
}
