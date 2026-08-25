package staff_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/staff"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/model"
)

const testPassword = "correct-horse-battery"

// harness wires a full staff handler chain (WrapStaff → mux) over an in-memory
// cache + mock store, so tests exercise the real auth middleware.
type harness struct {
	store    *mockStore
	sessions *staff.SessionManager
	handler  http.Handler
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	c := cache.New("") // in-memory
	store := newMockStore()
	sessions := staff.NewSessionManager(c, time.Hour)
	provider := staff.NewSessionProvider(store, sessions)
	h := staff.NewHandler(store, sessions, provider, nil)

	mux := http.NewServeMux()
	h.Register(mux)
	return &harness{store: store, sessions: sessions, handler: staff.WrapStaff(provider, mux)}
}

func mustHash(t *testing.T) string {
	t.Helper()
	hash, err := auth.Hash(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return hash
}

// login performs a login and returns the session cookie (nil if login failed).
func (h *harness) login(t *testing.T, email, password string) *http.Cookie {
	t.Helper()
	body := `{"email":` + jsonStr(email) + `,"password":` + jsonStr(password) + `}`
	req := httptest.NewRequest(http.MethodPost, "/admin/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return nil
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == staff.StaffSessionCookieName {
			return c
		}
	}
	return nil
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (h *harness) do(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, r)
	return rec
}

func TestLogin_HappyPath(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "ada@axiaops.io", "Ada", mustHash(t), "active", model.StaffRoleSupport)

	cookie := h.login(t, "ada@axiaops.io", testPassword)
	if cookie == nil {
		t.Fatal("expected a session cookie on successful login")
	}
	if !cookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
}

func TestLogin_WrongPassword_Collapses401(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "ada@axiaops.io", "Ada", mustHash(t), "active", model.StaffRoleSupport)

	rec := h.do(http.MethodPost, "/admin/auth/login", `{"email":"ada@axiaops.io","password":"wrong-password-here"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_credentials") {
		t.Errorf("want invalid_credentials, got %s", rec.Body.String())
	}
}

func TestLogin_UnknownEmail_SameShapeAsWrongPassword(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodPost, "/admin/auth/login", `{"email":"nobody@axiaops.io","password":"whatever-secret"}`, nil)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "invalid_credentials") {
		t.Fatalf("unknown email must collapse to 401 invalid_credentials, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestLogin_SuspendedStaff_Rejected(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "ada@axiaops.io", "Ada", mustHash(t), "suspended", model.StaffRoleSupport)
	if c := h.login(t, "ada@axiaops.io", testPassword); c != nil {
		t.Fatal("suspended staff must not receive a session")
	}
}

func TestProtectedRoute_RequiresAuth(t *testing.T) {
	h := newHarness(t)
	rec := h.do(http.MethodGet, "/admin/tenants", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for unauthenticated /admin/tenants, got %d", rec.Code)
	}
}

func TestMe_ReturnsRoles(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "ops@axiaops.io", "Ops", mustHash(t), "active", model.StaffRoleOps)
	cookie := h.login(t, "ops@axiaops.io", testPassword)
	if cookie == nil {
		t.Fatal("login failed")
	}
	rec := h.do(http.MethodGet, "/admin/me", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ops"`) {
		t.Errorf("me should report the ops role, got %s", rec.Body.String())
	}
}

func TestListTenants_Authed(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "sup@axiaops.io", "Sup", mustHash(t), "active", model.StaffRoleSupport)
	h.store.orgs = []model.Organization{
		{ID: "org-1", OrgCode: "acme", Name: "Acme"},
		{ID: "org-2", OrgCode: "globex", Name: "Globex"},
	}
	cookie := h.login(t, "sup@axiaops.io", testPassword)
	rec := h.do(http.MethodGet, "/admin/tenants", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "acme") || !strings.Contains(rec.Body.String(), "globex") {
		t.Errorf("expected both orgs, got %s", rec.Body.String())
	}
}

func TestGetTenant_NotFound(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "sup@axiaops.io", "Sup", mustHash(t), "active", model.StaffRoleSupport)
	cookie := h.login(t, "sup@axiaops.io", testPassword)
	rec := h.do(http.MethodGet, "/admin/tenants/nope", "", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestGetTenant_SummaryHasNoFinOpsDetail(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "sup@axiaops.io", "Sup", mustHash(t), "active", model.StaffRoleSupport)
	h.store.summaries["org-1"] = model.StaffTenantSummary{
		OrganizationID: "org-1", OrgCode: "acme", Name: "Acme", AccountCount: 3, LatestTotalZombies: 7,
	}
	cookie := h.login(t, "sup@axiaops.io", testPassword)
	rec := h.do(http.MethodGet, "/admin/tenants/org-1", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// entitlement no longer exists as a concept — the field must be absent
	// entirely, not present-and-null.
	if _, present := got["entitlement"]; present {
		t.Errorf("entitlement field should no longer be present, got %v", got["entitlement"])
	}
	// must NOT leak per-zombie/cost rows — only the aggregate count.
	if _, leak := got["zombies"]; leak {
		t.Error("tenant summary must not include zombie detail rows")
	}
}

// ── staff management (superadmin) ───────────────────────────────────────────

func TestCreateStaff_RequiresSuperadmin(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "sup@axiaops.io", "Sup", mustHash(t), "active", model.StaffRoleSupport)
	cookie := h.login(t, "sup@axiaops.io", testPassword)
	rec := h.do(http.MethodPost, "/admin/staff",
		`{"email":"new@axiaops.io","name":"New","password":"correct-horse-battery","roles":["support"]}`, cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("support must not create staff, want 403 got %d", rec.Code)
	}
}

func TestCreateStaff_SuperadminSucceeds(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "boss@axiaops.io", "Boss", mustHash(t), "active", model.StaffRoleSuperadmin)
	cookie := h.login(t, "boss@axiaops.io", testPassword)
	rec := h.do(http.MethodPost, "/admin/staff",
		`{"email":"new@axiaops.io","name":"New","password":"correct-horse-battery","roles":["support","ops"]}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	if _, _, err := h.store.LookupStaffUserByEmail(context.Background(), "new@axiaops.io"); err != nil {
		t.Errorf("created staff should be lookup-able: %v", err)
	}
}

func TestCreateStaff_WeakPassword(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "boss@axiaops.io", "Boss", mustHash(t), "active", model.StaffRoleSuperadmin)
	cookie := h.login(t, "boss@axiaops.io", testPassword)
	rec := h.do(http.MethodPost, "/admin/staff",
		`{"email":"new@axiaops.io","name":"New","password":"short","roles":["support"]}`, cookie)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "weak_password") {
		t.Fatalf("want 400 weak_password, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestCreateStaff_BreachedPassword covers Tasks.md 2.7.11 at the staff-create
// site: a 12-char password in the breach corpus is rejected even though it
// passes the length floor.
func TestCreateStaff_BreachedPassword(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.store.addStaff("s1", "boss@axiaops.io", "Boss", mustHash(t), "active", model.StaffRoleSuperadmin)
	cookie := h.login(t, "boss@axiaops.io", testPassword)
	rec := h.do(http.MethodPost, "/admin/staff",
		`{"email":"new@axiaops.io","name":"New","password":"password1234","roles":["support"]}`, cookie)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "weak_password") {
		t.Fatalf("want 400 weak_password, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestCreateStaff_IdentityLookalikePassword covers the identity reject wired via
// CheckPolicyWithIdentity: a long, non-breached password embedding the new
// staffer's email local-part is rejected.
func TestCreateStaff_IdentityLookalikePassword(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.store.addStaff("s1", "boss@axiaops.io", "Boss", mustHash(t), "active", model.StaffRoleSuperadmin)
	cookie := h.login(t, "boss@axiaops.io", testPassword)
	rec := h.do(http.MethodPost, "/admin/staff",
		`{"email":"newstaffer@axiaops.io","name":"New Staffer","password":"newstaffer-secret99","roles":["support"]}`, cookie)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "weak_password") {
		t.Fatalf("want 400 weak_password, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeRole_LastSuperadminGuard(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "boss@axiaops.io", "Boss", mustHash(t), "active", model.StaffRoleSuperadmin)
	cookie := h.login(t, "boss@axiaops.io", testPassword)
	rec := h.do(http.MethodDelete, "/admin/staff/s1/roles/superadmin", "", cookie)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "last_superadmin") {
		t.Fatalf("want 409 last_superadmin, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeRole_SucceedsWithTwoSuperadmins(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "boss@axiaops.io", "Boss", mustHash(t), "active", model.StaffRoleSuperadmin)
	h.store.addStaff("s2", "boss2@axiaops.io", "Boss2", mustHash(t), "active", model.StaffRoleSuperadmin)
	cookie := h.login(t, "boss@axiaops.io", testPassword)
	rec := h.do(http.MethodDelete, "/admin/staff/s2/roles/superadmin", "", cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestLogout_ClearsSession(t *testing.T) {
	h := newHarness(t)
	h.store.addStaff("s1", "ada@axiaops.io", "Ada", mustHash(t), "active", model.StaffRoleSupport)
	cookie := h.login(t, "ada@axiaops.io", testPassword)
	if rec := h.do(http.MethodPost, "/admin/auth/logout", "", cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("logout want 204, got %d", rec.Code)
	}
	// The session is revoked server-side: reusing the cookie now 401s.
	if rec := h.do(http.MethodGet, "/admin/me", "", cookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie should 401, got %d", rec.Code)
	}
}
