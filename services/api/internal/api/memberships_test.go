package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/model"
)

// memHandler builds a Handler around a pre-configured MockStore. The caller
// gets back the mux (which wraps Register) plus the mock for further setup.
func memHandler(store *MockStore) *http.ServeMux {
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

// memReq builds a request with full identity matching the seeded membership.
// The mock's RoleOf can be overridden via WithRole; default is owner.
func memReq(method, path, body string) *http.Request {
	src := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		src.Header.Set("Content-Type", "application/json")
	}
	return src.WithContext(meRequest(method, path).Context())
}

// ── List ────────────────────────────────────────────────────────────────────

func TestListMemberships_ReturnsOrganizationMemberships(t *testing.T) {
	store := NewMockStore().WithMemberships([]model.MembershipWithUser{
		{
			Membership: model.Membership{
				ID: "m-1", OrganizationID: "organization-me", UserID: "u-1", Role: "admin",
				CreatedAt: time.Now(), UpdatedAt: time.Now(),
			},
			Email: "a@x.com",
			Name:  "Alice",
		},
	})
	mux := memHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodGet, "/v1/memberships", ""))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	var rows []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["email"] != "a@x.com" {
		t.Errorf("email=%v", rows[0]["email"])
	}
}

// ── Invite ──────────────────────────────────────────────────────────────────

func TestCreateMembership_InvitesExistingUser(t *testing.T) {
	store := NewMockStore().WithUsers([]model.User{
		{ID: "u-target", OrganizationID: "organization-me", Email: "target@x.com", Name: "Target"},
	})
	mux := memHandler(store)
	body := `{"email":"target@x.com","role":"member"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPost, "/v1/memberships", body))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestCreateMembership_UserNeverLoggedIn_404(t *testing.T) {
	store := NewMockStore() // no users
	mux := memHandler(store)
	body := `{"email":"ghost@x.com","role":"viewer"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPost, "/v1/memberships", body))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"user_not_found"`) {
		t.Errorf("expected user_not_found error code in body, got: %s", w.Body.String())
	}
}

func TestCreateMembership_DuplicateReturns409(t *testing.T) {
	store := NewMockStore().WithUsers([]model.User{
		{ID: "u-target", OrganizationID: "organization-me", Email: "dup@x.com"},
	}).WithMemberships([]model.MembershipWithUser{
		{Membership: model.Membership{
			ID: "m-existing", OrganizationID: "organization-me", UserID: "u-target", Role: "viewer",
		}},
	})
	mux := memHandler(store)
	body := `{"email":"dup@x.com","role":"member"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPost, "/v1/memberships", body))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"already_a_member"`) {
		t.Errorf("expected already_a_member error code in body, got: %s", w.Body.String())
	}
}

func TestCreateMembership_OwnerRoleRejected(t *testing.T) {
	store := NewMockStore().WithUsers([]model.User{
		{ID: "u-target", OrganizationID: "organization-me", Email: "target@x.com"},
	})
	mux := memHandler(store)
	body := `{"email":"target@x.com","role":"owner"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPost, "/v1/memberships", body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (owner not assignable), got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"invalid_role"`) {
		t.Errorf("expected invalid_role error code in body, got: %s", w.Body.String())
	}
}

func TestCreateMembership_AdminInviteByNonOwnerForbidden(t *testing.T) {
	store := NewMockStore().WithRole("admin").WithUsers([]model.User{
		{ID: "u-target", OrganizationID: "organization-me", Email: "target@x.com"},
	})
	mux := memHandler(store)
	body := `{"email":"target@x.com","role":"admin"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPost, "/v1/memberships", body))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (admin cannot promote to admin), got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"forbidden"`) {
		t.Errorf("expected forbidden error code in body, got: %s", w.Body.String())
	}
}

func TestCreateMembership_InvalidRole(t *testing.T) {
	store := NewMockStore().WithUsers([]model.User{
		{ID: "u-target", OrganizationID: "organization-me", Email: "x@x.com"},
	})
	mux := memHandler(store)
	body := `{"email":"x@x.com","role":"superuser"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPost, "/v1/memberships", body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"invalid_role"`) {
		t.Errorf("expected invalid_role error code in body, got: %s", w.Body.String())
	}
}

// TestCreateMembership_InvalidJSON_400 pins the bad_request shape on the
// JSON-decode early-exit. Part of #86's full handler error-shape sweep.
func TestCreateMembership_InvalidJSON_400(t *testing.T) {
	store := NewMockStore()
	mux := memHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPost, "/v1/memberships", `{not json`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"bad_request"`) {
		t.Errorf("expected bad_request error code in body, got: %s", w.Body.String())
	}
}

// TestCreateMembership_MissingFields_400 pins the bad_request shape on the
// empty-email / empty-role early-exit. The handler guard is an OR, so any
// of the three combinations (both empty, only email empty, only role
// empty) trip it — covering all three so a future refactor that splits
// the guard doesn't silently regress two of them. Part of #86's sweep.
func TestCreateMembership_MissingFields_400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"both empty", `{"email":"","role":""}`},
		{"email empty", `{"email":"","role":"member"}`},
		{"role empty", `{"email":"alice@x.com","role":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMockStore()
			mux := memHandler(store)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, memReq(http.MethodPost, "/v1/memberships", tc.body))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d — body: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"bad_request"`) {
				t.Errorf("expected bad_request error code in body, got: %s", w.Body.String())
			}
		})
	}
}

// TestCreateMembership_MissingTLD_400 pins issue #85's strict-email
// contract on the add-existing-user fallback path: emails like
// "alice@intranet" are rejected even though net/mail.ParseAddress
// accepts them. Catches typos before a membership row is created
// against an unreachable address.
func TestCreateMembership_MissingTLD_400(t *testing.T) {
	store := NewMockStore()
	mux := memHandler(store)
	body := `{"email":"alice@intranet","role":"member"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPost, "/v1/memberships", body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d — body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_email") {
		t.Errorf("expected invalid_email error code in body, got: %s", w.Body.String())
	}
}

// ── Update role ─────────────────────────────────────────────────────────────

func TestUpdateMembershipRole_BasicChange(t *testing.T) {
	store := NewMockStore().WithMemberships([]model.MembershipWithUser{
		{Membership: model.Membership{ID: "m-1", OrganizationID: "organization-me", UserID: "u-1", Role: "viewer"}},
	})
	mux := memHandler(store)
	body := `{"role":"member"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPatch, "/v1/memberships/m-1/role", body))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestUpdateMembershipRole_LastOwnerReturns409(t *testing.T) {
	store := NewMockStore().WithMemberships([]model.MembershipWithUser{
		{Membership: model.Membership{ID: "m-1", OrganizationID: "organization-me", UserID: "u-1", Role: "owner"}},
	})
	mux := memHandler(store)
	body := `{"role":"admin"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPatch, "/v1/memberships/m-1/role", body))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 (last owner), got %d", w.Code)
	}
}

func TestUpdateMembershipRole_AdminPromoteToAdminBlocked(t *testing.T) {
	// caller is admin, target is member; promoting to admin requires owner perm.
	store := NewMockStore().WithRole("admin").WithMemberships([]model.MembershipWithUser{
		{Membership: model.Membership{ID: "m-1", OrganizationID: "organization-me", UserID: "u-1", Role: "member"}},
	})
	mux := memHandler(store)
	body := `{"role":"admin"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPatch, "/v1/memberships/m-1/role", body))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestUpdateMembershipRole_OwnerRoleRejected(t *testing.T) {
	store := NewMockStore().WithMemberships([]model.MembershipWithUser{
		{Membership: model.Membership{ID: "m-1", OrganizationID: "organization-me", UserID: "u-1", Role: "admin"}},
	})
	mux := memHandler(store)
	body := `{"role":"owner"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPatch, "/v1/memberships/m-1/role", body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ── Delete / leave ─────────────────────────────────────────────────────────

func TestDeleteMembership_SelfLeaveAllowed(t *testing.T) {
	// caller is viewer (no manage perms), but is removing their own row.
	store := NewMockStore().WithRole("viewer").WithMemberships([]model.MembershipWithUser{
		{Membership: model.Membership{ID: "m-self", OrganizationID: "organization-me", UserID: "user-me", Role: "viewer"}},
		{Membership: model.Membership{ID: "m-owner", OrganizationID: "organization-me", UserID: "u-owner", Role: "owner"}},
	})
	mux := memHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodDelete, "/v1/memberships/m-self", ""))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 (self-leave), got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestDeleteMembership_LastOwnerSelfLeaveBlocked(t *testing.T) {
	// Even self-leave must respect last-owner guard.
	store := NewMockStore().WithMemberships([]model.MembershipWithUser{
		{Membership: model.Membership{ID: "m-self", OrganizationID: "organization-me", UserID: "user-me", Role: "owner"}},
	})
	mux := memHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodDelete, "/v1/memberships/m-self", ""))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestDeleteMembership_AdminCannotDeleteAdmin(t *testing.T) {
	store := NewMockStore().WithRole("admin").WithMemberships([]model.MembershipWithUser{
		{Membership: model.Membership{ID: "m-target", OrganizationID: "organization-me", UserID: "u-target", Role: "admin"}},
	})
	mux := memHandler(store)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodDelete, "/v1/memberships/m-target", ""))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (admin cannot remove admin), got %d", w.Code)
	}
}

// ── Transfer ownership ─────────────────────────────────────────────────────

func TestTransferOwnership_TargetNotMember(t *testing.T) {
	store := NewMockStore().WithMemberships([]model.MembershipWithUser{
		{Membership: model.Membership{ID: "m-self", OrganizationID: "organization-me", UserID: "user-me", Role: "owner"}},
	})
	mux := memHandler(store)
	body := `{"to_user_id":"u-stranger"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPost, "/v1/organizations/transfer-ownership", body))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestTransferOwnership_Atomic(t *testing.T) {
	store := NewMockStore().WithMemberships([]model.MembershipWithUser{
		{Membership: model.Membership{ID: "m-self", OrganizationID: "organization-me", UserID: "user-me", Role: "owner"}},
		{Membership: model.Membership{ID: "m-target", OrganizationID: "organization-me", UserID: "u-target", Role: "admin"}},
	})
	mux := memHandler(store)
	body := `{"to_user_id":"u-target"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, memReq(http.MethodPost, "/v1/organizations/transfer-ownership", body))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Verify both rows flipped roles.
	rows, _ := store.ListMemberships(memReq(http.MethodGet, "/", "").Context())
	for _, m := range rows {
		switch m.UserID {
		case "user-me":
			if m.Role != "admin" {
				t.Errorf("old owner: expected admin, got %q", m.Role)
			}
		case "u-target":
			if m.Role != "owner" {
				t.Errorf("new owner: expected owner, got %q", m.Role)
			}
		}
	}
}
