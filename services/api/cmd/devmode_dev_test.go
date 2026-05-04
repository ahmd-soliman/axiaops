//go:build !production

package main

import (
	"testing"
)

// TestDevModeEnabled_DevBuild_HonoursEnvVar pins the default-build (dev)
// shape: devModeEnabled() reads DEV_MODE at runtime. The production-build
// counterpart in devmode_production_test.go asserts the inverse. Together
// they form the layer-3 regression pin (plan §4.10.2): a future bug that
// flipped the build-tag gate, accidentally compiled the wrong file, or
// silently wired devModeEnabled() to a different source would fail one or
// both halves of this test pair before it could ship.
func TestDevModeEnabled_DevBuild_HonoursEnvVar(t *testing.T) {
	t.Setenv("DEV_MODE", "true")
	if !devModeEnabled() {
		t.Error("devModeEnabled() = false under default build with DEV_MODE=true; want true")
	}
	t.Setenv("DEV_MODE", "false")
	if devModeEnabled() {
		t.Error("devModeEnabled() = true under default build with DEV_MODE=false; want false")
	}
	t.Setenv("DEV_MODE", "")
	if devModeEnabled() {
		t.Error("devModeEnabled() = true under default build with DEV_MODE unset; want false")
	}
}
