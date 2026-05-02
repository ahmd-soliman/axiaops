package sso_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
)

// fakeInitiateStore implements sso.InitiateStore for handler tests.
type fakeInitiateStore struct {
	conn model.SSOConnection
	err  error
}

func (f fakeInitiateStore) GetSSOConnectionByID(_ context.Context, _ string) (model.SSOConnection, error) {
	return f.conn, f.err
}

// initiateHandlerWithMux wraps the initiate handler in a mux so tests can
// exercise path-value parsing through the real route shape.
func initiateHandlerWithMux(t *testing.T, store sso.InitiateStore, idp *idpFixture) http.Handler {
	t.Helper()
	v := sso.NewValidator(newMockCache())
	v.SetHTTPClient(idp.server.Client())
	ss := sso.NewStateStore(newMockCache())
	mux := http.NewServeMux()
	mux.Handle("GET /v1/sso/oidc/{cid}/initiate",
		sso.NewInitiateHandler(store, v, ss, "https://app.example.com"))
	return mux
}

func activeOIDCConn(idp *idpFixture, cid string) model.SSOConnection {
	return model.SSOConnection{
		ID:               cid,
		OrganizationID:   "org-test",
		Protocol:         model.SSOProtocolOIDC,
		Status:           model.SSOStatusActive,
		OIDCClientID:     "client-test",
		OIDCDiscoveryURL: idp.discoveryURL,
	}
}

// ─── happy path ─────────────────────────────────────────────────────────────

func TestInitiate_Redirects_WithAllRequiredParams(t *testing.T) {
	idp := newIDPFixture(t)
	cid := "conn-1"
	store := fakeInitiateStore{conn: activeOIDCConn(idp, cid)}
	h := initiateHandlerWithMux(t, store, idp)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sso/oidc/"+cid+"/initiate?email=alice@acme.com", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status: got %d want %d. body=%q", rec.Code, http.StatusFound, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("no Location header on 302")
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	q := u.Query()

	for _, key := range []string{"response_type", "client_id", "redirect_uri", "scope",
		"state", "nonce", "code_challenge", "code_challenge_method", "login_hint"} {
		if q.Get(key) == "" {
			t.Errorf("authorize URL missing %q param: %s", key, loc)
		}
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type: got %q want code", q.Get("response_type"))
	}
	if q.Get("client_id") != "client-test" {
		t.Errorf("client_id: got %q want client-test", q.Get("client_id"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method: got %q want S256", q.Get("code_challenge_method"))
	}
	wantRedirectURI := "https://app.example.com/v1/sso/oidc/" + cid + "/callback"
	if q.Get("redirect_uri") != wantRedirectURI {
		t.Errorf("redirect_uri: got %q want %q", q.Get("redirect_uri"), wantRedirectURI)
	}
	if q.Get("login_hint") != "alice@acme.com" {
		t.Errorf("login_hint: got %q want alice@acme.com", q.Get("login_hint"))
	}
	if !strings.HasPrefix(loc, idp.server.URL+"/authorize") {
		t.Errorf("redirect target should be IdP authorize endpoint, got %s", loc)
	}
}

func TestInitiate_OmitsLoginHint_WhenEmailQueryAbsent(t *testing.T) {
	idp := newIDPFixture(t)
	cid := "conn-1"
	store := fakeInitiateStore{conn: activeOIDCConn(idp, cid)}
	h := initiateHandlerWithMux(t, store, idp)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sso/oidc/"+cid+"/initiate", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusFound)
	}
	u, _ := url.Parse(rec.Header().Get("Location"))
	if u.Query().Has("login_hint") {
		t.Errorf("login_hint should be absent when ?email= not supplied; got %s", u.RawQuery)
	}
}

// ─── error paths ────────────────────────────────────────────────────────────

func TestInitiate_ConnectionNotFound_Returns404(t *testing.T) {
	idp := newIDPFixture(t)
	store := fakeInitiateStore{err: storage.ErrSSOConnectionNotFound}
	h := initiateHandlerWithMux(t, store, idp)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sso/oidc/missing/initiate", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d want %d", rec.Code, http.StatusNotFound)
	}
}

func TestInitiate_DraftConnection_Returns400(t *testing.T) {
	idp := newIDPFixture(t)
	conn := activeOIDCConn(idp, "conn-1")
	conn.Status = model.SSOStatusDraft
	h := initiateHandlerWithMux(t, fakeInitiateStore{conn: conn}, idp)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sso/oidc/conn-1/initiate", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("draft connection: got %d want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestInitiate_NonOIDCProtocol_Returns400(t *testing.T) {
	idp := newIDPFixture(t)
	conn := activeOIDCConn(idp, "conn-1")
	conn.Protocol = "saml"
	h := initiateHandlerWithMux(t, fakeInitiateStore{conn: conn}, idp)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sso/oidc/conn-1/initiate", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("saml connection on oidc initiate: got %d want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestInitiate_ConnectionMissingDiscoveryURL_Returns500(t *testing.T) {
	idp := newIDPFixture(t)
	conn := activeOIDCConn(idp, "conn-1")
	conn.OIDCDiscoveryURL = ""
	h := initiateHandlerWithMux(t, fakeInitiateStore{conn: conn}, idp)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sso/oidc/conn-1/initiate", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("misconfigured connection: got %d want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestInitiate_StoreError_Returns500(t *testing.T) {
	idp := newIDPFixture(t)
	store := fakeInitiateStore{err: errors.New("DB exploded")}
	h := initiateHandlerWithMux(t, store, idp)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sso/oidc/conn-1/initiate", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("DB error: got %d want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ─── return_to validation (open-redirect defense, architect N4) ────────────

func TestInitiate_ReturnTo_AcceptsRelativePath(t *testing.T) {
	idp := newIDPFixture(t)
	cid := "conn-1"
	v := sso.NewValidator(newMockCache())
	v.SetHTTPClient(idp.server.Client())
	stateCache := newMockCache()
	ss := sso.NewStateStore(stateCache)
	mux := http.NewServeMux()
	mux.Handle("GET /v1/sso/oidc/{cid}/initiate",
		sso.NewInitiateHandler(fakeInitiateStore{conn: activeOIDCConn(idp, cid)}, v, ss, "https://app.example.com"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sso/oidc/"+cid+"/initiate?return_to=/dashboard/zombies", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status: %d", rec.Code)
	}

	u, _ := url.Parse(rec.Header().Get("Location"))
	state := u.Query().Get("state")
	data, err := ss.Consume(context.Background(), state)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if data.RedirectAfterLogin != "/dashboard/zombies" {
		t.Errorf("RedirectAfterLogin: got %q want /dashboard/zombies", data.RedirectAfterLogin)
	}
}

func TestInitiate_ReturnTo_DropsHostileValues(t *testing.T) {
	hostile := []string{
		"https://evil.com",
		"//evil.com",
		"javascript:alert(1)",
		"data:text/html,<script>",
		"foo",          // doesn't start with /
		"/path\x00ok",  // contains null
		"/path\nfoo",   // contains newline
		"/path\\../",   // backslash
	}
	for _, badValue := range hostile {
		t.Run(badValue, func(t *testing.T) {
			idp := newIDPFixture(t)
			cid := "conn-1"
			v := sso.NewValidator(newMockCache())
			v.SetHTTPClient(idp.server.Client())
			ss := sso.NewStateStore(newMockCache())
			mux := http.NewServeMux()
			mux.Handle("GET /v1/sso/oidc/{cid}/initiate",
				sso.NewInitiateHandler(fakeInitiateStore{conn: activeOIDCConn(idp, cid)}, v, ss, "https://app.example.com"))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet,
				"/v1/sso/oidc/"+cid+"/initiate?return_to="+url.QueryEscape(badValue), nil)
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusFound {
				t.Fatalf("expected 302 (handler still proceeds), got %d", rec.Code)
			}

			u, _ := url.Parse(rec.Header().Get("Location"))
			state := u.Query().Get("state")
			data, err := ss.Consume(context.Background(), state)
			if err != nil {
				t.Fatalf("Consume: %v", err)
			}
			if data.RedirectAfterLogin != "" {
				t.Errorf("hostile return_to %q leaked into state: %q", badValue, data.RedirectAfterLogin)
			}
		})
	}
}

// ─── state persistence ──────────────────────────────────────────────────────

// TestInitiate_StateIsRedeemableByCallback proves the round-trip: initiate
// persists, callback (simulated here) consumes, and the same StateData
// returns. Catches any drift in the encoding contract between the two halves
// of the ceremony.
func TestInitiate_StateIsRedeemableByCallback(t *testing.T) {
	idp := newIDPFixture(t)
	cid := "conn-1"

	// Build the handler with shared state cache so the test can poke at it.
	v := sso.NewValidator(newMockCache())
	v.SetHTTPClient(idp.server.Client())
	stateCache := newMockCache()
	ss := sso.NewStateStore(stateCache)
	mux := http.NewServeMux()
	mux.Handle("GET /v1/sso/oidc/{cid}/initiate",
		sso.NewInitiateHandler(fakeInitiateStore{conn: activeOIDCConn(idp, cid)}, v, ss, "https://app.example.com"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sso/oidc/"+cid+"/initiate", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("initiate: got %d want %d", rec.Code, http.StatusFound)
	}

	// Pull the state token out of the redirect URL — same path the IdP would.
	u, _ := url.Parse(rec.Header().Get("Location"))
	stateToken := u.Query().Get("state")
	if stateToken == "" {
		t.Fatal("state empty in authorize redirect")
	}

	data, err := ss.Consume(context.Background(), stateToken)
	if err != nil {
		t.Fatalf("Consume state from initiate: %v", err)
	}
	if data.CID != cid {
		t.Errorf("state.CID: got %q want %q", data.CID, cid)
	}
	if data.CodeVerifier == "" {
		t.Error("state.CodeVerifier empty — PKCE round-trip broken")
	}
	if data.Nonce == "" {
		t.Error("state.Nonce empty")
	}
}
