// Command hash-password prints an argon2id hash of a password using the
// same parameters the api validates against at /v1/auth/login time
// (services/api/internal/auth/password.go).
//
// Operator-side only — used by scripts/seed_test_data.sh's --demo block
// to mint hashes for the alice/bob/carol demo personas without baking
// a known credential into source control. The password is read from
// stdin so it never appears in process listings.
//
// Usage:
//
//	echo -n 'demo@AxiaOps!' | go run ./services/api/cmd/hash-password
//	# prints: $argon2id$v=19$m=65536,t=3,p=2$...$...
//
// The hash is written to stdout; all diagnostics go to stderr. Exits
// non-zero with a clear message if the password fails CheckPolicy
// (< PasswordPolicyMinLength chars) — the seed script wants the same
// policy floor as a real signup.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"axiaops.io/api/internal/auth"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "hash-password:", err)
		os.Exit(1)
	}
}

func run() error {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	// Trim ONLY trailing newlines (the shell convention for piping a
	// password). Internal whitespace stays — passwords can contain
	// spaces and other significant whitespace.
	pw := strings.TrimRight(string(raw), "\r\n")
	if pw == "" {
		return errors.New("password is empty (pipe it via stdin, e.g. `echo -n '<pw>' | hash-password`)")
	}
	if err := auth.CheckPolicy(pw); err != nil {
		return err
	}
	h, err := auth.Hash(pw)
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}
	fmt.Println(h)
	return nil
}
