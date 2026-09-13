package data

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNicknameLinks_ParsesExpectedClusters confirms a seeded cluster is fully
// mutually linked and normalized as documented in the data-model note.
func TestNicknameLinks_ParsesExpectedClusters(t *testing.T) {
	links := NicknameLinks()

	assert.ElementsMatch(t, []string{"michael", "mikey", "mick", "mickey"}, links["mike"])
	assert.ElementsMatch(t, []string{"mike", "mikey", "mick", "mickey"}, links["michael"])
	assert.ElementsMatch(t, []string{"robert", "bobby", "rob", "robbie"}, links["bob"])
	assert.ElementsMatch(t, []string{"samuel", "sammy"}, links["sam"])
}

// TestNicknameLinks_Symmetric proves the adjacency map is bidirectional: if b is
// linked from a, then a is linked from b.
func TestNicknameLinks_Symmetric(t *testing.T) {
	links := NicknameLinks()
	for a, others := range links {
		for _, b := range others {
			assert.Containsf(t, links[b], a, "link %q->%q is not mirrored by %q->%q", a, b, b, a)
		}
	}
}

// TestNicknameLinks_NoSelfLinks guards against a form linking to itself, which
// would let a name match its own record as a distinct alias.
func TestNicknameLinks_NoSelfLinks(t *testing.T) {
	for form, others := range NicknameLinks() {
		assert.NotContainsf(t, others, form, "form %q links to itself", form)
	}
}

// TestNicknameLinks_Sorted ensures every neighbor slice is sorted so downstream
// output ordering stays byte-stable across runs.
func TestNicknameLinks_Sorted(t *testing.T) {
	for form, others := range NicknameLinks() {
		assert.Truef(t, sort.StringsAreSorted(others), "links for %q are not sorted: %v", form, others)
	}
}

// TestNicknameLinks_Deterministic verifies repeated calls yield identical maps,
// keeping the resolution path deterministic and local-first (no model calls).
func TestNicknameLinks_Deterministic(t *testing.T) {
	first := NicknameLinks()
	second := NicknameLinks()
	assert.Equal(t, first, second)
}

// TestNicknameLinks_ReturnsFreshMap confirms callers cannot corrupt the shared
// asset: mutating a returned map never affects a later call.
func TestNicknameLinks_ReturnsFreshMap(t *testing.T) {
	first := NicknameLinks()
	original := append([]string(nil), first["mike"]...)
	first["mike"] = []string{"MUTATED"}
	first["injected"] = []string{"garbage"}

	second := NicknameLinks()
	assert.Equal(t, original, second["mike"])
	assert.NotContains(t, second, "injected")
}

// TestNicknameLinks_UnknownFormHasNoLinks pins the safe false-negative: a form
// with no curated cluster (and a deliberately excluded ambiguous nickname like
// "alex") returns no links rather than guessing.
func TestNicknameLinks_UnknownFormHasNoLinks(t *testing.T) {
	links := NicknameLinks()
	assert.Empty(t, links["alex"])
	assert.Empty(t, links["torres"])
	assert.Empty(t, links["zzunknown"])
}

// TestNicknameLinks_EveryFormInAtMostOneCluster enforces the curation invariant
// that no form appears in two clusters, which would silently fuse two canonicals.
func TestNicknameLinks_EveryFormInAtMostOneCluster(t *testing.T) {
	clusters := parseNicknameClusters(nicknamesRaw)
	// Two forms share a cluster iff each is in the other's neighbor set. Group
	// forms into connected components and assert every component is fully
	// connected (a clique) — a form bridging two clusters would break that.
	for form, neighbors := range clusters {
		for other := range neighbors {
			for third := range neighbors {
				if other == third {
					continue
				}
				assert.Truef(t, clusters[other][third],
					"forms %q and %q share %q but are not linked — %q bridges clusters",
					other, third, form, form)
			}
		}
	}
}

// TestNicknameForm_FirstComponentOnly proves the shared lookup rule both
// consumers rely on: only the normalized first whitespace-delimited component is
// used, so "Michael Torres" maps to "michael" and later components are ignored.
func TestNicknameForm_FirstComponentOnly(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "full name", raw: "Michael Torres", want: "michael"},
		{name: "single token", raw: "Mike", want: "mike"},
		{name: "already normalized", raw: "mike", want: "mike"},
		{name: "leading whitespace", raw: "   Mikey  Smith ", want: "mikey"},
		{name: "punctuation stripped", raw: "M. Torres", want: "m"},
		{name: "empty", raw: "", want: ""},
		{name: "whitespace only", raw: "   \t ", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, NicknameForm(tc.raw))
		})
	}
}

// TestNicknameForm_LinksBareToFull ties the helper and the map together: the
// bare "Mike" and the full "Michael Torres" resolve to linked lookup forms.
func TestNicknameForm_LinksBareToFull(t *testing.T) {
	links := NicknameLinks()
	bare := NicknameForm("Mike")
	full := NicknameForm("Michael Torres")

	require.NotEmpty(t, bare)
	require.NotEmpty(t, full)
	assert.Contains(t, links[bare], full)
}

// TestParseNicknameClusters_IgnoresBlankAndComments confirms blank lines, stray
// whitespace, comment lines, and duplicate tokens never produce spurious forms.
func TestParseNicknameClusters_IgnoresBlankAndComments(t *testing.T) {
	raw := "# header comment\n\n  alpha, beta ,alpha \n\t\n# trailing note\ngamma\n"
	got := parseNicknameClusters(raw)

	assert.ElementsMatch(t, []string{"beta"}, keysAsSlice(got["alpha"]))
	assert.ElementsMatch(t, []string{"alpha"}, keysAsSlice(got["beta"]))
	assert.NotContains(t, got, "gamma", "a single-form line yields no links")
	assert.NotContains(t, got, "header")
	assert.NotContains(t, got, "comment")
}

func keysAsSlice(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
