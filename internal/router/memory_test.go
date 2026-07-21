package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// bootedMemoryRouter returns a booted router over a fresh temp Ledger with the
// (enable-gated) memory kind turned on — the surface every story-capture test
// needs, since a default Ledger keeps memory off (config.go).
func bootedMemoryRouter(t *testing.T) (*Router, *storage.Adapter, string) {
	t.Helper()
	r, a, home := newBootedRouter(t)
	require.NoError(t, a.ScaffoldObservations())
	cfg, err := a.ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = append(cfg.KindsEnabled, observations.KindMemory)
	require.NoError(t, a.SaveObservationsConfig(cfg))
	return r, a, home
}

// readMemoryEvent reads back the appended event by id from its logical day.
func readMemoryEvent(t *testing.T, a *storage.Adapter, res MemoryWriteResult) observations.Event {
	t.Helper()
	events, _, err := a.ReadObservationsDay(res.LogicalDate)
	require.NoError(t, err)
	for _, ev := range events {
		if ev.ID == res.EventID {
			return ev
		}
	}
	t.Fatalf("event %s not found in %s", res.EventID, res.LogicalDate)
	return observations.Event{}
}

// TestWriteMemory_ConventionPayloadAndRefs proves a story writes the full
// convention payload on the frozen envelope, sourced as excavation, with the
// era referenced verbatim and the place resolved + registered exactly like a
// sticky location.
func TestWriteMemory_ConventionPayloadAndRefs(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)

	res, err := r.WriteMemory(MemoryWriteRequest{
		Text:         "we drove to the coast at 2am",
		Certainty:    "vivid",
		Era:          "era_wild-summer",
		Place:        "Ocean City boardwalk",
		Tone:         "reckless, alive",
		WhyItMatters: "first time I felt free",
		FollowUp:     "who was driving?",
		Now:          fixedNow(),
	})
	require.NoError(t, err)
	assert.False(t, res.Partial)
	assert.False(t, res.Rejected)

	ev := readMemoryEvent(t, a, res)
	assert.Equal(t, observations.KindMemory, ev.Kind)
	assert.Equal(t, observations.SourceExcavation, ev.Source, "a story is sourced as excavation")
	assert.Equal(t, 1, ev.Schema, "the envelope stays frozen at schema 1")

	// Convention payload.
	assert.Equal(t, "we drove to the coast at 2am", ev.Payload[observations.MemoryFieldText])
	assert.Equal(t, "vivid", ev.Payload[observations.MemoryFieldCertainty])
	assert.Equal(t, "reckless, alive", ev.Payload[observations.MemoryFieldTone])
	assert.Equal(t, "first time I felt free", ev.Payload[observations.MemoryFieldWhyItMatters])
	assert.Equal(t, "who was driving?", ev.Payload[observations.MemoryFieldFollowUp])

	// Refs: era verbatim, place resolved to a registry key.
	assert.Equal(t, "era_wild-summer", ev.Refs["era"], "era is referenced verbatim")
	placeKey, ok := ev.Refs["place"].(string)
	require.True(t, ok, "place ref is a resolved key")
	assert.NotEmpty(t, placeKey)

	// The place was registered exactly like a sticky location.
	rec, found, err := a.ReadRegistry(observations.RegistryPlace, placeKey)
	require.NoError(t, err)
	require.True(t, found, "the story's place is upserted into the place registry")
	assert.Equal(t, "Ocean City boardwalk", rec.DisplayName)
}

// TestWriteMemory_BackdatedFilesUnderItsOwnDay proves the bitemporal placement:
// a story with a past --day files under that day's logical_date at approximate
// precision, and writing it does not touch a current-day log (no overwrite).
func TestWriteMemory_BackdatedFilesUnderItsOwnDay(t *testing.T) {
	r, a, home := bootedMemoryRouter(t)

	// A current-day story lands under today; snapshot its file bytes.
	today, err := r.WriteMemory(MemoryWriteRequest{Text: "quiet memory from just now", Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "2026-07-05", today.LogicalDate)
	before := readDayFileBytes(t, home, today.LogicalDate)

	// A backdated story files under its own decades-old day, approximate.
	old, err := r.WriteMemory(MemoryWriteRequest{
		Text: "the summer everything changed", Certainty: "hazy", Day: "1999-06-01", Now: fixedNow(),
	})
	require.NoError(t, err)
	assert.Equal(t, "1999-06-01", old.LogicalDate, "an excavated memory keeps its own calendar day")

	ev := readMemoryEvent(t, a, old)
	assert.Equal(t, observations.PrecisionApproximate, ev.OccurredAtPrecision)

	// The current-day file is byte-identical — a backdated write never rewrites
	// a day already lived.
	after := readDayFileBytes(t, home, today.LogicalDate)
	assert.Equal(t, before, after, "the current-day log is byte-unaffected by a backdated write")
}

// TestWriteMemory_PeopleLinkExistingOnly proves people are always kept as
// payload testimony, and refs.person links only names that already resolve to
// exactly one person record — no minting here.
func TestWriteMemory_PeopleLinkExistingOnly(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)

	// Seed one known person via the deterministic People routine.
	pr, err := a.UpdatePerson(storage.PersonMention{DisplayName: "Dana", RawEntryID: "raw_2020_01_01_10_00", At: fixedNow()})
	require.NoError(t, err)

	res, err := r.WriteMemory(MemoryWriteRequest{
		Text:   "the road trip",
		People: []string{"Dana", "a stranger we never named"},
		Now:    fixedNow(),
	})
	require.NoError(t, err)

	ev := readMemoryEvent(t, a, res)
	// Both names are kept in the payload as testimony.
	assert.Equal(t, []any{"Dana", "a stranger we never named"}, ev.Payload[observations.MemoryFieldPeople])
	// Only the resolvable name is linked into refs.person.
	assert.Equal(t, []any{pr.PersonKey}, ev.Refs["person"])
}

// TestWriteMemory_TextOnlyStory proves a bare text story writes cleanly with no
// refs beyond nothing — media never gates it and no era/place is invented.
func TestWriteMemory_TextOnlyStory(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)

	res, err := r.WriteMemory(MemoryWriteRequest{Text: "just a plain memory", Now: fixedNow()})
	require.NoError(t, err)
	assert.False(t, res.Partial)

	ev := readMemoryEvent(t, a, res)
	assert.Equal(t, "just a plain memory", ev.Payload[observations.MemoryFieldText])
	assert.NotContains(t, ev.Refs, "entry")
	assert.NotContains(t, ev.Refs, "era")
	assert.NotContains(t, ev.Refs, "place")
}

// TestWriteMemory_MediaEntryRef proves an attached photo's raw id is referenced
// from refs.entry when supplied by the CLI attach step.
func TestWriteMemory_MediaEntryRef(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)

	res, err := r.WriteMemory(MemoryWriteRequest{
		Text: "the photo from that night", EntryRef: "raw_1999_06_01_00_00", Now: fixedNow(),
	})
	require.NoError(t, err)

	ev := readMemoryEvent(t, a, res)
	assert.Equal(t, "raw_1999_06_01_00_00", ev.Refs["entry"])
}

// TestWriteMemory_RejectsBadCertainty rejects an out-of-vocabulary certainty
// before any write (error-states.md §St-1: nothing saved).
func TestWriteMemory_RejectsBadCertainty(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)

	_, err := r.WriteMemory(MemoryWriteRequest{Text: "x", Certainty: "sure", Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")

	mems, err := a.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)
	assert.Empty(t, mems, "a rejected certainty leaves nothing on disk")
}

// TestWriteMemory_RejectsDisabledKind reports the enable hint and writes nothing
// when the memory kind is off — the one reject path, mirroring Capture.
func TestWriteMemory_RejectsDisabledKind(t *testing.T) {
	r, a, _ := newBootedRouter(t) // default Ledger keeps memory disabled

	res, err := r.WriteMemory(MemoryWriteRequest{Text: "should not land", Now: fixedNow()})
	require.NoError(t, err)
	assert.True(t, res.Rejected)
	assert.Contains(t, res.Ack, "isn't enabled")
	assert.Empty(t, res.EventID, "a rejected capture writes no event")

	mems, err := a.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)
	assert.Empty(t, mems)
}

// readDayFileBytes reads the raw JSONL bytes of a logical day's observation file
// (observations/<year>/<month>/obs_<date>.jsonl) so a test can assert
// byte-stability across an unrelated write.
func readDayFileBytes(t *testing.T, home, date string) []byte {
	t.Helper()
	year, month := date[0:4], date[5:7]
	name := "obs_" + strings.ReplaceAll(date, "-", "_") + ".jsonl"
	b, err := os.ReadFile(filepath.Join(home, "observations", year, month, name))
	require.NoError(t, err)
	return b
}
