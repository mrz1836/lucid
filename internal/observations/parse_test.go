package observations

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// now is the fixed "now" for parse fixtures: 2026-07-02 21:45 in loc.
var now = at(2026, 7, 2, 21, 45) //nolint:gochecknoglobals // deterministic test-fixture clock

func parse(kind Kind, class string, args ...string) ParseResult {
	return ParseMicrolog(ParseInput{Kind: kind, Class: class, Args: args, Now: now, SpelledOK: true})
}

func TestResolveVerb(t *testing.T) {
	cases := []struct {
		verb  string
		kind  Kind
		class string
		ok    bool
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
		{"withdrawal", KindWithdrawal, "", true},    // generic companion-context kind
		{"habit_change", KindHabitChange, "", true}, // underscore kind reachable via /obs
		{"commitment", KindCommitment, "", true},
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
	assert.True(t, IsCapturableKind(KindWithdrawal))  // companion-context kinds are capturable
	assert.True(t, IsCapturableKind(KindHabitChange))
	assert.True(t, IsCapturableKind(KindCommitment))
}

// TestParse_CompanionContextKinds covers the three companion-context kinds
// (observations.md §3): withdrawal/habit_change reuse the optional-scale head
// (0–10, non-numeric head → note, out-of-range → partial); commitment is
// free-text. All are voice-to-text friendly — trailing text lands in note/what.
func TestParse_CompanionContextKinds(t *testing.T) {
	// withdrawal: optional 0–10 severity, trailing text to note.
	wd := parse(KindWithdrawal, "", "6", "rough", "morning")
	assert.False(t, wd.Partial)
	assert.Equal(t, 6, wd.Payload["severity"])
	assert.Equal(t, "rough morning", wd.Payload["note"])

	// withdrawal non-numeric head → no severity, full text is the note.
	wdFree := parse(KindWithdrawal, "", "groggy", "and", "irritable")
	assert.False(t, wdFree.Partial)
	assert.NotContains(t, wdFree.Payload, "severity")
	assert.Equal(t, "groggy and irritable", wdFree.Payload["note"])

	// withdrawal out-of-range severity → partial, never clamped, kind kept.
	wdOOR := parse(KindWithdrawal, "", "15")
	assert.True(t, wdOOR.Partial)
	assert.Equal(t, KindWithdrawal, wdOOR.Kind)
	assert.Equal(t, "15", wdOOR.Payload["note"])
	assert.Equal(t, ParseMarkerPartial, wdOOR.Payload["parse"])

	// bare withdrawal → valid event (optional scale), empty payload.
	wdBare := parse(KindWithdrawal, "")
	assert.False(t, wdBare.Partial)
	assert.NotContains(t, wdBare.Payload, "severity")

	// habit_change: optional 0–10 load, trailing text to note.
	hc := parse(KindHabitChange, "", "7", "cut", "coffee")
	assert.False(t, hc.Partial)
	assert.Equal(t, 7, hc.Payload["load"])
	assert.Equal(t, "cut coffee", hc.Payload["note"])

	// habit_change out-of-range → partial.
	hcOOR := parse(KindHabitChange, "", "12")
	assert.True(t, hcOOR.Partial)
	assert.Equal(t, KindHabitChange, hcOOR.Kind)

	// commitment: free-text `what`.
	cm := parse(KindCommitment, "", "call", "the", "dentist")
	assert.False(t, cm.Partial)
	assert.Equal(t, "call the dentist", cm.Payload["what"])

	// bare commitment → partial (nothing to record).
	cmEmpty := parse(KindCommitment, "")
	assert.True(t, cmEmpty.Partial)
	assert.Equal(t, KindCommitment, cmEmpty.Kind)
	assert.Equal(t, ParseMarkerPartial, cmEmpty.Payload["parse"])
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

// TestIsMemoryCertainty covers the shared certainty vocabulary gate: only the
// three documented keywords are valid, case is the caller's concern.
func TestIsMemoryCertainty(t *testing.T) {
	for _, ok := range []string{"vivid", "hazy", "reconstructed"} {
		assert.Truef(t, IsMemoryCertainty(ok), "%q is a certainty", ok)
	}
	for _, bad := range []string{"", "Vivid", "sure", "foggy"} {
		assert.Falsef(t, IsMemoryCertainty(bad), "%q is not a certainty", bad)
	}
}

// TestParseMemoryFields_ConventionKeys proves the structured story path fills
// the convention payload keys on the frozen envelope (life-archive.md §3): text
// anchors the memory, a valid certainty and the tone/why/follow-up/people keys
// are set when present, and an out-of-vocabulary certainty is dropped rather
// than stored — the write verb rejects it up front, so the payload never carries
// a junk certainty.
func TestParseMemoryFields_ConventionKeys(t *testing.T) {
	payload, partial := ParseMemoryFields(MemoryInput{
		Text:         "we drove to the coast at 2am",
		Certainty:    "Vivid", // case-folded to a valid keyword
		Tone:         "wild, free",
		WhyItMatters: "first real taste of not asking permission",
		FollowUp:     "who else was in the car?",
		People:       []string{"Dana", " ", "Sam"},
	})
	assert.False(t, partial)
	assert.Equal(t, "we drove to the coast at 2am", payload[MemoryFieldText])
	assert.Equal(t, "vivid", payload[MemoryFieldCertainty])
	assert.Equal(t, "wild, free", payload[MemoryFieldTone])
	assert.Equal(t, "first real taste of not asking permission", payload[MemoryFieldWhyItMatters])
	assert.Equal(t, "who else was in the car?", payload[MemoryFieldFollowUp])
	assert.Equal(t, []string{"Dana", "Sam"}, payload[MemoryFieldPeople], "blank people are dropped")

	// An unknown certainty is omitted, not stored (validated by the write verb).
	junk, _ := ParseMemoryFields(MemoryInput{Text: "something", Certainty: "sure"})
	assert.NotContains(t, junk, MemoryFieldCertainty)
}

// TestParseMemoryFields_PartialPath proves the capture-never-blocks contract:
// an empty text yields the partial payload ({note, parse}), exactly like the
// token grammar's empty-text path.
func TestParseMemoryFields_PartialPath(t *testing.T) {
	payload, partial := ParseMemoryFields(MemoryInput{Text: "   ", Tone: "dropped on partial"})
	assert.True(t, partial)
	assert.Equal(t, ParseMarkerPartial, payload["parse"])
	assert.Contains(t, payload, "note")
	assert.NotContains(t, payload, MemoryFieldTone, "convention keys are not kept on the partial path")
}

// TestResolveBackdate covers the single-token --day grammar the story-capture
// verb reuses: empty is now/exact, a bare date is approximate (its own calendar
// day, never rolled), @yesterday is approximate, a range yields an end, and an
// unrecognized token falls back to now/exact (capture never blocks).
func TestResolveBackdate(t *testing.T) {
	// Empty → now at exact precision.
	occ, prec, end := ResolveBackdate("", now)
	assert.Equal(t, now, occ)
	assert.Equal(t, PrecisionExact, prec)
	assert.Nil(t, end)

	// A bare date (no @) → approximate, its own calendar day (the excavated case).
	occ, prec, end = ResolveBackdate("1999-06-01", now)
	assert.Equal(t, PrecisionApproximate, prec)
	assert.Nil(t, end)
	assert.Equal(t, "1999-06-01", DeriveLogicalDate(occ, prec, DefaultRolloverMin))

	// A leading @ is optional and equivalent.
	occAt, precAt, _ := ResolveBackdate("@1999-06-01", now)
	assert.Equal(t, occ, occAt)
	assert.Equal(t, prec, precAt)

	// @yesterday → approximate, the prior civil date.
	occ, prec, _ = ResolveBackdate("@yesterday", now)
	assert.Equal(t, PrecisionApproximate, prec)
	assert.Equal(t, at(2026, 7, 1, 0, 0), occ)

	// A range → range precision with an end.
	_, prec, end = ResolveBackdate("@09:00-12:30", now)
	assert.Equal(t, PrecisionRange, prec)
	assert.NotNil(t, end)

	// Junk falls back to now/exact — never a crash, never a block.
	occ, prec, _ = ResolveBackdate("@notatime", now)
	assert.Equal(t, now, occ)
	assert.Equal(t, PrecisionExact, prec)
}
