package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/api/internal/api"
	"axiaops.io/api/internal/kinde"
	"axiaops.io/shared/model"
)

// invHandler builds a Handler with a kinde stub installed and returns the
// mux + the stub for assertions.
func invHandler(store *MockStore) (*http.ServeMux, *kinde.Stub) {
	stub := kinde.NewStub()
	h := api.New(store, noopQueue()).WithKinde(stub)
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, stub
}

// invReq mirrors memReq — request with seeded identity.
func invReq(method, path, body string) *http.Request {
	src := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		src.Header.Set("Content-Type", "application/json")
	}
	return src.WithContext(meRequest(method, path).Context())
}

// ── POST /v1/invitations ────────────────────────────────────────────────────

func TestCreateInvitation_HappyPath(t *testing.T) {
	store := NewMockStore() // no users / no memberships
	mux, stub := invHandler(store)

	body := `{"email":"new@example.com","role":"member","name":"New Hire"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["email"] != "new@example.com" || resp["role"] != "member" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if resp["status"] != "pending" {
		t.Errorf("status=%v want pending", resp["status"])
	}
	// Stub should have an invited email.
	if name := stub.OrgName("organization-me"); name != "" {
		t.Errorf("OrgName should be empty (no rename happened): %q", name)
	}
}

func TestCreateInvitation_ReInvite_Returns200(t *testing.T) {
	store := NewMockStore().WithPendingInvitations([]model.PendingInvitation{
		{
			ID:             "inv-old",
			OrganizationID: "organization-me",
			Email:          "again@example.com",
			Role:           "viewer",
			Status:         model.InvitationStatusPending,
		},
	})
	mux, _ := invHandler(store)

	body := `{"email":"again@example.com","role":"member"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on re-invite, got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["role"] != "member" {
		t.Errorf("role should have been updated to member, got %v", resp["role"])
	}
}

func TestCreateInvitation_AlreadyMember_409(t *testing.T) {
	store := NewMockStore().WithUsers([]model.User{
		{ID: "u-target", OrganizationID: "organization-me", Email: "member@example.com"},
	}).WithMemberships([]model.MembershipWithUser{
		{
			Membership: model.Membership{
				ID: "m-1", OrganizationID: "organization-me", UserID: "u-target", Role: "member",
			},
			Email: "member@example.com",
		},
	})
	mux, _ := invHandler(store)

	body := `{"email":"member@example.com","role":"member"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "already_a_member" {
		t.Errorf("error code=%q, want already_a_member", resp["error"])
	}
}

func TestCreateInvitation_UserExistsNoMembership_409(t *testing.T) {
	store := NewMockStore().WithUsers([]model.User{
		{ID: "u-orphan", OrganizationID: "organization-me", Email: "orphan@example.com"},
	})
	mux, _ := invHandler(store)

	body := `{"email":"orphan@example.com","role":"member"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "user_exists_use_memberships" {
		t.Errorf("error code=%q, want user_exists_use_memberships", resp["error"])
	}
}

func TestCreateInvitation_KindeFailure_502_AndCompensates(t *testing.T) {
	store := NewMockStore()
	stub := kinde.NewStub()
	stub.FailNextInvite(errors.New("kinde down"))
	h := api.New(store, noopQueue()).WithKinde(stub)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"email":"new@example.com","role":"member"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d (body: %s)", w.Code, w.Body.String())
	}
	// Compensating revoke flipped the local row.
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.pendingInvitations) != 1 {
		t.Fatalf("expected 1 pending row (revoked), got %d", len(store.pendingInvitations))
	}
	if store.pendingInvitations[0].Status != model.InvitationStatusRevoked {
		t.Errorf("status=%q, want revoked", store.pendingInvitations[0].Status)
	}
}

func TestCreateInvitation_Owner_403_FromValidationNotPerm(t *testing.T) {
	// Owner role is invalid for invitations regardless of caller permissions.
	store := NewMockStore()
	mux, _ := invHandler(store)

	body := `{"email":"new@example.com","role":"owner"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestCreateInvitation_AdminTarget_RequiresOwner(t *testing.T) {
	store := NewMockStore().WithRole("admin") // caller is admin, not owner
	mux, _ := invHandler(store)

	body := `{"email":"new@example.com","role":"admin"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestCreateInvitation_Member_403(t *testing.T) {
	store := NewMockStore().WithRole("member") // members can't invite
	mux, _ := invHandler(store)

	body := `{"email":"new@example.com","role":"viewer"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestCreateInvitation_InvalidEmail_400(t *testing.T) {
	store := NewMockStore()
	mux, _ := invHandler(store)

	body := `{"email":"not-an-email","role":"member"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ── GET /v1/invitations ─────────────────────────────────────────────────────

func TestListInvitations_EmptyReturnsEmptyArray(t *testing.T) {
	store := NewMockStore()
	mux, _ := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodGet, "/v1/invitations", ""))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.HasPrefix(strings.TrimSpace(w.Body.String()), "[") {
		t.Errorf("expected JSON array, got %q", w.Body.String())
	}
}

func TestListInvitations_FiltersByStatus(t *testing.T) {
	store := NewMockStore().WithPendingInvitations([]model.PendingInvitation{
		{ID: "inv-1", OrganizationID: "organization-me", Email: "p@x.com", Role: "member", Status: model.InvitationStatusPending},
		{ID: "inv-2", OrganizationID: "organization-me", Email: "r@x.com", Role: "member", Status: model.InvitationStatusRevoked},
	})
	mux, _ := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodGet, "/v1/invitations?status=revoked", ""))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var rows []map[string]any
	_ = json.NewDecoder(w.Body).Decode(&rows)
	if len(rows) != 1 || rows[0]["id"] != "inv-2" {
		t.Errorf("expected only the revoked row, got %+v", rows)
	}
}

func TestListInvitations_InvalidStatus_400(t *testing.T) {
	store := NewMockStore()
	mux, _ := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodGet, "/v1/invitations?status=bogus", ""))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ── DELETE /v1/invitations/{id} ─────────────────────────────────────────────

func TestRevokeInvitation_HappyPath_CallsKinde(t *testing.T) {
	store := NewMockStore().WithPendingInvitations([]model.PendingInvitation{
		{
			ID: "inv-1", OrganizationID: "organization-me", Email: "x@x.com", Role: "member",
			Status: model.InvitationStatusPending, KindeUserID: "kinde-u-x",
		},
	})
	mux, stub := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodDelete, "/v1/invitations/inv-1", ""))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body: %s)", w.Code, w.Body.String())
	}
	removed := stub.RemovedUserIDs()
	if len(removed) != 1 || removed[0] != "kinde-u-x" {
		t.Errorf("expected RemoveUser called with kinde-u-x, got %v", removed)
	}
}

func TestRevokeInvitation_AlreadyRevoked_410(t *testing.T) {
	store := NewMockStore().WithPendingInvitations([]model.PendingInvitation{
		{
			ID: "inv-1", OrganizationID: "organization-me", Email: "x@x.com", Role: "member",
			Status: model.InvitationStatusRevoked, KindeUserID: "kinde-u-x",
		},
	})
	mux, _ := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodDelete, "/v1/invitations/inv-1", ""))

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", w.Code)
	}
}

func TestRevokeInvitation_KindeFails_502(t *testing.T) {
	store := NewMockStore().WithPendingInvitations([]model.PendingInvitation{
		{
			ID: "inv-1", OrganizationID: "organization-me", Email: "x@x.com", Role: "member",
			Status: model.InvitationStatusPending, KindeUserID: "kinde-u-x",
		},
	})
	stub := kinde.NewStub()
	stub.FailNextRemove(errors.New("kinde 502"))
	h := api.New(store, noopQueue()).WithKinde(stub)
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodDelete, "/v1/invitations/inv-1", ""))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
	// Local row stays pending so retry works.
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.pendingInvitations[0].Status != model.InvitationStatusPending {
		t.Errorf("status=%q, want pending", store.pendingInvitations[0].Status)
	}
}

func TestRevokeInvitation_AdminTargetNeedsOwner(t *testing.T) {
	store := NewMockStore().WithRole("admin").WithPendingInvitations([]model.PendingInvitation{
		{
			ID: "inv-1", OrganizationID: "organization-me", Email: "x@x.com", Role: "admin",
			Status: model.InvitationStatusPending, KindeUserID: "kinde-u-x",
		},
	})
	mux, _ := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodDelete, "/v1/invitations/inv-1", ""))

	if w.Code != http.StatusForbidden {
		t.Fatalf("admin caller revoking admin invitation should 403, got %d", w.Code)
	}
}

// TestRevokeInvitation_NativeAuth_NoKindeCall_204 pins the fix for the
// broken-feature bug where DELETE /v1/invitations/{id} returned 503 under
// AUTH_PROVIDER=native because the legacy `h.kinde == nil` guard fired
// unconditionally even when there's no Kinde to call. Now: native-auth
// callers skip the Kinde RemoveUser entirely and revoke the local row via
// RevokePendingInvitation only.
func TestRevokeInvitation_NativeAuth_NoKindeCall_204(t *testing.T) {
	store := NewMockStore().WithPendingInvitations([]model.PendingInvitation{
		{
			ID: "inv-1", OrganizationID: "organization-me", Email: "x@x.com", Role: "member",
			Status: model.InvitationStatusPending,
		},
	})
	// Build the handler with NativeInvitations=true and NO Kinde stub —
	// the previous bug surface was the handler refusing to run because
	// `h.kinde == nil`. The test setup deliberately mirrors that posture.
	h := api.New(store, noopQueue()).WithNativeInvitations(true, "")
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodDelete, "/v1/invitations/inv-1", ""))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestRevokeInvitation_NotFound_404(t *testing.T) {
	store := NewMockStore()
	mux, _ := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodDelete, "/v1/invitations/missing", ""))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
