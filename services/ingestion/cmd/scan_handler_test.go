package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"axiaops.io/shared/entitlement"
	"axiaops.io/shared/httpauth"
	"axiaops.io/shared/license"
	"axiaops.io/shared/model"
)

var hmacSecret = []byte("0123456789abcdef0123456789abcdef")

func newProtectedScanMux(t *testing.T, secrets [][]byte) http.Handler {
	t.Helper()
	store := &mockStoreForScheduler{}
	protect := composeHMACProtect(secrets, 5*time.Minute, false)
	mux := http.NewServeMux()
	mux.Handle("POST /scan", protect(http.HandlerFunc(scanHandler(store, nil, 0))))
	return mux
}

// withValidLicense installs a valid in-process license so the scan-gate
// fall-through is enabled while the test exercises the auth path. The
// scan-gate is a sibling check, not the focus of these tests.
func withValidLicense(t *testing.T) {
	t.Helper()
	license.ClearEnforcementBypass()
	license.SetCurrent(&license.License{
		LicenseID:       "lic_scan_test",
		CustomerID:      "scan-test-001",
		ExpiresAt:       time.Now().Add(365 * 24 * time.Hour),
		GracePeriodDays: 30,
	})
	t.Cleanup(func() {
		license.SetCurrent(nil)
		license.SetEnforcementBypass()
	})
}

// newProtectedScanMuxWithResolver wires the SaaS scan-gate path (non-nil
// entitlement resolver). The license is irrelevant on this path.
func newProtectedScanMuxWithResolver(t *testing.T, secrets [][]byte, resolver entitlement.Resolver) http.Handler {
	t.Helper()
	store := &mockStoreForScheduler{}
	protect := composeHMACProtect(secrets, 5*time.Minute, false)
	mux := http.NewServeMux()
	mux.Handle("POST /scan", protect(http.HandlerFunc(scanHandler(store, resolver, 21*24*time.Hour))))
	return mux
}

func signedScanRequest(body []byte) *http.Request {
	now := time.Now()
	req := httptest.NewRequest(http.MethodPost, "/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpauth.HeaderTimestamp, strconv.FormatInt(now.Unix(), 10))
	req.Header.Set(httpauth.HeaderSignature, httpauth.Sign(hmacSecret, now, http.MethodPost, "/scan", body))
	return req
}

// TestScanRoute_SaaS_EntitlementGate covers the ingestion POST /scan SaaS path:
// a suspended org is blocked with not_entitled (gate fires before runScan); an
// active org passes the gate (no 403/401). HMAC still runs first, so this only
// reaches the gate because the request is signed.
func TestScanRoute_SaaS_EntitlementGate(t *testing.T) {
	body := []byte(`{"account_id":"acc-1","organization_id":"org-A"}`)

	suspended := newProtectedScanMuxWithResolver(t, [][]byte{hmacSecret},
		stubResolver{ent: model.Entitlement{Status: model.StatusSuspended}})
	rr := httptest.NewRecorder()
	suspended.ServeHTTP(rr, signedScanRequest(body))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("suspended org: status %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not_entitled") {
		t.Fatalf("suspended org: body %q missing not_entitled", rr.Body.String())
	}

	active := newProtectedScanMuxWithResolver(t, [][]byte{hmacSecret},
		stubResolver{ent: model.Entitlement{Status: model.StatusActive}})
	rr2 := httptest.NewRecorder()
	active.ServeHTTP(rr2, signedScanRequest(body))
	if rr2.Code == http.StatusForbidden || rr2.Code == http.StatusUnauthorized {
		t.Fatalf("active org: gate should pass, got %d; body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestScanRoute_Unsigned_401(t *testing.T) {
	withValidLicense(t)
	mux := newProtectedScanMux(t, [][]byte{hmacSecret})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/scan",
		bytes.NewReader([]byte(`{"account_id":"acc-1","organization_id":"organization-1"}`)))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ingestion_unauthorised") {
		t.Fatalf("body %q missing ingestion_unauthorised", rr.Body.String())
	}
}

func TestScanRoute_Signed_PassesAuth(t *testing.T) {
	withValidLicense(t)
	mux := newProtectedScanMux(t, [][]byte{hmacSecret})

	body := []byte(`{"account_id":"acc-1","organization_id":"organization-1"}`)
	now := time.Now()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpauth.HeaderTimestamp, strconv.FormatInt(now.Unix(), 10))
	req.Header.Set(httpauth.HeaderSignature, httpauth.Sign(hmacSecret, now, http.MethodPost, "/scan", body))
	mux.ServeHTTP(rr, req)

	// The handler may return 200 or 500 depending on mock-store behaviour
	// for an unrecognised account ID — the important assertion is that the
	// HMAC gate accepted the request (no 401).
	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("signed request rejected (401); body=%s", rr.Body.String())
	}
}

func TestScanRoute_DevMode_Passthrough(t *testing.T) {
	// DEV_MODE: no secret configured → PassthroughWithWarning. Unsigned
	// requests reach the inner handler (license gate then runScan).
	withValidLicense(t)
	mux := newProtectedScanMux(t, nil)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/scan",
		bytes.NewReader([]byte(`{"account_id":"acc-1","organization_id":"organization-1"}`)))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rr, req)

	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("DEV_MODE passthrough must not 401; body=%s", rr.Body.String())
	}
}

func TestScanRoute_WrongSecret_401(t *testing.T) {
	withValidLicense(t)
	mux := newProtectedScanMux(t, [][]byte{hmacSecret})

	body := []byte(`{"account_id":"acc-1","organization_id":"organization-1"}`)
	now := time.Now()
	wrong := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpauth.HeaderTimestamp, strconv.FormatInt(now.Unix(), 10))
	req.Header.Set(httpauth.HeaderSignature, httpauth.Sign(wrong, now, http.MethodPost, "/scan", body))
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rr.Code)
	}
}

func TestScanRoute_RotationSecondSecretAccepts(t *testing.T) {
	withValidLicense(t)
	// Verifier has [current=A, next=B] — request signed with B should verify.
	current := hmacSecret
	next := []byte("ffffffffffffffffffffffffffffffff")
	mux := newProtectedScanMux(t, [][]byte{current, next})

	body := []byte(`{"account_id":"acc-1","organization_id":"organization-1"}`)
	now := time.Now()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpauth.HeaderTimestamp, strconv.FormatInt(now.Unix(), 10))
	req.Header.Set(httpauth.HeaderSignature, httpauth.Sign(next, now, http.MethodPost, "/scan", body))
	mux.ServeHTTP(rr, req)

	if rr.Code == http.StatusUnauthorized {
		t.Fatalf("rotation: request signed with NEXT secret rejected; body=%s", rr.Body.String())
	}
}

func TestLoadIngestionSecrets_DevMode_AllowsEmpty(t *testing.T) {
	t.Setenv("INGESTION_SHARED_SECRET", "")
	t.Setenv("INGESTION_SHARED_SECRET_NEXT", "")
	secrets, soft := loadIngestionSecrets(true)
	if len(secrets) != 0 {
		t.Fatalf("DEV_MODE should yield no secrets, got %d", len(secrets))
	}
	if soft {
		t.Fatalf("INGESTION_HMAC_SOFT_ENFORCE unset → soft should be false")
	}
}

func TestLoadIngestionSecrets_BothSlots(t *testing.T) {
	t.Setenv("INGESTION_SHARED_SECRET", "a1b2c3d4e5f60718293a4b5c6d7e8f902132435465768798a9b0c1d2e3f40516")
	t.Setenv("INGESTION_SHARED_SECRET_NEXT", "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")
	t.Setenv("INGESTION_HMAC_SOFT_ENFORCE", "true")
	secrets, soft := loadIngestionSecrets(false)
	if len(secrets) != 2 {
		t.Fatalf("expected 2 secrets (current + next), got %d", len(secrets))
	}
	if !soft {
		t.Fatalf("INGESTION_HMAC_SOFT_ENFORCE=true → soft should be true")
	}
}
