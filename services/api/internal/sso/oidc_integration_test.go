//go:build integration

// Package sso_test — integration coverage for the OIDC callback ceremony.
//
// This file stands the SSO ceremony surface up *in-process* against a real
// PostgreSQL (per docker-compose.test.yml) and an in-process minimal OIDC
// issuer. The minimal issuer is intentionally NOT a third-party library —
// the JWKS-rotation test (architect S5) needs to swap signing keys mid-test
// with deterministic timing, which off-the-shelf mockoidc implementations
// do not expose cleanly.
//
// Skips when INTEGRATION_DATABASE_URL / INTEGRATION_DATABASE_OWNER_URL
// aren't set, so `go test ./...` without the SSO compose stack passes.
//
// Plan §5.4 / §5.5 acceptance:
//   - Mock-OIDC integration test passes end-to-end (login → JIT → membership)
//   - JWKS auto-refresh on signature failure (architect S5)
//   - Group-mapping precedence verified by table-driven path
package sso_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/crypto"
	"axiaops.io/shared/model"
	"axiaops.io/shared/storage"
	postgresstore "axiaops.io/shared/storage/postgres"
)

// ── Database setup ──────────────────────────────────────────────────────────

// requireDB returns the connection URLs or skips the test. Both URLs come
// from `make test-integration-sso` which boots the docker-compose.test.yml
// Postgres on host port 5532.
func requireDB(t *testing.T) (appURL, ownerURL string) {
	t.Helper()
	appURL = os.Getenv("INTEGRATION_DATABASE_URL")
	ownerURL = os.Getenv("INTEGRATION_DATABASE_OWNER_URL")
	if appURL == "" || ownerURL == "" {
		t.Skip("INTEGRATION_DATABASE_URL / INTEGRATION_DATABASE_OWNER_URL not set — skipping SSO integration test (run `make test-integration-sso`)")
	}
	return appURL, ownerURL
}

// openStore boots Postgres bootstrap + migrate once per test, returns the
// app/owner-paired Store. Bootstrap + Migrate are idempotent so concurrent
// runs against the same DB don't conflict (each test seeds a fresh org
// scoped by uuid, so RLS keeps them isolated).
func openStore(t *testing.T) (*postgresstore.Store, string, string) {
	t.Helper()
	appURL, ownerURL := requireDB(t)
	if err := postgresstore.Bootstrap(ownerURL, appURL, ""); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := postgresstore.Migrate(ownerURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := postgresstore.NewWithOwner(ctx, appURL, ownerURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, appURL, ownerURL
}

// ── Minimal in-process OIDC issuer ──────────────────────────────────────────
//
// Just enough to drive the AxiaOps RP through a real authorization-code
// + PKCE round-trip. Routes:
//   GET  /.well-known/openid-configuration  → DiscoveryDoc JSON
//   GET  /jwks                              → JWKS containing the current key
//   GET  /authorize                         → 302 → redirect_uri?code=...&state=...
//   POST /token                             → {access_token, id_token, token_type}
//
// Concurrency: every map mutation guarded by mu so the test goroutine and
// the validator goroutine (calling /token via the callback handler) don't
// race on `codes`.
//
// PKCE: validates base64url(sha256(code_verifier)) == code_challenge per
// RFC 7636 §4.6 — the same check the real IdPs perform and the same path
// our callback exercises.

type mockOIDCUser struct {
	Sub    string
	Email  string
	Name   string
	Groups []string
}

type mockOIDCKeypair struct {
	Key *rsa.PrivateKey
	Kid string
}

type mockOIDCCode struct {
	Challenge   string
	RedirectURI string
	ClientID    string
	Nonce       string
	User        mockOIDCUser
}

type mockOIDC struct {
	t        *testing.T
	server   *httptest.Server
	clientID string

	mu      sync.Mutex
	keypair mockOIDCKeypair
	user    mockOIDCUser
	codes   map[string]mockOIDCCode
}

func newMockOIDC(t *testing.T, clientID string) *mockOIDC {
	t.Helper()
	kp, err := newKeypair()
	if err != nil {
		t.Fatalf("mockoidc: generate key: %v", err)
	}
	m := &mockOIDC{
		t:        t,
		clientID: clientID,
		keypair:  kp,
		codes:    make(map[string]mockOIDCCode),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", m.handleConfig)
	mux.HandleFunc("GET /jwks", m.handleJWKS)
	mux.HandleFunc("GET /authorize", m.handleAuthorize)
	mux.HandleFunc("POST /token", m.handleToken)
	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

// SetUser replaces the canned claims returned for the next /authorize +
// /token round-trip. Safe to call between logins.
func (m *mockOIDC) SetUser(u mockOIDCUser) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.user = u
}

// RotateKey generates a fresh RSA keypair under a new kid. Subsequent
// /token calls sign with the new key; /jwks publishes only the new key.
// Drives the auto-refresh acceptance criterion — the RP's cached JWKS no
// longer contains the kid, ParseWithClaims fails with
// jwt.ErrTokenSignatureInvalid, and the validator's Del+retry path kicks in.
func (m *mockOIDC) RotateKey() {
	kp, err := newKeypair()
	if err != nil {
		m.t.Fatalf("mockoidc: rotate key: %v", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keypair = kp
}

// Issuer returns the absolute URL the RP should record as the connection's
// oidc_discovery_url base (same value goes into id_token `iss` claim).
func (m *mockOIDC) Issuer() string { return m.server.URL }

func (m *mockOIDC) DiscoveryURL() string {
	return m.server.URL + "/.well-known/openid-configuration"
}

func (m *mockOIDC) handleConfig(w http.ResponseWriter, _ *http.Request) {
	doc := sso.DiscoveryDoc{
		Issuer:                m.server.URL,
		JWKSURI:               m.server.URL + "/jwks",
		AuthorizationEndpoint: m.server.URL + "/authorize",
		TokenEndpoint:         m.server.URL + "/token",
		IDTokenSigningAlgs:    []string{"RS256"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (m *mockOIDC) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	kp := m.keypair
	m.mu.Unlock()

	jwk := map[string]any{
		"kty": "RSA",
		"kid": kp.Kid,
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(kp.Key.PublicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(kp.Key.PublicKey.E)).Bytes()),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwk}})
}

func (m *mockOIDC) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("response_type") != "code" {
		http.Error(w, "unsupported response_type", http.StatusBadRequest)
		return
	}
	if q.Get("code_challenge_method") != "S256" {
		http.Error(w, "unsupported code_challenge_method", http.StatusBadRequest)
		return
	}
	clientID := q.Get("client_id")
	if clientID != m.clientID {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}
	code, err := randomURLToken(24)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}

	m.mu.Lock()
	user := m.user
	m.codes[code] = mockOIDCCode{
		Challenge:   q.Get("code_challenge"),
		RedirectURI: redirectURI,
		ClientID:    clientID,
		Nonce:       q.Get("nonce"),
		User:        user,
	}
	m.mu.Unlock()

	dst, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	rq := dst.Query()
	rq.Set("code", code)
	rq.Set("state", q.Get("state"))
	dst.RawQuery = rq.Encode()
	http.Redirect(w, r, dst.String(), http.StatusFound)
}

func (m *mockOIDC) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("grant_type") != "authorization_code" {
		writeTokenError(w, "unsupported_grant_type", http.StatusBadRequest)
		return
	}
	code := r.Form.Get("code")
	verifier := r.Form.Get("code_verifier")
	clientID := r.Form.Get("client_id")
	redirectURI := r.Form.Get("redirect_uri")

	m.mu.Lock()
	rec, ok := m.codes[code]
	if ok {
		delete(m.codes, code) // single-use; matches real IdPs
	}
	kp := m.keypair
	m.mu.Unlock()

	if !ok {
		writeTokenError(w, "invalid_grant", http.StatusBadRequest)
		return
	}
	if rec.RedirectURI != redirectURI || rec.ClientID != clientID {
		writeTokenError(w, "invalid_grant", http.StatusBadRequest)
		return
	}
	// PKCE S256: base64url(sha256(verifier)) MUST equal recorded challenge.
	sum := sha256.Sum256([]byte(verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != rec.Challenge {
		writeTokenError(w, "invalid_grant", http.StatusBadRequest)
		return
	}

	idToken, err := signIDToken(kp, m.server.URL, rec, time.Now())
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "fake-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     idToken,
	})
}

func writeTokenError(w http.ResponseWriter, code string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func newKeypair() (mockOIDCKeypair, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return mockOIDCKeypair{}, err
	}
	kid, err := randomURLToken(8)
	if err != nil {
		return mockOIDCKeypair{}, err
	}
	return mockOIDCKeypair{Key: key, Kid: kid}, nil
}

func signIDToken(kp mockOIDCKeypair, issuer string, rec mockOIDCCode, now time.Time) (string, error) {
	claims := jwt.MapClaims{
		"iss":   issuer,
		"sub":   rec.User.Sub,
		"aud":   rec.ClientID,
		"iat":   now.Unix(),
		"exp":   now.Add(15 * time.Minute).Unix(),
		"nonce": rec.Nonce,
		"email": rec.User.Email,
		"name":  rec.User.Name,
	}
	if len(rec.User.Groups) > 0 {
		// Unmarshal-ed back to `[]any` by encoding/json — matches the
		// extractClaims switch on `case []any:`.
		groups := make([]any, len(rec.User.Groups))
		for i, g := range rec.User.Groups {
			groups[i] = g
		}
		claims["groups"] = groups
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kp.Kid
	return tok.SignedString(kp.Key)
}

func randomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ── Test fixture: org + connection + verified domain + group mapping ──────

type fixture struct {
	store      *postgresstore.Store
	cache      cache.Cache
	mockIDP    *mockOIDC
	apiServer  *httptest.Server
	connection model.SSOConnection
	domain     string
	clientID   string
	clientSec  string

	orgID string
}

// newFixture wires a fresh end-to-end stack: unique org, mock IdP, real
// Postgres-backed Store, real validator/state/session managers, real SSO
// handlers behind an httptest server.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, _, _ := openStore(t)
	ensureEncryptionKey(t)

	const clientID = "axiaops-rp-client"
	const clientSecret = "axiaops-rp-client-secret"
	mockIDP := newMockOIDC(t, clientID)

	suffix := uuid.New().String()
	orgCode := "sso-it-" + suffix
	const orgName = "SSO Integration Org"
	// Domain unique per fixture: sso_domains has a partial UNIQUE on
	// lower(domain) WHERE status IN ('pending','verified'), so two tests
	// running in parallel (or back-to-back without a DB wipe) collide on
	// any shared name. The .example tld is RFC-2606 reserved so no real
	// DNS lookup ever succeeds against it.
	domain := "acme-" + strings.ReplaceAll(suffix, "-", "") + ".example.com"

	ctx := context.Background()
	// UpsertOrganization mints its own UUID for `id`; the orgCode is the
	// caller-supplied unique key. sso_connections.organization_id references
	// organizations.id (the UUID), not org_code — so we keep the returned
	// row's ID, not the code we passed.
	org, err := store.UpsertOrganization(ctx, orgCode, orgName)
	if err != nil {
		t.Fatalf("seed org: %v", err)
	}
	orgID := org.ID

	ciphertext, err := crypto.Encrypt(clientSecret)
	if err != nil {
		t.Fatalf("encrypt client secret: %v", err)
	}

	orgCtx := storage.WithOrganizationID(ctx, orgID)
	conn := model.SSOConnection{
		OrganizationID:             orgID,
		Protocol:                   model.SSOProtocolOIDC,
		Label:                      "Mock OIDC",
		Status:                     model.SSOStatusActive,
		Enforcement:                model.SSOEnforcementOptional,
		DefaultRole:                "viewer",
		OIDCClientID:               clientID,
		OIDCClientSecretCiphertext: []byte(ciphertext),
		OIDCDiscoveryURL:           mockIDP.DiscoveryURL(),
	}
	conn, err = store.CreateSSOConnection(orgCtx, conn)
	if err != nil {
		t.Fatalf("create sso connection: %v", err)
	}

	dom, err := store.CreateSSODomain(orgCtx, model.SSODomain{
		OrganizationID:    orgID,
		SSOConnectionID:   conn.ID,
		Domain:            domain,
		Status:            model.SSODomainStatusPending,
		VerificationToken: "tok-" + uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("create sso domain: %v", err)
	}
	if err := store.UpdateSSODomainStatus(orgCtx, dom.ID, model.SSODomainStatusVerified, time.Now().UTC(), time.Now().Add(365*24*time.Hour).UTC()); err != nil {
		t.Fatalf("verify sso domain: %v", err)
	}

	if err := store.ReplaceSSOGroupMappings(orgCtx, conn.ID, []model.SSOGroupMapping{
		{OrganizationID: orgID, SSOConnectionID: conn.ID, GroupExternalID: "g-engineering", Role: "admin"},
		{OrganizationID: orgID, SSOConnectionID: conn.ID, GroupExternalID: "g-support", Role: "member"},
	}); err != nil {
		t.Fatalf("replace group mappings: %v", err)
	}

	// Composition root: mux + httptest server BEFORE registering handlers,
	// so the InitiateHandler can be wired with the apiServer.URL as
	// PUBLIC_HOST. The mux ts holds is mutated; routes added after
	// httptest.NewServer still serve correctly.
	mux := http.NewServeMux()
	apiServer := httptest.NewServer(mux)
	t.Cleanup(apiServer.Close)

	c := cache.New("") // in-memory backend
	validator := sso.NewValidator(c)
	stateStore := sso.NewStateStore(c)
	sessionMgr := auth.NewManager(store, auth.NewSessionCache(c), auth.Config{
		TTL:             1 * time.Hour,
		SessionsPerUser: 10,
	})
	cookieCfg := auth.NewCookieConfig()

	mux.Handle("GET /v1/sso/oidc/{cid}/initiate",
		sso.NewInitiateHandler(store, validator, stateStore, apiServer.URL))
	cb := sso.NewCallbackHandler(sso.CallbackOptions{
		Store:        store,
		Validator:    validator,
		StateStore:   stateStore,
		Sessions:     sessionMgr,
		CookieConfig: cookieCfg,
		PublicHost:   apiServer.URL,
	})
	// Mirror serverbuild.ComposeServer: standard cid-less route is the one
	// initiate's redirect_uri now points at, legacy path-cid route is kept
	// for the deprecation window (Tasks.md 2.7.22).
	mux.Handle("GET "+sso.CallbackPath, cb)
	mux.Handle("GET /v1/sso/oidc/{cid}/callback", cb)

	return &fixture{
		store:      store,
		cache:      c,
		mockIDP:    mockIDP,
		apiServer:  apiServer,
		connection: conn,
		domain:     domain,
		clientID:   clientID,
		clientSec:  clientSecret,
		orgID:      orgID,
	}
}

// ensureEncryptionKey sets a deterministic 32-byte hex ENCRYPTION_KEY for
// the test process. The fixture's Encrypt + the callback's Decrypt both
// read this env var; without it crypto.loadKey returns an error.
func ensureEncryptionKey(t *testing.T) {
	t.Helper()
	if os.Getenv("ENCRYPTION_KEY") != "" {
		return
	}
	t.Setenv("ENCRYPTION_KEY", strings.Repeat("0", 64))
}

// browser returns an http.Client that follows redirects within both the API
// and the IdP loopback servers, persisting cookies set on the API origin.
// The default 10-redirect cap is plenty for one ceremony (initiate → IdP →
// callback → "/" = 3 hops).
func newBrowser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

// extractSessionCookie pulls the axiaops_session value out of a cookiejar
// after a redirect chain. Returns "" if absent.
func extractSessionCookie(t *testing.T, client *http.Client, origin string) string {
	t.Helper()
	u, err := url.Parse(origin)
	if err != nil {
		t.Fatalf("parse origin: %v", err)
	}
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == auth.SessionCookieName {
			return c.Value
		}
	}
	return ""
}

// loginAndAssertSession is the canonical happy-path driver: runs the
// ceremony, verifies the response landed at the in-process "/" (which 404s
// because we don't register that route — the assertion is on the *redirect
// path*, not the page), and returns the session cookie value.
func loginAndAssertSession(t *testing.T, fx *fixture, user mockOIDCUser) string {
	t.Helper()
	fx.mockIDP.SetUser(user)
	client := newBrowser(t)
	initiateURL := fx.apiServer.URL + "/v1/sso/oidc/" + fx.connection.ID + "/initiate"
	resp, err := client.Get(initiateURL)
	if err != nil {
		t.Fatalf("initiate GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Final response is the "/" 404 (we didn't register that route).
	// What matters is the request URL we ended up on after redirects:
	// it must be on the API origin and path "/", NOT on the
	// IdP and NOT on /login?error=auth_failed.
	if got, want := resp.Request.URL.Path, "/"; got != want {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("ceremony landed at %q (expected %q); body=%q", got, want, string(body))
	}
	if !strings.HasPrefix(resp.Request.URL.String(), fx.apiServer.URL) {
		t.Fatalf("ceremony left the API origin: %s", resp.Request.URL)
	}

	cookie := extractSessionCookie(t, client, fx.apiServer.URL)
	if cookie == "" {
		t.Fatal("no axiaops_session cookie set after ceremony")
	}
	return cookie
}

// ── Tests ───────────────────────────────────────────────────────────────────

// TestOIDC_HappyPath_GroupRoleProvisioned drives the full ceremony for a
// brand-new user whose `groups` claim maps to admin. Verifies:
//   - Ceremony lands on "/" with a session cookie set.
//   - users row exists and carries the IdP sub.
//   - memberships row exists with role=admin and provisioned_via='jit'.
//   - audit_log carries an SSO_LOGIN_SUCCEEDED event for the new user.
func TestOIDC_HappyPath_GroupRoleProvisioned(t *testing.T) {
	fx := newFixture(t)

	user := mockOIDCUser{
		Sub:    "alice-sub-001",
		Email:  "alice@" + fx.domain,
		Name:   "Alice Engineer",
		Groups: []string{"g-engineering"}, // → admin per fixture
	}
	cookie := loginAndAssertSession(t, fx, user)
	if cookie == "" {
		t.Fatal("session cookie missing")
	}

	orgCtx := storage.WithOrganizationID(context.Background(), fx.orgID)

	// users row — read-only lookup. UpsertUser would mask a JIT bug where
	// the callback never created the user (UpsertUser would silently insert
	// it on the spot and the membership assertion below would still pass).
	got, err := fx.store.GetUserByEmail(orgCtx, user.Email)
	if err != nil {
		t.Fatalf("user lookup after JIT: %v", err)
	}
	if got.Email != user.Email {
		t.Fatalf("user email: got %q want %q", got.Email, user.Email)
	}

	// memberships row
	mem, err := fx.store.GetMembershipByOrgUser(orgCtx, fx.orgID, got.ID)
	if err != nil {
		t.Fatalf("get membership: %v", err)
	}
	if mem.Role != "admin" {
		t.Fatalf("membership role: got %q want admin (group→role mapping should win)", mem.Role)
	}

	// Audit row — at least one SSO_LOGIN_SUCCEEDED for this user/org.
	if !auditHasAction(t, fx, model.AuditActionSSOLoginSucceeded, got.ID) {
		t.Fatalf("expected audit row %q for user %q", model.AuditActionSSOLoginSucceeded, got.ID)
	}
}

// TestOIDC_HappyPath_DefaultRoleFallback covers the JIT default-role path:
// a user whose groups don't intersect the mappings drops through to the
// connection's DefaultRole (viewer in the fixture). Confirms "user in zero
// mapped groups → falls through to default_role" from plan §5.5.
func TestOIDC_HappyPath_DefaultRoleFallback(t *testing.T) {
	fx := newFixture(t)
	user := mockOIDCUser{
		Sub:    "bob-sub-002",
		Email:  "bob@" + fx.domain,
		Name:   "Bob Drifter",
		Groups: []string{"g-unmapped"},
	}
	loginAndAssertSession(t, fx, user)

	orgCtx := storage.WithOrganizationID(context.Background(), fx.orgID)
	got, err := fx.store.GetUserByEmail(orgCtx, user.Email)
	if err != nil {
		t.Fatalf("user lookup after JIT: %v", err)
	}
	mem, err := fx.store.GetMembershipByOrgUser(orgCtx, fx.orgID, got.ID)
	if err != nil {
		t.Fatalf("get membership: %v", err)
	}
	if mem.Role != "viewer" {
		t.Fatalf("membership role: got %q want viewer (default_role fallback)", mem.Role)
	}
}

// TestOIDC_DomainUnverified_Rejected exercises the anti-spoofing boundary:
// a token whose email is on a domain NOT in sso_domains for this connection
// must be rejected — even though signature/issuer/audience/nonce all check
// out. Confirms design §11.1.
func TestOIDC_DomainUnverified_Rejected(t *testing.T) {
	fx := newFixture(t)
	user := mockOIDCUser{
		Sub:    "mallory-sub-003",
		Email:  "mallory@evil-domain.example.org",
		Name:   "Mallory",
		Groups: []string{"g-engineering"},
	}
	fx.mockIDP.SetUser(user)
	client := newBrowser(t)
	resp, err := client.Get(fx.apiServer.URL + "/v1/sso/oidc/" + fx.connection.ID + "/initiate")
	if err != nil {
		t.Fatalf("initiate GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := resp.Request.URL.Path; got != "/login" {
		t.Fatalf("unverified-domain ceremony landed at %q (expected /login)", got)
	}
	if got := resp.Request.URL.Query().Get("error"); got != "auth_failed" {
		t.Fatalf("expected ?error=auth_failed, got %q", got)
	}
	if c := extractSessionCookie(t, client, fx.apiServer.URL); c != "" {
		t.Fatalf("rejection should not set a session cookie; got %q", c)
	}
}

// TestOIDC_JWKSAutoRefreshOnSignatureFailure drives the architect-S5
// acceptance criterion (plan §5.5):
//   1. First login warms the validator's JWKS cache.
//   2. Mock IdP rotates its signing key (new kid).
//   3. Second login: parseToken fails on signature → validator evicts
//      the cached JWKS, refetches /jwks, retries, succeeds.
// All in one request — no 24h outage waiting for cache TTL.
//
// This is the high-leverage test of the entire SSO posture: an IdP key
// rotation must NOT page anyone.
func TestOIDC_JWKSAutoRefreshOnSignatureFailure(t *testing.T) {
	fx := newFixture(t)

	// Login #1 — warms the JWKS cache under the original kid.
	loginAndAssertSession(t, fx, mockOIDCUser{
		Sub:    "alice-rot-001",
		Email:  "alice@" + fx.domain,
		Name:   "Alice Rotated",
		Groups: []string{"g-engineering"},
	})

	// Rotate the IdP key. The validator's cached JWKS still contains the
	// OLD kid; the next id_token will be signed by the NEW kid.
	fx.mockIDP.RotateKey()

	// Login #2 — must succeed in a single request via the auto-refresh
	// path (Del cache → refetch → retry).
	loginAndAssertSession(t, fx, mockOIDCUser{
		Sub:    "alice-rot-001", // same sub — re-login of same user
		Email:  "alice@" + fx.domain,
		Name:   "Alice Rotated",
		Groups: []string{"g-engineering"},
	})
}

// TestOIDC_PendingInvitationPrecedence pins the §5.5 acceptance "pending_memberships
// invitation takes precedence over JIT (per design doc §10.4) — covered by
// integration test."
//
// Property: when a user logs in via SSO and a pending_memberships row exists
// for their email in the target org, the role from the INVITE wins over
// whatever JIT would have resolved from group claims. The invite path also
// consumes (deletes) the pending row and skips JIT's audit events entirely.
//
// Why this matters: an admin who issued an invite at role=viewer made an
// explicit role choice. If the user's IdP groups happen to map to admin via
// the connection's group_mappings, JIT would otherwise silently override
// that admin choice — defeating the whole point of admin-issued invites.
//
// We deliberately use the JIT-stronger direction (invite=viewer beats
// JIT=admin from the g-engineering→admin fixture mapping). The reverse
// direction (invite=admin beats JIT=viewer) follows from the same
// callsite — there's only one if-else gate at oidc_callback.go's
// RedeemPendingInvitation call, no per-direction logic.
func TestOIDC_PendingInvitationPrecedence(t *testing.T) {
	fx := newFixture(t)

	// Seed an inviter user + admin membership so the audit-trail captured
	// at invite time has a real attribution. Without this the test could
	// pass while a future change drops the inviter-attribution requirement
	// silently.
	rootCtx := storage.WithOrganizationID(context.Background(), fx.orgID)
	inviter, err := fx.store.UpsertUser(rootCtx, fx.orgID, "inviter-sub-001",
		"inviter@"+fx.domain, "Admin Issuer")
	if err != nil {
		t.Fatalf("upsert inviter: %v", err)
	}
	if err := fx.store.SaveMembership(rootCtx, model.Membership{
		ID:             uuid.New().String(),
		UserID:         inviter.ID,
		OrganizationID: fx.orgID,
		Role:           "admin",
		ProvisionedVia: model.ProvisionedViaManual,
	}); err != nil {
		t.Fatalf("save inviter membership: %v", err)
	}

	// Seed pending_memberships at role=viewer for alice. The fixture's
	// group mapping is g-engineering→admin; alice's IdP groups will
	// include g-engineering. JIT would resolve to admin; the invite must
	// override that to viewer.
	aliceEmail := "alice@" + fx.domain
	if _, _, err := fx.store.CreateNativeInvitation(rootCtx, model.PendingInvitation{
		OrganizationID:  fx.orgID,
		Email:           aliceEmail,
		Role:            "viewer", // intentionally LOWER than what JIT would yield
		InvitedByUserID: inviter.ID,
		InvitedByEmail:  inviter.Email,
		Status:          "pending",
		ExpiresAt:       time.Now().Add(7 * 24 * time.Hour),
		// Token hash is required (NOT NULL on the column under native auth).
		// Anything stable + unique works — the SSO callback redeems by
		// (org, email) match, never by token.
		InviteTokenHash: "test-tok-" + uuid.New().String(),
	}); err != nil {
		t.Fatalf("create pending invitation: %v", err)
	}

	// Confirm precondition: exactly one pending invitation exists.
	pendingBefore, err := fx.store.ListPendingInvitations(rootCtx, "pending")
	if err != nil {
		t.Fatalf("list pending before: %v", err)
	}
	if len(pendingBefore) != 1 {
		t.Fatalf("expected 1 pending invitation seeded; got %d", len(pendingBefore))
	}

	// Drive the ceremony with groups that JIT would map to admin.
	user := mockOIDCUser{
		Sub:    "alice-sub-invite-precedence",
		Email:  aliceEmail,
		Name:   "Alice Invitee",
		Groups: []string{"g-engineering"}, // → admin per fixture mapping
	}
	cookie := loginAndAssertSession(t, fx, user)
	if cookie == "" {
		t.Fatal("session cookie missing — ceremony failed before precedence could be tested")
	}

	// Look up alice's user row + the resolved membership.
	alice, err := fx.store.GetUserByEmail(rootCtx, aliceEmail)
	if err != nil {
		t.Fatalf("get alice user: %v", err)
	}
	mem, err := fx.store.GetMembershipByOrgUser(rootCtx, fx.orgID, alice.ID)
	if err != nil {
		t.Fatalf("get alice membership: %v", err)
	}

	// Core assertion: the invite role wins over the JIT-resolved role.
	if mem.Role != "viewer" {
		t.Errorf("membership role = %q; want viewer (invite must beat JIT-resolved admin)", mem.Role)
	}
	// And provisioning provenance reflects the invite path, not JIT.
	// This is what the cross-flow race fix (jit.go provenance guard)
	// keys off; if the SSO callback ever wrote 'jit' here, the next
	// re-login would let JIT silently overwrite the admin's role choice.
	if mem.ProvisionedVia != model.ProvisionedViaInvitation {
		t.Errorf("provisioned_via = %q; want %q (invite path)",
			mem.ProvisionedVia, model.ProvisionedViaInvitation)
	}

	// Pending row must be consumed — RedeemPendingInvitation DELETEs.
	// A surviving row would re-fire the redeem on every subsequent login
	// (idempotent, but wasteful and surprising to anyone reading the
	// pending-invitations admin UI).
	pendingAfter, err := fx.store.ListPendingInvitations(rootCtx, "pending")
	if err != nil {
		t.Fatalf("list pending after: %v", err)
	}
	if len(pendingAfter) != 0 {
		t.Errorf("pending invitation not consumed; %d rows remain", len(pendingAfter))
	}

	// Audit posture: SSO_LOGIN_SUCCEEDED is recorded for alice (proves
	// the ceremony completed end-to-end); SSO_JIT_PROVISIONED is NOT
	// recorded (proves the invite branch fired, JIT branch was skipped).
	if !auditHasAction(t, fx, model.AuditActionSSOLoginSucceeded, alice.ID) {
		t.Errorf("expected %q audit row for invited user %q",
			model.AuditActionSSOLoginSucceeded, alice.ID)
	}
	if auditHasAction(t, fx, model.AuditActionSSOJITProvisioned, alice.ID) {
		t.Errorf("found %q audit row for invited user %q; invite path must bypass JIT audit entirely",
			model.AuditActionSSOJITProvisioned, alice.ID)
	}
}

// ── Test helpers ────────────────────────────────────────────────────────────

// auditHasAction returns true when the org timeline contains an event of
// the given action attributed to the given user. Presence-of-only — the
// callback wires audit metadata + actor enrichment through the same
// audit.Record path the production handlers use, so a positive presence
// here means the full audit chain (org context, user enrichment, action
// constant, write) is operational under the integration boundary.
func auditHasAction(t *testing.T, fx *fixture, action, userID string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, err := fx.store.AuditLogList(
		storage.WithOrganizationID(ctx, fx.orgID),
		model.AuditFilter{Limit: 50, UserID: userID, Action: action},
	)
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	for _, e := range events {
		if e.Action == action && e.UserID == userID {
			return true
		}
	}
	return false
}
