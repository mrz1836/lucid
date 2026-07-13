package observations

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
)

// Registry kinds (observations.md §8). The key is prefixed with the singular
// kind, so a filename is the full kind-prefixed key
// (registries/injuries/injury_a-cedar.json where the key is injury_a-cedar).
const (
	RegistryInjury = "injury"
	RegistryThread = "thread"
	RegistryPlace  = "place"
	RegistryEra    = "era"
)

// Registry status values (observations.md §8: active → managed → resolved,
// recorded in an append-only status_history, never overwritten).
const (
	StatusActive   = "active"
	StatusManaged  = "managed"
	StatusResolved = "resolved"
)

// RegistryDir maps a registry kind to its directory name under registries/.
func RegistryDir(kind string) (string, bool) {
	switch kind {
	case RegistryInjury:
		return "injuries", true
	case RegistryThread:
		return "threads", true
	case RegistryPlace:
		return "places", true
	case RegistryEra:
		return "eras", true
	default:
		return "", false
	}
}

// StatusEvent is one append-only entry in a registry record's history: every
// patch appends one, stamping the status in force at that time
// (observations.md §1 merge/history semantics).
type StatusEvent struct {
	At     string `json:"at"`
	Status string `json:"status"`
}

// Registry is one long-lived referent (observations.md §8). Registries are
// primary, backup-critical data — a place's coordinates and an injury's notes
// exist nowhere else — with an append-only status_history so transitions are
// recorded, not overwritten. Fields holds the kind-specific data (a place's
// lat/lon, an injury's onset/notes).
type Registry struct {
	Key           string         `json:"key"`
	Kind          string         `json:"kind"`
	DisplayName   string         `json:"display_name"`
	Aka           []string       `json:"aka"`
	Status        string         `json:"status"`
	StatusHistory []StatusEvent  `json:"status_history"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
	Fields        map[string]any `json:"fields"`
}

// RegistryPatch is a merge applied to a registry record (observations.md §1):
// patches add or replace fields; a status change is recorded; every apply
// appends a status_history entry. A field is removed only by an explicit null
// in Fields.
type RegistryPatch struct {
	DisplayName string
	Status      string
	At          string
	Fields      map[string]any
}

// DeriveRegistryKey computes the salted, kind-prefixed key for a referent
// name (observations-module.md §"Registry keys"): the people/ wordlist
// algorithm, but the hash is salted with the per-instance key_salt so the
// low-signal filenames cannot be reversed against the tiny public
// dictionaries injury and place names come from. Collision suffixes (-2, -3)
// are applied by [ResolveRegistryKey], not here.
func DeriveRegistryKey(kind, name, salt string, wordlist []string) (string, error) {
	prefix, ok := kindPrefix(kind)
	if !ok {
		return "", fmt.Errorf("observations: unknown registry kind %q", kind)
	}
	normalized := normalizeRegistryName(name)
	if normalized == "" {
		return "", fmt.Errorf("observations: cannot derive %s key from %q (empty after normalize)", kind, name)
	}
	n := len(wordlist)
	if n == 0 {
		return "", fmt.Errorf("observations: cannot derive registry key: empty wordlist")
	}

	// Salt-prefix the normalized name before hashing (a NUL separates salt
	// from name so no two (salt, name) pairs can collide by concatenation).
	hash := sha256.Sum256([]byte(salt + "\x00" + normalized))
	word1 := wordlist[(int(hash[0])*256+int(hash[1]))%n]
	word2 := wordlist[(int(hash[2])*256+int(hash[3]))%n]
	if word1 == "" {
		return "", fmt.Errorf("observations: wordlist entry is empty")
	}
	initial := []rune(word1)[0]
	return fmt.Sprintf("%s_%c-%s", prefix, initial, word2), nil
}

// RegistryKeyOwnerFunc reports the normalized name stored under a candidate
// key, or exists=false when free — the seam that lets [ResolveRegistryKey]
// apply the collision-suffix rule without this pure package reading disk.
type RegistryKeyOwnerFunc func(key string) (normalized string, exists bool)

// ResolveRegistryKey returns the final key for a referent name, applying the
// collision-suffix rule (observations.md §8 shares people/'s derivation): if
// the salted base key is already held by a *different* normalized name,
// append -2, -3, … until the slug is free or already belongs to this name. A
// nil owner yields the bare salted key.
func ResolveRegistryKey(kind, name, salt string, wordlist []string, owner RegistryKeyOwnerFunc) (string, error) {
	base, err := DeriveRegistryKey(kind, name, salt, wordlist)
	if err != nil {
		return "", err
	}
	if owner == nil {
		return base, nil
	}
	self := normalizeRegistryName(name)
	candidate := base
	for suffix := 2; ; suffix++ {
		stored, exists := owner(candidate)
		if !exists || stored == self {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
}

// NewRegistry builds a fresh registry record for a first mention.
func NewRegistry(kind, key, displayName, at string) Registry {
	return Registry{
		Key:           key,
		Kind:          kind,
		DisplayName:   displayName,
		Aka:           []string{displayName},
		Status:        StatusActive,
		StatusHistory: []StatusEvent{{At: at, Status: StatusActive}},
		CreatedAt:     at,
		UpdatedAt:     at,
		Fields:        map[string]any{},
	}
}

// Apply merges a patch onto a registry record and returns the updated copy
// (observations.md §1): the display name and any known aka are recorded, a
// status change updates Status, fields add/replace/null, and every apply
// appends a status_history entry with the patch timestamp. The receiver is
// not mutated.
func (r Registry) Apply(p RegistryPatch) Registry {
	out := r
	out.Aka = slices.Clone(r.Aka)
	out.StatusHistory = slices.Clone(r.StatusHistory)
	out.Fields = cloneAnyMap(r.Fields)

	if p.DisplayName != "" && p.DisplayName != out.DisplayName {
		out.DisplayName = p.DisplayName
	}
	if p.DisplayName != "" && !slices.Contains(out.Aka, p.DisplayName) {
		out.Aka = append(out.Aka, p.DisplayName)
	}
	if p.Status != "" {
		out.Status = p.Status
	}
	for k, v := range p.Fields {
		if v == nil {
			delete(out.Fields, k)
			continue
		}
		out.Fields[k] = v
	}
	out.UpdatedAt = p.At
	out.StatusHistory = append(out.StatusHistory, StatusEvent{At: p.At, Status: out.Status})
	return out
}

// Normalized returns a copy whose slice/map fields are non-nil so the written
// record always carries [] / {} rather than null.
func (r Registry) Normalized() Registry {
	if r.Aka == nil {
		r.Aka = []string{}
	}
	if r.StatusHistory == nil {
		r.StatusHistory = []StatusEvent{}
	}
	if r.Fields == nil {
		r.Fields = map[string]any{}
	}
	return r
}

// NormalizedName exposes the canonical identity of a referent name (the value
// the collision-suffix owner check compares), so the storage adapter can key
// its owner lookups on the same normalization the derivation uses.
func NormalizedName(name string) string { return normalizeRegistryName(name) }

// kindPrefix returns the singular key prefix for a registry kind.
func kindPrefix(kind string) (string, bool) {
	switch kind {
	case RegistryInjury, RegistryThread, RegistryPlace, RegistryEra:
		return kind, true
	default:
		return "", false
	}
}

// normalizeRegistryName lowercases a name and strips punctuation/whitespace,
// leaving only letters and digits — the same canonicalization people/ uses so
// "Left knee" and "left knee." share one key.
func normalizeRegistryName(name string) string {
	var b strings.Builder
	for _, ru := range name {
		if unicode.IsLetter(ru) || unicode.IsDigit(ru) {
			b.WriteRune(unicode.ToLower(ru))
		}
	}
	return b.String()
}

// cloneAnyMap deep-copies a one-level map (nil stays a fresh empty map). The
// values are scalars/strings from a patch, so a shallow value copy is safe.
func cloneAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return maps.Clone(m)
}

// sortedKeys returns the keys of a map in sorted order — a small determinism
// helper the day view uses when it must iterate a map.
func sortedKeys(m map[string]any) []string {
	return slices.Sorted(maps.Keys(m))
}
