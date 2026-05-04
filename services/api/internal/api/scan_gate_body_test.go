package api

import (
	"encoding/json"
	"strings"
	"testing"

	"axiaops.io/shared/license"
)

// TestScanGateBody_KnownStates pins the JSON body for every license state
// the scan-gate currently classifies as blocking. Adding a new blocking
// state requires extending this test; the runtime slog.Warn in the default
// branch is the safety net for shipping a new state without test coverage.
func TestScanGateBody_KnownStates(t *testing.T) {
	cases := []struct {
		name      string
		state     license.State
		wantError string
	}{
		{"expired", license.StateExpired, "license_expired"},
		{"not_loaded", license.StateNotLoaded, "license_not_loaded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := scanGateBody(tc.state)
			var parsed map[string]string
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatalf("body not JSON: %v — %s", err, body)
			}
			if parsed["error"] != tc.wantError {
				t.Errorf("error = %q, want %q", parsed["error"], tc.wantError)
			}
			if parsed["detail"] == "" {
				t.Errorf("detail should be non-empty — operators rely on it for actionable copy")
			}
		})
	}
}

// TestScanGateBody_UnhandledStateFallsThrough is the regression guard: a
// future blocking state added to license.State (e.g. StateRevoked,
// StateTrialExpired) that ships without a corresponding scanGateBody case
// must surface as license_inactive, NOT as the install-URL copy. Pre-fix
// this test would have failed because the default branch returned
// license_not_loaded for any non-StateExpired value.
//
// Synthesising an unknown state by casting a higher integer than the iota
// reaches is fragile to State changes — but State is iota-typed and any
// real new state would extend the iota, exercising this test branch via
// the compiler. The key assertion is "default-case body is the generic
// inactive copy, not a wrong-state-specific copy".
func TestScanGateBody_UnhandledStateFallsThrough(t *testing.T) {
	// Cast a value past the current iota range — the package's State String()
	// returns "unknown" for these per services/shared/license/license.go.
	body := scanGateBody(license.State(99))
	var parsed map[string]string
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v — %s", err, body)
	}
	if parsed["error"] != "license_inactive" {
		t.Errorf("error = %q, want license_inactive — unhandled states must NOT silently mis-classify as license_not_loaded or license_expired", parsed["error"])
	}
	// Defensive: the body must NOT contain the install URL or renewal
	// contact, because those are state-specific copy that would mislead an
	// operator into the wrong remediation.
	bodyStr := string(body)
	if strings.Contains(bodyStr, "axiaops.io/install") {
		t.Error("unhandled-state body should not include the install URL — that's StateNotLoaded copy")
	}
	if strings.Contains(bodyStr, "sales@axiaops.io") {
		t.Error("unhandled-state body should not include the renewal contact — that's StateExpired copy")
	}
}

// TestScanGateBody_AllowListedStates_FallThroughCopy — defensive: if a
// caller mistakenly invokes scanGateBody for StateValid or StateInGrace
// (these are allow-listed by IsScanAllowedForState and shouldn't reach the
// gate body builder), it should return the generic inactive copy + a
// slog.Warn at runtime, NOT silently fall back to the install URL.
func TestScanGateBody_AllowListedStates_FallThroughCopy(t *testing.T) {
	for _, state := range []license.State{license.StateValid, license.StateInGrace} {
		t.Run(state.String(), func(t *testing.T) {
			body := scanGateBody(state)
			var parsed map[string]string
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatalf("body not JSON: %v — %s", err, body)
			}
			if parsed["error"] != "license_inactive" {
				t.Errorf("error = %q, want license_inactive — allow-listed state reaching the body builder is a misalignment, surface as inactive not install/renewal", parsed["error"])
			}
		})
	}
}
