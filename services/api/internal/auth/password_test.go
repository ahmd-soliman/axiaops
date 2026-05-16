package auth_test

import (
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

