//go:build !production

package main

import (
	"testing"
)

// TestDevModeEnabled_DevBuild_HonoursEnvVar — see services/api/cmd's
// matching test for the full layer-3 regression rationale. Mirroring
// here because the ingestion binary has its own build-tag split.
func TestDevModeEnabled_DevBuild_HonoursEnvVar(t *testing.T) {
	t.Setenv("DEV_MODE", "true")
	if !devModeEnabled() {
		t.Error("devModeEnabled() = false under default build with DEV_MODE=true; want true")
	}
	t.Setenv("DEV_MODE", "false")
	if devModeEnabled() {
		t.Error("devModeEnabled() = true under default build with DEV_MODE=false; want false")
	}
}
