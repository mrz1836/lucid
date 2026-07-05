package data

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWordlist_ExactlySize256 pins the fixed derivation constant N=256
// (data-model.md §"person_key derivation"). Changing the count is a
// breaking schema change and must fail this test loudly.
func TestWordlist_ExactlySize256(t *testing.T) {
	wl := Wordlist()
	require.Len(t, wl, 256)
}

// TestWordlist_AllNonEmptyLowerAlpha guards the low-signal invariant:
// every entry is a non-empty, lowercase a–z word (no names, digits, or
// punctuation) so the derived slugs stay low-signal.
func TestWordlist_AllNonEmptyLowerAlpha(t *testing.T) {
	for i, w := range Wordlist() {
		require.NotEmptyf(t, w, "entry %d is empty", i)
		for _, r := range w {
			assert.Truef(t, r >= 'a' && r <= 'z', "entry %d %q has non lowercase-alpha rune %q", i, w, r)
		}
	}
}

// TestWordlist_Unique ensures no duplicate words, which would waste
// slug space and skew the hash distribution.
func TestWordlist_Unique(t *testing.T) {
	seen := make(map[string]int, 256)
	for i, w := range Wordlist() {
		if first, dup := seen[w]; dup {
			t.Fatalf("duplicate word %q at lines %d and %d", w, first, i)
		}
		seen[w] = i
	}
}

// TestWordlist_ReturnsDefensiveCopy verifies mutating a returned slice
// cannot corrupt the package-level wordlist backing the derivation.
func TestWordlist_ReturnsDefensiveCopy(t *testing.T) {
	a := Wordlist()
	original := a[0]
	a[0] = "MUTATED"
	b := Wordlist()
	assert.Equal(t, original, b[0])
}

// TestParseWordlist_IgnoresBlankAndWhitespace confirms trailing
// newlines and stray whitespace never produce empty entries.
func TestParseWordlist_IgnoresBlankAndWhitespace(t *testing.T) {
	got := parseWordlist("  alpha \n\n bravo\n\t\ncharlie\n")
	assert.Equal(t, []string{"alpha", "bravo", "charlie"}, got)
}
