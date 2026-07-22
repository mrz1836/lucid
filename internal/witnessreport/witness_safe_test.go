package witnessreport

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWitnessSafe_PassesCleanProse: ordinary witness-report prose — a warm read,
// constructive faults framing, and grounded friend-asks — carries no
// private-detail marker and passes the scan cleanly.
func TestWitnessSafe_PassesCleanProse(t *testing.T) {
	clean := []string{
		"A mixed week, honestly read — the chain held on the back half.",
		"Two days slipped midweek; nothing alarming, just worth naming.",
		"Check in on me Wednesday and ask how the workout block went.",
		"", // an empty slot is trivially safe
	}
	assert.True(t, witnessSafe(clean...), "clean friend-facing prose must pass")
}

// TestWitnessSafe_NoFalseTripOnFriendAsks is the A5 guard: the phrasings a real
// friend-ask uses — post/dm/send a reminder, "clearly", "my week" — must NOT
// trip. Those are the safety agent's external-action / overclaim categories,
// which this witness scan deliberately does not borrow.
func TestWitnessSafe_NoFalseTripOnFriendAsks(t *testing.T) {
	asks := []string{
		"DM me midweek to check in.",
		"Post me a reminder to log daily.",
		"Send a nudge if I go quiet.",
		"Ask me about my week and clearly tell me if I'm dodging.",
	}
	for _, a := range asks {
		assert.Truef(t, witnessSafe(a), "friend-ask phrasing must not trip the private-detail scan: %q", a)
	}
}

// TestWitnessSafe_TripsPrivateDetail pins every private-detail category to a
// trip: a raw journal citation, a wikilink, an on-disk ledger path, elevated
// medical detail, and named private-relationship detail.
func TestWitnessSafe_TripsPrivateDetail(t *testing.T) {
	cases := []struct {
		name  string
		prose string
	}{
		{"journal id", "as I wrote in entry_42 this week"},
		{"wikilink citation", "this ties to [[the-hard-monday]] from the journal"},
		{"ledger path", "logged under raw/2026-07-20.jsonl"},
		{"dotdir path", "pulled straight from ~/.lucid"},
		{"diagnosis", "after the doctor's diagnosis on Tuesday"},
		{"dosage", "bumped to 50 mg this week"},
		{"vitals", "blood pressure was up again"},
		{"relationship", "a rough patch with my partner"},
		{"therapist", "my therapist pointed out the pattern"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Falsef(t, witnessSafe(tc.prose), "must trip on %q", tc.prose)
		})
	}
}

// TestWitnessSafe_TripsWhenAnySlotUnsafe: the scan is over the union of the
// slots — one unsafe slot fails the whole set closed even when the others are
// clean.
func TestWitnessSafe_TripsWhenAnySlotUnsafe(t *testing.T) {
	assert.False(t, witnessSafe(
		"A steady week overall.",
		"The chain held.",
		"See [[private-entry]] for the detail.",
	), "one unsafe slot fails the whole set closed")
}

// TestPackage_NoPrivateReaderImport is the structural-firewall assertion (the
// primary layer): NO non-test file in the package imports an observations,
// journal, or raw-entry reader. The model-allowed compose path may reach
// internal/provider — that is the model seam, not a private-data seam — but a
// reader that could pull raw journal detail must not exist anywhere in the
// package, so private detail is unreachable by construction.
func TestPackage_NoPrivateReaderImport(t *testing.T) {
	forbidden := []string{
		"internal/observations",
		"internal/agents/reflection",
		"internal/agents/intake",
		"internal/agents/structuring",
	}
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Clean(name), nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range f.Imports {
			for _, bad := range forbidden {
				assert.NotContainsf(t, imp.Path.Value, bad,
					"%s imports %s — the witness report package must wire no private-data reader", name, imp.Path.Value)
			}
		}
		checked++
	}
	assert.Positive(t, checked, "the import scan must actually inspect the package's source files")
}
