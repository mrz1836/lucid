package keyderive

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
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
	for in, want := range cases {
		assert.Equalf(t, want, Normalize(in), "Normalize(%q)", in)
	}
}

// TestDerive_UnsaltedVsSalted proves the seed is honored byte-for-byte: an
// unsalted seed and a salt+NUL+name seed for the same name produce different
// keys (the property person_keys and registry keys rely on).
func TestDerive_UnsaltedVsSalted(t *testing.T) {
	wl := []string{"apple", "apricot", "cedar", "flame", "hazel", "marsh"}
	unsalted, err := Derive("person_", []byte("alex"), wl)
	require.NoError(t, err)
	salted, err := Derive("person_", []byte("salt\x00alex"), wl)
	require.NoError(t, err)
	assert.NotEqual(t, unsalted, salted, "salt must change the derived slug")
	assert.True(t, strings.HasPrefix(unsalted, "person_"))
}

// TestDerive_Deterministic: the same prefix+seed+wordlist always yields the
// same key.
func TestDerive_Deterministic(t *testing.T) {
	wl := []string{"apple", "apricot", "cedar", "flame"}
	first, err := Derive("injury_", []byte("x\x00left knee"), wl)
	require.NoError(t, err)
	for range 5 {
		got, err := Derive("injury_", []byte("x\x00left knee"), wl)
		require.NoError(t, err)
		assert.Equal(t, first, got)
	}
}

// TestDerive_Shape: the initial is word1[0] and word2 is a wordlist entry; a
// two-word list that shares a first letter pins the initial unambiguously.
func TestDerive_Shape(t *testing.T) {
	wl := []string{"apple", "apricot"} // both start with 'a'
	key, err := Derive("person_", []byte("whoever"), wl)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(key, "person_a-a"), "got %q", key)
}

func TestDerive_Errors(t *testing.T) {
	_, err := Derive("person_", []byte("m"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty wordlist")

	_, err = Derive("person_", []byte("m"), []string{""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wordlist entry is empty")
}

func TestResolve(t *testing.T) {
	base := "person_p-hazel"

	t.Run("nil owner yields base", func(t *testing.T) {
		assert.Equal(t, base, Resolve(base, "m", nil))
	})
	t.Run("free key yields base", func(t *testing.T) {
		assert.Equal(t, base, Resolve(base, "m", func(string) (string, bool) { return "", false }))
	})
	t.Run("same self reuses base", func(t *testing.T) {
		owner := func(k string) (string, bool) {
			if k == base {
				return "m", true
			}
			return "", false
		}
		assert.Equal(t, base, Resolve(base, "m", owner))
	})
	t.Run("collision walks suffixes", func(t *testing.T) {
		taken := map[string]string{base: "alice", base + "-2": "bob"}
		owner := func(k string) (string, bool) {
			n, ok := taken[k]
			return n, ok
		}
		assert.Equal(t, base+"-3", Resolve(base, "m", owner))
	})
}
