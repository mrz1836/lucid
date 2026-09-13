package data

import (
	_ "embed"
	"sort"
	"strings"

	"github.com/mrz1836/lucid/internal/keyderive"
)

// nicknamesRaw is the canonical, reviewable curated nickname map embedded so the
// alias-aware resolution is location-independent (it works from an installed
// binary with no repo tree), while the editable source stays at
// data/person_nicknames.txt (data-model.md §"Alias-aware person resolution").
//
//go:embed person_nicknames.txt
var nicknamesRaw string

// NicknameForm reduces a raw display_name or aka to the single lookup form used
// for nickname matching: the normalized first whitespace-delimited component of
// the name. Single-component names pass through unchanged after normalization,
// and an empty or whitespace-only name yields the empty string. Both the
// capture-time router and the reconcile heuristic derive lookup keys through this
// one helper, so a bare "Mike" links to "Michael Torres" without ever treating a
// surname as a given name; titles and family-name-first forms stay safe
// false-negatives.
func NicknameForm(rawName string) string {
	fields := strings.Fields(rawName)
	if len(fields) == 0 {
		return ""
	}
	return keyderive.Normalize(fields[0])
}

// NicknameLinks parses the embedded curated nickname map fresh on each call and
// returns a symmetric adjacency map: each normalized form maps to the sorted,
// de-duplicated set of the other forms sharing one of its clusters. A form never
// links to itself, and callers may freely mutate the returned map because it is
// built anew every call. Forms with no curated cluster are simply absent from the
// map (a nil lookup yields no links).
func NicknameLinks() map[string][]string {
	neighbors := parseNicknameClusters(nicknamesRaw)
	out := make(map[string][]string, len(neighbors))
	for form, set := range neighbors {
		linked := make([]string, 0, len(set))
		for other := range set {
			linked = append(linked, other)
		}
		sort.Strings(linked)
		out[form] = linked
	}
	return out
}

// parseNicknameClusters turns the raw embedded file into a symmetric neighbor set
// keyed by normalized form. Each non-blank, non-comment line is one
// comma-separated cluster; every distinct form in a cluster links to every other
// distinct form in it. Tokens are normalized with keyderive.Normalize so the map
// keys and values always match the lookup forms NicknameForm produces, and
// self-links are dropped. Blank lines and lines starting with "#" are ignored.
func parseNicknameClusters(raw string) map[string]map[string]bool {
	neighbors := make(map[string]map[string]bool)
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		forms := clusterForms(trimmed)
		for _, a := range forms {
			for _, b := range forms {
				if a == b {
					continue
				}
				if neighbors[a] == nil {
					neighbors[a] = make(map[string]bool)
				}
				neighbors[a][b] = true
			}
		}
	}
	return neighbors
}

// clusterForms normalizes and de-duplicates the comma-separated forms on one
// cluster line, preserving first-seen order and dropping empty tokens.
func clusterForms(line string) []string {
	parts := strings.Split(line, ",")
	seen := make(map[string]bool, len(parts))
	out := make([]string, 0, len(parts))
	for _, tok := range parts {
		form := keyderive.Normalize(tok)
		if form == "" || seen[form] {
			continue
		}
		seen[form] = true
		out = append(out, form)
	}
	return out
}
