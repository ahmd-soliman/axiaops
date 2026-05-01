package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/shared/license"
	"axiaops.io/shared/model"
)

// withLicense installs a fixture *License via SetCurrent for the duration of
// the test, then resets to nil so subsequent tests don't inherit state.
func withLicense(t *testing.T, lic *license.License) {
	t.Helper()
	license.SetCurrent(lic)
	t.Cleanup(func() { license.SetCurrent(nil) })
}

// TestScanAccount_LicenseGate_ExpiredReturns403 — the single mid-flight
// feature gate B1.6 ships. Past-grace boots are blocked at the api boundary
// before any DB work happens.
func TestScanAccount_LicenseGate_ExpiredReturns403(t *testing.T) {
	withLicense(t, &license.License{
		LicenseID:       "lic_test",
		CustomerID:      "test-001",
		ExpiresAt:       time.Now().Add(-60 * 24 * time.Hour), // 60 days past exp
		GracePeriodDays: 30,                                   // grace ended 30 days ago
	})
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-99", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "eu-west-1"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodPost, "/v1/accounts/acc-99/scan"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d — body: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v — %s", err, w.Body.String())
	}
	if body["error"] != "license_expired" {
		t.Errorf("error = %q, want license_expired", body["error"])
	}
	if body["detail"] == "" {
		t.Error("detail should not be empty — operator needs the renewal contact in the response body")
	}
}

// TestScanAccount_LicenseGate_InGraceFallsThrough — in-grace deliberately
// does NOT block scans (plan §4.9 Option-3 scope). The dashboard banner +
// metrics + slog warns are the renewal-pressure mechanism for this state.
func TestScanAccount_LicenseGate_InGraceFallsThrough(t *testing.T) {
	withLicense(t, &license.License{
		LicenseID:       "lic_test",
		CustomerID:      "test-001",
		ExpiresAt:       time.Now().Add(-2 * 24 * time.Hour),
		GracePeriodDays: 30,
	})
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-99", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "eu-west-1"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodPost, "/v1/accounts/acc-99/scan"))

	if w.Code != http.StatusOK {
		t.Fatalf("in-grace must allow scans (Option-3 scope), got %d — body: %s", w.Code, w.Body.String())
	}
}

// TestScanAccount_LicenseGate_NotLoadedFallsThrough — DEV_MODE / SaaS leave
// SetCurrent at nil; the gate must NOT block scans in that case (DEV_MODE
// bypass; SaaS gating is by Stripe, not license file).
func TestScanAccount_LicenseGate_NotLoadedFallsThrough(t *testing.T) {
	license.SetCurrent(nil) // explicit
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-99", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "eu-west-1"},
		})
	_, mux := newTrackingHandler(mockStore)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodPost, "/v1/accounts/acc-99/scan"))

	if w.Code != http.StatusOK {
		t.Fatalf("StateNotLoaded must fall through (DEV_MODE / SaaS), got %d — body: %s", w.Code, w.Body.String())
	}
}

// TestScanAccount_LicenseGate_OnlyScanRouteIsGated — regression guard for
// the Option-3 scope decision: dismissals, account CRUD, member mgmt, GDPR
// erasure all stay open under expired-past-grace. Slice-2 plan §4.9.7
// acceptance criterion. We exercise one representative endpoint here; the
// principle is that the gate lives ONLY in scanAccount and adding new gated
// endpoints requires explicit code, not implicit policy.
func TestScanAccount_LicenseGate_OnlyScanRouteIsGated(t *testing.T) {
	withLicense(t, &license.License{
		LicenseID:       "lic_test",
		CustomerID:      "test-001",
		ExpiresAt:       time.Now().Add(-60 * 24 * time.Hour),
		GracePeriodDays: 30,
	})
	mockStore := NewMockStore().
		WithAccounts([]model.Account{
			{ID: "acc-99", OrganizationID: "organization-test-uuid", Provider: "aws", AccessKeyID: "AKIA", Region: "eu-west-1"},
		})
	_, mux := newTrackingHandler(mockStore)

	// GET /v1/accounts must succeed — only POST /v1/accounts/{id}/scan is gated.
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/accounts"))
	if w.Code == http.StatusForbidden {
		t.Errorf("GET /v1/accounts incorrectly gated by license — only the scan route should be gated")
	}
}
