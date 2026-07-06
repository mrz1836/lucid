// Package data ships the read-only reference tables that are compiled
// into the lucid binary. Today that is the person_key wordlist used by
// the deterministic People routine (data-model.md §"person_key
// derivation"). The wordlist is embedded so the derivation is
// location-independent — it works from an installed binary that has no
// access to the repo tree — while the canonical, reviewable copy still
// lives at data/person_keys_wordlist.txt (the path recorded in
// lucid.json wordlist_path and asserted by the Phase 1 acceptance
// checks). Changing the file is a breaking schema change: existing
// person_key slugs are derived from its exact contents and order.
package data

import (
	_ "embed"
	"strings"
)

//go:embed person_keys_wordlist.txt
var wordlistRaw string

// parseWordlist splits the embedded file into non-empty, trimmed lines.
// Blank lines and surrounding whitespace are ignored so a trailing
// newline in the file does not produce an empty final entry.
func parseWordlist(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if w := strings.TrimSpace(ln); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// Wordlist returns the embedded person_key wordlist, parsed fresh so
// callers can never mutate a shared backing slice. The MVP wordlist has
// exactly 256 entries; callers that depend on the size should assert it
// (the derivation math in data-model.md fixes N = 256).
func Wordlist() []string {
	return parseWordlist(wordlistRaw)
}
