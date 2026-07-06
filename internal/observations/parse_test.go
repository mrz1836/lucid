package observations

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// now is the fixed "now" for parse fixtures: 2026-07-02 21:45 in loc.
var now = at(2026, 7, 2, 21, 45) //nolint:gochecknoglobals // deterministic test-fixture clock

func parse(kind, class string, args ...string) ParseResult {
	return ParseMicrolog(ParseInput{Kind: kind, Class: class, Args: args, Now: now, SpelledOK: true})
}

func TestResolveVerb(t *testing.T) {
	cases := []struct {
		verb, kind, class string
		ok                bool
	}{
		{"pain", KindPain, "", true},
		{"ate", KindIntake, "food", true},
		{"drank", KindIntake, "liquid", true},
		{"bm", KindElimination, "bm", true},
		{"urine", KindElimination, "urine", true},
		{"mood", KindMood, "", true},
		{"slept", KindSleep, "", true},
		{"where", KindLocation, "", true},
		{"symptom", KindSymptom, "", true},
		{"med", KindMed, "", true},
		{"PAIN", KindPain, "", true}, // case-insensitive verb
		{"context.day", "", "", false},
		{"nonsense", "", "", false},
	}
	for _, c := range cases {
		kind, class, ok := ResolveVerb(c.verb)
		assert.Equalf(t, c.ok, ok, "verb %q ok", c.verb)
		if c.ok {
			assert.Equal(t, c.kind, kind, c.verb)
			assert.Equal(t, c.class, class, c.verb)
		}
	}
}

func TestIsCapturableKind(t *testing.T) {
	assert.True(t, IsCapturableKind(KindPain))
	assert.True(t, IsCapturableKind(KindLocation))
	assert.False(t, IsCapturableKind(KindContextDay)) // enricher-written only
	assert.False(t, IsCapturableKind("hypothesis"))   // Scientist layer, out of MVP
}

// TestParse_Shorthands checks each named shorthand yields the documented kind
// and head fields (observations.md §3/§4).
func TestParse_Shorthands(t *testing.T) {
	pain := parse(KindPain, "", "6", "knee", "aching", "after", "the", "run")
	assert.False(t, pain.Partial)
	assert.Equal(t, 6, pain.Payload["intensity"])
	assert.Equal(t, "knee", pain.Payload["site"])
	assert.Equal(t, "aching after the run", pain.Payload["note"])

	painSide := parse(KindPain, "", "8", "shoulder", "left", "after", "loading")
	assert.Equal(t, "left", painSide.Payload["side"])
	assert.Equal(t, "shoulder", painSide.Payload["site"])

	ate := parse(KindIntake, "food", "eggs,", "toast,", "coffee")
	assert.Equal(t, "food", ate.Payload["class"])
	assert.Equal(t, "eggs, toast, coffee", ate.Payload["what"])

	drank := parse(KindIntake, "liquid", "500ml", "water")
	assert.Equal(t, "liquid", drank.Payload["class"])
	assert.Equal(t, "500ml", drank.Payload["amount"])
	assert.Equal(t, "water", drank.Payload["what"])

	// `/ate 3 eggs`: a bare integer belongs to `what`, not amount.
	ateBare := parse(KindIntake, "food", "3", "eggs")
	assert.NotContains(t, ateBare.Payload, "amount")
	assert.Equal(t, "3 eggs", ateBare.Payload["what"])

	moodEv := parse(KindMood, "", "2", "wired")
	assert.Equal(t, 2, moodEv.Payload["level"])
	assert.Equal(t, "wired", moodEv.Payload["word"])

	med := parse(KindMed, "", "ibuprofen", "400")
	assert.Equal(t, "ibuprofen", med.Payload["what"])
	assert.Equal(t, "400", med.Payload["dose"])
	assert.Equal(t, true, med.Payload["taken"])

	interv := parse(KindIntervention, "", "physio", "session", "left-knee")
	assert.Equal(t, "physio session left-knee", interv.Payload["what"])

	meas := parse(KindMeasurement, "", "weight", "180", "lb")
	assert.Equal(t, "weight", meas.Payload["metric"])
	assert.Equal(t, "180", meas.Payload["value"])
	assert.Equal(t, "lb", meas.Payload["unit"])

	sym := parse(KindSymptom, "", "headache", "4")
	assert.Equal(t, "headache", sym.Payload["name"])
	assert.Equal(t, 4, sym.Payload["severity"])

	mem := parse(KindMemory, "", "reconstructed", "started", "my", "second", "job")
	assert.Equal(t, "reconstructed", mem.Payload["certainty"])
	assert.Equal(t, "started my second job", mem.Payload["text"])
}

// TestParse_BareForms: bare valid events and the partial path with the kind
// preserved (observations.md §4; error-states out-of-range).
func TestParse_BareForms(t *testing.T) {
	painBare := parse(KindPain, "", "6")
	assert.False(t, painBare.Partial)
	assert.Equal(t, 6, painBare.Payload["intensity"])
	assert.NotContains(t, painBare.Payload, "site")

	bm := parse(KindElimination, "bm")
	assert.False(t, bm.Partial)
	assert.Equal(t, "bm", bm.Payload["class"])
	assert.NotContains(t, bm.Payload, "bristol")

	bm4 := parse(KindElimination, "bm", "4")
	assert.False(t, bm4.Partial)
	assert.Equal(t, 4, bm4.Payload["bristol"])

	// Missing required scale → partial, kind kept, full text to note.
	moodPartial := parse(KindMood, "", "wired")
	assert.True(t, moodPartial.Partial)
	assert.Equal(t, KindMood, moodPartial.Kind)
	assert.Equal(t, "wired", moodPartial.Payload["note"])
	assert.Equal(t, ParseMarkerPartial, moodPartial.Payload["parse"])
	assert.NotContains(t, moodPartial.Payload, "level")

	// Out-of-range scale → partial, never clamped.
	painOOR := parse(KindPain, "", "15")
	assert.True(t, painOOR.Partial)
	assert.Equal(t, KindPain, painOOR.Kind)
	assert.Equal(t, "15", painOOR.Payload["note"])
	assert.Equal(t, ParseMarkerPartial, painOOR.Payload["parse"])

	bmOOR := parse(KindElimination, "bm", "9")
	assert.True(t, bmOOR.Partial)
	assert.Equal(t, KindElimination, bmOOR.Kind)

	// A required-scale kind with no args at all → partial.
	painEmpty := parse(KindPain, "")
	assert.True(t, painEmpty.Partial)
}

// TestParse_Backdating covers the @-token forms (observations.md §4).
func TestParse_Backdating(t *testing.T) {
	// @yesterday 19:30 → yesterday at 19:30, exact.
	yd := parse(KindPain, "", "6", "@yesterday", "19:30")
	assert.Equal(t, PrecisionExact, yd.Precision)
	assert.Equal(t, at(2026, 7, 1, 19, 30), yd.OccurredAt)
	assert.Equal(t, "2026-07-01", DeriveLogicalDate(yd.OccurredAt, yd.Precision, DefaultRolloverMin))
	assert.Equal(t, 6, yd.Payload["intensity"]) // @-tokens are stripped from the head

	// @18:30 → today at that time, exact.
	tt := parse(KindMood, "", "3", "calm", "@18:30")
	assert.Equal(t, PrecisionExact, tt.Precision)
	assert.Equal(t, at(2026, 7, 2, 18, 30), tt.OccurredAt)

	// @yesterday alone → approximate, midnight.
	ydAlone := parse(KindMood, "", "3", "@yesterday")
	assert.Equal(t, PrecisionApproximate, ydAlone.Precision)
	assert.Equal(t, at(2026, 7, 1, 0, 0), ydAlone.OccurredAt)

	// @YYYY-MM-DD → approximate midnight (the excavated-memory case).
	oldDate := parse(KindMemory, "", "hazy", "second", "job", "@2014-09-01")
	assert.Equal(t, PrecisionApproximate, oldDate.Precision)
	assert.Equal(t, "2014-09-01", DeriveLogicalDate(oldDate.OccurredAt, oldDate.Precision, DefaultRolloverMin))

	// @HH:MM-HH:MM → a range with occurred_at_end.
	rng := parse(KindPain, "", "8", "shoulder", "@09:00-12:30")
	assert.Equal(t, PrecisionRange, rng.Precision)
	require.NotNil(t, rng.OccurredEnd)
	assert.Equal(t, at(2026, 7, 2, 9, 0), rng.OccurredAt)
	assert.Equal(t, at(2026, 7, 2, 12, 30), *rng.OccurredEnd)

	// Full timestamp inside the token → exact.
	full := parse(KindMood, "", "3", "@2026-07-01T08:15")
	assert.Equal(t, PrecisionExact, full.Precision)
	assert.Equal(t, at(2026, 7, 1, 8, 15), full.OccurredAt)

	// A non-@ line defaults to now, exact.
	def := parse(KindMood, "", "3", "steady")
	assert.Equal(t, PrecisionExact, def.Precision)
	assert.Equal(t, now, def.OccurredAt)

	// An unrecognized @-form is left as verbatim text, not a crash.
	junk := parse(KindMood, "", "3", "@notatime")
	assert.Equal(t, PrecisionExact, junk.Precision)
	assert.Equal(t, now, junk.OccurredAt)
}

// TestParse_TagsAndNote: #tags copied into Tags AND kept verbatim in the note.
func TestParse_TagsAndNote(t *testing.T) {
	res := parse(KindPain, "", "6", "knee", "#running", "aching")
	assert.Equal(t, []string{"running"}, res.Tags)
	assert.Contains(t, res.Payload["note"], "#running", "the note keeps the tag verbatim")
}

// TestParse_DictationTolerance: q<n>, spelled digits, colon-less times.
func TestParse_DictationTolerance(t *testing.T) {
	spelled := parse(KindPain, "", "six", "knee")
	assert.Equal(t, 6, spelled.Payload["intensity"])

	sleep := parse(KindSleep, "", "2340", "0710", "q3")
	assert.Equal(t, PrecisionRange, sleep.Precision)
	assert.Equal(t, 3, sleep.Payload["quality"])
	require.NotNil(t, sleep.OccurredEnd)
	// bed on the prior evening, wake this morning → night's start is the key.
	assert.Equal(t, at(2026, 7, 1, 23, 40), sleep.OccurredAt)
	assert.Equal(t, at(2026, 7, 2, 7, 10), *sleep.OccurredEnd)

	sleepQualityWord := parse(KindSleep, "", "quality", "2")
	assert.Equal(t, 2, sleepQualityWord.Payload["quality"])

	// A time-less sleep anchors approximately to the prior logical day.
	sleepBare := parse(KindSleep, "", "q4")
	assert.Equal(t, PrecisionApproximate, sleepBare.Precision)
	assert.Equal(t, 4, sleepBare.Payload["quality"])
}

// TestParse_LocationAndIntakeClass covers the where head and generic intake
// class token.
func TestParse_LocationAndIntakeClass(t *testing.T) {
	where := parse(KindLocation, "", "Lisbon")
	assert.Equal(t, "Lisbon", where.PlaceName)
	assert.False(t, where.Partial)

	whereMulti := parse(KindLocation, "", "New", "York")
	assert.Equal(t, "New York", whereMulti.PlaceName)

	whereEmpty := parse(KindLocation, "")
	assert.True(t, whereEmpty.Partial)

	supp := parse(KindIntake, "", "supplement", "vitamin", "d")
	assert.Equal(t, "supplement", supp.Payload["class"])
	assert.Equal(t, "vitamin d", supp.Payload["what"])
}

// TestParse_ZeroNowDefaults exercises the wall-clock default branch.
func TestParse_ZeroNowDefaults(t *testing.T) {
	res := ParseMicrolog(ParseInput{Kind: KindMood, Args: []string{"3"}})
	assert.Equal(t, PrecisionExact, res.Precision)
	assert.WithinDuration(t, time.Now(), res.OccurredAt, 5*time.Second)
}
