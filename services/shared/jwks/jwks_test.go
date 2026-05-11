package jwks_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"axiaops.io/shared/cache"
	"axiaops.io/shared/jwks"
)

// rsaPublicKeyToJWKS serialises an RSA public key into a minimal JWKS JSON.
// Used by both call shapes the package supports — tests are written against
// real RSA keys rather than mocked keyfuncs so the FromBytes parse path is
// exercised end-to-end.
func rsaPublicKeyToJWKS(t *testing.T, pub *rsa.PublicKey) []byte {
	t.Helper()
	enc := func(b []byte) string {
		return base64.RawURLEncoding.EncodeToString(b)
	}
	n := enc(pub.N.Bytes())
	e := enc(big.NewInt(int64(pub.E)).Bytes())
	return []byte(`{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":"test-key","n":"` + n + `","e":"` + e + `"}]}`)
}

// mockCache is an in-memory cache implementation for tests. Allows
// injecting a get-error to exercise the cache-error → live-fetch fall-through.
type mockCache struct {
	mu     sync.Mutex
	data   map[string][]byte
	getErr error
}

func newMockCache() *mockCache { return &mockCache{data: make(map[string][]byte)} }

func (m *mockCache) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
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

func (m *mockCache) GetDel(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	v, ok := m.data[key]
	if !ok {
		return nil, cache.ErrNotFound
	}
	delete(m.data, key)
	return v, nil
}

func (m *mockCache) Incr(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockCache) Ping(_ context.Context) error { return nil }
func (m *mockCache) Close() error                 { return nil }

// has reports whether the cache holds an entry for key. Test-only helper
// so callers don't reach into m.data directly and bypass the mutex.
func (m *mockCache) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.data[key]
	return ok
}

// TestFromCache_CacheHit_SkipsHTTPFetch covers the issuer-bound consumer
// (Kinde-style: one stable issuer URL, one JWKS endpoint). First call
// populates the cache; second call must hit the cache and skip the HTTP
// fetch — the test counts fetches against the test server.
func TestFromCache_CacheHit_SkipsHTTPFetch(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	fetchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rsaPublicKeyToJWKS(t, &priv.PublicKey))
	}))
	defer srv.Close()

	cacheID := srv.URL // issuer URL — Kinde call shape
	jwksURL := srv.URL + "/.well-known/jwks.json"
	c := newMockCache()

	if _, err := jwks.FromCache(context.Background(), cacheID, jwksURL, c); err != nil {
		t.Fatalf("first FromCache: %v", err)
	}
	if fetchCount != 1 {
		t.Fatalf("expected 1 HTTP fetch after cache miss, got %d", fetchCount)
	}

	if _, err := jwks.FromCache(context.Background(), cacheID, jwksURL, c); err != nil {
		t.Fatalf("second FromCache: %v", err)
	}
	if fetchCount != 1 {
		t.Fatalf("expected still 1 HTTP fetch after cache hit, got %d", fetchCount)
	}
}

// TestFromCache_CacheError_FallsBackToLiveFetch — when the cache layer
// returns an error other than ErrNotFound, FromCache must not block auth;
// it logs a warn and proceeds with a live fetch.
func TestFromCache_CacheError_FallsBackToLiveFetch(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	fetchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rsaPublicKeyToJWKS(t, &priv.PublicKey))
	}))
	defer srv.Close()

	c := newMockCache()
	c.getErr = errors.New("redis: connection refused")

	if _, err := jwks.FromCache(context.Background(), srv.URL, srv.URL+"/.well-known/jwks.json", c); err != nil {
		t.Fatalf("FromCache with cache error: %v", err)
	}
	if fetchCount != 1 {
		t.Fatalf("expected live fetch on cache error, got %d", fetchCount)
	}
}

// TestFromCache_PerConnectionShape covers the per-connection consumer
// (OIDC RP call shape, B2): cacheID is an opaque connection identifier, NOT
// an issuer URL. Two distinct connections sharing one HTTP origin must end
// up with two cache entries — the cache key is derived from cacheID, not
// jwksURL. Without this, two SSO connections to the same IdP collapse onto
// one cache entry and a key rotation on one breaks the other.
func TestFromCache_PerConnectionShape(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	fetchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rsaPublicKeyToJWKS(t, &priv.PublicKey))
	}))
	defer srv.Close()

	jwksURL := srv.URL + "/.well-known/jwks.json"
	c := newMockCache()

	if _, err := jwks.FromCache(context.Background(), "conn_acme", jwksURL, c); err != nil {
		t.Fatalf("conn_acme FromCache: %v", err)
	}
	if _, err := jwks.FromCache(context.Background(), "conn_globex", jwksURL, c); err != nil {
		t.Fatalf("conn_globex FromCache: %v", err)
	}
	if fetchCount != 2 {
		t.Fatalf("two distinct connections must produce two fetches; got %d", fetchCount)
	}

	// Re-call conn_acme — must hit cache.
	if _, err := jwks.FromCache(context.Background(), "conn_acme", jwksURL, c); err != nil {
		t.Fatalf("conn_acme second FromCache: %v", err)
	}
	if fetchCount != 2 {
		t.Fatalf("conn_acme re-call must hit cache; got %d fetches", fetchCount)
	}
}

// TestFromCache_NilCache — callers may pass a nil cache (e.g. dev-mode
// without Redis); FromCache must always fetch live and not panic.
func TestFromCache_NilCache(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	fetchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rsaPublicKeyToJWKS(t, &priv.PublicKey))
	}))
	defer srv.Close()

	if _, err := jwks.FromCache(context.Background(), srv.URL, srv.URL+"/.well-known/jwks.json", nil); err != nil {
		t.Fatalf("FromCache(nil cache): %v", err)
	}
	if _, err := jwks.FromCache(context.Background(), srv.URL, srv.URL+"/.well-known/jwks.json", nil); err != nil {
		t.Fatalf("FromCache(nil cache) second call: %v", err)
	}
	if fetchCount != 2 {
		t.Fatalf("nil cache must always fetch live; got %d fetches", fetchCount)
	}
}

// TestFromBytes_InvalidPayload — a malformed JWKS body surfaces a parse
// error rather than a working keyfunc that fails later at signature time.
func TestFromBytes_InvalidPayload(t *testing.T) {
	if _, err := jwks.FromBytes([]byte("not json")); err == nil {
		t.Fatal("FromBytes(invalid JSON) succeeded; want parse error")
	}
}

// TestFromCache_NonOKStatus — a 404 / 503 from the IdP must surface a
// status error rather than reaching FromBytes and producing a confusing
// "parse" error. Also asserts the cache is NOT populated with the error
// body.
func TestFromCache_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newMockCache()
	cacheID := "broken_idp"
	if _, err := jwks.FromCache(context.Background(), cacheID, srv.URL+"/.well-known/jwks.json", c); err == nil {
		t.Fatal("FromCache(503) succeeded; want status error")
	}
	if c.has(jwks.CacheKey(cacheID)) {
		t.Fatal("FromCache(503) cached the error body; cache should be untouched on non-2xx")
	}
}

// TestFromCache_ForceRefreshViaCacheKey — exercises the auto-refresh
// pattern (plan §5 architect S5): caller can force a re-fetch by deleting
// the cache key and calling FromCache again. Proves the exported CacheKey
// helper produces the same key FromCache writes.
func TestFromCache_ForceRefreshViaCacheKey(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	fetchCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetchCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rsaPublicKeyToJWKS(t, &priv.PublicKey))
	}))
	defer srv.Close()

	cacheID := "conn_rotated"
	jwksURL := srv.URL + "/.well-known/jwks.json"
	c := newMockCache()

	// Prime the cache.
	if _, err := jwks.FromCache(context.Background(), cacheID, jwksURL, c); err != nil {
		t.Fatalf("prime FromCache: %v", err)
	}
	if fetchCount != 1 {
		t.Fatalf("expected 1 fetch, got %d", fetchCount)
	}

	// Force eviction via the exported helper, then re-call.
	if err := c.Del(context.Background(), jwks.CacheKey(cacheID)); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if _, err := jwks.FromCache(context.Background(), cacheID, jwksURL, c); err != nil {
		t.Fatalf("post-evict FromCache: %v", err)
	}
	if fetchCount != 2 {
		t.Fatalf("expected re-fetch after eviction, got %d total fetches", fetchCount)
	}
}
