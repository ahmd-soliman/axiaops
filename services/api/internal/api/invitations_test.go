package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"axiaops.io/api/internal/api"
	"axiaops.io/shared/model"
)

// invHandler builds a Handler ready to serve invitation routes.
func invHandler(store *MockStore) *http.ServeMux {
	h := api.New(store, noopQueue())
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
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
	mux := invHandler(store)

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
	// Native flow MUST surface a redemption URL — admins share OOB.
	if redir, _ := resp["redemption_url"].(string); redir == "" {
		t.Errorf("redemption_url missing from response: %+v", resp)
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
	mux := invHandler(store)

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
	mux := invHandler(store)

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
	mux := invHandler(store)

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

// TestCreateInvitation_EnforcementHint pins the Tasks.md 2.7.20 contract:
// the response carries `enforcement_hint: "sso_required"` iff the org has
// at least one ACTIVE OIDC connection with enforcement="required". Draft
// / disabled / SAML / non-required connections must NOT contribute — a
// false-positive hint would mislead the admin into telling the invitee
// to use SSO when password redemption would actually work.
func TestCreateInvitation_EnforcementHint(t *testing.T) {
	cases := []struct {
		name     string
		conns    []model.SSOConnection
		wantHint string // "" means field omitted
	}{
		{
			name:     "no_sso_configured",
			conns:    nil,
			wantHint: "",
		},
		{
			name: "active_oidc_optional",
			conns: []model.SSOConnection{
				{ID: "c1", Status: model.SSOStatusActive, Protocol: model.SSOProtocolOIDC, Enforcement: model.SSOEnforcementOptional},
			},
			wantHint: "",
		},
		{
			name: "active_oidc_preferred",
			conns: []model.SSOConnection{
				{ID: "c1", Status: model.SSOStatusActive, Protocol: model.SSOProtocolOIDC, Enforcement: model.SSOEnforcementPreferred},
			},
			wantHint: "",
		},
		{
			name: "active_oidc_required",
			conns: []model.SSOConnection{
				{ID: "c1", Status: model.SSOStatusActive, Protocol: model.SSOProtocolOIDC, Enforcement: model.SSOEnforcementRequired},
			},
			wantHint: "sso_required",
		},
		{
			name: "draft_oidc_required_does_not_count",
			conns: []model.SSOConnection{
				{ID: "c1", Status: model.SSOStatusDraft, Protocol: model.SSOProtocolOIDC, Enforcement: model.SSOEnforcementRequired},
			},
			wantHint: "",
		},
		{
			name: "disabled_oidc_required_does_not_count",
			conns: []model.SSOConnection{
				{ID: "c1", Status: model.SSOStatusDisabled, Protocol: model.SSOProtocolOIDC, Enforcement: model.SSOEnforcementRequired},
			},
			wantHint: "",
		},
		{
			name: "highest_wins_among_mixed",
			conns: []model.SSOConnection{
				{ID: "c1", Status: model.SSOStatusActive, Protocol: model.SSOProtocolOIDC, Enforcement: model.SSOEnforcementOptional},
				{ID: "c2", Status: model.SSOStatusActive, Protocol: model.SSOProtocolOIDC, Enforcement: model.SSOEnforcementRequired},
			},
			wantHint: "sso_required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMockStore().WithSSOConnections(tc.conns)
			mux := invHandler(store)

			body := `{"email":"new@example.com","role":"member"}`
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

			if w.Code != http.StatusCreated {
				t.Fatalf("expected 201, got %d (body: %s)", w.Code, w.Body.String())
			}
			var resp map[string]any
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			gotHint, _ := resp["enforcement_hint"].(string)
			if gotHint != tc.wantHint {
				t.Errorf("enforcement_hint=%q, want %q (resp=%+v)", gotHint, tc.wantHint, resp)
			}
		})
	}
}

func TestCreateInvitation_Owner_403_FromValidationNotPerm(t *testing.T) {
	// Owner role is invalid for invitations regardless of caller permissions.
	store := NewMockStore()
	mux := invHandler(store)

	body := `{"email":"new@example.com","role":"owner"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestCreateInvitation_AdminTarget_RequiresOwner(t *testing.T) {
	store := NewMockStore().WithRole("admin") // caller is admin, not owner
	mux := invHandler(store)

	body := `{"email":"new@example.com","role":"admin"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestCreateInvitation_Member_403(t *testing.T) {
	store := NewMockStore().WithRole("member") // members can't invite
	mux := invHandler(store)

	body := `{"email":"new@example.com","role":"viewer"}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodPost, "/v1/invitations", body))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestCreateInvitation_InvalidEmail_400(t *testing.T) {
	store := NewMockStore()
	mux := invHandler(store)

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
	mux := invHandler(store)

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
	mux := invHandler(store)

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
	mux := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodGet, "/v1/invitations?status=bogus", ""))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// ── DELETE /v1/invitations/{id} ─────────────────────────────────────────────

func TestRevokeInvitation_HappyPath_204(t *testing.T) {
	store := NewMockStore().WithPendingInvitations([]model.PendingInvitation{
		{
			ID: "inv-1", OrganizationID: "organization-me", Email: "x@x.com", Role: "member",
			Status: model.InvitationStatusPending,
		},
	})
	mux := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodDelete, "/v1/invitations/inv-1", ""))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (body: %s)", w.Code, w.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.pendingInvitations[0].Status != model.InvitationStatusRevoked {
		t.Errorf("status=%q, want revoked", store.pendingInvitations[0].Status)
	}
}

func TestRevokeInvitation_AlreadyRevoked_410(t *testing.T) {
	store := NewMockStore().WithPendingInvitations([]model.PendingInvitation{
		{
			ID: "inv-1", OrganizationID: "organization-me", Email: "x@x.com", Role: "member",
			Status: model.InvitationStatusRevoked,
		},
	})
	mux := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodDelete, "/v1/invitations/inv-1", ""))

	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", w.Code)
	}
}

func TestRevokeInvitation_AdminTargetNeedsOwner(t *testing.T) {
	store := NewMockStore().WithRole("admin").WithPendingInvitations([]model.PendingInvitation{
		{
			ID: "inv-1", OrganizationID: "organization-me", Email: "x@x.com", Role: "admin",
			Status: model.InvitationStatusPending,
		},
	})
	mux := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodDelete, "/v1/invitations/inv-1", ""))

	if w.Code != http.StatusForbidden {
		t.Fatalf("admin caller revoking admin invitation should 403, got %d", w.Code)
	}
}

func TestRevokeInvitation_NotFound_404(t *testing.T) {
	store := NewMockStore()
	mux := invHandler(store)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, invReq(http.MethodDelete, "/v1/invitations/missing", ""))

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
