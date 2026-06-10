package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"axiaops.io/shared/license"
)

// selfHostedLicensePosture clears the package-wide enforcement bypass (which
// test_main_test.go sets on by default) so a test sees the real self-hosted
// license sub-object, then restores the default on cleanup. Needed since
// licenseSummary now collapses to {state:"managed"} when bypass is on (SaaS).
func selfHostedLicensePosture(t *testing.T) {
	t.Helper()
	license.ClearEnforcementBypass()
	t.Cleanup(license.SetEnforcementBypass)
}

// /v1/version is auth-required (sits under /v1/) but doesn't read organization data,
// so an organization context is enough — no zombies/dismissals fixtures needed.
//
// NB: the LicenseLoaded / LicenseInGrace / LicenseExpired tests mutate the
// package-level atomic.Pointer in services/shared/license via SetCurrent.
// Do NOT add t.Parallel() to these tests — the snapshot is process-wide and
// concurrent SetCurrent calls would race. t.Cleanup(SetCurrent(nil)) is
// enough for sequential isolation but is not a parallel-safety mechanism.

func TestVersion_DefaultsWhenEnvUnset(t *testing.T) {
	t.Setenv("APP_VERSION", "")
	t.Setenv("APP_COMMIT_SHA", "")
	t.Setenv("APP_ENV", "")
	selfHostedLicensePosture(t)
	_, mux := testHandler()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/version"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["service"] != "api" {
		t.Errorf("service: got %q, want api", resp["service"])
	}
	if resp["version"] != "dev" {
		t.Errorf("version fallback: got %q, want dev", resp["version"])
	}
	if resp["commit"] != "local" {
		t.Errorf("commit fallback: got %q, want local", resp["commit"])
	}
	if resp["env"] != "development" {
		t.Errorf("env fallback: got %q, want development", resp["env"])
	}
	// Slice 6 — license sub-object is always present. Tests don't call
	// license.SetCurrent, so state is "not_loaded" and no other fields appear.
	lic, ok := resp["license"].(map[string]any)
	if !ok {
		t.Fatalf("license: missing or wrong type, got %T", resp["license"])
	}
	if lic["state"] != "not_loaded" {
		t.Errorf("license.state: got %v, want not_loaded", lic["state"])
	}
	if _, present := lic["customer_id"]; present {
		t.Errorf("license.customer_id should be omitted when not loaded, got %v", lic["customer_id"])
	}
	if _, present := lic["expires_at"]; present {
		t.Errorf("license.expires_at should be omitted when not loaded, got %v", lic["expires_at"])
	}
}

func TestVersion_HonoursEnvVars(t *testing.T) {
	t.Setenv("APP_VERSION", "v2.6.0")
	t.Setenv("APP_COMMIT_SHA", "abc1234")
	t.Setenv("APP_ENV", "production")
	_, mux := testHandler()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/version"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["version"] != "v2.6.0" {
		t.Errorf("version: got %q, want v2.6.0", resp["version"])
	}
	if resp["commit"] != "abc1234" {
		t.Errorf("commit: got %q, want abc1234", resp["commit"])
	}
	if resp["env"] != "production" {
		t.Errorf("env: got %q, want production", resp["env"])
	}
}

// TestVersion_LicenseLoaded covers the slice-6 license sub-object when a
// boot-time License is set. Drives license.SetCurrent directly — bypassing
// VerifyAtBoot keeps the test independent of the JWT signing fixture, which
// is exercised in services/shared/license/license_test.go.
//
// Cleanup nils the snapshot so subsequent tests in the package see "not_loaded"
// — package-level atomic.Pointer state would otherwise leak across tests.
func TestVersion_LicenseLoaded(t *testing.T) {
	selfHostedLicensePosture(t)
	expires := time.Now().Add(45 * 24 * time.Hour).UTC().Truncate(time.Second)
	license.SetCurrent(&license.License{
		LicenseID:        "lic_test_2026",
		CustomerID:       "acme-001",
		ContractID:       "MSA-2026-007",
		IssuedAt:         time.Now().Add(-30 * 24 * time.Hour),
		ExpiresAt:        expires,
		MaxOrganizations: 5,
		Features:         []string{"base"},
		GracePeriodDays:  30,
	})
	t.Cleanup(func() { license.SetCurrent(nil) })

	_, mux := testHandler()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/version"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	lic, ok := resp["license"].(map[string]any)
	if !ok {
		t.Fatalf("license: missing or wrong type, got %T", resp["license"])
	}
	if lic["state"] != "valid" {
		t.Errorf("license.state: got %v, want valid", lic["state"])
	}
	if lic["customer_id"] != "acme-001" {
		t.Errorf("license.customer_id: got %v, want acme-001", lic["customer_id"])
	}
	if got, want := lic["expires_at"], expires.Format(time.RFC3339); got != want {
		t.Errorf("license.expires_at: got %v, want %s", got, want)
	}
	// JSON numbers decode as float64 in map[string]any.
	if got, ok := lic["max_organizations"].(float64); !ok || int(got) != 5 {
		t.Errorf("license.max_organizations: got %v (%T), want 5", lic["max_organizations"], lic["max_organizations"])
	}
	// 45 days to exp + 30 days grace = ~75 days remaining. Allow a 1-day
	// floor for clock-edge cases (the floor in DaysRemaining can give us 74
	// if the test crosses a UTC midnight mid-call).
	days, ok := lic["days_remaining"].(float64)
	if !ok {
		t.Fatalf("license.days_remaining: wrong type, got %T", lic["days_remaining"])
	}
	if days < 73 || days > 75 {
		t.Errorf("license.days_remaining: got %v, want ~75 (±1)", days)
	}
}

// TestVersion_LicenseInGrace covers the operationally most-relevant state —
// past exp but inside the grace window. This is the state the slice-8
// LicenseBanner fires on for renewal nags, so a regression in the state-string
// (e.g. "in grace" with a space, dropped underscore) would silently break
// the banner. Days-remaining stays positive because the hard cutoff is
// 30 days out from exp.
func TestVersion_LicenseInGrace(t *testing.T) {
	selfHostedLicensePosture(t)
	expires := time.Now().Add(-10 * 24 * time.Hour).UTC().Truncate(time.Second)
	license.SetCurrent(&license.License{
		LicenseID:        "lic_in_grace",
		CustomerID:       "acme-001",
		ExpiresAt:        expires,
		MaxOrganizations: 5,
		GracePeriodDays:  30,
	})
	t.Cleanup(func() { license.SetCurrent(nil) })

	_, mux := testHandler()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/version"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	lic, ok := resp["license"].(map[string]any)
	if !ok {
		t.Fatalf("license: missing or wrong type, got %T", resp["license"])
	}
	if lic["state"] != "in_grace" {
		t.Errorf("license.state: got %v, want in_grace", lic["state"])
	}
	days, ok := lic["days_remaining"].(float64)
	if !ok {
		t.Fatalf("license.days_remaining: wrong type, got %T", lic["days_remaining"])
	}
	// 30-day grace minus 10 days past exp = ~20 days. Allow ±1 for
	// midnight-edge floor cases.
	if days < 19 || days > 21 {
		t.Errorf("license.days_remaining: got %v, want ~20 (±1)", days)
	}
}

// TestVersion_LicenseExpired covers the past-grace state. The boot-time
// License is loaded but CheckExpiry classifies it as expired — the version
// endpoint reports state="expired" with negative days_remaining so dashboards
// can surface the renewal banner without reaching for /v1/me.
func TestVersion_LicenseExpired(t *testing.T) {
	selfHostedLicensePosture(t)
	// exp 60 days ago, 30-day grace → 30 days past hard cutoff.
	expires := time.Now().Add(-60 * 24 * time.Hour).UTC().Truncate(time.Second)
	license.SetCurrent(&license.License{
		LicenseID:        "lic_expired",
		CustomerID:       "acme-001",
		ExpiresAt:        expires,
		MaxOrganizations: 5,
		GracePeriodDays:  30,
	})
	t.Cleanup(func() { license.SetCurrent(nil) })

	_, mux := testHandler()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/version"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	lic, ok := resp["license"].(map[string]any)
	if !ok {
		t.Fatalf("license: missing or wrong type, got %T", resp["license"])
	}
	if lic["state"] != "expired" {
		t.Errorf("license.state: got %v, want expired", lic["state"])
	}
	days, ok := lic["days_remaining"].(float64)
	if !ok {
		t.Fatalf("license.days_remaining: wrong type, got %T", lic["days_remaining"])
	}
	if days >= 0 {
		t.Errorf("license.days_remaining: got %v, want negative (past hard cutoff)", days)
	}
}

// TestVersion_ManagedWhenBypassed pins the SaaS posture (cmd/api-saashosted):
// when the license enforcement is bypassed, /v1/version collapses the license
// sub-object to {state:"managed"} and emits NO license fields — there is no
// customer-facing license under SaaS (design §7.4), so the dashboard hides the
// License banner/page. A real license snapshot is loaded to prove bypass wins
// over a present license.
func TestVersion_ManagedWhenBypassed(t *testing.T) {
	// Bypass is the package default (test_main_test.go); set it explicitly for
	// legibility and to be robust if a prior test cleared it.
	license.SetEnforcementBypass()
	license.SetCurrent(&license.License{
		CustomerID:       "acme-001",
		ExpiresAt:        time.Now().Add(45 * 24 * time.Hour),
		MaxOrganizations: 5,
		GracePeriodDays:  30,
	})
	t.Cleanup(func() { license.SetCurrent(nil) })

	_, mux := testHandler()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, orgRequest(http.MethodGet, "/v1/version"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	lic, ok := resp["license"].(map[string]any)
	if !ok {
		t.Fatalf("license: missing or wrong type, got %T", resp["license"])
	}
	if lic["state"] != "managed" {
		t.Errorf("license.state: got %v, want managed", lic["state"])
	}
	for _, k := range []string{"customer_id", "expires_at", "days_remaining", "max_organizations"} {
		if _, present := lic[k]; present {
			t.Errorf("license.%s must be omitted under SaaS managed state, got %v", k, lic[k])
		}
	}
}
