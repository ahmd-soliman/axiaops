package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"axiaops.io/api/internal/api"
	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
	"axiaops.io/shared/model"
	"axiaops.io/shared/queue"
)

// meRequest builds a request with full identity (organization_id, user_id, email)
// via DevBypass — the unexported context keys can only be set through that
// path. DevBypass populates the context for any non-public path; we feed it
// the actual request so the caller's method and path are honoured.
func meRequest(method, path string) *http.Request {
	src := httptest.NewRequest(method, path, nil)
	var captured *http.Request
	middleware.DevBypass("organization-me", "user-me", "me@example.com", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
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
	if resp.OrganizationID != "organization-me" {
		t.Errorf("organization_id=%q", resp.OrganizationID)
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

// fakeAuthProvider is a stub auth.Provider used to drive WrapNative so
// we can assert that /v1/me's auth_provider / auth_mode fields reflect
// the active session's AuthMode. DevBypass never sets these context
// keys, so we go through the real middleware here.
type fakeAuthProvider struct {
	identity auth.Identity
}

func (f fakeAuthProvider) Authenticate(*http.Request) (auth.Identity, error) {
	return f.identity, nil
}

func TestGetMe_AuthProviderTier(t *testing.T) {
	cases := []struct {
		name             string
		authMode         string
		wantAuthProvider string
	}{
		{"password-maps-to-native", "password", "native"},
		{"sso-maps-to-native", "sso", "native"},
		{"bootstrap-maps-to-native", "bootstrap", "native"},
		{"unknown-maps-to-unknown", "kinde", "unknown"},
		{"empty-maps-to-empty", "", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := NewMockStore().WithRole("admin")
			h := api.New(store, newQueueShim())
			mux := http.NewServeMux()
			h.Register(mux)

			provider := fakeAuthProvider{identity: auth.Identity{
				UserID:         "u-1",
				OrganizationID: "org-1",
				Role:           "admin",
				Email:          "x@example.com",
				AuthMode:       tc.authMode,
			}}
			wrapped := middleware.WrapNative(provider, mux)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
			wrapped.ServeHTTP(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d / %s", w.Code, w.Body.String())
			}
			var resp struct {
				AuthProvider string `json:"auth_provider"`
				AuthMode     string `json:"auth_mode"`
			}
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.AuthProvider != tc.wantAuthProvider {
				t.Errorf("auth_provider = %q; want %q", resp.AuthProvider, tc.wantAuthProvider)
			}
			if resp.AuthMode != tc.authMode {
				t.Errorf("auth_mode = %q; want %q", resp.AuthMode, tc.authMode)
			}
		})
	}
}

// TestGetMe_MembershipsArrayPresent locks in the B1.5 contract that /v1/me
// always serialises `memberships` as a JSON array (never null) and includes
// every active membership for the authenticated user with the org's display
// name. The frontend's org-switcher iterates this list directly.
func TestGetMe_MembershipsArrayPresent(t *testing.T) {
	store := NewMockStore().WithRole("admin")
	store.UserMemberships = []model.MembershipWithOrganization{
		{
			Membership:       model.Membership{OrganizationID: "organization-me", UserID: "user-me", Role: "admin"},
			OrganizationName: "Acme Co",
		},
		{
			Membership:       model.Membership{OrganizationID: "organization-other", UserID: "user-me", Role: "viewer"},
			OrganizationName: "Side Project",
		},
	}
	h := api.New(store, newQueueShim())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodGet, "/v1/me"))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Memberships []struct {
			OrganizationID   string `json:"organization_id"`
			OrganizationName string `json:"organization_name"`
			Role             string `json:"role"`
		} `json:"memberships"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Memberships) != 2 {
		t.Fatalf("got %d memberships, want 2: %+v", len(resp.Memberships), resp.Memberships)
	}
	if resp.Memberships[0].OrganizationID != "organization-me" || resp.Memberships[0].Role != "admin" {
		t.Errorf("first membership = %+v", resp.Memberships[0])
	}
	if resp.Memberships[1].OrganizationID != "organization-other" || resp.Memberships[1].Role != "viewer" {
		t.Errorf("second membership = %+v", resp.Memberships[1])
	}
	if resp.Memberships[0].OrganizationName != "Acme Co" {
		t.Errorf("organization_name not joined: %q", resp.Memberships[0].OrganizationName)
	}
}

// TestGetMe_MembershipsEmptyArrayNotNull guards the JSON shape: a user with
// zero memberships must still get `[]`, not `null`. Frontend code does
// `me.memberships.length` and would NPE on null.
func TestGetMe_MembershipsEmptyArrayNotNull(t *testing.T) {
	store := NewMockStore().WithRole("")
	// UserMemberships left nil → ListUserMemberships returns nil → handler
	// must still emit "memberships": [].
	h := api.New(store, newQueueShim())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodGet, "/v1/me"))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}

	// Raw-decode to distinguish [] from null in the wire output.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := string(raw["memberships"])
	if got != "[]" {
		t.Errorf("memberships wire shape = %q; want \"[]\" (empty array, not null)", got)
	}
}

// TestGetMe_DisplayName locks in the contract that /v1/me returns the
// authenticated user's display name (users.name) under the JSON key "name".
// Drives the dashboard's Profile page (issue #78). Returns the empty string
// when the row exists but name is unset; returns the empty string when the
// row lookup fails (best-effort — see slog.Warn in the handler).
func TestGetMe_DisplayName(t *testing.T) {
	store := NewMockStore().WithRole("admin").WithUsers([]model.User{
		{ID: "user-me", OrganizationID: "organization-me", Email: "me@example.com", Name: "Test User"},
	})
	h := api.New(store, newQueueShim())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodGet, "/v1/me"))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "Test User" {
		t.Errorf("name = %q; want \"Test User\"", resp.Name)
	}
}

// TestGetMe_DisplayNameAlwaysPresent guards the wire-shape contract: the
// "name" key MUST be in the JSON response even when the user row is missing
// from the store (handler degrades to empty string, never omits the field).
// Frontend code does `me.name || '—'` and would not handle `undefined`.
func TestGetMe_DisplayNameAlwaysPresent(t *testing.T) {
	store := NewMockStore().WithRole("admin") // no users seeded
	h := api.New(store, newQueueShim())
	mux := http.NewServeMux()
	h.Register(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, meRequest(http.MethodGet, "/v1/me"))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, ok := raw["name"]
	if !ok {
		t.Fatalf("`name` key missing from response: %s", w.Body.String())
	}
	if string(got) != `""` {
		t.Errorf("name = %s; want \"\" (empty string, not null/missing)", got)
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
