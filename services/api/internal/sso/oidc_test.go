package sso_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/api/internal/sso"
	"axiaops.io/shared/cache"
	"axiaops.io/shared/model"
)

// mockCache is a minimal in-memory cache.Cache impl for the validator tests.
// Mirrors the shape used in services/shared/jwks/jwks_test.go but is local
// to this package to avoid a test-fixture export.
type mockCache struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMockCache() *mockCache { return &mockCache{data: make(map[string][]byte)} }

func (m *mockCache) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, cache.ErrNotFound
	}
	return v, nil
}

func (m *mockCache) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *mockCache) Del(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *mockCache) Incr(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockCache) Ping(_ context.Context) error { return nil }
func (m *mockCache) Close() error                 { return nil }

// idpFixture stands up an httptest server that serves a synthetic OIDC
// discovery doc + JWKS endpoint + token endpoint backed by a freshly-generated
// RSA key. SwapKey() rotates the key so the auto-refresh test can prove the
// validator re-fetches JWKS on signature failure. SetNextToken() programmes
// the token endpoint's next response (callback tests).
type idpFixture struct {
	t            *testing.T
	server       *httptest.Server
	mu           sync.Mutex
	signingKey   *rsa.PrivateKey
	discoveryURL string
	issuer       string
	// nextTokenClaims controls what the /token endpoint signs and returns.
	// nil → /token returns a 400 invalid_grant. Reset on each callback test
	// so a forgotten setup fails loudly.
	nextTokenClaims jwt.MapClaims
	// nextTokenError lets a test simulate the IdP rejecting the code
	// (e.g. invalid_grant). When non-empty, /token returns RFC 6749 §5.2
	// error shape with this code.
	nextTokenError string
}

func newIDPFixture(t *testing.T) *idpFixture {
	t.Helper()
	f := &idpFixture{t: t, signingKey: newRSAKey(t)}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]any{
			"issuer":                                f.issuer,
			"jwks_uri":                              f.server.URL + "/jwks",
			"authorization_endpoint":                f.server.URL + "/authorize",
			"token_endpoint":                        f.server.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		key := f.signingKey
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rsaPublicKeyToJWKS(t, &key.PublicKey))
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		claims := f.nextTokenClaims
		errCode := f.nextTokenError
		// Single-use: clear armed state so a stale arm can't pollute a
		// later test that forgets to call SetNextToken / SetTokenError.
		f.nextTokenClaims = nil
		f.nextTokenError = ""
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if errCode != "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": errCode})
			return
		}
		if claims == nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "no_token_set"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id_token":     f.SignToken(claims),
			"access_token": "stub-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	f.server = httptest.NewServer(mux)
	f.discoveryURL = f.server.URL + "/.well-known/openid-configuration"
	f.issuer = f.server.URL
	t.Cleanup(f.server.Close)
	return f
}

// SwapKey rotates the RSA key the JWKS endpoint serves. The next live JWKS
// fetch sees the new key; cached JWKS still points at the old one.
func (f *idpFixture) SwapKey() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signingKey = newRSAKey(f.t)
}

// SetNextToken arms the /token endpoint to return an id_token signed with
// the given claims on the next request. Call before triggering a callback.
func (f *idpFixture) SetNextToken(claims jwt.MapClaims) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextTokenClaims = claims
	f.nextTokenError = ""
}

// SetTokenError arms the /token endpoint to return an RFC 6749 §5.2 error
// shape on the next request. Used to simulate IdP-side code rejection.
func (f *idpFixture) SetTokenError(code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextTokenClaims = nil
	f.nextTokenError = code
}

// SignToken signs a token with whatever key is currently active.
func (f *idpFixture) SignToken(claims jwt.MapClaims) string {
	f.mu.Lock()
	key := f.signingKey
	f.mu.Unlock()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "test-key"
	signed, err := tok.SignedString(key)
	if err != nil {
		f.t.Fatalf("sign token: %v", err)
	}
	return signed
}

// connection returns a model.SSOConnection wired to the fixture.
func (f *idpFixture) connection(cid string) model.SSOConnection {
	return model.SSOConnection{
		ID:               cid,
		OrganizationID:   "org-test",
		Protocol:         "oidc",
		OIDCClientID:     "client-test",
		OIDCDiscoveryURL: f.discoveryURL,
	}
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return k
}

func rsaPublicKeyToJWKS(t *testing.T, pub *rsa.PublicKey) []byte {
	t.Helper()
	enc := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	n := enc(pub.N.Bytes())
	e := enc(big.NewInt(int64(pub.E)).Bytes())
	return []byte(`{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":"test-key","n":"` + n + `","e":"` + e + `"}]}`)
}

func validClaims(f *idpFixture) jwt.MapClaims {
	return jwt.MapClaims{
		"iss":   f.issuer,
		"aud":   "client-test",
		"sub":   "user-123",
		"email": "alice@acme.com",
		"exp":   time.Now().Add(5 * time.Minute).Unix(),
		"iat":   time.Now().Unix(),
	}
}

// ─── happy path ─────────────────────────────────────────────────────────────

func TestValidator_ValidateIDToken_Valid(t *testing.T) {
	f := newIDPFixture(t)
	v := sso.NewValidator(newMockCache())
	v.SetHTTPClient(f.server.Client())

	token := f.SignToken(validClaims(f))
	claims, err := v.ValidateIDToken(context.Background(), f.connection("conn-1"), token, "")
	if err != nil {
		t.Fatalf("expected valid token to pass, got %v", err)
	}
	if got, _ := claims["sub"].(string); got != "user-123" {
		t.Errorf("sub claim: got %q want %q", got, "user-123")
	}
}

// ─── alg-confusion cases (design §11.3) ─────────────────────────────────────

func TestValidator_ValidateIDToken_RejectsNoneAlg(t *testing.T) {
	f := newIDPFixture(t)
	v := sso.NewValidator(newMockCache())
	v.SetHTTPClient(f.server.Client())

	tok := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims(f))
	signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none token: %v", err)
	}
	_, err = v.ValidateIDToken(context.Background(), f.connection("conn-1"), signed, "")
	if !errors.Is(err, sso.ErrIDTokenInvalid) {
		t.Fatalf("expected ErrIDTokenInvalid for alg=none, got %v", err)
	}
}

func TestValidator_ValidateIDToken_RejectsHS256(t *testing.T) {
	f := newIDPFixture(t)
	v := sso.NewValidator(newMockCache())
	v.SetHTTPClient(f.server.Client())

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims(f))
	signed, err := tok.SignedString([]byte("attacker-known-secret"))
	if err != nil {
		t.Fatalf("sign hs256 token: %v", err)
	}
	_, err = v.ValidateIDToken(context.Background(), f.connection("conn-1"), signed, "")
	if !errors.Is(err, sso.ErrIDTokenInvalid) {
		t.Fatalf("expected ErrIDTokenInvalid for HS256, got %v", err)
	}
}

// ─── claim validation ───────────────────────────────────────────────────────

func TestValidator_ValidateIDToken_RejectsWrongAudience(t *testing.T) {
	f := newIDPFixture(t)
	v := sso.NewValidator(newMockCache())
	v.SetHTTPClient(f.server.Client())

	c := validClaims(f)
	c["aud"] = "different-client"
	_, err := v.ValidateIDToken(context.Background(), f.connection("conn-1"), f.SignToken(c), "")
	if !errors.Is(err, sso.ErrIDTokenInvalid) {
		t.Fatalf("expected ErrIDTokenInvalid for wrong audience, got %v", err)
	}
}

func TestValidator_ValidateIDToken_RejectsWrongIssuer(t *testing.T) {
	f := newIDPFixture(t)
	v := sso.NewValidator(newMockCache())
	v.SetHTTPClient(f.server.Client())

	c := validClaims(f)
	c["iss"] = "https://attacker.example/"
	_, err := v.ValidateIDToken(context.Background(), f.connection("conn-1"), f.SignToken(c), "")
	if !errors.Is(err, sso.ErrIDTokenInvalid) {
		t.Fatalf("expected ErrIDTokenInvalid for wrong issuer, got %v", err)
	}
}

func TestValidator_ValidateIDToken_RejectsExpired(t *testing.T) {
	f := newIDPFixture(t)
	v := sso.NewValidator(newMockCache())
	v.SetHTTPClient(f.server.Client())

	c := validClaims(f)
	c["exp"] = time.Now().Add(-1 * time.Hour).Unix()
	_, err := v.ValidateIDToken(context.Background(), f.connection("conn-1"), f.SignToken(c), "")
	if !errors.Is(err, sso.ErrIDTokenInvalid) {
		t.Fatalf("expected ErrIDTokenInvalid for expired token, got %v", err)
	}
}

func TestValidator_ValidateIDToken_RejectsNonceMismatch(t *testing.T) {
	f := newIDPFixture(t)
	v := sso.NewValidator(newMockCache())
	v.SetHTTPClient(f.server.Client())

	c := validClaims(f)
	c["nonce"] = "actual-nonce"
	_, err := v.ValidateIDToken(context.Background(), f.connection("conn-1"), f.SignToken(c), "expected-nonce")
	if !errors.Is(err, sso.ErrIDTokenInvalid) {
		t.Fatalf("expected ErrIDTokenInvalid for nonce mismatch, got %v", err)
	}
}

// ─── architect S5: JWKS auto-refresh on signature failure ───────────────────

func TestValidator_ValidateIDToken_AutoRefreshOnSignatureFailure(t *testing.T) {
	f := newIDPFixture(t)
	c := newMockCache()
	v := sso.NewValidator(c)
	v.SetHTTPClient(f.server.Client())

	// 1. Prime caches with a successful validation against the original key.
	if _, err := v.ValidateIDToken(context.Background(), f.connection("conn-1"), f.SignToken(validClaims(f)), ""); err != nil {
		t.Fatalf("priming validation: %v", err)
	}

	// 2. IdP rotates its key. JWKS endpoint now serves the new key; our
	//    cache still holds the old one.
	f.SwapKey()

	// 3. Token signed with the new key — first parse against cached JWKS
	//    must fail signature; the validator must evict and retry, which
	//    fetches the new JWKS and succeeds.
	token := f.SignToken(validClaims(f))
	if _, err := v.ValidateIDToken(context.Background(), f.connection("conn-1"), token, ""); err != nil {
		t.Fatalf("expected auto-refresh to recover after key rotation, got %v", err)
	}
}
