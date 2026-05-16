//go:build production

package main

import (
	"testing"
)

// TestDevModeEnabled_ProductionBuild_IgnoresEnvVar pins the production-tag
// shape: regardless of DEV_MODE, devModeEnabled() returns false. This is
// the load-bearing assertion for B1.7 layer 3 (plan §4.10.2) — without it
// a build-tag regression that recompiled devmode_dev.go into the
// production binary would silently re-introduce the bypass.
//
// Three values exercised: "true", "false", and unset, because someone
// reading the test should be able to verify by inspection that there is
// no env-var value that flips the production gate.
func TestDevModeEnabled_ProductionBuild_IgnoresEnvVar(t *testing.T) {
	t.Setenv("DEV_MODE", "true")
	if devModeEnabled() {
		t.Error("devModeEnabled() = true under production build with DEV_MODE=true; layer 3 regression — production binary must ignore DEV_MODE")
	}
	t.Setenv("DEV_MODE", "false")
	if devModeEnabled() {
		t.Error("devModeEnabled() = true under production build with DEV_MODE=false; should never return true regardless of value")
	}
	t.Setenv("DEV_MODE", "")
	if devModeEnabled() {
		t.Error("devModeEnabled() = true under production build with DEV_MODE unset; should never return true regardless of value")
	}
}
