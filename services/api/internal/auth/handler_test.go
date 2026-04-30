package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/api/internal/auth"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/model"
)

// newHandlerTest wires a Handler against the in-package fakeStore and
// the in-memory cache. Returns the handler, the store, and the manager
// (so individual tests can poke state directly).
func newHandlerTest(t *testing.T) (*auth.Handler, *fakeStore, *auth.Manager) {
	t.Helper()
	store := newFakeStore()
	mem := cache.New("")
	t.Cleanup(func() { _ = mem.Close() })
	mgr := auth.NewManager(store, auth.NewSessionCache(mem), auth.Config{
		TTL:             time.Hour,
		SessionsPerUser: 10,
	})
	h := auth.NewHandler(store, mgr, auth.NewCookieConfig(true /* DEV — Secure off */), nil)
	return h, store, mgr
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
		out = append(out, model.Membership{
			ID:             "m-" + email + "-" + string(rune('a'+i)),
			OrganizationID: "org-" + email + "-" + string(rune('a'+i)),
			UserID:         id,
			Role:           "owner",
			CreatedAt:      now,
			UpdatedAt:      now,
		})
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

func TestLoginMultiOrgReturns409(t *testing.T) {
	t.Parallel()
	h, store, _ := newHandlerTest(t)
	seedAccount(t, store, "alice@example.com", "correct horse battery staple", 2)

	w := postJSON(t, mux(h), "/v1/auth/login", map[string]string{
		"email": "alice@example.com", "password": "correct horse battery staple",
	}, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409; body = %s", w.Code, w.Body.String())
	}
	body := mustDecode[map[string]any](t, w)
	if body["error"] != "multi_org_not_supported" {
		t.Errorf("error code = %v; want multi_org_not_supported", body["error"])
	}
	if body["b15_pending"] != true {
		t.Errorf("b15_pending = %v; want true (frontend marker for B1.5 picker)", body["b15_pending"])
	}
}

// ── /v1/auth/logout ─────────────────────────────────────────────────────────

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
