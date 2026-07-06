package observations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpelledDigits covers the full dictation digit table zero–ten.
func TestSpelledDigits(t *testing.T) {
	want := map[string]int{
		"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	}
	for word, val := range want {
		got, ok := parseScaleToken(word, true)
		require.Truef(t, ok, "spelled %q", word)
		assert.Equal(t, val, got, word)
	}
	// Not spelled-enabled → non-numeric.
	_, ok := parseScaleToken("six", false)
	assert.False(t, ok)
	_, ok = parseScaleToken("eleven", true)
	assert.False(t, ok, "only zero–ten are spelled digits")
}

func TestParse_KindEdgeCases(t *testing.T) {
	// symptom with an out-of-range severity → partial.
	symOOR := parse(KindSymptom, "", "headache", "15")
	assert.True(t, symOOR.Partial)

	// symptom with just a name (no severity) is valid.
	sym := parse(KindSymptom, "", "nausea")
	assert.False(t, sym.Partial)
	assert.Equal(t, "nausea", sym.Payload["name"])

	// symptom with no args → partial.
	assert.True(t, parse(KindSymptom, "").Partial)

	// memory without a certainty keyword keeps just the text.
	mem := parse(KindMemory, "", "the", "kitchen", "on", "Elm")
	assert.False(t, mem.Partial)
	assert.Equal(t, "the kitchen on Elm", mem.Payload["text"])
	assert.NotContains(t, mem.Payload, "certainty")

	// memory that is only a certainty keyword (no text) → partial.
	assert.True(t, parse(KindMemory, "", "vivid").Partial)

	// med with no dose.
	med := parse(KindMed, "", "tylenol")
	assert.Equal(t, "tylenol", med.Payload["what"])
	assert.NotContains(t, med.Payload, "dose")
	assert.True(t, parse(KindMed, "").Partial)

	// measurement with a note and with too few tokens.
	meas := parse(KindMeasurement, "", "bp", "120", "mmHg", "resting")
	assert.Equal(t, "resting", meas.Payload["note"])
	assert.True(t, parse(KindMeasurement, "", "weight").Partial)

	// intervention with no text → partial.
	assert.True(t, parse(KindIntervention, "").Partial)

	// elimination with an empty class defaults to bm.
	elim := parse(KindElimination, "")
	assert.Equal(t, "bm", elim.Payload["class"])

	// intake amount forms: unit-suffixed vs x-count vs bare.
	assert.Equal(t, "500ml", parse(KindIntake, "liquid", "500ml", "water").Payload["amount"])
	assert.Equal(t, "x2", parse(KindIntake, "supplement", "x2", "pills").Payload["amount"])
	assert.NotContains(t, parse(KindIntake, "food", "3", "eggs").Payload, "amount")
}

func TestIsAmount(t *testing.T) {
	assert.True(t, isAmount("500ml"))
	assert.True(t, isAmount("40g"))
	assert.True(t, isAmount("x2"))
	assert.False(t, isAmount("3"))    // bare integer → what, not amount
	assert.False(t, isAmount(""))     // empty
	assert.False(t, isAmount("eggs")) // no leading digit
	assert.False(t, isAmount("x"))    // x with no count
}

func TestRegistryDir_AllKinds(t *testing.T) {
	for kind, dir := range map[string]string{
		RegistryInjury: "injuries", RegistryThread: "threads",
		RegistryPlace: "places", RegistryEra: "eras",
	} {
		got, ok := RegistryDir(kind)
		require.Truef(t, ok, kind)
		assert.Equal(t, dir, got)
	}
}

func TestIsRangeSpanning_BadDates(t *testing.T) {
	night := rangeEvent("obs_2026_07_01_004", "2026-07-01", "2026-07-02T07:10:00-04:00")
	assert.False(t, IsRangeSpanning(night, "not-a-date", loc), "a bad target date never spans")

	badStart := night
	badStart.LogicalDate = "garbage"
	assert.False(t, IsRangeSpanning(badStart, "2026-07-02", loc))

	badEnd := night
	badEnd.OccurredAtEnd = strptr("not-a-timestamp")
	assert.False(t, IsRangeSpanning(badEnd, "2026-07-02", loc))
}

func TestMarshalLine_WithRangeEnd(t *testing.T) {
	end := "2026-07-02T07:10:00-04:00"
	e := Event{
		ID: "obs_2026_07_01_004", Schema: Schema, Kind: KindSleep,
		RecordedAt: end, OccurredAt: "2026-07-01T23:00:00-04:00",
		OccurredAtPrecision: PrecisionRange, OccurredAtEnd: &end,
		LogicalDate: "2026-07-01", Source: SourceMicrolog,
		Payload: map[string]any{"quality": 3},
	}
	line, err := e.MarshalLine()
	require.NoError(t, err)
	assert.Contains(t, string(line), `"occurred_at_end":"2026-07-02T07:10:00-04:00"`)
	assert.NotContains(t, string(line), "\n")
}

func TestParseClockHM_Forms(t *testing.T) {
	for _, tok := range []string{"23:40", "7:10", "2340", "0710"} {
		_, ok := parseClockHM(tok)
		assert.Truef(t, ok, "clock %q", tok)
	}
	for _, tok := range []string{"", "24:61", "99", "notime"} {
		_, ok := parseClockHM(tok)
		assert.Falsef(t, ok, "clock %q should be rejected", tok)
	}
}

func TestParse_MultipleTagsDeduped(t *testing.T) {
	res := parse(KindPain, "", "6", "knee", "#run", "#run", "#am")
	assert.Equal(t, []string{"run", "am"}, res.Tags)
	assert.Contains(t, res.Payload["note"], "#run")
}
