package validate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckPeopleRedirectGraph_Clean: a canonical record plus a valid single-hop
// tombstone forwarding to it has no findings.
func TestCheckPeopleRedirectGraph_Clean(t *testing.T) {
	found, err := CheckPeopleRedirectGraph(&fakeLedger{
		people:    []string{"person_a-alex", "person_a-andy"},
		redirects: map[string]string{"person_a-andy": "person_a-alex"},
	})
	require.NoError(t, err)
	assert.Empty(t, found)
}

// TestCheckPeopleRedirectGraph_TargetMissing flags a tombstone pointing at a key
// with no record.
func TestCheckPeopleRedirectGraph_TargetMissing(t *testing.T) {
	found, err := CheckPeopleRedirectGraph(&fakeLedger{
		people:    []string{"person_a-andy"},
		redirects: map[string]string{"person_a-andy": "person_z-ghost"},
	})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, CheckPeopleRedirects, found[0].Check)
	assert.Equal(t, SeverityError, found[0].Severity)
	assert.Equal(t, ruleRedirectTargetMissing, found[0].Rule)
	assert.Equal(t, "people/person_a-andy", found[0].Path)
}

// TestCheckPeopleRedirectGraph_Self flags a record redirecting to itself.
func TestCheckPeopleRedirectGraph_Self(t *testing.T) {
	found, err := CheckPeopleRedirectGraph(&fakeLedger{
		people:    []string{"person_a-alex"},
		redirects: map[string]string{"person_a-alex": "person_a-alex"},
	})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, ruleRedirectSelf, found[0].Rule)
}

// TestCheckPeopleRedirectGraph_Chain flags a tombstone pointing at another
// tombstone; the tail hop (to a canonical) is clean.
func TestCheckPeopleRedirectGraph_Chain(t *testing.T) {
	found, err := CheckPeopleRedirectGraph(&fakeLedger{
		people: []string{"person_a-alex", "person_a-andy", "person_a-ali"},
		redirects: map[string]string{
			"person_a-ali":  "person_a-andy", // → tombstone (chain)
			"person_a-andy": "person_a-alex", // → canonical (clean)
		},
	})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, ruleRedirectChain, found[0].Rule)
	assert.Equal(t, "people/person_a-ali", found[0].Path)
}

// TestCheckPeopleRedirectGraph_Cycle flags a two-hop loop; both members are
// reported.
func TestCheckPeopleRedirectGraph_Cycle(t *testing.T) {
	found, err := CheckPeopleRedirectGraph(&fakeLedger{
		people: []string{"person_a-alex", "person_a-andy"},
		redirects: map[string]string{
			"person_a-alex": "person_a-andy",
			"person_a-andy": "person_a-alex",
		},
	})
	require.NoError(t, err)
	require.Len(t, found, 2)
	for _, f := range found {
		assert.Equal(t, ruleRedirectCycle, f.Rule)
	}
	// Deterministic order: sorted by path.
	assert.Equal(t, "people/person_a-alex", found[0].Path)
	assert.Equal(t, "people/person_a-andy", found[1].Path)
}

// TestCheckPeopleRedirectGraph_UnreadableSkipped: a record the schema check will
// flag as unparseable is not double-reported by the redirect check.
func TestCheckPeopleRedirectGraph_UnreadableSkipped(t *testing.T) {
	found, err := CheckPeopleRedirectGraph(&fakeLedger{
		people:    []string{"person_a-alex", "person_a-bad"},
		badPerson: map[string]bool{"person_a-bad": true},
	})
	require.NoError(t, err)
	assert.Empty(t, found)
}

// TestCheckPeopleRedirectGraph_ListError propagates a people-listing failure as
// an error, not a finding (an unreadable tree is internal, not malformed data).
func TestCheckPeopleRedirectGraph_ListError(t *testing.T) {
	_, err := CheckPeopleRedirectGraph(&fakeLedger{peopleListErr: errSentinel})
	require.ErrorIs(t, err, errSentinel)
}

// TestCheckPeopleRedirectGraph_Empty: an empty people tree is clean.
func TestCheckPeopleRedirectGraph_Empty(t *testing.T) {
	found, err := CheckPeopleRedirectGraph(&fakeLedger{})
	require.NoError(t, err)
	assert.Empty(t, found)
}
