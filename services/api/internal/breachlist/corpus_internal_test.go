package breachlist

import (
	"bytes"
	"testing"
)

// TestCorpusInvariants guards the structural promises the binary search depends
// on, asserted directly against the embedded blob: every record is exactly 20
// bytes, the blob is non-empty, and records are strictly sorted ascending (also
// proving no duplicates). White-box (package breachlist, not _test) so it can
// read the unexported `corpus` var.
func TestCorpusInvariants(t *testing.T) {
	if len(corpus)%digestLen != 0 {
		t.Fatalf("corpus length %d is not a multiple of %d — record corruption", len(corpus), digestLen)
	}
	n := len(corpus) / digestLen
	if n == 0 {
		t.Fatal("corpus is empty — the embedded breached-passwords.bin is missing or blank")
	}
	for i := 1; i < n; i++ {
		prev := corpus[(i-1)*digestLen : i*digestLen]
		cur := corpus[i*digestLen : (i+1)*digestLen]
		if bytes.Compare(prev, cur) >= 0 {
			t.Fatalf("corpus not strictly sorted ascending at record %d (binary-search invariant broken)", i)
		}
	}
	t.Logf("embedded corpus: %d records, %d bytes", n, len(corpus))
}
