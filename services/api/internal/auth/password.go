// Package auth — Phase B1 native auth primitives.
//
// password.go: argon2id password hashing and verification. Defaults match
// docs/sso-implementation-plan.md decision D6.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// PasswordPolicyMinLength is the minimum password length accepted by
// CheckPolicy. 12 chars matches OWASP ASVS L2 recommendation while keeping
// legitimate passphrases comfortable.
const PasswordPolicyMinLength = 12

// argon2id parameters (decision D6). The encoded hash carries these inline,
// so changing the defaults later is forward-compatible — old hashes verify
// against their stored parameters.
const (
	argon2idTime    uint32 = 3
	argon2idMemory  uint32 = 64 * 1024 // 64 MiB, expressed in KiB
	argon2idThreads uint8  = 2
	argon2idSaltLen uint32 = 16
	argon2idKeyLen  uint32 = 32
)

// ErrPasswordTooShort is returned by CheckPolicy when the candidate is below
// the minimum length.
var ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", PasswordPolicyMinLength)

// ErrInvalidHash is returned by Verify when the stored hash does not parse as
// a PHC-string-encoded argon2id digest. Treat this identically to a wrong
// password at the boundary — never echo the parser error to the client.
var ErrInvalidHash = errors.New("password hash is not a valid argon2id PHC string")

// ErrIncompatibleVersion is returned when the stored hash uses a different
// argon2 version. Today this is purely defensive — argon2.Version has been
// 0x13 since 2017.
var ErrIncompatibleVersion = errors.New("password hash uses an incompatible argon2 version")

// CheckPolicy validates a candidate plaintext against the password policy.
// Returns nil if acceptable, a sentinel error otherwise.
//
// The policy is intentionally minimal: length only. The "blocklist of N
// common passwords" approach was considered and dropped — at min-length
// 12 the obvious weak passwords (123456, password, qwerty) are already
// excluded by length, and a static blocklist of any practical size
// covers <0.1% of the breach corpus. The real defences live a layer
// above (rate-limiting per IP/email — slice 10) and a layer below (a
// future HIBP k-anonymity integration that catches passwords known to
// be compromised regardless of pattern). Tracked in Tasks.md under
// "Password breach-corpus check (HIBP)".
//
// candidate is passed by value (not pointer) deliberately — the Go
// runtime is permitted to copy it on assignment, but no caller needs
// the original after validation, so the exposed surface is bounded to
// one stack copy.
func CheckPolicy(candidate string) error {
	if len(candidate) < PasswordPolicyMinLength {
		return ErrPasswordTooShort
	}
	return nil
}

// Hash returns a PHC-string-encoded argon2id digest of the given plaintext.
// Format:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<base64-salt>$<base64-key>
//
// The encoding is parameter-self-describing so future tuning of the cost
// factors can land without invalidating existing hashes.
func Hash(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("auth: hash: plaintext required")
	}
	salt := make([]byte, argon2idSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: hash: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(plaintext), salt, argon2idTime, argon2idMemory, argon2idThreads, argon2idKeyLen)
	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argon2idMemory, argon2idTime, argon2idThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	return encoded, nil
}

// Verify constant-time-compares plaintext against an encoded argon2id hash.
// Returns nil iff the password is correct. Any decoding error or mismatch
// surfaces as a non-nil error; the caller should map all of these to the
// same "invalid credentials" response to avoid leaking which input was wrong
// (architect: failure-mode observability §4.5 N5 reports them via the
// outcome label, not the response body).
func Verify(plaintext, encoded string) error {
	mem, time, threads, salt, key, err := decode(encoded)
	if err != nil {
		return err
	}
	candidate := argon2.IDKey([]byte(plaintext), salt, time, mem, threads, uint32(len(key)))
	if subtle.ConstantTimeCompare(key, candidate) != 1 {
		return errors.New("auth: verify: password mismatch")
	}
	return nil
}

// decode parses a PHC-string-encoded argon2id hash. Returns the parameters
// and the binary salt + key. Tightly bound to the format produced by Hash().
func decode(encoded string) (mem, time uint32, threads uint8, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	// "$argon2id$v=19$m=65536,t=3,p=2$<salt>$<key>" → 6 parts because the
	// leading '$' makes parts[0] == "".
	if len(parts) != 6 || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	var version int
	if _, perr := fmt.Sscanf(parts[2], "v=%d", &version); perr != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		return 0, 0, 0, nil, nil, ErrIncompatibleVersion
	}
	if _, perr := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &time, &threads); perr != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	if key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHash
	}
	return mem, time, threads, salt, key, nil
}
