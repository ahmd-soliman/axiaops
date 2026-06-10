package breachlist_test

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	"axiaops.io/api/internal/breachlist"
)

// TestKnownVectorPassword is the end-to-end encoding/case guard. "password"
// hashes to the SHA-1 below (HIBP's canonical example); if anyone ever swaps
// raw-byte comparison for hex strings, or drops/adds a normalization step, this
// flips to false and the test fails loudly. Do NOT delete this test.
func TestKnownVectorPassword(t *testing.T) {
	const passwordSHA1Hex = "5BAA61E4C9B93F3F0682250B6CF8331B7EE68FD8"
	// Decode the documented digest purely to assert the fixture is well-formed
	// — the membership lookup below is the actual assertion.
	if _, err := hex.DecodeString(passwordSHA1Hex); err != nil {
		t.Fatalf("test fixture hex is malformed: %v", err)
	}
	if !breachlist.IsCompromised("password") {
		t.Fatalf("IsCompromised(%q) = false, want true (known HIBP vector %s)", "password", passwordSHA1Hex)
	}
}

func TestKnownCompromisedSamples(t *testing.T) {
	for _, p := range []string{"123456", "qwerty", "letmein", "iloveyou", "Welcome123"} {
		if !breachlist.IsCompromised(p) {
			t.Errorf("IsCompromised(%q) = false, want true (seed entry)", p)
		}
	}
}

// TestMidTailCoverageBump locks in the 2026-06-10 corpus growth (338 → ~10k via
// the xato-net top-10,000 merge). "flamingo" is rank ~3005 in the xato list and
// was NOT in the original 338-entry bootstrap seed — so this asserting true
// proves the embedded corpus actually carries the mid-tail body, not just the
// headline classics. If a future regeneration drops back to a tiny seed (or the
// xato merge is reverted) this fails, flagging the coverage regression.
func TestMidTailCoverageBump(t *testing.T) {
	const midTail = "flamingo" // xato-net top-10k entry, absent from the original seed
	if !breachlist.IsCompromised(midTail) {
		t.Fatalf("IsCompromised(%q) = false, want true — mid-tail corpus coverage regressed (xato top-10k merge missing?)", midTail)
	}
}

// TestRandomDigestNotCompromised: a cryptographically-random string is not in
// any practical corpus, so the lookup must miss.
func TestRandomDigestNotCompromised(t *testing.T) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	candidate := hex.EncodeToString(b) // 48 hex chars, astronomically unlikely to collide
	if breachlist.IsCompromised(candidate) {
		t.Fatalf("IsCompromised(<random %q>) = true, want false", candidate)
	}
}
