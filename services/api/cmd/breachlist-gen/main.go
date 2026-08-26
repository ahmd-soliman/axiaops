// Command breachlist-gen builds the embedded breached-password corpus
// (services/api/internal/breachlist/breached-passwords.bin) consumed by the
// breachlist package's binary-search membership check.
//
// It accepts EITHER a plaintext wordlist (the bootstrap-seed mode, default) OR
// HIBP's prevalence-ordered SHA-1 hash file (the real-corpus swap mode, -hibp).
// In both modes the output is identical in shape: a sorted, concatenated stream
// of RAW 20-byte SHA-1 digests, no delimiters — exactly what
// breachlist.IsCompromised binary-searches over. Sorting is by raw digest
// (ascending), NOT by prevalence: binary search needs digest order.
//
// Usage:
//
//	# bootstrap seed (default): hash each plaintext line
//	breachlist-gen -in seed-wordlist.txt -out breached-passwords.bin
//
//	# real HIBP swap: decode HIBP's "HASH:count" ordered file
//	breachlist-gen -hibp -in pwned-passwords-ordered.txt -n 1000000 -out breached-passwords.bin
//
// The companion scripts/gen-breachlist.sh wraps this and refreshes the
// provenance manifest + the SHA-256 of the output. See docs/AUTHENTICATION.md § 4.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha1" //nolint:gosec // G505: SHA-1 is the HIBP corpus index, not a security primitive; password storage is argon2id.
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

const digestLen = 20

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "breachlist-gen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	in := flag.String("in", "", "input file: plaintext wordlist (default) or HIBP ordered hash file (-hibp)")
	out := flag.String("out", "", "output .bin path (sorted concatenated raw 20-byte digests)")
	hibp := flag.Bool("hibp", false, "treat -in as HIBP's prevalence-ordered 'HASH:count' file instead of a plaintext wordlist")
	n := flag.Int("n", 0, "in -hibp mode, take only the first N lines (0 = all); ignored for the plaintext wordlist")
	flag.Parse()

	if *in == "" || *out == "" {
		return fmt.Errorf("both -in and -out are required")
	}

	f, err := os.Open(*in)
	if err != nil {
		return fmt.Errorf("open input: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only handle; close error is not actionable

	var digests [][digestLen]byte
	if *hibp {
		digests, err = readHIBP(f, *n)
	} else {
		digests, err = readWordlist(f)
	}
	if err != nil {
		return err
	}
	if len(digests) == 0 {
		return fmt.Errorf("input produced zero digests — refusing to write an empty corpus")
	}

	// Sort ascending by raw digest — binary search needs digest order, not
	// the prevalence order HIBP ships in. Deduplicate (a wordlist may repeat
	// a password; HIBP may overlap with the seed on a later swap).
	sort.Slice(digests, func(i, j int) bool {
		return bytes.Compare(digests[i][:], digests[j][:]) < 0
	})
	deduped := digests[:0]
	var prev [digestLen]byte
	havePrev := false
	for _, d := range digests {
		if havePrev && d == prev {
			continue
		}
		deduped = append(deduped, d)
		prev = d
		havePrev = true
	}

	buf := make([]byte, 0, len(deduped)*digestLen)
	for _, d := range deduped {
		buf = append(buf, d[:]...)
	}
	if err := os.WriteFile(*out, buf, 0o644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	fmt.Fprintf(os.Stderr, "breachlist-gen: wrote %d unique digests (%d bytes) to %s\n",
		len(deduped), len(buf), *out)
	return nil
}

// readWordlist hashes each non-blank, non-comment line of a plaintext wordlist.
// A single trailing CR is stripped so CRLF-saved files hash the intended bytes;
// no other normalization is applied — the bytes hashed here MUST match the
// bytes auth.Hash later stores.
func readWordlist(f *os.File) ([][digestLen]byte, error) {
	var digests [][digestLen]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		//nolint:gosec // G401: see package doc — SHA-1 is the corpus index.
		digests = append(digests, sha1.Sum([]byte(line)))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan wordlist: %w", err)
	}
	return digests, nil
}

// readHIBP decodes HIBP's prevalence-ordered file. Each line is
// "40-HEX-SHA1:count" (uppercase hex). We take the first n lines (already
// prevalence-sorted), strip the :count suffix, and hex-decode to raw 20 bytes.
func readHIBP(f *os.File, n int) ([][digestLen]byte, error) {
	var digests [][digestLen]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	count := 0
	for sc.Scan() {
		if n > 0 && count >= n {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		hexHash := line
		if i := strings.IndexByte(line, ':'); i >= 0 {
			hexHash = line[:i]
		}
		raw, err := hex.DecodeString(hexHash)
		if err != nil {
			return nil, fmt.Errorf("hex-decode %q: %w", hexHash, err)
		}
		if len(raw) != digestLen {
			return nil, fmt.Errorf("expected %d-byte digest, got %d from %q", digestLen, len(raw), hexHash)
		}
		var d [digestLen]byte
		copy(d[:], raw)
		digests = append(digests, d)
		count++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan HIBP file: %w", err)
	}
	return digests, nil
}
