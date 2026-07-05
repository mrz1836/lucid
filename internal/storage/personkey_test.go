package storage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/data"
)

func TestNormalizeName(t *testing.T) {
	tests := map[string]string{
		"M.":            "m",
		"M":             "m",
		"m":             "m",
		"Dr. J. Smith":  "drjsmith",
		"  Anne-Marie ": "annemarie",
		"O'Brien":       "obrien",
		"José":          "josé",
		"user_42":       "user42",
		".!?":           "",
	}
	for in, want := range tests {
		assert.Equalf(t, want, NormalizeName(in), "NormalizeName(%q)", in)
	}
}

// TestDerivePersonKey_Golden pins the derivation against the shipped
// wordlist. These are regression anchors: if the wordlist file or the
// derivation math changes, existing slugs would drift and this fails.
func TestDerivePersonKey_Golden(t *testing.T) {
	wl := data.Wordlist()
	golden := map[string]string{
		"M.":   "person_p-hazel",
		"M":    "person_p-hazel",
		"m":    "person_p-hazel",
		"J.":   "person_m-anvil",
		"Alex": "person_c-marsh",
		"Sam":  "person_g-rye",
	}
	for name, want := range golden {
		got, err := DerivePersonKey(name, wl)
		require.NoErrorf(t, err, "DerivePersonKey(%q)", name)
		assert.Equalf(t, want, got, "DerivePersonKey(%q)", name)
	}
}

// TestDerivePersonKey_Deterministic proves repeated derivation is
// stable and that punctuation/case variants collapse to one key.
func TestDerivePersonKey_Deterministic(t *testing.T) {
	wl := data.Wordlist()
	first, err := DerivePersonKey("Riley", wl)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		got, err := DerivePersonKey("riley!!", wl)
		require.NoError(t, err)
		assert.Equal(t, first, got)
	}
}

// TestDerivePersonKey_Shape asserts every derived key matches the
// documented person_<initial>-<word2> shape: the initial is a single
// lowercase letter that begins some wordlist word (it is word1[0], and
// word1 is independent of word2), and word2 is itself a wordlist entry.
func TestDerivePersonKey_Shape(t *testing.T) {
	wl := data.Wordlist()
	inList := make(map[string]bool, len(wl))
	initials := make(map[string]bool, len(wl))
	for _, w := range wl {
		inList[w] = true
		initials[w[:1]] = true
	}
	for _, name := range []string{"Ana", "Ben", "Chloe", "Devon", "Esme", "Finn"} {
		key, err := DerivePersonKey(name, wl)
		require.NoError(t, err)
		rest, ok := strings.CutPrefix(key, "person_")
		require.Truef(t, ok, "key %q lacks person_ prefix", key)
		initial, word2, ok := strings.Cut(rest, "-")
		require.Truef(t, ok, "key %q lacks the -word2 segment", key)
		require.Len(t, initial, 1)
		assert.GreaterOrEqual(t, initial, "a")
		assert.LessOrEqual(t, initial, "z")
		assert.True(t, initials[initial], "initial %q begins no wordlist word", initial)
		assert.True(t, inList[word2], "word2 %q not in wordlist", word2)
	}
}

func TestDerivePersonKey_Errors(t *testing.T) {
	wl := data.Wordlist()

	_, err := DerivePersonKey(".!?", wl)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty after normalize")

	_, err = DerivePersonKey("M.", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty wordlist")

	// A wordlist containing an empty entry would otherwise panic on
	// word1[0]; the guard turns it into an error.
	_, err = DerivePersonKey("M.", []string{""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wordlist entry is empty")
}

// TestDerivePersonKey_InitialIsWord1 constructs a controlled two-word
// list so the initial ("a" from "apple") is unambiguous regardless of
// which index the hash selects.
func TestDerivePersonKey_InitialIsWord1(t *testing.T) {
	wl := []string{"apple", "apricot"} // both start with 'a'
	key, err := DerivePersonKey("whoever", wl)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(key, "person_a-a"), "got %q", key)
}

// TestResolvePersonKey_NoCollision returns the bare deterministic key
// when the slot is free (or owner is nil).
func TestResolvePersonKey_NoCollision(t *testing.T) {
	wl := data.Wordlist()
	base, err := DerivePersonKey("M.", wl)
	require.NoError(t, err)

	got, err := ResolvePersonKey("M.", wl, nil)
	require.NoError(t, err)
	assert.Equal(t, base, got)

	got, err = ResolvePersonKey("M.", wl, func(string) (string, bool) { return "", false })
	require.NoError(t, err)
	assert.Equal(t, base, got)
}

// TestResolvePersonKey_SamePersonReuses returns the existing key when
// it is already owned by the same normalized name (not a collision).
func TestResolvePersonKey_SamePersonReuses(t *testing.T) {
	wl := data.Wordlist()
	base, err := DerivePersonKey("M.", wl)
	require.NoError(t, err)

	owner := func(k string) (string, bool) {
		if k == base {
			return "m", true // already M.'s slot
		}
		return "", false
	}
	got, err := ResolvePersonKey("M", wl, owner)
	require.NoError(t, err)
	assert.Equal(t, base, got)
}

// TestResolvePersonKey_CollisionSuffixes walks the -2/-3 suffix rule:
// the base and -2 are held by different people, so a third distinct
// name lands on -3 (data-model.md §"person_key derivation").
func TestResolvePersonKey_CollisionSuffixes(t *testing.T) {
	wl := data.Wordlist()
	base, err := DerivePersonKey("M.", wl)
	require.NoError(t, err)

	taken := map[string]string{
		base:        "alice",
		base + "-2": "bob",
	}
	owner := func(k string) (string, bool) {
		n, ok := taken[k]
		return n, ok
	}

	got, err := ResolvePersonKey("M.", wl, owner) // normalized "m" — different from alice/bob
	require.NoError(t, err)
	assert.Equal(t, base+"-3", got)
}

func TestResolvePersonKey_DeriveError(t *testing.T) {
	_, err := ResolvePersonKey(".!?", data.Wordlist(), nil)
	assert.Error(t, err)
}
