package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// /v1/version is auth-required (sits under /v1/) but doesn't read organization
// data, so an organization context is enough — no zombies/dismissals fixtures
// needed.

func TestVersion_DefaultsWhenEnvUnset(t *testing.T) {
	t.Setenv("APP_VERSION", "")
	t.Setenv("APP_COMMIT_SHA", "")
	t.Setenv("APP_ENV", "")
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
	if _, present := resp["license"]; present {
		t.Errorf("license field should no longer be present, got %v", resp["license"])
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
