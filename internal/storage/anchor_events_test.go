package storage

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

// Every anchor, label, note and event in these tests is synthetic.

const anchorEvRecordedAt = "2026-03-02T21:10:00-05:00"

// syntheticAnchorEvent builds a well-formed event of any type that needs no
// per-type field (slip, miss, graduate).
func syntheticAnchorEvent(id, anchorID, eventType, day string) engine.AnchorEvent {
	return engine.AnchorEvent{ID: id, AnchorID: anchorID, Type: eventType, Day: day, RecordedAt: anchorEvRecordedAt}
}

// syntheticKindEvent builds a well-formed kind event.
func syntheticKindEvent(id, anchorID, day, kind string, allowance, window int) engine.AnchorEvent {
	e := syntheticAnchorEvent(id, anchorID, engine.AnchorEventKind, day)
	e.Kind, e.Allowance, e.WindowDays = kind, allowance, window
	return e
}

// syntheticAddKindIntent builds the pair intent `anchor add --kind habit`
// writes: the kind event first, then the new v3 definition.
func syntheticAddKindIntent(eventID, anchorID, label string) engine.AnchorPairIntent {
	return engine.AnchorPairIntent{
		Order: engine.AnchorPairEventFirst,
		Event: syntheticKindEvent(eventID, anchorID, "2026-03-02", engine.AnchorKindHabit, 0, 0),
		Compat: engine.Anchor{
			ID: anchorID, Label: label, Date: "2026-03-02", RecordedAt: anchorEvRecordedAt,
		},
	}
}

// readStoreBytes returns the store's bytes, or nil when it does not exist.
func readStoreBytes(t *testing.T, a *Adapter) []byte {
	t.Helper()
	b, err := os.ReadFile(a.anchorEventsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	return b
}

// TestAnchorEventsMissingFileReadsEmpty: the store is never scaffolded, and its
// absence is the empty log — so a Ledger that has never recorded a kind has no
// store and reads exactly as it always has.
func TestAnchorEventsMissingFileReadsEmpty(t *testing.T) {
	t.Run("scaffold never creates the store", func(t *testing.T) {
		a := newEngineAdapter(t)
		_, err := os.Stat(a.anchorEventsPath())
		require.ErrorIs(t, err, fs.ErrNotExist, "init writes no event store; the first event write does")

		log, err := a.ReadAnchorEvents()
		require.NoError(t, err)
		assert.Empty(t, log.Events)
		assert.Empty(t, log.PairIntents)
		assert.Equal(t, 0, log.Version, "a tolerant read reports what is there; it never repairs the envelope")

		_, err = os.Stat(a.anchorEventsPath())
		require.ErrorIs(t, err, fs.ErrNotExist, "and the read creates nothing — a read that writes is not a read")
	})

	t.Run("an unscaffolded home reads as the empty log", func(t *testing.T) {
		a := New(filepath.Join(t.TempDir(), ".lucid"))
		log, err := a.ReadAnchorEvents()
		require.NoError(t, err)
		assert.Empty(t, log.Events)
	})

	t.Run("a re-scaffold leaves an existing store byte-unchanged", func(t *testing.T) {
		a := newEngineAdapter(t)
		require.NoError(t, a.AppendAnchorEvent(syntheticKindEvent(
			"anchorev_2026_03_02_a", "anchor_2026_03_01_a", "2026-03-02", engine.AnchorKindQuit, 0, 0,
		)))
		before := readStoreBytes(t, a)

		require.NoError(t, a.ScaffoldEngine())
		assert.Equal(t, string(before), string(readStoreBytes(t, a)))
	})
}

// TestAnchorEventsAppendAtomicAndVersioned: every write is one read, one
// atomic write and a version stamp; events and pair intents are append-only;
// a reserved or recorded id is never reused; and a refused write leaves the
// store byte-unchanged.
func TestAnchorEventsAppendAtomicAndVersioned(t *testing.T) {
	quit := syntheticKindEvent("anchorev_2026_03_02_a", "anchor_2026_03_01_a", "2026-03-02", engine.AnchorKindQuit, 0, 0)
	slip := syntheticAnchorEvent("anchorev_2026_03_09_a", "anchor_2026_03_01_a", engine.AnchorEventSlip, "2026-03-08")
	limit := syntheticKindEvent("anchorev_2026_03_02_b", "anchor_2026_03_01_b", "2026-03-02", engine.AnchorKindLimit, 4, 90)

	t.Run("the first write creates the versioned envelope", func(t *testing.T) {
		a := newEngineAdapter(t)
		require.NoError(t, a.AppendAnchorEvent(quit))

		var envelope map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(readStoreBytes(t, a), &envelope))
		assert.JSONEq(t, "1", string(envelope["version"]))
		assert.Contains(t, envelope, "events")
		assert.NotContains(t, envelope, "pair_intents", "pair_intents is absent until the first intent")

		log, err := a.ReadAnchorEvents()
		require.NoError(t, err)
		assert.Equal(t, engine.AnchorEventsVersion, log.Version)
		assert.Equal(t, []engine.AnchorEvent{quit}, log.Events)

		info, err := os.Stat(a.anchorEventsPath())
		require.NoError(t, err)
		assert.Equal(t, filePerm, info.Mode().Perm(), "the store is owner-only")
		assert.Empty(t, stagedTempFiles(t, a.engineDir()), "an atomic write stages nothing that outlives it")
	})

	t.Run("appends keep the history in order", func(t *testing.T) {
		a := newEngineAdapter(t)
		require.NoError(t, a.AppendAnchorEvent(quit))
		require.NoError(t, a.AppendAnchorEvents(limit, slip), "a batch lands in one write")

		log, err := a.ReadAnchorEvents()
		require.NoError(t, err)
		assert.Equal(t, []engine.AnchorEvent{quit, limit, slip}, log.Events)
	})

	t.Run("an empty append writes nothing", func(t *testing.T) {
		a := newEngineAdapter(t)
		require.NoError(t, a.AppendAnchorEvents())
		assert.Nil(t, readStoreBytes(t, a), "no events means no store is created")

		require.NoError(t, a.AppendAnchorEvent(quit))
		before := readStoreBytes(t, a)
		require.NoError(t, a.AppendAnchorEvents())
		assert.Equal(t, string(before), string(readStoreBytes(t, a)))
	})

	t.Run("every write stamps the current version", func(t *testing.T) {
		a := newEngineAdapter(t)
		require.NoError(t, os.WriteFile(a.anchorEventsPath(), []byte("{\n  \"version\": 0,\n  \"events\": []\n}\n"), filePerm))

		require.NoError(t, a.AppendAnchorEvent(quit))
		log, err := a.ReadAnchorEvents()
		require.NoError(t, err)
		assert.Equal(t, engine.AnchorEventsVersion, log.Version)
	})

	t.Run("a failed write leaves the store byte-unchanged", func(t *testing.T) {
		skipIfRoot(t)
		a := newEngineAdapter(t)
		require.NoError(t, a.AppendAnchorEvent(quit))
		before := readStoreBytes(t, a)

		// A read-only engine dir fails the temp-file staging while the store
		// itself stays readable, so the failure is the write, not the read.
		require.NoError(t, os.Chmod(a.engineDir(), 0o500))
		t.Cleanup(func() { _ = os.Chmod(a.engineDir(), 0o700) })

		require.Error(t, a.AppendAnchorEvents(limit, slip))
		require.Error(t, a.AppendAnchorPairIntent(syntheticAddKindIntent("anchorev_2026_03_02_c", "anchor_2026_03_02_a", "morning-walk")))
		assert.Equal(t, string(before), string(readStoreBytes(t, a)), "neither event of the batch landed")
	})

	t.Run("an unscaffolded tree is refused, not created", func(t *testing.T) {
		a := New(filepath.Join(t.TempDir(), ".lucid"))
		require.Error(t, a.AppendAnchorEvent(quit))
		_, err := os.Stat(a.engineDir())
		require.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("pair intents are append-only and leave the events untouched", func(t *testing.T) {
		a := newEngineAdapter(t)
		require.NoError(t, a.AppendAnchorEvent(quit))
		intent := syntheticAddKindIntent("anchorev_2026_03_02_c", "anchor_2026_03_02_a", "morning-walk")
		require.NoError(t, a.AppendAnchorPairIntent(intent))

		log, err := a.ReadAnchorEvents()
		require.NoError(t, err)
		assert.Equal(t, []engine.AnchorEvent{quit}, log.Events, "an intent is metadata, never an applied event")
		assert.Equal(t, []engine.AnchorPairIntent{intent}, log.PairIntents)
		assert.Equal(t, engine.AnchorEventsVersion, log.Version)

		// The event half is the intent's exact planned event, under its reserved id.
		require.NoError(t, a.AppendAnchorEvent(intent.Event))
		second := syntheticAddKindIntent("anchorev_2026_03_02_d", "anchor_2026_03_02_b", "evening-walk")
		require.NoError(t, a.AppendAnchorPairIntent(second))

		log, err = a.ReadAnchorEvents()
		require.NoError(t, err)
		assert.Equal(t, []engine.AnchorEvent{quit, intent.Event}, log.Events)
		assert.Equal(t, []engine.AnchorPairIntent{intent, second}, log.PairIntents, "intents keep their append order")
	})

	t.Run("an intent alone still writes the events envelope", func(t *testing.T) {
		a := newEngineAdapter(t)
		require.NoError(t, a.AppendAnchorPairIntent(syntheticAddKindIntent("anchorev_2026_03_02_a", "anchor_2026_03_02_a", "morning-walk")))

		var envelope map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(readStoreBytes(t, a), &envelope))
		assert.JSONEq(t, "[]", string(envelope["events"]), "events reads [] — never null")
		assert.Contains(t, envelope, "pair_intents")
	})

	t.Run("an exact intent replay is a no-op", func(t *testing.T) {
		a := newEngineAdapter(t)
		intent := syntheticAddKindIntent("anchorev_2026_03_02_a", "anchor_2026_03_02_a", "morning-walk")
		require.NoError(t, a.AppendAnchorPairIntent(intent))
		before := readStoreBytes(t, a)

		require.NoError(t, a.AppendAnchorPairIntent(intent), "a retry re-presenting its intent is not an error")
		assert.Equal(t, string(before), string(readStoreBytes(t, a)), "and it writes nothing — the reservation already stands")
	})

	t.Run("a reserved or recorded id is never reused", func(t *testing.T) {
		a := newEngineAdapter(t)
		intent := syntheticAddKindIntent("anchorev_2026_03_02_a", "anchor_2026_03_02_a", "morning-walk")
		require.NoError(t, a.AppendAnchorPairIntent(intent))
		require.NoError(t, a.AppendAnchorEvent(slip))
		before := readStoreBytes(t, a)

		rival := syntheticAddKindIntent("anchorev_2026_03_02_a", "anchor_2026_03_02_b", "evening-walk")
		require.Error(t, a.AppendAnchorPairIntent(rival), "a different intent under a reserved id is refused, never an overwrite")

		impostor := intent.Event
		impostor.Kind = engine.AnchorKindQuit
		require.Error(t, a.AppendAnchorEvent(impostor), "a reserved id takes only its intent's exact planned event")

		require.Error(t, a.AppendAnchorEvent(slip), "an event id is a receipt and is never reissued")
		require.Error(t, a.AppendAnchorEvents(quit, quit), "nor twice in one batch")

		claimsRecorded := syntheticAddKindIntent(slip.ID, "anchor_2026_03_02_b", "evening-walk")
		require.Error(t, a.AppendAnchorPairIntent(claimsRecorded), "an intent reserves an unused id")

		assert.Equal(t, string(before), string(readStoreBytes(t, a)), "every refusal leaves the store byte-unchanged")

		log, err := a.ReadAnchorEvents()
		require.NoError(t, err)
		next := engine.NextAnchorEventID(log, time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC))
		assert.Equal(t, "anchorev_2026_03_02_b", next, "a reserved id is never minted again")
	})

	t.Run("a malformed event or intent is refused before any write", func(t *testing.T) {
		a := newEngineAdapter(t)
		bad := quit
		bad.Type = "relapse"
		require.Error(t, a.AppendAnchorEvent(bad), "the fold would skip it, so it is never written")

		badIntent := syntheticAddKindIntent("anchorev_2026_03_02_a", "anchor_2026_03_02_a", "morning-walk")
		badIntent.Order = "whenever"
		require.Error(t, a.AppendAnchorPairIntent(badIntent))

		assert.Nil(t, readStoreBytes(t, a), "a refusal on a fresh Ledger creates no store")
	})
}

// TestAnchorEventsReadUngated: the version is a signal, not a gate. An
// envelope at any version — or with none — reads, its events fold, and fields
// this binary does not know are tolerated.
func TestAnchorEventsReadUngated(t *testing.T) {
	const event = `{"id": "anchorev_2026_03_02_a", "anchor_id": "anchor_2026_03_01_a",
		"type": "kind", "day": "2026-03-02", "recorded_at": "2026-03-02T21:10:00-05:00",
		"kind": "limit", "allowance": 4, "window_days": 90, "added_later": "tolerated"}`
	cases := []struct {
		name     string
		envelope string
		version  int
	}{
		{"version 0", `{"version": 0, "events": [` + event + `]}`, 0},
		{"version 9", `{"version": 9, "events": [` + event + `], "future_section": {"x": 1}}`, 9},
		{"no version", `{"events": [` + event + `]}`, 0},
	}
	anchors := engine.AnchorLog{Version: engine.AnchorVersion, History: []engine.Anchor{
		{ID: "anchor_2026_03_01_a", Label: "late-night-snacks", Date: "2026-03-01", RecordedAt: "2026-03-01T20:00:00-05:00"},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newEngineAdapter(t)
			require.NoError(t, os.WriteFile(a.anchorEventsPath(), []byte(tc.envelope), filePerm))

			log, err := a.ReadAnchorEvents()
			require.NoError(t, err)
			assert.Equal(t, tc.version, log.Version, "the version is read as written, never checked")
			require.Len(t, log.Events, 1)

			facts := engine.FoldAnchorEvents(log, anchors)["anchor_2026_03_01_a"]
			assert.Equal(t, engine.AnchorKindLimit, facts.Kind)
			assert.Equal(t, 4, facts.Allowance)
			assert.Equal(t, 90, facts.WindowDays)
		})
	}
}

// TestAnchorEventsMalformedRecordNoPanic: a record that decodes but is not well
// formed passes through the read untouched — the fold skips it, validate
// reports it, and the next append keeps it — while a store that does not
// decode is a loud error naming the file. Nothing panics either way.
func TestAnchorEventsMalformedRecordNoPanic(t *testing.T) {
	t.Run("per-record defects pass through to the fold", func(t *testing.T) {
		const store = `{"version": 1, "events": [
			{"id": "anchorev_2026_03_02_a", "anchor_id": "anchor_2026_03_01_a", "type": "kind",
			 "day": "2026-03-02", "recorded_at": "2026-03-02T21:10:00-05:00", "kind": "quit"},
			{"id": "anchorev_2026_03_03_a", "anchor_id": "anchor_2026_03_01_a", "type": "relapse",
			 "day": "2026-03-03", "recorded_at": "2026-03-03T21:10:00-05:00"},
			{"id": "anchorev_2026_03_04_a", "anchor_id": "anchor_2026_03_01_a", "type": "slip",
			 "day": "the fourth", "recorded_at": "2026-03-04T21:10:00-05:00"},
			{"id": "anchorev_2026_03_05_a", "anchor_id": "anchor_2026_03_01_a", "type": "use",
			 "day": "2026-03-05", "recorded_at": "2026-03-05T21:10:00-05:00"},
			{"id": "anchorev_2026_03_06_a", "anchor_id": "anchor_2026_03_01_a", "type": "void",
			 "day": "2026-03-06", "recorded_at": "2026-03-06T21:10:00-05:00", "target": "anchorev_2026_03_02_a"},
			{"id": "event-seven", "anchor_id": "anchor_2026_03_01_a", "type": "slip",
			 "day": "2026-03-07", "recorded_at": "2026-03-07T21:10:00-05:00"},
			{"id": "anchorev_2026_03_08_a", "anchor_id": "legacy:sobriety", "type": "slip",
			 "day": "2026-03-08", "recorded_at": "2026-03-08T21:10:00-05:00"}
		], "pair_intents": [
			{"order": "eventually", "event": {"id": "anchorev_2026_03_09_a"}, "compat": {"label": ""}}
		]}`
		a := newEngineAdapter(t)
		require.NoError(t, os.WriteFile(a.anchorEventsPath(), []byte(store), filePerm))

		log, err := a.ReadAnchorEvents()
		require.NoError(t, err, "a defective record is the fold's and validate's concern, not a read error")
		require.Len(t, log.Events, 7, "every record passes through, defects included")
		require.Len(t, log.PairIntents, 1)

		anchors := engine.AnchorLog{Version: engine.AnchorVersion, History: []engine.Anchor{
			{ID: "anchor_2026_03_01_a", Label: "sobriety", Date: "2026-03-01", RecordedAt: "2026-03-01T20:00:00-05:00"},
		}}
		var facts map[string]engine.AnchorFacts
		require.NotPanics(t, func() { facts = engine.FoldAnchorEvents(log, anchors) })
		got := facts["anchor_2026_03_01_a"]
		assert.Equal(t, engine.AnchorKindQuit, got.Kind, "a void aimed at a kind event is ignored")
		assert.Empty(t, got.Slips, "the malformed slips are skipped, never counted")
		assert.Empty(t, got.Uses, "a use without an intent is skipped")
		assert.Empty(t, got.Voids)
		assert.Empty(t, got.Pending, "a malformed intent pends nothing")

		var states []engine.AnchorPairState
		require.NotPanics(t, func() { states = engine.AnchorPairStates(log, anchors) })
		require.Len(t, states, 1)
		assert.Equal(t, engine.AnchorPairMalformed, states[0].Status)

		// The next append keeps every defective record for validate to report.
		require.NoError(t, a.AppendAnchorEvent(syntheticAnchorEvent(
			"anchorev_2026_03_10_a", "anchor_2026_03_01_a", engine.AnchorEventSlip, "2026-03-10",
		)))
		log, err = a.ReadAnchorEvents()
		require.NoError(t, err)
		assert.Len(t, log.Events, 8, "an append never erases what the fold skips")
		assert.Len(t, log.PairIntents, 1)
	})

	t.Run("an undecodable store is a loud error naming the file", func(t *testing.T) {
		cases := map[string]string{
			"truncated":          `{"version": 1, "events": [{"id": "anchorev_2026_03_02_a", "anchor`,
			"garbage":            `not json at all`,
			"empty file":         ``,
			"events not a list":  `{"version": 1, "events": {"id": "anchorev_2026_03_02_a"}}`,
			"wrong-typed field":  `{"version": 1, "events": [{"id": "anchorev_2026_03_02_a", "allowance": "four"}]}`,
			"version not number": `{"version": "one", "events": []}`,
		}
		for name, body := range cases {
			t.Run(name, func(t *testing.T) {
				a := newEngineAdapter(t)
				require.NoError(t, os.WriteFile(a.anchorEventsPath(), []byte(body), filePerm))

				var err error
				require.NotPanics(t, func() { _, err = a.ReadAnchorEvents() })
				require.Error(t, err)
				assert.Contains(t, err.Error(), "anchor_events.json", "the error names the file")

				// A corrupt store is never buried under an append.
				require.Error(t, a.AppendAnchorEvent(syntheticAnchorEvent(
					"anchorev_2026_03_10_a", "anchor_2026_03_01_a", engine.AnchorEventSlip, "2026-03-10",
				)))
				require.Error(t, a.AppendAnchorPairIntent(syntheticAddKindIntent(
					"anchorev_2026_03_02_b", "anchor_2026_03_02_a", "morning-walk",
				)))
				assert.Equal(t, body, string(readStoreBytes(t, a)), "the corrupt bytes are left exactly as found")
			})
		}
	})
}

// oldBinaryAnchor is an anchors.json record exactly as a binary built before
// anchor kinds decodes it: the v3 shape and nothing else. It is declared here
// rather than borrowed from engine.Anchor so the simulation stays an older
// binary even if the current record ever grows a field.
type oldBinaryAnchor struct {
	ID         string `json:"id,omitempty"`
	Label      string `json:"label"`
	Date       string `json:"date"`
	Note       string `json:"note,omitempty"`
	RecordedAt string `json:"recorded_at"`
	State      string `json:"state,omitempty"`
	Adopts     string `json:"adopts,omitempty"`
	AdoptedAt  string `json:"adopted_at,omitempty"`
}

// oldBinaryAnchorLog is anchors.json's envelope as an older binary knows it.
type oldBinaryAnchorLog struct {
	Version int               `json:"version"`
	History []oldBinaryAnchor `json:"history"`
}

// oldBinaryAppendAnchors performs an older binary's anchor write: decode
// anchors.json into the v3 shape, append, stamp version 3 and re-marshal the
// whole file. It touches anchors.json and nothing else — an older binary does
// not know the event store exists.
func oldBinaryAppendAnchors(t *testing.T, home string, recs ...oldBinaryAnchor) {
	t.Helper()
	path := filepath.Join(home, engineDirName, anchorsFile)
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var log oldBinaryAnchorLog
	require.NoError(t, json.Unmarshal(b, &log))
	log.Version = 3
	log.History = append(log.History, recs...)
	out, err := json.MarshalIndent(log, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(out, '\n'), filePerm))
}

// countFacts is every event a fold accounted for: each fact list, plus the
// voided events (which a fold records as voided instead of filing).
func countFacts(facts map[string]engine.AnchorFacts) int {
	n := 0
	for _, f := range facts {
		n += len(f.KindHistory) + len(f.Slips) + len(f.Uses) + len(f.Misses) +
			len(f.Confirms) + len(f.Graduates) + len(f.Voids) + len(f.Voided)
	}
	return n
}

// TestHonestyOldBinaryWriteKeepsKindsAndEvents: an older binary's anchor write
// never loses a kind or an event. It decodes anchors.json into the v3 shape,
// appends, and re-marshals that file only — so the event store, holding every
// kind, fact and pair intent, comes through byte-identical, and the new
// binary's fold reads every anchor exactly as it did before the write.
func TestHonestyOldBinaryWriteKeepsKindsAndEvents(t *testing.T) {
	a := newEngineAdapter(t)
	const (
		gate    = "anchor_2026_01_01_a"
		sober   = "anchor_2026_01_01_b"
		snacks  = "anchor_2026_01_01_c"
		walk    = "anchor_2026_01_01_d"
		stretch = "anchor_2026_01_01_e"
	)
	require.NoError(t, a.AppendAnchors(
		engine.Anchor{ID: gate, Label: "first-gate", Date: "2026-03-01", RecordedAt: "2026-01-01T09:00:00-05:00"},
		engine.Anchor{ID: sober, Label: "sobriety", Date: "2026-01-01", RecordedAt: "2026-01-01T09:05:00-05:00"},
		engine.Anchor{ID: snacks, Label: "late-night-snacks", Date: "2026-01-01", RecordedAt: "2026-01-01T09:10:00-05:00"},
		engine.Anchor{ID: walk, Label: "morning-walk", Date: "2026-01-01", RecordedAt: "2026-01-01T09:15:00-05:00"},
		engine.Anchor{ID: stretch, Label: "evening-stretch", Date: "2026-01-01", RecordedAt: "2026-01-01T09:20:00-05:00"},
	))

	planned := syntheticAnchorEvent("anchorev_2026_02_14_a", snacks, engine.AnchorEventUse, "2026-02-14")
	planned.Intent, planned.Note = engine.AnchorIntentPlanned, "a slice of cake at a birthday"
	slipUse := syntheticAnchorEvent("anchorev_2026_02_20_a", snacks, engine.AnchorEventUse, "2026-02-20")
	slipUse.Intent = engine.AnchorIntentSlip
	voidUse := syntheticAnchorEvent("anchorev_2026_02_21_a", snacks, engine.AnchorEventVoid, "2026-02-21")
	voidUse.Target, voidUse.Note = slipUse.ID, "misheard"
	miss := syntheticAnchorEvent("anchorev_2026_02_10_a", walk, engine.AnchorEventMiss, "2026-02-10")
	miss.Note = "at a friend's place"
	confirm := syntheticAnchorEvent("anchorev_2026_02_15_a", walk, engine.AnchorEventConfirm, "2026-02-14")
	confirm.From = "2026-01-02"
	require.NoError(t, a.AppendAnchorEvents(
		syntheticKindEvent("anchorev_2026_01_02_a", sober, "2026-01-02", engine.AnchorKindQuit, 0, 0),
		syntheticKindEvent("anchorev_2026_01_02_b", snacks, "2026-01-02", engine.AnchorKindLimit, 4, 90),
		syntheticKindEvent("anchorev_2026_01_02_c", walk, "2026-01-02", engine.AnchorKindHabit, 0, 0),
		syntheticKindEvent("anchorev_2026_01_02_d", stretch, "2026-01-02", engine.AnchorKindHabit, 0, 0),
		planned, slipUse, voidUse, miss, confirm,
	))

	// A completed slip pair (compat first): intent, v3 re-date, slip event.
	slipAt := "2026-03-02T08:15:00-05:00"
	slip := engine.AnchorEvent{ID: "anchorev_2026_03_02_a", AnchorID: sober, Type: engine.AnchorEventSlip, Day: "2026-03-01", RecordedAt: slipAt}
	slipRedate := engine.Anchor{ID: sober, Label: "sobriety", Date: "2026-03-01", RecordedAt: slipAt}
	require.NoError(t, a.AppendAnchorPairIntent(engine.AnchorPairIntent{Order: engine.AnchorPairCompatFirst, Event: slip, Compat: slipRedate}))
	require.NoError(t, a.AppendAnchor(slipRedate))
	require.NoError(t, a.AppendAnchorEvent(slip))

	// A completed graduation (event first): intent, graduate event, plain sunset.
	gradAt := "2026-04-01T20:00:00-04:00"
	graduate := engine.AnchorEvent{ID: "anchorev_2026_04_01_a", AnchorID: stretch, Type: engine.AnchorEventGraduate, Day: "2026-04-01", RecordedAt: gradAt}
	sunset := engine.Anchor{ID: stretch, Label: "evening-stretch", Date: "2026-01-01", RecordedAt: gradAt, State: engine.AnchorStateSunset}
	require.NoError(t, a.AppendAnchorPairIntent(engine.AnchorPairIntent{Order: engine.AnchorPairEventFirst, Event: graduate, Compat: sunset}))
	require.NoError(t, a.AppendAnchorEvent(graduate))
	require.NoError(t, a.AppendAnchor(sunset))

	// An interrupted slip (compat first): the re-date landed, the event did not.
	pendingAt := "2026-04-10T22:00:00-04:00"
	pending := engine.AnchorEvent{ID: "anchorev_2026_04_10_a", AnchorID: sober, Type: engine.AnchorEventSlip, Day: "2026-04-10", RecordedAt: pendingAt}
	pendingRedate := engine.Anchor{ID: sober, Label: "sobriety", Date: "2026-04-10", RecordedAt: pendingAt}
	require.NoError(t, a.AppendAnchorPairIntent(engine.AnchorPairIntent{Order: engine.AnchorPairCompatFirst, Event: pending, Compat: pendingRedate}))
	require.NoError(t, a.AppendAnchor(pendingRedate))

	read := func() (engine.AnchorEventLog, engine.AnchorLog) {
		t.Helper()
		events, err := a.ReadAnchorEvents()
		require.NoError(t, err)
		anchors, err := a.ReadAnchors()
		require.NoError(t, err)
		return events, anchors
	}
	events, anchors := read()
	before := engine.FoldAnchorEvents(events, anchors)
	beforeStates := engine.AnchorPairStates(events, anchors)

	// The seed really carries every kind, every fact type and both pair states.
	assert.Equal(t, engine.AnchorKindMilestone, before[gate].Kind)
	assert.Equal(t, engine.AnchorKindQuit, before[sober].Kind)
	assert.Equal(t, engine.AnchorKindLimit, before[snacks].Kind)
	assert.Equal(t, engine.AnchorKindHabit, before[walk].Kind)
	assert.True(t, before[stretch].Graduated)
	assert.Equal(t, "2026-04-10", before[sober].Floor, "the interrupted re-date is a pending floor")
	require.Len(t, before[sober].Pending, 1)
	assert.Equal(t, len(events.Events), countFacts(before), "every stored event is folded")
	storeBefore := readStoreBytes(t, a)

	// The older binary: an anchor add, then a rename of the quit that keeps the
	// date its own fold shows (today's LatestAnchors path).
	var oldDate string
	for _, an := range engine.LatestAnchors(anchors) {
		if an.ID == sober {
			oldDate = an.Date
		}
	}
	require.Equal(t, "2026-04-10", oldDate, "an older binary counts from the latest re-date — never a longer run")
	oldBinaryAppendAnchors(t, a.Home(),
		oldBinaryAnchor{ID: "anchor_2026_04_12_a", Label: "second-gate", Date: "2026-06-01", RecordedAt: "2026-04-12T10:00:00-04:00"},
		oldBinaryAnchor{ID: sober, Label: "smoke-free", Date: oldDate, RecordedAt: "2026-04-12T10:05:00-04:00"},
	)

	assert.Equal(t, string(storeBefore), string(readStoreBytes(t, a)), "an older binary never touches the event store")

	events, anchors = read()
	after := engine.FoldAnchorEvents(events, anchors)
	require.Len(t, after, len(before)+1)
	assert.Equal(t, engine.AnchorKindMilestone, after["anchor_2026_04_12_a"].Kind, "the older binary's new anchor is a milestone")

	renamed := after[sober]
	assert.Equal(t, "smoke-free", renamed.Anchor.Label)
	renamed.Anchor = before[sober].Anchor
	after[sober] = renamed
	for id, want := range before {
		assert.Equalf(t, want, after[id], "anchor %s folds exactly as it did before the older binary's write", id)
	}

	afterStates := engine.AnchorPairStates(events, anchors)
	require.Len(t, afterStates, len(beforeStates))
	for i := range beforeStates {
		assert.Equal(t, beforeStates[i].Status, afterStates[i].Status, "pair %s keeps its state", beforeStates[i].Intent.Event.ID)
	}
	assert.Equal(t, len(events.Events), countFacts(after), "and every event is still folded")
}
