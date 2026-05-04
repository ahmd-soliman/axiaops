// White-box tests (`package api`, NOT `package api_test`) so we can reach
// the unexported scanGateBody helper directly. The CLAUDE.md convention is
// black-box for handler tests; this file is the deliberate exception
// because the function under test is itself unexported and exposing it
// purely for testing would weaken the encapsulation.

package api

import (
	"encoding/json"
	"strings"
	"testing"

	"axiaops.io/shared/license"
)

// scanGateBodyExpectations declares the contract per known state: which
// states are blocking (and produce a state-specific body) vs allow-listed
// (and produce the defensive inactive body). Sourced from license.AllStates
// so adding a new state to the iota WITHOUT adding it here fails
// TestScanGateBody_ExhaustiveCoverage below — that test is the
// fail-loudly-when-iota-grows guard.
var scanGateBodyExpectations = map[license.State]struct {
	wantError string
	blocking  bool // false = allow-listed, defensive case fires
}{
	license.StateValid:     {wantError: "license_inactive", blocking: false},
	license.StateInGrace:   {wantError: "license_inactive", blocking: false},
	license.StateExpired:   {wantError: "license_expired", blocking: true},
	license.StateNotLoaded: {wantError: "license_not_loaded", blocking: true},
}

// TestScanGateBody_ExhaustiveCoverage is the fail-loudly-when-iota-grows
// regression pin: it asserts that every license.State value covered by
// license.AllStates has an explicit expectation in
// scanGateBodyExpectations above. Adding `StateRevoked` or
// `StateTrialExpired` to the iota requires:
//   1. Append the new state to license.AllStates (one line).
//   2. Add the state to scanGateBodyExpectations here.
//   3. Add the state to scanGateBody's switch in handler.go.
// Forgetting (2) fails this test; forgetting (3) fails
// TestScanGateBody_KnownStates with the wrong error code; forgetting (1)
// fails this test by skipping the new state's coverage entirely. All three
// forgetting-modes surface in CI before merge.
func TestScanGateBody_ExhaustiveCoverage(t *testing.T) {
	for _, state := range license.AllStates {
		if _, ok := scanGateBodyExpectations[state]; !ok {
			t.Errorf("license.State %q (%d) is in license.AllStates but missing from scanGateBodyExpectations — add an entry above and update scanGateBody's switch in handler.go", state.String(), state)
		}
	}
	if got, want := len(scanGateBodyExpectations), len(license.AllStates); got != want {
		t.Errorf("scanGateBodyExpectations has %d entries; license.AllStates has %d — they must match exactly", got, want)
	}
}

// TestScanGateBody_KnownStates pins the JSON body for every license state
// the scan-gate currently classifies. Iterates AllStates so a new state in
// the iota force-surfaces here as a "no expectation" failure (via the
// scanGateBodyExpectations map miss) before silently mis-classifying.
func TestScanGateBody_KnownStates(t *testing.T) {
	for _, state := range license.AllStates {
		exp, ok := scanGateBodyExpectations[state]
		if !ok {
			continue // covered by TestScanGateBody_ExhaustiveCoverage above
		}
		t.Run(state.String(), func(t *testing.T) {
			body := scanGateBody(state)
			var parsed map[string]string
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatalf("body not JSON: %v — %s", err, body)
			}
			if parsed["error"] != exp.wantError {
				t.Errorf("error = %q, want %q", parsed["error"], exp.wantError)
			}
			if parsed["detail"] == "" {
				t.Errorf("detail should be non-empty — operators rely on it for actionable copy")
			}
			if exp.blocking {
				return
			}
			// Allow-listed states should produce the inactive body, NOT
			// state-specific copy that would mislead operators into the
			// wrong remediation if scanGateBody is invoked defensively.
			bodyStr := string(body)
			if strings.Contains(bodyStr, "axiaops.io/install") {
				t.Error("allow-listed-state body should not include the install URL — that's StateNotLoaded copy")
			}
			if strings.Contains(bodyStr, "sales@axiaops.io") {
				t.Error("allow-listed-state body should not include the renewal contact — that's StateExpired copy")
			}
		})
	}
}

// TestScanGateBody_UnhandledStateFallsThrough is the run-time defence
// against a State value that exists at compile time but isn't in any of
// the switch cases. Cast past the current iota range — String() returns
// "unknown" for these. This catches a different bug shape than
// TestScanGateBody_ExhaustiveCoverage (which catches "iota grew but
// expectations didn't"); this catches "switch case dropped" or "switch
// fell through to default by mistake".
func TestScanGateBody_UnhandledStateFallsThrough(t *testing.T) {
	body := scanGateBody(license.State(99))
	var parsed map[string]string
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("body not JSON: %v — %s", err, body)
	}
	if parsed["error"] != "license_inactive" {
		t.Errorf("error = %q, want license_inactive — unhandled states must NOT silently mis-classify as license_not_loaded or license_expired", parsed["error"])
	}
	bodyStr := string(body)
	if strings.Contains(bodyStr, "axiaops.io/install") {
		t.Error("unhandled-state body should not include the install URL — that's StateNotLoaded copy")
	}
	if strings.Contains(bodyStr, "sales@axiaops.io") {
		t.Error("unhandled-state body should not include the renewal contact — that's StateExpired copy")
	}
}

// TestScanGateBody_AllowListedStates_FallThroughCopy was here pre-rebase;
// its assertion is now subsumed by TestScanGateBody_KnownStates which
// iterates license.AllStates and applies the allow-listed/blocking
// expectation per state. Removed to keep the test file as the single
// source of state-coverage truth.
