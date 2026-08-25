// Package breachlist screens candidate passwords against an embedded corpus of
// known-compromised passwords (HIBP Pwned Passwords, a bootstrap seed for now —
// see docs/AUTHENTICATION.md (§4)), implementing the NIST SP 800-63B §5.1.1.2
// compromised-credential screen for AxiaOps' self-hosted native-auth path.
//
// Why offline-embedded and not the live HIBP k-anonymity API: AxiaOps ships
// self-hosted, possibly egress-restricted or air-gapped, so a live external
// call is unavailable by design and a "soft warning / fail-open" degradation
// silently no-ops. We follow GitLab/Django prior art and bundle the corpus.
// See docs/AUTHENTICATION.md (§4).
//
// # SHA-1 is the corpus INDEX, never storage
//
// HIBP keys its corpus by SHA-1, so we SHA-1 the candidate solely to look it up.
// This is NOT a password-storage primitive — storage stays argon2id (auth.Hash).
// The gosec G401/G505 SHA-1 lints are scoped-suppressed at the import + hash site
// for exactly this reason.
//
// # Raw-byte invariant (read before touching this file)
//
// The embedded blob and the lookup BOTH operate on RAW 20-byte digests
// (`sha1.Sum` output / hex-decode of HIBP's uppercase-hex line) — NEVER on hex
// strings. Raw-byte comparison is precisely what makes the check case-agnostic
// w.r.t. the hex encoding. An "optimization" to compare hex strings, or a
// stray ToUpper/ToLower, silently turns this into an always-miss with no error.
// The end-to-end known-vector test (IsCompromised("password") == true) exists to
// make any such regression fail loudly.
//
// # No-normalization invariant
//
// IsCompromised MUST hash the EXACT bytes ([]byte(candidate)) that auth.Hash
// later stores — no trimming, no Unicode normalization, on either path. If
// anyone ever adds TrimSpace/NFC to one path and not the other, the breach check
// screens a different string than the one stored. Keep both on raw UTF-8 bytes.
package breachlist

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // G505: SHA-1 is the HIBP corpus index, not a security primitive; password storage is argon2id.
	"sort"
)

// digestLen is the SHA-1 output width and the fixed record stride in the blob.
const digestLen = 20

// IsCompromised reports whether plaintext appears in the embedded
// known-compromised corpus. It SHA-1s the candidate's raw bytes and
// binary-searches the sorted 20-byte-record blob.
//
// Lookup-only: the SHA-1 here is the corpus index, NOT password storage
// (argon2id, in auth.Hash). The bytes hashed here are the exact []byte of the
// candidate — see the no-normalization invariant in the package doc.
func IsCompromised(plaintext string) bool {
	//nolint:gosec // G401: see package doc — SHA-1 is the corpus index, not storage.
	want := sha1.Sum([]byte(plaintext))

	n := len(corpus) / digestLen
	// sort.Search finds the first record >= want; we then confirm equality.
	i := sort.Search(n, func(i int) bool {
		rec := corpus[i*digestLen : i*digestLen+digestLen]
		return bytes.Compare(rec, want[:]) >= 0
	})
	if i >= n {
		return false
	}
	rec := corpus[i*digestLen : i*digestLen+digestLen]
	return bytes.Equal(rec, want[:])
}
