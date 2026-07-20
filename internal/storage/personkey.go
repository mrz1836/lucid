package storage

import (
	"fmt"

	"github.com/mrz1836/lucid/internal/keyderive"
)

// personKeyPrefix is the fixed slug prefix for every person_key
// (data-model.md §"person_key derivation").
const personKeyPrefix = "person_"

// NormalizeName lowercases a display name and strips all punctuation
// and whitespace, leaving only letters and digits. It is the canonical
// identity of a person for keying: "M.", "M", and "m" all normalize to
// "m" and therefore share one person_key (data-model.md §"person_key
// derivation").
func NormalizeName(displayName string) string {
	return keyderive.Normalize(displayName)
}

// DerivePersonKey computes the deterministic base slug for a display
// name from the wordlist, exactly per data-model.md §"person_key
// derivation":
//
//	normalized = lowercase(strip_punct_and_whitespace(display_name))
//	hash       = sha256(normalized)
//	word1      = WORDLIST[(hash[0]*256 + hash[1]) % N]
//	word2      = WORDLIST[(hash[2]*256 + hash[3]) % N]
//	key        = "person_" + word1[0] + "-" + word2
//
// It returns an error if the normalized name is empty or the wordlist
// is empty (both would make the derivation meaningless). Collision
// suffixes are handled by [ResolvePersonKey], not here. The seed is the
// bare normalized name (person_keys are unsalted); [keyderive.Derive]
// owns the hash→word math.
func DerivePersonKey(displayName string, wordlist []string) (string, error) {
	normalized := NormalizeName(displayName)
	if normalized == "" {
		return "", fmt.Errorf("storage: cannot derive person_key from %q (empty after normalize)", displayName)
	}
	return keyderive.Derive(personKeyPrefix, []byte(normalized), wordlist)
}

// KeyOwnerFunc reports, for a candidate person_key, the normalized name
// currently stored under it. It returns exists=false when the key is
// free. It lets [ResolvePersonKey] apply the collision-suffix rule
// without this package reaching into people/ directly (the record I/O
// lands in a later phase).
type KeyOwnerFunc func(key string) (normalized string, exists bool)

// ResolvePersonKey returns the final person_key for a display name,
// applying the collision-suffix rule from data-model.md §"person_key
// derivation": if the deterministic base key is already held by a
// *different* normalized name, append -2, -3, ... until the slug is
// free or already belongs to this same person.
//
// owner reports the normalized name stored at a candidate key (or
// exists=false when free). A nil owner is treated as "every key free",
// which yields the bare deterministic key.
func ResolvePersonKey(displayName string, wordlist []string, owner KeyOwnerFunc) (string, error) {
	base, err := DerivePersonKey(displayName, wordlist)
	if err != nil {
		return "", err
	}
	return keyderive.Resolve(base, NormalizeName(displayName), keyderive.OwnerFunc(owner)), nil
}
