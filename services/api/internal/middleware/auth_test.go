package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"axiaops.io/shared/cache"
)

const testIssuer = "https://axiaops.kinde.com"

// testSetup generates an RSA key pair and returns a ready-to-use Auth middleware.
func testSetup(t *testing.T) (*Auth, *rsa.PrivateKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	kf := func(_ *jwt.Token) (any, error) { return &priv.PublicKey, nil }
	auth := newWithKeyfunc(testIssuer, kf)
	return auth, priv
}

// signToken creates a signed JWT with the given claims.
func signToken(t *testing.T, priv *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// validClaims returns a minimal set of claims that should pass the middleware.
func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss":      testIssuer,
		"sub":      "user_123",
		"org_code": "org_abc",
		"exp":      time.Now().Add(time.Hour).Unix(),
		"iat":      time.Now().Unix(),
	}
}

// okHandler is a downstream handler that records whether it was reached.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// ── Missing / malformed token ─────────────────────────────────────────────────

func TestAuth_NoAuthorizationHeader_Returns401(t *testing.T) {
	auth, _ := testSetup(t)
	h := auth.Wrap(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuth_MalformedAuthorizationHeader_Returns401(t *testing.T) {
	auth, _ := testSetup(t)
	h := auth.Wrap(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	req.Header.Set("Authorization", "Token abc123") // wrong scheme
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuth_InvalidToken_Returns401(t *testing.T) {
	auth, _ := testSetup(t)
	h := auth.Wrap(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	req.Header.Set("Authorization", "Bearer not.a.jwt")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuth_ExpiredToken_Returns401(t *testing.T) {
	auth, priv := testSetup(t)
	h := auth.Wrap(okHandler)

	claims := validClaims()
	claims["exp"] = time.Now().Add(-time.Hour).Unix() // already expired
	token := signToken(t, priv, claims)

	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuth_WrongIssuer_Returns401(t *testing.T) {
	auth, priv := testSetup(t)
	h := auth.Wrap(okHandler)

	claims := validClaims()
	claims["iss"] = "https://attacker.kinde.com"
	token := signToken(t, priv, claims)

	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuth_MissingOrgCode_Returns401(t *testing.T) {
	auth, priv := testSetup(t)
	h := auth.Wrap(okHandler)

	claims := validClaims()
	delete(claims, "org_code")
	token := signToken(t, priv, claims)

	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// ── Valid token ───────────────────────────────────────────────────────────────

func TestAuth_ValidToken_Returns200(t *testing.T) {
	auth, priv := testSetup(t)
	h := auth.Wrap(okHandler)

	token := signToken(t, priv, validClaims())

	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAuth_ValidToken_OrganizationIDInContext(t *testing.T) {
	auth, priv := testSetup(t)

	var gotOrganizationID string
	capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOrganizationID = OrganizationID(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	h := auth.Wrap(capture)
	token := signToken(t, priv, validClaims())

	req := httptest.NewRequest(http.MethodGet, "/v1/zombies", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotOrganizationID != "org_abc" {
		t.Errorf("expected organization_id org_abc, got %q", gotOrganizationID)
	}
}

// ── OPTIONS preflight ─────────────────────────────────────────────────────────

func TestAuth_OPTIONSPassesThrough(t *testing.T) {
	auth, _ := testSetup(t)
	h := auth.Wrap(okHandler)

	req := httptest.NewRequest(http.MethodOptions, "/v1/zombies", nil)
	// no Authorization header
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected OPTIONS to pass through without auth, got %d", w.Code)
	}
}

// /metrics, /livez, /readyz must remain reachable from Prometheus and
// container orchestration without a JWT.
func TestAuth_PublicPathsBypassAuth(t *testing.T) {
	auth, _ := testSetup(t)
	h := auth.Wrap(okHandler)

	for _, path := range []string{"/health", "/livez", "/readyz", "/metrics"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s without auth: expected 200, got %d", path, w.Code)
		}
	}
}

func TestDevBypass_PublicPathsSkipContextPopulation(t *testing.T) {
	captured := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Public paths must not have identity injected — they're for infra.
		if OrganizationID(r.Context()) != "" {
			t.Errorf("public path should not have organization_id populated")
		}
		w.WriteHeader(http.StatusOK)
	})
	h := DevBypass("dev-organization", "dev-user", "dev@x.com", captured)

	for _, path := range []string{"/health", "/livez", "/readyz", "/metrics"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", path, w.Code)
		}
	}
}

// ── JWKS cache behaviour ──────────────────────────────────────────────────────

// mockCache is a minimal in-memory cache for testing.
type mockCache struct {
	data     map[string][]byte
	getErr   error
	setErr   error
	getCalls int
}

func newMockCache() *mockCache { return &mockCache{data: make(map[string][]byte)} }

func (m *mockCache) Get(_ context.Context, key string) ([]byte, error) {
	m.getCalls++
	if m.getErr != nil {
		return nil, m.getErr
	}
	v, ok := m.data[key]
	if !ok {
		return nil, cache.ErrNotFound
	}
	return v, nil
}
func (m *mockCache) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.data[key] = value
	return nil
}
func (m *mockCache) Del(_ context.Context, key string) error                          { delete(m.data, key); return nil }
func (m *mockCache) Incr(_ context.Context, _ string, _ time.Duration) (int64, error) { return 0, nil }
func (m *mockCache) Ping(_ context.Context) error                                     { return nil }
func (m *mockCache) Close() error                                                     { return nil }

// rsaPublicKeyToJWKS serialises an RSA public key into a minimal JWKS JSON.
func rsaPublicKeyToJWKS(t *testing.T, pub *rsa.PublicKey) []byte {
	t.Helper()
	import64 := func(b []byte) string {
		return base64.RawURLEncoding.EncodeToString(b)
	}
	n := import64(pub.N.Bytes())
	e := import64(big.NewInt(int64(pub.E)).Bytes())
	return []byte(`{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":"test-key","n":"` + n + `","e":"` + e + `"}]}`)
}

func TestAuth_JWKSCacheHit_SkipsHTTPFetch(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	fetchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount++
		jwks := rsaPublicKeyToJWKS(t, &priv.PublicKey)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	}))
	defer srv.Close()

	issuer := srv.URL
	jwksURL := issuer + "/.well-known/jwks.json"
	c := newMockCache()

	// First call — cache miss, fetches live and populates cache.
	_, err = keyfuncFromCache(context.Background(), issuer, jwksURL, c)
	if err != nil {
		t.Fatalf("first keyfuncFromCache: %v", err)
	}
	if fetchCount != 1 {
		t.Fatalf("expected 1 HTTP fetch, got %d", fetchCount)
	}

	// Second call — cache hit, no HTTP fetch.
	_, err = keyfuncFromCache(context.Background(), issuer, jwksURL, c)
	if err != nil {
		t.Fatalf("second keyfuncFromCache: %v", err)
	}
	if fetchCount != 1 {
		t.Fatalf("expected still 1 HTTP fetch after cache hit, got %d", fetchCount)
	}
}

func TestAuth_CacheError_FallsBackToLiveFetch(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	fetchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount++
		jwks := rsaPublicKeyToJWKS(t, &priv.PublicKey)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	}))
	defer srv.Close()

	issuer := srv.URL
	jwksURL := issuer + "/.well-known/jwks.json"
	c := newMockCache()
	c.getErr = errors.New("redis: connection refused")

	_, err = keyfuncFromCache(context.Background(), issuer, jwksURL, c)
	if err != nil {
		t.Fatalf("keyfuncFromCache with cache error: %v", err)
	}
	if fetchCount != 1 {
		t.Fatalf("expected live fetch on cache error, got %d fetches", fetchCount)
	}
}
