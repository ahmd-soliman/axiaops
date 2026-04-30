package auth

import "strings"

// commonPasswords is a small, manually-curated list of the most-frequently
// guessed passwords drawn from public breach corpora (RockYou, SecLists
// 2024). Kept short on purpose — at PasswordPolicyMinLength=12 most of the
// classic top-1000 short passwords ("123456", "qwerty") are already excluded
// by length. What remains are the long-but-still-popular ones that pass
// length and warrant explicit rejection.
//
// Expansion path: when a customer reports a real-world bypass we add the
// offending password here; we do NOT pull in a 100k-entry breach list — at
// that scale the rejection rate harms more legit users than it helps. Rate
// limiting + per-user account lockout do the heavy lifting; this list is
// just the "obvious" floor.
// Keys MUST be lowercase. isCommonPassword normalises the candidate via
// strings.ToLower before lookup; mixed-case keys would never match.
var commonPasswords = map[string]struct{}{
	"password1234":     {},
	"password123!":     {},
	"qwerty1234567":    {},
	"qwertyuiop1234":   {},
	"asdfghjkl1234":    {},
	"123456789012":     {},
	"1234567890123":    {},
	"abcdefghijkl":     {},
	"letmein123456":    {},
	"welcome123456":    {},
	"administrator":    {},
	"administrator1":   {},
	"axiaopsaxiaops":   {},
	"changemechangeme": {},
	"changeme1234567":  {},
}

// isCommonPassword returns true when candidate matches a commonPasswords
// entry, case-insensitively. The lookup is O(1). The map intentionally
// stores only lowercase keys; the `strings.ToLower` normalisation makes mixed
// casings ("Password1234", "PASSWORD1234") collapse to the same entry.
func isCommonPassword(candidate string) bool {
	_, ok := commonPasswords[strings.ToLower(candidate)]
	return ok
}
