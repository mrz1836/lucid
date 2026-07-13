package observations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzParseMicrolog asserts ParseMicrolog upholds its documented totality
// (parse.go §ParseMicrolog: "deterministic and total — every input yields a
// well-formed result, because capture never blocks"). For any kind, backdating
// token, tag, or garbage, the parser must not panic and must not return a
// half-built result: the invoked kind is always preserved and the payload and
// refs maps are always initialized, so a projection can read the result
// unconditionally. Seeds cover one representative line per capturable kind plus
// the empty, enricher-only, and unknown-kind edges, in both spelled-number
// modes.
func FuzzParseMicrolog(f *testing.F) {
	seeds := []struct{ kind, argline string }{
		{KindPain, "7 knee @yesterday"},
		{KindMood, "4 #anxious wired"},
		{KindSleep, "10pm 6am quality 3"},
		{KindIntake, "food apple @8am"},
		{KindElimination, "5"},
		{KindMed, "ibuprofen 400 skipped"},
		{KindMeasurement, "weight 70"},
		{KindMemory, "vivid a long walk"},
		{KindLocation, "the old house"},
		{KindSymptom, "@2h nausea"},
		{"", ""},
		{KindContextDay, "enricher-only"},
		{"nonsense", "@@@ #### ????"},
	}
	for _, s := range seeds {
		f.Add(s.kind, s.argline, true)
		f.Add(s.kind, s.argline, false)
	}

	f.Fuzz(func(t *testing.T, kind, argline string, spelled bool) {
		res := ParseMicrolog(ParseInput{
			Kind:      kind,
			Args:      strings.Fields(argline),
			Now:       now,
			SpelledOK: spelled,
		})
		// Total: the result is always well-formed, whatever the input.
		require.NotNil(t, res.Payload, "Payload must always be initialized")
		require.NotNil(t, res.Refs, "Refs must always be initialized")
		require.Equal(t, kind, res.Kind, "the invoked kind is always preserved")
	})
}
