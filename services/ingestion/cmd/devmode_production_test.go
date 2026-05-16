//go:build production

package main

import (
	"testing"
)

// TestDevModeEnabled_ProductionBuild_IgnoresEnvVar — see
// services/api/cmd/devmode_production_test.go for the full layer-3
// rationale. Mirrors here so an ingestion-side build-tag regression
// surfaces even if the api side stays correct.
func TestDevModeEnabled_ProductionBuild_IgnoresEnvVar(t *testing.T) {
	t.Setenv("DEV_MODE", "true")
	if devModeEnabled() {
		t.Error("devModeEnabled() = true under production build with DEV_MODE=true; layer 3 regression — ingestion binary must ignore DEV_MODE")
	}
	t.Setenv("DEV_MODE", "false")
	if devModeEnabled() {
		t.Error("devModeEnabled() = true under production build with DEV_MODE=false; should never return true regardless of value")
	}
}
