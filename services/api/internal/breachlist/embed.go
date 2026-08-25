package breachlist

import _ "embed"

// corpus is the embedded breached-password blob: a sorted, concatenated stream
// of RAW 20-byte SHA-1 digests, no delimiters. IsCompromised binary-searches
// over it. Built by cmd/breachlist-gen from internal/breachlist/seed-wordlist.txt
// (bootstrap seed) — see docs/AUTHENTICATION.md (§4) for the source and the
// procedure to swap in HIBP's full prevalence-ordered top-1M.
//
// The embed is UNCONDITIONAL — no build-tag split. Unlike the license
// dev-fixture (stripped from `-tags production` for a security boundary), the
// corpus must be present in EVERY shipped build, because production binaries are
// exactly the ones doing real signups. Do not make this optional: an operator
// who built it out would silently be NIST SP 800-63B §5.1.1.2 non-compliant.
//
//go:embed breached-passwords.bin
var corpus []byte
