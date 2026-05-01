package middleware_test

import (
	"errors"
	"net/http/httptest"
	"testing"

	"axiaops.io/api/internal/auth"
	"axiaops.io/api/internal/middleware"
)

// KindeProvider's happy path duplicates the JWT-validate + DB-upsert chain
// in Auth.Wrap byte-for-byte. The DB-write portion has no in-package
// happy-path coverage today (existing auth_test.go uses newWithKeyfunc
// → nil store) and is exercised end-to-end via start-staging against a
// real Kinde tenant. These tests focus on the *new* surface area:
// the negative paths where KindeProvider must collapse to ErrUnauthenticated
// and not echo internal reasons to the caller.

func TestKindeProviderRequiresNonNilAuth(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("middleware.NewKindeProvider(nil) must panic")
		}
	}()
	_ = middleware.NewKindeProvider(nil)
}

func TestKindeProviderRejectsMissingBearer(t *testing.T) {
	t.Parallel()
	a, _ := testSetup(t)
	p := middleware.NewKindeProvider(a)
	_, err := p.Authenticate(httptest.NewRequest("GET", "/v1/zombies", nil))
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate without bearer = %v; want ErrUnauthenticated", err)
	}
}

func TestKindeProviderRejectsMalformedJWT(t *testing.T) {
	t.Parallel()
	a, _ := testSetup(t)
	p := middleware.NewKindeProvider(a)
	r := httptest.NewRequest("GET", "/v1/zombies", nil)
	r.Header.Set("Authorization", "Bearer this-is-not-a-jwt")
	_, err := p.Authenticate(r)
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate w/ malformed JWT = %v; want ErrUnauthenticated", err)
	}
}

func TestKindeProviderRejectsWrongIssuer(t *testing.T) {
	t.Parallel()
	a, priv := testSetup(t)
	p := middleware.NewKindeProvider(a)
	claims := validClaims()
	claims["iss"] = "https://attacker.example.com"
	r := httptest.NewRequest("GET", "/v1/zombies", nil)
	r.Header.Set("Authorization", "Bearer "+signToken(t, priv, claims))
	_, err := p.Authenticate(r)
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate w/ wrong issuer = %v; want ErrUnauthenticated", err)
	}
}

func TestKindeProviderRejectsMissingOrgCode(t *testing.T) {
	t.Parallel()
	a, priv := testSetup(t)
	p := middleware.NewKindeProvider(a)
	claims := validClaims()
	delete(claims, "org_code")
	r := httptest.NewRequest("GET", "/v1/zombies", nil)
	r.Header.Set("Authorization", "Bearer "+signToken(t, priv, claims))
	_, err := p.Authenticate(r)
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate w/ missing org_code = %v; want ErrUnauthenticated", err)
	}
}

func TestKindeProviderRejectsNilStore(t *testing.T) {
	// `store == nil` is a misconfiguration (only reachable via the
	// test-only newWithKeyfunc shape). Production wiring always supplies
	// a real Store. Surface it as ErrUnauthenticated rather than letting
	// the request pass with a degraded identity.
	t.Parallel()
	a, priv := testSetup(t)
	p := middleware.NewKindeProvider(a)
	r := httptest.NewRequest("GET", "/v1/zombies", nil)
	r.Header.Set("Authorization", "Bearer "+signToken(t, priv, validClaims()))
	_, err := p.Authenticate(r)
	if !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("Authenticate w/ nil store = %v; want ErrUnauthenticated", err)
	}
}

