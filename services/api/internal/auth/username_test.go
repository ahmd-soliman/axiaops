package auth_test

import (
	"strings"
	"testing"

	"axiaops.io/api/internal/auth"
)

// TestValidUserName covers the rules in validUserName: length cap, control
// chars rejected, email-shaped strings rejected, and a representative set of
// legitimate names accepted (apostrophes, hyphens, accented chars, mononyms,
// digits, scripts beyond Latin).
func TestValidUserName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// Reject — empty / whitespace-only post trim is handled by callers,
		// but validUserName itself must also refuse the empty string as the
		// last line of defence.
		{"empty", "", false},

		// Reject — over the 100-rune cap.
		{"too long ascii", strings.Repeat("a", 101), false},
		{"too long unicode", strings.Repeat("ñ", 101), false},

		// Reject — control characters.
		{"null byte", "Alice\x00", false},
		{"newline", "Alice\nBob", false},
		{"tab", "Alice\tBob", false},
		{"carriage return", "Alice\rBob", false},

		// Reject — parses as an email address (the test@test typo case the
		// user flagged on first-run bootstrap).
		{"plain email", "test@test", false},
		{"full email", "alice@example.com", false},
		{"email with name part", "Alice <alice@example.com>", false},

		// Accept — common Western names.
		{"plain ascii", "Alice", true},
		{"first last", "Alice Engineer", true},
		{"hyphenated", "Marie-Curie", true},
		{"apostrophe", "O'Brien", true},

		// Accept — international scripts.
		{"accented", "José Müller", true},
		{"japanese", "田中太郎", true},
		{"korean", "김민준", true},
		{"arabic", "محمد", true},

		// Accept — mononyms and unusual shapes.
		{"mononym", "Madonna", true},
		{"with digit", "Owen2", true},
		{"at exact cap", strings.Repeat("a", 100), true},
		{"unicode at exact cap", strings.Repeat("ñ", 100), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := auth.ValidUserName(c.in)
			if got != c.want {
				t.Errorf("ValidUserName(%q) = %v; want %v", c.in, got, c.want)
			}
		})
	}
}
