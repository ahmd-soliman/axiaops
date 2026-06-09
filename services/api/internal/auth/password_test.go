package auth_test

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"axiaops.io/api/internal/auth"
)

func TestHashAndVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	password := "correct horse battery staple"
	hashed, err := auth.Hash(password)
	if err != nil {
		t.Fatalf("Hash returned error: %v", err)
	}
	if !strings.HasPrefix(hashed, "$argon2id$v=19$") {
		t.Fatalf("Hash output not in expected PHC format: %q", hashed)
	}
	// Encoded hash carries the documented parameters (D6).
	if !strings.Contains(hashed, "m=65536,t=3,p=2") {
		t.Fatalf("expected m=65536,t=3,p=2 in encoded hash, got %q", hashed)
	}
	if err := auth.Verify(password, hashed); err != nil {
		t.Fatalf("Verify on correct password returned error: %v", err)
	}
}

func TestHashesAreSaltedAndDistinct(t *testing.T) {
	t.Parallel()
	password := "correct horse battery staple"
	a, err := auth.Hash(password)
	if err != nil {
		t.Fatalf("Hash 1 error: %v", err)
	}
	b, err := auth.Hash(password)
	if err != nil {
		t.Fatalf("Hash 2 error: %v", err)
	}
	if a == b {
		t.Fatal("two hashes of the same password were identical — salt is not random")
	}
	// Both must still verify.
	if err := auth.Verify(password, a); err != nil {
		t.Errorf("Verify against first hash failed: %v", err)
	}
	if err := auth.Verify(password, b); err != nil {
		t.Errorf("Verify against second hash failed: %v", err)
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	t.Parallel()
	hashed, err := auth.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash error: %v", err)
	}
	if err := auth.Verify("incorrect horse battery staple", hashed); err == nil {
		t.Fatal("Verify accepted a wrong password")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"plaintext-not-phc", "hunter2"},
		{"wrong-algo", "$bcrypt$abc$xyz"},
		{"truncated", "$argon2id$v=19$m=65536,t=3,p=2"},
		{"non-base64-salt", "$argon2id$v=19$m=65536,t=3,p=2$@@@@$!!!!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := auth.Verify("correct horse battery staple", tc.in)
			if err == nil {
				t.Fatal("Verify accepted a malformed hash")
			}
		})
	}
}

func TestVerifyRejectsIncompatibleVersion(t *testing.T) {
	t.Parallel()
	// Forge a hash with a non-current version field.
	bogus := "$argon2id$v=18$m=65536,t=3,p=2$YWJjZGVmZ2hpamtsbW5vcA$YWJjZGVmZ2hpamtsbW5vcHFyc3R1dndYeXowMTIzNDU"
	err := auth.Verify("correct horse battery staple", bogus)
	if !errors.Is(err, auth.ErrIncompatibleVersion) {
		t.Fatalf("expected ErrIncompatibleVersion, got %v", err)
	}
}

func TestCheckPolicyRejectsShort(t *testing.T) {
	t.Parallel()
	err := auth.CheckPolicy("short")
	if !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("expected ErrPasswordTooShort, got %v", err)
	}
}

func TestCheckPolicyAcceptsAtMinimumLength(t *testing.T) {
	t.Parallel()
	// Exactly 12 chars, not in the common list.
	err := auth.CheckPolicy("zXcVbNm,./12")
	if err != nil {
		t.Fatalf("expected acceptance at minimum length, got %v", err)
	}
}

// TestCheckPolicyRejectsBreached covers the NIST §5.1.1.2 screen. "password"
// is < min-length so it would be caught by length anyway; the mandated
// fixtures here are a 12-char string that IS in the seed corpus (passes length,
// must fail on breach) — proving the breach check, not just length, fires.
func TestCheckPolicyRejectsBreached(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"password1234", "Password2024"} { // both 12 chars, both in the seed
		if len(p) < auth.PasswordPolicyMinLength {
			t.Fatalf("fixture %q must be >= min length to prove the breach branch", p)
		}
		err := auth.CheckPolicy(p)
		if !errors.Is(err, auth.ErrPasswordBreached) {
			t.Fatalf("CheckPolicy(%q): expected ErrPasswordBreached, got %v", p, err)
		}
	}
}

// TestCheckPolicyAcceptsStrongRandom: a high-entropy 24-char password is in no
// corpus and passes both checks.
func TestCheckPolicyAcceptsStrongRandom(t *testing.T) {
	t.Parallel()
	pw := randomPassword(t, 24)
	if err := auth.CheckPolicy(pw); err != nil {
		t.Fatalf("CheckPolicy(<random 24-char>) = %v, want nil", err)
	}
}

// TestCheckPolicyWithIdentityRejectsSelfSimilar covers the GitLab-style
// identity reject: a password equal to / containing the email local-part must
// be rejected even though it is long enough and not in the breach corpus.
func TestCheckPolicyWithIdentityRejectsSelfSimilar(t *testing.T) {
	t.Parallel()
	id := auth.PolicyContext{Email: "janedoe@example.com", Name: "Jane Doe"}
	cases := []string{
		"janedoe",             // exact local-part (< 12, but identity slots before length? no — length is first)
		"janedoe-2026-aXz",    // contains local-part, long enough
		"my-Jane Doe-pass99",  // contains display name
		"janedoe@example.com", // full email
	}
	for _, p := range cases {
		err := auth.CheckPolicyWithIdentity(p, id)
		// Short candidates legitimately fail on length first; the ones that
		// are long enough must fail on identity.
		if len(p) >= auth.PasswordPolicyMinLength && !errors.Is(err, auth.ErrPasswordContainsIdentity) {
			t.Fatalf("CheckPolicyWithIdentity(%q): expected ErrPasswordContainsIdentity, got %v", p, err)
		}
		if err == nil {
			t.Fatalf("CheckPolicyWithIdentity(%q): expected rejection, got nil", p)
		}
	}
}

// TestCheckPolicyWithIdentityAcceptsUnrelatedStrong: a strong password
// unrelated to the identity passes the full chain.
func TestCheckPolicyWithIdentityAcceptsUnrelatedStrong(t *testing.T) {
	t.Parallel()
	id := auth.PolicyContext{Email: "janedoe@example.com", Name: "Jane Doe"}
	pw := randomPassword(t, 24)
	if err := auth.CheckPolicyWithIdentity(pw, id); err != nil {
		t.Fatalf("CheckPolicyWithIdentity(<random>, id) = %v, want nil", err)
	}
}

// randomPassword returns a hex-encoded crypto/rand password of length n. Hex is
// deliberate: it can never collide with a dictionary word and the bytes are
// uniformly random, so the result is not in any breach corpus.
func randomPassword(t *testing.T, n int) string {
	t.Helper()
	raw := make([]byte, (n+1)/2)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(raw)[:n]
}
