package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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

	req := httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuth_MalformedAuthorizationHeader_Returns401(t *testing.T) {
	auth, _ := testSetup(t)
	h := auth.Wrap(okHandler)

	req := httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAuth_ValidToken_TenantIDInContext(t *testing.T) {
	auth, priv := testSetup(t)

	var gotTenantID string
	capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTenantID = TenantID(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	h := auth.Wrap(capture)
	token := signToken(t, priv, validClaims())

	req := httptest.NewRequest(http.MethodGet, "/v1/ghosts", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if gotTenantID != "org_abc" {
		t.Errorf("expected tenant_id org_abc, got %q", gotTenantID)
	}
}

// ── OPTIONS preflight ─────────────────────────────────────────────────────────

func TestAuth_OPTIONSPassesThrough(t *testing.T) {
	auth, _ := testSetup(t)
	h := auth.Wrap(okHandler)

	req := httptest.NewRequest(http.MethodOptions, "/v1/ghosts", nil)
	// no Authorization header
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected OPTIONS to pass through without auth, got %d", w.Code)
	}
}
