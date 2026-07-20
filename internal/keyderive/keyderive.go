// Package keyderive derives deterministic, human-readable slugs from a
// wordlist — the shared core behind storage person_keys and observations
// registry keys (data-model.md §"person_key derivation", observations.md §8).
//
// It is pure: no filesystem, no model, no obligation. Callers keep their own
// public wrappers and error wording; this package owns only the normalization,
// the hash→word slug math, and the collision-suffix rule that all key families
// share.
package keyderive

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"
)

// Normalize lowercases a name and strips everything but letters and digits, so
// "M.", "M", and "m" collapse to one identity. It is the canonical key
// identity both person and registry keys derive from and compare on.
func Normalize(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// Derive builds "<prefix><initial>-<word2>" from sha256(seed):
//
//	word1 = wordlist[(hash[0]*256 + hash[1]) % N]
//	word2 = wordlist[(hash[2]*256 + hash[3]) % N]
//	key   = prefix + word1[0] + "-" + word2
//
// prefix carries its own trailing separator ("person_", "injury_") so each key
// family keeps its exact on-disk shape. Callers compose seed to preserve their
// format byte-for-byte: an unsalted key hashes the normalized name; a salted
// key hashes salt + "\x00" + normalized-name. Derive errors only on an empty
// wordlist or an empty wordlist entry (which would otherwise panic indexing
// word1[0]); the empty-name guard stays in the caller so it keeps its wording.
func Derive(prefix string, seed []byte, wordlist []string) (string, error) {
	n := len(wordlist)
	if n == 0 {
		return "", fmt.Errorf("keyderive: empty wordlist")
	}
	hash := sha256.Sum256(seed)
	word1 := wordlist[(int(hash[0])*256+int(hash[1]))%n]
	word2 := wordlist[(int(hash[2])*256+int(hash[3]))%n]
	if word1 == "" {
		return "", fmt.Errorf("keyderive: wordlist entry is empty")
	}
	initial := []rune(word1)[0]
	return fmt.Sprintf("%s%c-%s", prefix, initial, word2), nil
}

// OwnerFunc reports the normalized name stored under a candidate key, or
// exists=false when the key is free — the seam that lets [Resolve] apply the
// collision-suffix rule without this pure package reading disk.
type OwnerFunc func(key string) (normalized string, exists bool)

// Resolve applies the collision-suffix rule to a base key: if base is already
// held by a *different* normalized name, it appends -2, -3, … until the slug
// is free or already belongs to self. self is the normalized identity claiming
// the key; a nil owner treats every key as free and yields base unchanged.
func Resolve(base, self string, owner OwnerFunc) string {
	if owner == nil {
		return base
	}
	candidate := base
	for suffix := 2; ; suffix++ {
		stored, exists := owner(candidate)
		if !exists || stored == self {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
}
