package postgres

import (
	"strings"
	"testing"
)

func TestClampError(t *testing.T) {
	if got := clampError(""); got != "" {
		t.Errorf("empty in should be empty out, got %q", got)
	}

	short := "email: send: 535 auth rejected"
	if got := clampError(short); got != short {
		t.Errorf("short string must pass through unchanged, got %q", got)
	}

	long := strings.Repeat("x", maxDispatchErrorLen+500)
	got := clampError(long)
	if len([]rune(got)) != maxDispatchErrorLen+1 { // clamped runes + the ellipsis
		t.Errorf("clamped length = %d runes, want %d", len([]rune(got)), maxDispatchErrorLen+1)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("clamped string should end with the ellipsis, got %q", got[len(got)-4:])
	}

	// Multi-byte: don't split a rune mid-byte. A string of N+10 multi-byte runes
	// must clamp to exactly maxDispatchErrorLen runes + ellipsis, still valid UTF-8.
	multi := strings.Repeat("é", maxDispatchErrorLen+10)
	gm := clampError(multi)
	if !strings.HasSuffix(gm, "…") || len([]rune(gm)) != maxDispatchErrorLen+1 {
		t.Errorf("multi-byte clamp wrong: %d runes", len([]rune(gm)))
	}
}
