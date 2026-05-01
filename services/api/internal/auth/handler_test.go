package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/model"
)

// newHandlerTest wires a Handler against the in-package fakeStore and
// the in-memory cache. Returns the handler, the store, and the manager
// (so individual tests can poke state directly). auditFn is nil — use
// newHandlerTestWithAudit when a test needs to capture audit events.
func newHandlerTest(t *testing.T) (*auth.Handler, *fakeStore, *auth.Manager) {
	t.Helper()
	h, store, mgr, _ := newHandlerTestWithAudit(t)
	// Discard the audit capture — caller didn't ask for it.
	return h, store, mgr
}

// auditCapture records calls to the auth.AuditWriter closure so tests
// can assert that the bootstrap / invitation-redeem / password-reset
// flows emitted the documented audit actions.
type auditCapture struct {
	mu     sync.Mutex
	events []capturedAudit
}

type capturedAudit struct {
	OrgID, UserID, Action string
	Metadata              map[string]any
}

func (c *auditCapture) writer() auth.AuditWriter {
	return func(_ context.Context, orgID, userID, action string, metadata map[string]any) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.events = append(c.events, capturedAudit{
			OrgID: orgID, UserID: userID, Action: action, Metadata: metadata,
		})
	}
}

func (c *auditCapture) get() []capturedAudit {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedAudit, len(c.events))
	copy(out, c.events)
	return out
}

func newHandlerTestWithAudit(t *testing.T) (*auth.Handler, *fakeStore, *auth.Manager, *auditCapture) {
	t.Helper()
	store := newFakeStore()
	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })
	mgr := auth.NewManager(store, auth.NewSessionCache(mem), auth.Config{
		TTL:             time.Hour,
		SessionsPerUser: 10,
	})
	cap := &auditCapture{}
	h := auth.NewHandler(store, mgr, auth.NewCookieConfig(), cap.writer())
	return h, store, mgr, cap
}

func mux(h *auth.Handler) http.Handler {
	m := http.NewServeMux()
	h.Register(m)
	return m
}

func postJSON(t *testing.T, mux http.Handler, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	buf := &bytes.Buffer{}
	if body != nil {
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	r := httptest.NewRequest(http.MethodPost, path, buf)
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func mustDecode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(w.Body).Decode(&v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return v
}

// ── /v1/auth/bootstrap ──────────────────────────────────────────────────────

// seedInstallToken plants a bootstrap_state row. Returns the plaintext
// token so the test can submit it.
func seedInstallToken(t *testing.T, store *fakeStore) string {
	t.Helper()
	plaintext := "install-token-test-fixture-deadbeef"
	if _, err := store.CreateBootstrapState(context.Background(), auth.HashToken(plaintext), "test-pod"); err != nil {
		t.Fatalf("CreateBootstrapState: %v", err)
	}
	return plaintext
}

func TestBootstrapHappyPath(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	token := seedInstallToken(t, store)

	w := postJSON(t, mux(h), "/v1/auth/bootstrap", map[string]string{
		"token":             token,
		"email":             "owner@example.com",
		"name":              "Owner Person",
		"password":          "correct horse battery staple",
		"organization_name": "Acme Inc",
	}, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie on bootstrap response")
	}
	if !sessionCookie.HttpOnly {
		t.Error("session cookie should be HttpOnly")
	}
}

func TestBootstrapWrongTokenReturns401(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	_ = seedInstallToken(t, store)

	w := postJSON(t, mux(h), "/v1/auth/bootstrap", map[string]string{
		"token":    "wrong-token",
		"email":    "owner@example.com",
		"name":     "Owner",
		"password": "correct horse battery staple",
	}, nil)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401; body = %s", w.Code, w.Body.String())
	}
}

func TestBootstrapSealedAfterSuccess(t *testing.T) {
	// First bootstrap consumes the singleton; a second call must 409.
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	token := seedInstallToken(t, store)

	w1 := postJSON(t, mux(h), "/v1/auth/bootstrap", map[string]string{
		"token":    token,
		"email":    "first@example.com",
		"name":     "First",
		"password": "correct horse battery staple",
	}, nil)
	if w1.Code != http.StatusOK {
		t.Fatalf("first bootstrap failed: %d / %s", w1.Code, w1.Body.String())
	}

	w2 := postJSON(t, mux(h), "/v1/auth/bootstrap", map[string]string{
		"token":    token,
		"email":    "second@example.com",
		"name":     "Second",
		"password": "correct horse battery staple",
	}, nil)
	if w2.Code != http.StatusConflict {
		t.Errorf("second bootstrap status = %d; want 409 (sealed); body = %s", w2.Code, w2.Body.String())
	}
}

func TestBootstrapMissingFieldsReturns400(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	_ = seedInstallToken(t, store)

	w := postJSON(t, mux(h), "/v1/auth/bootstrap", map[string]string{
		"token": "x", "email": "", "password": "x", "name": "x",
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400; body = %s", w.Code, w.Body.String())
	}
}

// TestBootstrapTokenNeverInURL nails plan §4.6 acceptance AC7: the
// install token must NEVER appear in any URL — no Location header,
// no Set-Cookie value, no Referer (the request URL itself is just
// "/v1/auth/bootstrap"). Defence against accidental leakage via
// browser history, access logs, or copy-paste.
func TestBootstrapTokenNeverInURL(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	token := seedInstallToken(t, store)
	const literalToken = "install-token-test-fixture-deadbeef"
	if token != literalToken {
		t.Fatalf("seedInstallToken changed; update the test fixture")
	}

	w := postJSON(t, mux(h), "/v1/auth/bootstrap", map[string]string{
		"token": token, "email": "owner@example.com", "name": "Owner",
		"password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d / %s", w.Code, w.Body.String())
	}

	// 1. Response Location header must not carry the token (we don't
	//    redirect, but assert anyway in case a future change adds one).
	if loc := w.Header().Get("Location"); strings.Contains(loc, literalToken) {
		t.Errorf("Location header carries install token: %q", loc)
	}

	// 2. Set-Cookie must not carry the install token. The session
	//    cookie carries a session token (separate value); the install
	//    token must not appear anywhere in the cookie value.
	for _, c := range w.Result().Cookies() {
		if strings.Contains(c.Value, literalToken) {
			t.Errorf("cookie %q carries install token: %q", c.Name, c.Value)
		}
	}

	// 3. Response body must not echo the install token. The handler
	//    returns user/org JSON; if a future change adds a debug field
	//    that leaks the token, this catches it.
	if strings.Contains(w.Body.String(), literalToken) {
		t.Errorf("response body carries install token: %s", w.Body.String())
	}
}

func TestBootstrapEmitsAuditEvent(t *testing.T) {
	t.Parallel()
	h, store, _, cap := newHandlerTestWithAudit(t)
	token := seedInstallToken(t, store)

	w := postJSON(t, mux(h), "/v1/auth/bootstrap", map[string]string{
		"token": token, "email": "owner@example.com", "name": "Owner",
		"password":          "correct horse battery staple",
		"organization_name": "Acme Inc",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d / %s", w.Code, w.Body.String())
	}

	events := cap.get()
	if len(events) != 1 {
		t.Fatalf("audit events = %d; want 1", len(events))
	}
	if events[0].Action != model.AuditActionBootstrapCompleted {
		t.Errorf("action = %q; want bootstrap_completed", events[0].Action)
	}
	if events[0].OrgID == "" || events[0].UserID == "" {
		t.Errorf("event missing identity: %+v", events[0])
	}
	if events[0].Metadata["organization_name"] != "Acme Inc" {
		t.Errorf("organization_name not recorded in metadata: %+v", events[0].Metadata)
	}
}

func TestBootstrapWeakPasswordReturns400(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	token := seedInstallToken(t, store)

	w := postJSON(t, mux(h), "/v1/auth/bootstrap", map[string]string{
		"token":    token,
		"email":    "owner@example.com",
		"name":     "Owner",
		"password": "short", // below 12 chars
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400; body = %s", w.Code, w.Body.String())
	}
}

// ── /v1/auth/login ──────────────────────────────────────────────────────────

// seedAccount plants a user + membership directly into the fake. Skips
// the real bootstrap flow so login tests aren't entangled with bootstrap.
func seedAccount(t *testing.T, store *fakeStore, email, password string, mships int) {
	t.Helper()
	hash, err := auth.Hash(password)
	if err != nil {
		t.Fatalf("auth.Hash: %v", err)
	}
	now := time.Now().UTC()
	id := "u-" + email
	user := model.User{
		ID:            id,
		Email:         email,
		PasswordHash:  hash,
		PasswordSetAt: &now,
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.usersByEmail[email] = user
	store.usersByID[id] = user
	store.organizationsCount += int64(mships)
	out := make([]model.Membership, 0, mships)
	for i := 0; i < mships; i++ {
		orgID := "org-" + email + "-" + string(rune('a'+i))
		out = append(out, model.Membership{
			ID:             "m-" + email + "-" + string(rune('a'+i)),
			OrganizationID: orgID,
			UserID:         id,
			Role:           "owner",
			CreatedAt:      now,
			UpdatedAt:      now,
		})
		store.orgsByID[orgID] = "Org " + string(rune('A'+i))
	}
	store.memberships[id] = out
}

func TestLoginHappyPath(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 1)

	w := postJSON(t, mux(h), "/v1/auth/login", map[string]string{
		"email": "alice@example.com", "password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", w.Code, w.Body.String())
	}
	gotCookie := false
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			gotCookie = true
		}
	}
	if !gotCookie {
		t.Error("expected session cookie on successful login")
	}
}

func TestLoginWrongPasswordReturns401(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 1)

	w := postJSON(t, mux(h), "/v1/auth/login", map[string]string{
		"email": "alice@example.com", "password": "wrong password 12345",
	}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", w.Code)
	}
}

func TestLoginUnknownEmailReturns401(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlerTest(t)

	w := postJSON(t, mux(h), "/v1/auth/login", map[string]string{
		"email": "nobody@example.com", "password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", w.Code)
	}
}

// TestLoginMultiOrgReturns200WithPicker is the B1.5 §4.7.1 contract: a user
// with >1 active membership gets 200 OK with `{needs_org_selection: true,
// orgs: [{id, name}, ...]}` and **no Set-Cookie**. The frontend lands on
// /select-org and POSTs the chosen org_id back to /v1/auth/select-org with
// re-supplied credentials (slice 3).
func TestLoginMultiOrgReturns200WithPicker(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 2)

	w := postJSON(t, mux(h), "/v1/auth/login", map[string]string{
		"email": "alice@example.com", "password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", w.Code, w.Body.String())
	}
	// No session cookie on the picker branch — the picker step (slice 3)
	// re-validates the password before minting.
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			t.Errorf("multi-org login set %s cookie to %q; expected no session cookie at all", c.Name, c.Value)
		}
	}
	body := mustDecode[map[string]any](t, w)
	if body["needs_org_selection"] != true {
		t.Errorf("needs_org_selection = %v; want true", body["needs_org_selection"])
	}
	orgs, ok := body["orgs"].([]any)
	if !ok {
		t.Fatalf("orgs is not an array: %v", body["orgs"])
	}
	if len(orgs) != 2 {
		t.Fatalf("orgs length = %d; want 2 (seedAccount mships=2)", len(orgs))
	}
	first, _ := orgs[0].(map[string]any)
	if first["id"] == "" || first["name"] == "" {
		t.Errorf("first org missing id/name: %+v", first)
	}
}

// TestLoginMultiOrg_DBErrorReturns500 closes the slice-1 review concern: if
// ListUserMemberships fails on the multi-org branch, the handler must 500
// rather than silently degrade to the single-org flow (which would land
// the user in whichever org happened to be first in the list — wrong org).
func TestLoginMultiOrg_DBErrorReturns500(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 2)
	store.mu.Lock()
	store.listUserMembershipsErr = errors.New("simulated db outage")
	store.mu.Unlock()

	w := postJSON(t, mux(h), "/v1/auth/login", map[string]string{
		"email": "alice@example.com", "password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500 on ListUserMemberships failure; body = %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			t.Errorf("DB-error path set %s cookie to %q; must not mint", c.Name, c.Value)
		}
	}
}

// ── /v1/auth/select-org (Phase B1.5 slice 3) ──────────────────────────────

// TestSelectOrgHappyPath: multi-org user picks one of their orgs, password
// is re-validated, session minted bound to the chosen org, response carries
// the matching role.
func TestSelectOrgHappyPath(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 2)
	chosen := store.memberships["u-alice@example.com"][1].OrganizationID

	w := postJSON(t, mux(h), "/v1/auth/select-org", map[string]string{
		"email":           "alice@example.com",
		"password":        "correct horse battery staple",
		"organization_id": chosen,
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", w.Code, w.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("expected non-empty session cookie after successful org pick")
	}
	body := mustDecode[map[string]any](t, w)
	org, _ := body["organization"].(map[string]any)
	if got := org["id"]; got != chosen {
		t.Errorf("response organization.id = %v; want %s (the picked org)", got, chosen)
	}
}

// TestSelectOrg_WrongPasswordReturns401: defence in depth — even if the
// caller passed the picker step, a wrong password here must reject. Never
// trust the frontend to remember step 1.
func TestSelectOrg_WrongPasswordReturns401(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 2)
	chosen := store.memberships["u-alice@example.com"][0].OrganizationID

	w := postJSON(t, mux(h), "/v1/auth/select-org", map[string]string{
		"email":           "alice@example.com",
		"password":        "wrong password 123456",
		"organization_id": chosen,
	}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			t.Errorf("wrong-password select-org set %s cookie to %q", c.Name, c.Value)
		}
	}
}

// TestSelectOrg_OrgNotInMembershipsReturns401: same 401 shape as
// wrong-password — distinguishing them at the wire level would let an
// attacker who has valid creds probe arbitrary org IDs for membership.
func TestSelectOrg_OrgNotInMembershipsReturns401(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 2)

	w := postJSON(t, mux(h), "/v1/auth/select-org", map[string]string{
		"email":           "alice@example.com",
		"password":        "correct horse battery staple",
		"organization_id": "org-someone-else",
	}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", w.Code)
	}
	// Pin the no-session invariant: even with a valid email + password,
	// a wrong org_id must NOT mint a session. A future refactor that
	// accidentally moved MintSession before the !found check would 200
	// the response (caught by status check) but might still leave a
	// half-baked Set-Cookie behind in some intermediate state — guard
	// against that explicitly.
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			t.Errorf("invalid org_id select-org set %s cookie to %q; must not mint", c.Name, c.Value)
		}
	}
	body := mustDecode[map[string]any](t, w)
	if body["error"] != "invalid_credentials" {
		t.Errorf("error = %v; want invalid_credentials (collapsed shape, not org_not_in_set)", body["error"])
	}
}

// TestSelectOrg_UnknownEmailReturns401: timing-flat 401 (we do a
// placeholder Verify in the handler to keep the latency envelope flat).
func TestSelectOrg_UnknownEmailReturns401(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlerTest(t)

	w := postJSON(t, mux(h), "/v1/auth/select-org", map[string]string{
		"email":           "ghost@example.com",
		"password":        "correct horse battery staple",
		"organization_id": "org-anything",
	}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", w.Code)
	}
}

// TestSelectOrg_MissingFieldsReturns400: organization_id is mandatory; not
// supplying it (or email/password) is a client-side bug, not a credential
// failure — distinguish with 400.
func TestSelectOrg_MissingFieldsReturns400(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlerTest(t)
	cases := []map[string]string{
		{"email": "a@b.com", "password": "x"},                                  // no org_id
		{"email": "a@b.com", "organization_id": "org-x"},                       // no password
		{"password": "correct horse battery staple", "organization_id": "org"}, // no email
	}
	for i, body := range cases {
		w := postJSON(t, mux(h), "/v1/auth/select-org", body, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("case %d body %+v status = %d; want 400", i, body, w.Code)
		}
	}
}

// TestSelectOrgRateLimitedSharesBudgetWithLogin: an attacker can't double
// their per-IP budget against one email by alternating /login and
// /select-org. The shared rate limiter is the contract.
//
// Test exercises the per-IP cap (perIP=1, perEmail=100). The per-email cap
// path is covered for /login by TestLoginRateLimitedEmailCapReturns429 —
// /select-org inherits the same limiter instance so it's transitively
// covered. A dedicated cross-endpoint email-cap test isn't worth the
// scaffolding for B1.5.
func TestSelectOrgRateLimitedSharesBudgetWithLogin(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 2)

	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })
	// WithLoginRateLimit returns the receiver — we assign the return value
	// to keep the test resilient if the method is ever changed to clone
	// (rather than mutate in place). Today it's a pointer-receiver mutation,
	// but writing it the right way costs nothing.
	h = h.WithLoginRateLimit(auth.NewLoginRateLimiter(mem).WithLimits(1, 100))

	body := map[string]string{
		"email": "alice@example.com", "password": "wrong password 12345",
		"organization_id": store.memberships["u-alice@example.com"][0].OrganizationID,
	}

	// First attempt: 401 (wrong password). Budget consumed by /login.
	loginBody := map[string]string{"email": "alice@example.com", "password": "wrong password 12345"}
	first := postJSON(t, mux(h), "/v1/auth/login", loginBody, nil)
	if first.Code != http.StatusUnauthorized {
		t.Fatalf("login first attempt status = %d; want 401", first.Code)
	}

	// Second attempt at /select-org: same IP, same email — must 429
	// because /login already consumed the budget.
	second := postJSON(t, mux(h), "/v1/auth/select-org", body, nil)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("select-org status = %d; want 429 (shared budget with /login)", second.Code)
	}
}

// TestLoginSingleOrg_DoesNotCallListUserMemberships locks in that the
// happy single-org path doesn't pay for the org-picker join — that lookup
// only fires in the multi-membership branch. We assert this by injecting
// an error and confirming login still succeeds for a single-org user.
func TestLoginSingleOrg_DoesNotCallListUserMemberships(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 1)
	store.mu.Lock()
	store.listUserMembershipsErr = errors.New("should not be called")
	store.mu.Unlock()

	w := postJSON(t, mux(h), "/v1/auth/login", map[string]string{
		"email": "alice@example.com", "password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (single-org happy path); body = %s", w.Code, w.Body.String())
	}
}

// ── /v1/auth/logout ─────────────────────────────────────────────────────────

func TestLoginRateLimitedReturns429(t *testing.T) {
	// Plan §4.2 acceptance: 11th login from same IP returns 429.
	// Wires a 1/min limiter to keep the test fast.
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 1)

	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })
	h.WithLoginRateLimit(auth.NewLoginRateLimiter(mem).WithLimits(1, 100))

	body := map[string]string{"email": "alice@example.com", "password": "wrong password 12345"}

	// First attempt: 401 (wrong password) — rate-limit budget consumed.
	first := postJSON(t, mux(h), "/v1/auth/login", body, nil)
	if first.Code != http.StatusUnauthorized {
		t.Fatalf("first attempt status = %d; want 401", first.Code)
	}

	// Second attempt: blocked by rate limiter, 429 with Retry-After.
	second := postJSON(t, mux(h), "/v1/auth/login", body, nil)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second attempt status = %d; want 429", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}
	var resp map[string]any
	if err := json.NewDecoder(second.Body).Decode(&resp); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if resp["error"] != "rate_limited" {
		t.Errorf("error = %v; want rate_limited", resp["error"])
	}
}

func TestLoginRateLimitedEmailCapReturns429(t *testing.T) {
	// Same shape as the IP-cap test but parameterised so the per-email
	// counter trips first. perIP=100 is effectively unlimited; perEmail=1
	// means the second attempt against the same email — even from a
	// different request — gets the 429.
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "victim@example.com", "correct horse battery staple", 1)

	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })
	h.WithLoginRateLimit(auth.NewLoginRateLimiter(mem).WithLimits(100, 1))

	body := map[string]string{"email": "victim@example.com", "password": "wrong password 12345"}

	first := postJSON(t, mux(h), "/v1/auth/login", body, nil)
	if first.Code != http.StatusUnauthorized {
		t.Fatalf("first attempt status = %d; want 401", first.Code)
	}
	second := postJSON(t, mux(h), "/v1/auth/login", body, nil)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second attempt status = %d; want 429 (email cap)", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 from email cap")
	}
}

func TestLogoutRevokesSessionAndClearsCookie(t *testing.T) {
	t.Parallel()
	h, store, mgr := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 1)

	mint, err := mgr.MintSession(context.Background(), auth.MintRequest{
		UserID:         "u-alice@example.com",
		OrganizationID: "org-alice@example.com-a",
		AuthMode:       model.AuthModePassword,
	})
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}

	w := postJSON(t, mux(h), "/v1/auth/logout", nil, &http.Cookie{
		Name: auth.SessionCookieName, Value: mint.PlaintextToken,
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want 204; body = %s", w.Code, w.Body.String())
	}
	// Cookie cleared (MaxAge < 0).
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("expected logout to clear the session cookie")
	}
	// Session row revoked.
	got, err := store.GetSessionByTokenHash(context.Background(), mint.Session.SessionTokenHash)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash after logout: %v", err)
	}
	if got.RevokedAt == nil {
		t.Error("logout did not revoke the session row")
	}
}

func TestLogoutToleratesNoCookie(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlerTest(t)

	w := postJSON(t, mux(h), "/v1/auth/logout", nil, nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d; want 204 even without cookie", w.Code)
	}
}

func TestLogoutToleratesUnknownToken(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlerTest(t)

	w := postJSON(t, mux(h), "/v1/auth/logout", nil, &http.Cookie{
		Name: auth.SessionCookieName, Value: "totally-unknown-token",
	})
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d; want 204 even for unknown token", w.Code)
	}
}

// ── /v1/auth/invitations/redeem ─────────────────────────────────────────────

func seedNativeInvitation(t *testing.T, store *fakeStore, orgID, email, role string) string {
	t.Helper()
	plaintext := "invite-token-fixture-" + email
	store.seedInvitation(orgID, email, role, auth.HashToken(plaintext))
	return plaintext
}

func TestRedeemInvitationHappyPath(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	token := seedNativeInvitation(t, store, "org-1", "newbie@example.com", "member")

	w := postJSON(t, mux(h), "/v1/auth/invitations/redeem", map[string]string{
		"token":    token,
		"password": "correct horse battery staple",
		"name":     "New Bee",
	}, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", w.Code, w.Body.String())
	}
	var got struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"user"`
		Org struct {
			ID string `json:"id"`
		} `json:"organization"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.User.Email != "newbie@example.com" {
		t.Errorf("user.email = %q; want newbie@example.com", got.User.Email)
	}
	if got.User.Role != "member" {
		t.Errorf("user.role = %q; want member", got.User.Role)
	}
	if got.Org.ID != "org-1" {
		t.Errorf("org.id = %q; want org-1", got.Org.ID)
	}
	// Cookie set.
	cookieFound := false
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			cookieFound = true
		}
	}
	if !cookieFound {
		t.Error("expected session cookie on successful invite redemption")
	}
}

func TestRedeemInvitationSingleUse(t *testing.T) {
	// A redeemed token can't be redeemed again — Store.RedeemNativeInvitation
	// deletes the row in the same tx that creates the membership.
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	token := seedNativeInvitation(t, store, "org-1", "single@example.com", "viewer")

	if w := postJSON(t, mux(h), "/v1/auth/invitations/redeem", map[string]string{
		"token": token, "password": "correct horse battery staple", "name": "Once",
	}, nil); w.Code != http.StatusOK {
		t.Fatalf("first redeem status = %d", w.Code)
	}

	w := postJSON(t, mux(h), "/v1/auth/invitations/redeem", map[string]string{
		"token": token, "password": "another correct horse staple", "name": "Twice",
	}, nil)
	if w.Code != http.StatusGone {
		t.Errorf("second redeem status = %d; want 410", w.Code)
	}
}

func TestRedeemInvitationUnknownTokenReturns410(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlerTest(t)

	w := postJSON(t, mux(h), "/v1/auth/invitations/redeem", map[string]string{
		"token": "completely-unknown-token", "password": "correct horse battery staple", "name": "Ghost",
	}, nil)
	if w.Code != http.StatusGone {
		t.Errorf("status = %d; want 410", w.Code)
	}
}

func TestRedeemInvitationEmitsAuditEvent(t *testing.T) {
	t.Parallel()
	h, store, _, cap := newHandlerTestWithAudit(t)
	token := seedNativeInvitation(t, store, "org-redeem", "joiner@example.com", "viewer")

	w := postJSON(t, mux(h), "/v1/auth/invitations/redeem", map[string]string{
		"token": token, "password": "correct horse battery staple", "name": "Joiner",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d / %s", w.Code, w.Body.String())
	}
	events := cap.get()
	if len(events) != 1 {
		t.Fatalf("audit events = %d; want 1", len(events))
	}
	got := events[0]
	if got.Action != model.AuditActionInvitationRedeemedNative {
		t.Errorf("action = %q; want invitation_redeemed_native", got.Action)
	}
	if got.OrgID != "org-redeem" {
		t.Errorf("org = %q; want org-redeem (from invitation row)", got.OrgID)
	}
	if got.Metadata["role"] != "viewer" {
		t.Errorf("role metadata = %v; want viewer", got.Metadata["role"])
	}
}

func TestRedeemInvitationWeakPasswordReturns400(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	token := seedNativeInvitation(t, store, "org-1", "weak@example.com", "viewer")

	w := postJSON(t, mux(h), "/v1/auth/invitations/redeem", map[string]string{
		"token": token, "password": "short", "name": "Weak",
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", w.Code)
	}
}

// ── AC2: install-token file is removed post-bootstrap ──────────────────────

func TestBootstrapRemovesInstallTokenFile(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel(); env mutations must
	// be process-global for the duration of the test.
	dir := t.TempDir()
	tokenFile := dir + "/initial_setup_token"
	t.Setenv("BOOTSTRAP_TOKEN_FILE_PATH", tokenFile)

	h, store, _ := newHandlerTest(t)
	token := seedInstallToken(t, store)

	// Plant the file as if MaybeGenerateInstallToken had written it.
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		t.Fatalf("seed token file: %v", err)
	}

	w := postJSON(t, mux(h), "/v1/auth/bootstrap", map[string]string{
		"token": token, "email": "owner@example.com", "name": "Owner",
		"password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap failed: %d / %s", w.Code, w.Body.String())
	}

	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Errorf("token file %q should be removed post-bootstrap; stat err = %v", tokenFile, err)
	}
}

// ── /v1/auth/password-reset/redeem ──────────────────────────────────────────

func seedPasswordReset(t *testing.T, store *fakeStore, userID string) string {
	t.Helper()
	plaintext := "reset-token-fixture-" + userID
	if err := store.CreatePasswordReset(
		context.Background(),
		"reset-"+userID, userID, "org-x",
		auth.HashToken(plaintext),
		"admin-1",
		time.Now().UTC().Add(time.Hour),
	); err != nil {
		t.Fatalf("CreatePasswordReset: %v", err)
	}
	return plaintext
}

func TestRedeemPasswordResetHappyPath(t *testing.T) {
	t.Parallel()
	h, store, mgr := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "old password 12345", 1)

	// Mint a session BEFORE the reset so we can assert it gets revoked.
	mint, err := mgr.MintSession(context.Background(), auth.MintRequest{
		UserID:         "u-alice@example.com",
		OrganizationID: "org-alice@example.com-a",
		AuthMode:       model.AuthModePassword,
	})
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}

	token := seedPasswordReset(t, store, "u-alice@example.com")
	w := postJSON(t, mux(h), "/v1/auth/password-reset/redeem", map[string]string{
		"token":        token,
		"new_password": "brand new password 67890",
	}, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want 204; body = %s", w.Code, w.Body.String())
	}

	// Pre-existing session must now be revoked (architect: reset
	// implies all sessions are potentially compromised).
	got, err := store.GetSessionByTokenHash(context.Background(), mint.Session.SessionTokenHash)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash: %v", err)
	}
	if got.RevokedAt == nil {
		t.Error("password reset must revoke all live sessions for the user")
	}
}

func TestRedeemPasswordResetUnknownToken(t *testing.T) {
	t.Parallel()
	h, _, _ := newHandlerTest(t)

	w := postJSON(t, mux(h), "/v1/auth/password-reset/redeem", map[string]string{
		"token":        "completely-bogus-token",
		"new_password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusGone {
		t.Errorf("status = %d; want 410", w.Code)
	}
}

func TestRedeemPasswordResetSingleUse(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "single@example.com", "old password 12345", 1)
	token := seedPasswordReset(t, store, "u-single@example.com")

	if w := postJSON(t, mux(h), "/v1/auth/password-reset/redeem", map[string]string{
		"token": token, "new_password": "correct horse battery staple",
	}, nil); w.Code != http.StatusNoContent {
		t.Fatalf("first redeem status = %d", w.Code)
	}

	w := postJSON(t, mux(h), "/v1/auth/password-reset/redeem", map[string]string{
		"token": token, "new_password": "another correct horse staple",
	}, nil)
	if w.Code != http.StatusGone {
		t.Errorf("second redeem status = %d; want 410", w.Code)
	}
}

func TestRedeemPasswordResetWeakPasswordReturns400(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "weak@example.com", "old password 12345", 1)
	token := seedPasswordReset(t, store, "u-weak@example.com")

	w := postJSON(t, mux(h), "/v1/auth/password-reset/redeem", map[string]string{
		"token": token, "new_password": "short",
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", w.Code)
	}
}

func TestRedeemPasswordResetEmitsAuditEvent(t *testing.T) {
	t.Parallel()
	h, store, _, cap := newHandlerTestWithAudit(t)
	seedAccount(t, store, "audited@example.com", "old password 12345", 1)
	token := seedPasswordReset(t, store, "u-audited@example.com")

	w := postJSON(t, mux(h), "/v1/auth/password-reset/redeem", map[string]string{
		"token": token, "new_password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	events := cap.get()
	if len(events) != 1 {
		t.Fatalf("audit events = %d; want 1", len(events))
	}
	got := events[0]
	if got.Action != model.AuditActionUserPasswordResetRedeemed {
		t.Errorf("action = %q; want user_password_reset_redeemed", got.Action)
	}
	// The redeem flow has no auth context — org must come from the
	// password_resets row itself. seedPasswordReset uses "org-x".
	if got.OrgID != "org-x" {
		t.Errorf("org = %q; want org-x (from password_resets row)", got.OrgID)
	}
	if got.UserID != "u-audited@example.com" {
		t.Errorf("user = %q; want u-audited@example.com", got.UserID)
	}
}

func TestRedeemPasswordResetExpiredReturns410(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "expired@example.com", "old password 12345", 1)

	plaintext := "expired-reset-fixture"
	// Insert a row with expires_at in the past.
	if err := store.CreatePasswordReset(
		context.Background(),
		"reset-expired", "u-expired@example.com", "org-x",
		auth.HashToken(plaintext),
		"admin-1",
		time.Now().UTC().Add(-1*time.Minute),
	); err != nil {
		t.Fatalf("CreatePasswordReset: %v", err)
	}
	w := postJSON(t, mux(h), "/v1/auth/password-reset/redeem", map[string]string{
		"token": plaintext, "new_password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusGone {
		t.Errorf("status = %d; want 410 for expired token", w.Code)
	}
}

func TestBootstrapWithDisabledFileSkipsRemoval(t *testing.T) {
	// BOOTSTRAP_TOKEN_FILE_PATH="" disables file management. The
	// bootstrap handler must not error trying to remove a path that
	// was never written. (t.Setenv prohibits t.Parallel.)
	t.Setenv("BOOTSTRAP_TOKEN_FILE_PATH", "")

	h, store, _ := newHandlerTest(t)
	token := seedInstallToken(t, store)

	w := postJSON(t, mux(h), "/v1/auth/bootstrap", map[string]string{
		"token": token, "email": "owner@example.com", "name": "Owner",
		"password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d / %s", w.Code, w.Body.String())
	}
}
