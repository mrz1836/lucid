package validate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/storage"
)

// Synthetic anchor ids for the anchors-check fixtures.
const (
	anchorSobriety  = "anchor_2026_01_01_a"
	anchorWalk      = "anchor_2026_02_01_a"
	anchorSnacks    = "anchor_2026_05_01_a"
	anchorFirstGate = "anchor_2026_01_15_a"
)

// anchorDef is one anchors.json definition record.
func anchorDef(id, label, date, at string) engine.Anchor {
	return engine.Anchor{ID: id, Label: label, Date: date, RecordedAt: at}
}

// anchorLog wraps records in a current-version anchors.json.
func anchorLog(recs ...engine.Anchor) *engine.AnchorLog {
	return &engine.AnchorLog{Version: engine.AnchorVersion, History: recs}
}

// anchorEvent is one well-formed event of type typ on day, stamped that
// evening.
func anchorEvent(id, anchorID, typ, day string) engine.AnchorEvent {
	return engine.AnchorEvent{ID: id, AnchorID: anchorID, Type: typ, Day: day, RecordedAt: day + "T21:00:00Z"}
}

// kindEvent is a kind event carrying kind and its parameters.
func kindEvent(id, anchorID, day, kind string, allowance, window int) engine.AnchorEvent {
	e := anchorEvent(id, anchorID, engine.AnchorEventKind, day)
	e.Kind, e.Allowance, e.WindowDays = kind, allowance, window
	return e
}

// voidEvent is a void of target.
func voidEvent(id, anchorID, day, target string) engine.AnchorEvent {
	e := anchorEvent(id, anchorID, engine.AnchorEventVoid, day)
	e.Target = target
	return e
}

// eventLog wraps events in a current-version anchor event store.
func eventLog(events ...engine.AnchorEvent) engine.AnchorEventLog {
	return engine.AnchorEventLog{Version: engine.AnchorEventsVersion, Events: events}
}

// withRules returns the findings that fired rule.
func withRules(fs []Finding, rule string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

// requireOnly asserts fs is exactly one finding, of rule, and returns it.
func requireOnly(t *testing.T, fs []Finding, rule string) Finding {
	t.Helper()
	require.Len(t, fs, 1, "findings: %+v", fs)
	assert.Equal(t, rule, fs[0].Rule)
	assert.Equal(t, CheckAnchors, fs[0].Check)
	return fs[0]
}

// diskLedger is a fakeLedger whose two anchors files are read from a real
// Ledger home through the storage adapter, so a hand-corrupted file on disk
// travels the same read path `lucid validate` takes.
type diskLedger struct {
	fakeLedger

	a *storage.Adapter
}

func (d *diskLedger) ReadAnchorsErr() (engine.AnchorLog, error) { return d.a.ReadAnchors() }

func (d *diskLedger) ReadAnchorEventsErr() (engine.AnchorEventLog, error) {
	return d.a.ReadAnchorEvents()
}

// newDiskLedger is a scaffolded engine tree under a temp home.
func newDiskLedger(t *testing.T) (*diskLedger, string) {
	t.Helper()
	home := t.TempDir()
	a := storage.New(home)
	require.NoError(t, a.ScaffoldEngine())
	return &diskLedger{a: a}, filepath.Join(home, "engine")
}

// TestValidateAnchorsSchema is the anchors.json schema check: today's v1, v2
// and v3 shapes — and a missing or freshly scaffolded file — validate with no
// finding, and each malformed record is one finding per defect.
func TestValidateAnchorsSchema(t *testing.T) {
	t.Run("today's v1, v2 and v3 shapes are clean", func(t *testing.T) {
		v1 := &engine.AnchorLog{Version: 1, History: []engine.Anchor{
			{Label: "first-gate", Date: "2026-01-05", RecordedAt: "2026-01-05T09:00:00Z"},
			{Label: "first-gate", Date: "2026-01-06", RecordedAt: "2026-01-06T09:00:00Z", Note: "corrected"},
		}}
		v2 := &engine.AnchorLog{Version: 2, History: []engine.Anchor{
			anchorDef(anchorSobriety, "sobriety", "2026-01-01", "2026-01-01T09:00:00Z"),
			{ID: anchorSobriety, Label: "sobriety", Date: "2026-01-01", RecordedAt: "2026-03-01T09:00:00Z", State: "sunset"},
		}}
		v3 := &engine.AnchorLog{Version: 3, History: []engine.Anchor{
			{Label: "morning-walk", Date: "2026-02-01", RecordedAt: "2026-02-01T09:00:00Z"},
			{
				ID: anchorWalk, Label: "morning-walk", Date: "2026-02-01", RecordedAt: "2026-02-01T09:00:00Z",
				Adopts: "legacy:morning-walk", AdoptedAt: "2026-04-01T09:00:00Z",
			},
			{ID: anchorWalk, Label: "evening-walk", Date: "2026-02-01", RecordedAt: "2026-04-02T09:00:00Z"},
		}}
		for name, log := range map[string]*engine.AnchorLog{"v1": v1, "v2": v2, "v3": v3} {
			assert.Empty(t, CheckAnchorFiles(&fakeLedger{anchors: log}), name)
		}
	})

	t.Run("a missing anchors.json is clean", func(t *testing.T) {
		assert.Empty(t, CheckAnchorFiles(&fakeLedger{}))
	})

	t.Run("a freshly scaffolded Ledger is clean", func(t *testing.T) {
		d, _ := newDiskLedger(t)
		assert.Empty(t, CheckAnchorFiles(d))
	})

	t.Run("each malformed record is one finding per defect", func(t *testing.T) {
		found := CheckAnchorFiles(&fakeLedger{anchors: anchorLog(
			anchorDef(anchorSobriety, "sobriety", "2026-01-01", "2026-01-01T09:00:00Z"),
			anchorDef(anchorWalk, "  ", "2026-02-01", "2026-02-01T09:00:00Z"),
			engine.Anchor{Label: "first-gate", Date: "2026-13-01", RecordedAt: "2026-01-05T09:00:00Z"},
			engine.Anchor{ID: anchorSnacks, Label: "", Date: "someday", RecordedAt: "x", State: "paused"},
		)})
		require.Len(t, found, 5, "findings: %+v", found)

		labels := withRules(found, ruleAnchorLabel)
		require.Len(t, labels, 2)
		assert.Contains(t, labels[0].Message, "history[1] ("+anchorWalk+")")
		assert.Contains(t, labels[1].Message, "history[3] ("+anchorSnacks+")")

		dates := withRules(found, ruleAnchorDate)
		require.Len(t, dates, 2)
		assert.Contains(t, dates[0].Message, `history[2] ("first-gate"): date "2026-13-01"`)
		assert.Contains(t, dates[1].Message, `date "someday"`)

		state := requireOnly(t, withRules(found, ruleAnchorState), ruleAnchorState)
		assert.Contains(t, state.Message, `state "paused"`)
		for _, f := range found {
			assert.Equal(t, anchorsFilePath, f.Path)
			assert.Equal(t, SeverityError, f.Severity)
		}
	})

	t.Run("a version below 1 is a finding", func(t *testing.T) {
		log := anchorLog(anchorDef(anchorSobriety, "sobriety", "2026-01-01", "2026-01-01T09:00:00Z"))
		log.Version = 0
		f := requireOnly(t, CheckAnchorFiles(&fakeLedger{anchors: log}), ruleAnchorsVersion)
		assert.Contains(t, f.Message, "version 0")
	})

	t.Run("an unreadable anchors.json is one finding naming the file", func(t *testing.T) {
		f := requireOnly(t, CheckAnchorFiles(&fakeLedger{anchorsErr: errSentinel}), ruleAnchorsRead)
		assert.Equal(t, anchorsFilePath, f.Path)
		assert.Contains(t, f.Message, "anchors.json could not be read")
	})
}

// TestValidateAnchorEventsSchema is the event-store schema check: every
// event type in its well-formed shape is clean, the store's version is never
// checked, and each malformed record — or a second record under an id already
// read — is one finding.
func TestValidateAnchorEventsSchema(t *testing.T) {
	anchors := anchorLog(
		anchorDef(anchorSobriety, "sobriety", "2026-01-01", "2026-01-01T09:00:00Z"),
		anchorDef(anchorSnacks, "late-night-snacks", "2026-01-01", "2026-01-01T09:00:00Z"),
		anchorDef(anchorWalk, "morning-walk", "2026-01-01", "2026-01-01T09:00:00Z"),
	)
	use := anchorEvent("anchorev_2026_02_01_a", anchorSnacks, engine.AnchorEventUse, "2026-02-01")
	use.Intent = engine.AnchorIntentPlanned
	confirm := anchorEvent("anchorev_2026_02_08_a", anchorWalk, engine.AnchorEventConfirm, "2026-02-07")
	confirm.From = "2026-02-01"
	wellFormed := []engine.AnchorEvent{
		kindEvent("anchorev_2026_01_01_a", anchorSobriety, "2026-01-01", engine.AnchorKindQuit, 0, 0),
		kindEvent("anchorev_2026_01_01_b", anchorSnacks, "2026-01-01", engine.AnchorKindLimit, 4, 90),
		kindEvent("anchorev_2026_01_01_c", anchorWalk, "2026-01-01", engine.AnchorKindHabit, 0, 0),
		anchorEvent("anchorev_2026_01_20_a", anchorSobriety, engine.AnchorEventSlip, "2026-01-20"),
		use,
		anchorEvent("anchorev_2026_02_03_a", anchorWalk, engine.AnchorEventMiss, "2026-02-03"),
		confirm,
		voidEvent("anchorev_2026_02_09_a", anchorWalk, "2026-02-09", "anchorev_2026_02_03_a"),
	}

	t.Run("every well-formed event type is clean, at any store version", func(t *testing.T) {
		for _, version := range []int{0, engine.AnchorEventsVersion, 9} {
			log := eventLog(wellFormed...)
			log.Version = version
			assert.Empty(t, CheckAnchorFiles(&fakeLedger{anchors: anchors, events: log}), "version %d", version)
		}
	})

	t.Run("a missing store is clean", func(t *testing.T) {
		assert.Empty(t, CheckAnchorFiles(&fakeLedger{anchors: anchors}))
	})

	t.Run("each malformed record is one finding", func(t *testing.T) {
		badID := anchorEvent("anchorev_2026_02_30_a", anchorSobriety, engine.AnchorEventSlip, "2026-02-03")
		legacyTarget := anchorEvent("anchorev_2026_02_04_a", "legacy:sobriety", engine.AnchorEventSlip, "2026-02-04")
		unknownType := anchorEvent("anchorev_2026_02_05_a", anchorSobriety, "relapse", "2026-02-05")
		badDay := anchorEvent("anchorev_2026_02_06_a", anchorSobriety, engine.AnchorEventSlip, "Feb 6")
		badStamp := anchorEvent("anchorev_2026_02_07_a", anchorSobriety, engine.AnchorEventSlip, "2026-02-07")
		badStamp.RecordedAt = "last night"
		noIntent := anchorEvent("anchorev_2026_02_08_b", anchorSnacks, engine.AnchorEventUse, "2026-02-08")
		noFrom := anchorEvent("anchorev_2026_02_09_b", anchorWalk, engine.AnchorEventConfirm, "2026-02-09")
		badVoid := voidEvent("anchorev_2026_02_10_a", anchorWalk, "2026-02-10", "anchor_2026_02_03_a")
		badParams := kindEvent("anchorev_2026_02_11_a", anchorSnacks, "2026-02-11", engine.AnchorKindLimit, 4, 0)
		unknownKind := kindEvent("anchorev_2026_02_12_a", anchorSnacks, "2026-02-12", "sometimes", 0, 0)
		bad := []engine.AnchorEvent{
			badID, legacyTarget, unknownType, badDay, badStamp, noIntent, noFrom, badVoid, badParams, unknownKind,
		}

		found := CheckAnchorFiles(&fakeLedger{anchors: anchors, events: eventLog(append(wellFormed, bad...)...)})
		require.Len(t, found, len(bad), "findings: %+v", found)
		wantFragments := []string{
			`"anchorev_2026_02_30_a" is not anchorev_YYYY_MM_DD_<slot>`,
			`targets "legacy:sobriety", not a minted anchor_ id`,
			`unknown type "relapse"`,
			`day "Feb 6"`,
			`recorded_at "last night"`,
			`intent ""`,
			`is a confirm whose from ""`,
			`void with target "anchor_2026_02_03_a"`,
			"a limit needs both --allowance N and --window D",
			`"sometimes" is not an anchor kind`,
		}
		for i, f := range found {
			assert.Equal(t, ruleAnchorEvent, f.Rule)
			assert.Equal(t, anchorEventsFilePath, f.Path)
			assert.Equal(t, SeverityError, f.Severity)
			assert.Contains(t, f.Message, wantFragments[i])
			assert.NotContains(t, f.Message, "engine: ", "engine package prefixes are dropped from the copy")
		}
		assert.True(t, strings.HasPrefix(found[0].Message, "events[8]: "), found[0].Message)
	})

	t.Run("a second record under an id already read is a duplicate", func(t *testing.T) {
		again := anchorEvent("anchorev_2026_01_20_a", anchorSobriety, engine.AnchorEventSlip, "2026-01-25")
		found := CheckAnchorFiles(&fakeLedger{anchors: anchors, events: eventLog(append(wellFormed, again)...)})
		f := requireOnly(t, found, ruleAnchorEventDuplicate)
		assert.Contains(t, f.Message, "events[8]: anchorev_2026_01_20_a reuses an event id already recorded")
	})

	t.Run("an unreadable store is one finding naming the file", func(t *testing.T) {
		f := requireOnly(t, CheckAnchorFiles(&fakeLedger{anchors: anchors, eventsErr: errSentinel}), ruleAnchorEventsRead)
		assert.Equal(t, anchorEventsFilePath, f.Path)
		assert.Contains(t, f.Message, "anchor_events.json could not be read")
	})
}

// TestValidateAnchorCrossFile covers the joins: an event whose anchor no
// anchors.json record holds, every illegal void target, kind parameters, and
// the rule that a file that cannot be read skips the joins instead of
// reporting every consequence of one defect.
func TestValidateAnchorCrossFile(t *testing.T) {
	anchors := anchorLog(
		anchorDef(anchorSobriety, "sobriety", "2026-01-01", "2026-01-01T09:00:00Z"),
		anchorDef(anchorWalk, "morning-walk", "2026-01-01", "2026-01-01T09:00:00Z"),
	)
	quit := kindEvent("anchorev_2026_01_01_a", anchorSobriety, "2026-01-01", engine.AnchorKindQuit, 0, 0)
	habit := kindEvent("anchorev_2026_01_01_b", anchorWalk, "2026-01-01", engine.AnchorKindHabit, 0, 0)
	slip := anchorEvent("anchorev_2026_02_01_a", anchorSobriety, engine.AnchorEventSlip, "2026-02-01")
	miss := anchorEvent("anchorev_2026_02_02_a", anchorWalk, engine.AnchorEventMiss, "2026-02-02")

	t.Run("an event targeting no recorded anchor", func(t *testing.T) {
		stray := anchorEvent("anchorev_2026_02_03_a", "anchor_2026_09_09_a", engine.AnchorEventSlip, "2026-02-03")
		found := CheckAnchorFiles(&fakeLedger{anchors: anchors, events: eventLog(quit, habit, slip, stray)})
		f := requireOnly(t, found, ruleAnchorEventTarget)
		assert.Contains(t, f.Message, "anchorev_2026_02_03_a targets anchor_2026_09_09_a, which no anchor in anchors.json holds")
	})

	t.Run("an event with no anchors.json at all targets nothing", func(t *testing.T) {
		found := CheckAnchorFiles(&fakeLedger{events: eventLog(quit)})
		requireOnly(t, found, ruleAnchorEventTarget)
	})

	t.Run("invalid kind parameters", func(t *testing.T) {
		limit := kindEvent("anchorev_2026_02_04_a", anchorSobriety, "2026-02-04", engine.AnchorKindLimit, 0, 90)
		windowed := kindEvent("anchorev_2026_02_04_b", anchorSobriety, "2026-02-04", engine.AnchorKindQuit, 0, 30)
		found := CheckAnchorFiles(&fakeLedger{anchors: anchors, events: eventLog(quit, limit, windowed)})
		require.Len(t, found, 2, "findings: %+v", found)
		assert.Contains(t, found[0].Message, "a limit needs both --allowance N and --window D")
		assert.Contains(t, found[1].Message, "only a limit or a habit takes it")
	})

	t.Run("a legal void is clean", func(t *testing.T) {
		v := voidEvent("anchorev_2026_02_05_a", anchorWalk, "2026-02-05", miss.ID)
		assert.Empty(t, CheckAnchorFiles(&fakeLedger{anchors: anchors, events: eventLog(quit, habit, miss, v)}))
	})

	t.Run("every illegal void target", func(t *testing.T) {
		malformed := anchorEvent("anchorev_2026_02_06_a", anchorWalk, engine.AnchorEventMiss, "not-a-day")
		voids := []engine.AnchorEvent{
			voidEvent("anchorev_2026_03_01_a", anchorWalk, "2026-03-01", "anchorev_2026_02_28_z"),
			voidEvent("anchorev_2026_03_01_b", anchorWalk, "2026-03-01", malformed.ID),
			voidEvent("anchorev_2026_03_01_c", anchorWalk, "2026-03-01", slip.ID),
			voidEvent("anchorev_2026_03_01_d", anchorWalk, "2026-03-01", habit.ID),
			voidEvent("anchorev_2026_03_01_e", anchorWalk, "2026-03-01", miss.ID),
			voidEvent("anchorev_2026_03_01_f", anchorWalk, "2026-03-01", miss.ID),
			voidEvent("anchorev_2026_03_01_g", anchorWalk, "2026-03-01", "anchorev_2026_03_01_e"),
		}
		events := append([]engine.AnchorEvent{quit, habit, slip, miss, malformed}, voids...)
		found := withRules(CheckAnchorFiles(&fakeLedger{anchors: anchors, events: eventLog(events...)}), ruleAnchorVoidTarget)
		require.Len(t, found, 6, "the first void of the miss is legal; every other void is a finding: %+v", found)
		wantFragments := []string{
			"anchorev_2026_03_01_a: voids anchorev_2026_02_28_z, which no recorded event holds",
			"anchorev_2026_03_01_b: voids anchorev_2026_02_06_a, a record that is not well formed",
			"anchorev_2026_03_01_c: voids anchorev_2026_02_01_a, an event of another anchor (" + anchorSobriety + ")",
			"anchorev_2026_03_01_d: voids the kind event anchorev_2026_01_01_b; a kind event is never voided",
			"anchorev_2026_03_01_f: voids anchorev_2026_02_02_a, which anchorev_2026_03_01_e already voided",
			"anchorev_2026_03_01_g: voids anchorev_2026_03_01_e, which is itself a void",
		}
		for i, f := range found {
			assert.Contains(t, f.Message, wantFragments[i])
			assert.Contains(t, f.Message, "the fold ignores it")
		}
	})

	t.Run("an unreadable anchors.json skips the joins", func(t *testing.T) {
		stray := anchorEvent("anchorev_2026_02_03_a", "anchor_2026_09_09_a", engine.AnchorEventSlip, "2026-02-03")
		found := CheckAnchorFiles(&fakeLedger{anchorsErr: errSentinel, events: eventLog(quit, stray)})
		requireOnly(t, found, ruleAnchorsRead)
	})

	t.Run("an unreadable store skips every event check", func(t *testing.T) {
		found := CheckAnchorFiles(&fakeLedger{anchors: anchors, eventsErr: errors.New("storage: read anchor_events.json: denied")})
		requireOnly(t, found, ruleAnchorEventsRead)
	})
}

// TestValidateCorruptAnchorEventNoPanic hand-corrupts the two anchors files on
// disk and runs the whole sweep through the storage read path: every defect is
// reported as a finding, never a panic, and Run itself never fails.
func TestValidateCorruptAnchorEventNoPanic(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		content string
		rule    string
		message string
	}{
		{
			name: "truncated store", file: "anchor_events.json",
			content: `{"version": 1, "events": [{"id": "anchorev_2026_`,
			rule:    ruleAnchorEventsRead, message: "anchor_events.json is not valid JSON",
		},
		{
			name: "binary garbage", file: "anchor_events.json", content: "\x00\xff\xfe garbage",
			rule: ruleAnchorEventsRead, message: "anchor_events.json is not valid JSON",
		},
		{
			name: "events of the wrong type", file: "anchor_events.json",
			content: `{"version": 1, "events": "nope"}`,
			rule:    ruleAnchorEventsRead, message: "anchor_events.json does not have the file's shape",
		},
		{
			name: "fields of the wrong type", file: "anchor_events.json",
			content: `{"version": 1, "events": [{"id": 42, "type": ["slip"], "allowance": "four"}]}`,
			rule:    ruleAnchorEventsRead, message: "anchor_events.json does not have the file's shape",
		},
		{
			name: "a version of the wrong type", file: "anchor_events.json",
			content: `{"version": "?", "events": [{"id": "zzz", "type": "teleport", "day": "someday", ` +
				`"bogus": {"a": [1, 2]}}]}`,
			rule: ruleAnchorEventsRead, message: "anchor_events.json does not have the file's shape",
		},
		{
			name: "an unknown record and a malformed intent", file: "anchor_events.json",
			content: `{"version": 1, "events": [{"id": "zzz", "anchor_id": "", "type": "teleport", ` +
				`"day": "someday", "recorded_at": "never", "bogus": {"a": [1]}}], ` +
				`"pair_intents": [{"order": "sideways", "event": {}, "compat": {}}, {}]}`,
			rule: ruleAnchorEvent, message: `events[0]: anchor event id "zzz"`,
		},
		{
			name: "truncated anchors.json", file: "anchors.json",
			content: `{"version": 3, "history": [{"id": "anchor_2026_`,
			rule:    ruleAnchorsRead, message: "anchors.json is not valid JSON",
		},
		{
			name: "anchors.json history of the wrong type", file: "anchors.json",
			content: `{"version": 3, "history": {"label": "sobriety"}}`,
			rule:    ruleAnchorsRead, message: "anchors.json does not have the file's shape",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, engineDir := newDiskLedger(t)
			require.NoError(t, os.WriteFile(filepath.Join(engineDir, tc.file), []byte(tc.content), 0o600))

			var rep Report
			var err error
			require.NotPanics(t, func() { rep, err = Run(Options{Ledger: d}) })
			require.NoError(t, err)
			assert.Contains(t, rep.Ran, CheckAnchors)
			assert.False(t, rep.OK(), "a corrupt anchors file fails the sweep")

			matched := withRules(rep.Findings, tc.rule)
			require.NotEmpty(t, matched, "findings: %+v", rep.Findings)
			assert.Contains(t, matched[0].Message, tc.message)
		})
	}

	t.Run("a malformed pair intent is a finding, never a panic", func(t *testing.T) {
		d, engineDir := newDiskLedger(t)
		content := `{"version": 1, "events": [], "pair_intents": [{"order": "sideways", "event": {}, "compat": {}}, {}]}`
		require.NoError(t, os.WriteFile(filepath.Join(engineDir, "anchor_events.json"), []byte(content), 0o600))
		var found []Finding
		require.NotPanics(t, func() { found = CheckAnchorFiles(d) })
		malformed := withRules(found, ruleAnchorPairMalformed)
		require.Len(t, malformed, 2, "findings: %+v", found)
		assert.Contains(t, malformed[0].Message, `pair_intents[0]: pair intent  has unknown order "sideways"`)
		assert.Contains(t, malformed[1].Message, "pair_intents[1]: ")
	})
}

// pairCase is one two-file verb's pair: the clean store it starts from, the
// intent it reserves, what the half-written finding names at each prefix, and
// the retry that finishes it.
type pairCase struct {
	name          string
	anchors       []engine.Anchor
	events        []engine.AnchorEvent
	intent        engine.AnchorPairIntent
	missingAll    string
	missingSecond string
	retry         string
}

// with returns the case's two files with the given halves of its pair written.
func (c pairCase) with(compat, event bool) (*engine.AnchorLog, engine.AnchorEventLog) {
	history := append([]engine.Anchor(nil), c.anchors...)
	events := append([]engine.AnchorEvent(nil), c.events...)
	if compat {
		history = append(history, c.intent.Compat)
	}
	if event {
		events = append(events, c.intent.Event)
	}
	log := eventLog(events...)
	log.PairIntents = []engine.AnchorPairIntent{c.intent}
	return anchorLog(history...), log
}

// pairCases builds one case per two-file verb, on synthetic anchors.
func pairCases() []pairCase {
	const stamp = "2026-04-02T08:00:00Z"
	sobriety := anchorDef(anchorSobriety, "sobriety", "2026-01-01", "2026-01-01T09:00:00Z")
	redated := func(a engine.Anchor, date string) engine.Anchor {
		a.Date, a.RecordedAt = date, stamp
		return a
	}
	stamped := func(e engine.AnchorEvent) engine.AnchorEvent {
		e.RecordedAt = stamp
		return e
	}
	quit := kindEvent("anchorev_2026_01_01_a", anchorSobriety, "2026-01-01", engine.AnchorKindQuit, 0, 0)
	priorSlip := anchorEvent("anchorev_2026_03_01_a", anchorSobriety, engine.AnchorEventSlip, "2026-03-01")
	use := anchorEvent("anchorev_2026_03_10_a", anchorSobriety, engine.AnchorEventUse, "2026-03-10")
	use.Intent = engine.AnchorIntentPlanned
	walk := anchorDef(anchorWalk, "morning walk", "2026-02-01", "2026-02-01T09:00:00Z")
	sunset := redated(walk, walk.Date)
	sunset.State = engine.AnchorStateSunset
	snacks := engine.Anchor{ID: anchorSnacks, Label: "late-night-snacks", Date: "2026-04-01", RecordedAt: stamp}

	return []pairCase{
		{
			name: "slip", anchors: []engine.Anchor{sobriety}, events: []engine.AnchorEvent{quit},
			intent: engine.AnchorPairIntent{
				Order:  engine.AnchorPairCompatFirst,
				Event:  stamped(anchorEvent("anchorev_2026_04_02_a", anchorSobriety, engine.AnchorEventSlip, "2026-04-01")),
				Compat: redated(sobriety, "2026-04-01"),
			},
			missingAll:    "the re-dated record and the slip event are missing",
			missingSecond: "the slip event is missing",
			retry:         "lucid anchor slip sobriety --day 2026-04-01",
		},
		{
			name: "reclassify to quit", anchors: []engine.Anchor{sobriety},
			events: []engine.AnchorEvent{
				kindEvent("anchorev_2026_01_01_a", anchorSobriety, "2026-01-01", engine.AnchorKindLimit, 4, 90), use,
			},
			intent: engine.AnchorPairIntent{
				Order:  engine.AnchorPairCompatFirst,
				Event:  stamped(kindEvent("anchorev_2026_04_02_a", anchorSobriety, "2026-04-02", engine.AnchorKindQuit, 0, 0)),
				Compat: redated(sobriety, "2026-03-10"),
			},
			missingAll:    "the re-dated record and the kind event are missing",
			missingSecond: "the kind event is missing",
			retry:         "lucid anchor kind sobriety quit",
		},
		{
			name:    "void of a slip",
			anchors: []engine.Anchor{sobriety, {ID: anchorSobriety, Label: "sobriety", Date: "2026-03-01", RecordedAt: "2026-03-01T21:00:00Z"}},
			events:  []engine.AnchorEvent{quit, priorSlip},
			intent: engine.AnchorPairIntent{
				Order:  engine.AnchorPairEventFirst,
				Event:  stamped(voidEvent("anchorev_2026_04_02_a", anchorSobriety, "2026-04-02", priorSlip.ID)),
				Compat: redated(sobriety, "2026-01-01"),
			},
			missingAll:    "the void event and the restoring record are missing",
			missingSecond: "the restoring record is missing",
			retry:         "lucid anchor void anchorev_2026_03_01_a",
		},
		{
			name: "add --kind", anchors: []engine.Anchor{sobriety}, events: []engine.AnchorEvent{quit},
			intent: engine.AnchorPairIntent{
				Order:  engine.AnchorPairEventFirst,
				Event:  stamped(kindEvent("anchorev_2026_04_02_a", anchorSnacks, "2026-04-02", engine.AnchorKindLimit, 4, 90)),
				Compat: snacks,
			},
			missingAll:    "the kind event and the anchor's definition are missing",
			missingSecond: "the anchor's definition is missing",
			retry:         "lucid anchor add late-night-snacks 2026-04-01 --kind limit --allowance 4 --window 90",
		},
		{
			name: "sunset --graduated", anchors: []engine.Anchor{walk},
			events: []engine.AnchorEvent{kindEvent("anchorev_2026_02_01_a", anchorWalk, "2026-02-01", engine.AnchorKindHabit, 0, 0)},
			intent: engine.AnchorPairIntent{
				Order:  engine.AnchorPairEventFirst,
				Event:  stamped(anchorEvent("anchorev_2026_04_02_a", anchorWalk, engine.AnchorEventGraduate, "2026-04-02")),
				Compat: sunset,
			},
			missingAll:    "the graduate event and the sunset record are missing",
			missingSecond: "the sunset record is missing",
			retry:         "lucid anchor sunset 'morning walk' --graduated",
		},
	}
}

// TestValidateHalfWrittenPair is the half-written pair report for every
// two-file verb: an intent-only and a first-half-only prefix are each one
// error naming the reserved id, the missing half and the retry; a completed
// pair is clean. It also covers the pairs a retry can't finish — an add --kind
// whose reserved anchor id is already recorded (the check's one warning) and a
// reserved id holding a different event — and malformed or duplicate intents.
func TestValidateHalfWrittenPair(t *testing.T) {
	for _, c := range pairCases() {
		t.Run(c.name, func(t *testing.T) {
			compatFirst := c.intent.Order == engine.AnchorPairCompatFirst
			prefixes := []struct {
				name          string
				compat, event bool
				missing       string
				halfWritten   bool
			}{
				{"intent only", false, false, c.missingAll, true},
				{"first half only", compatFirst, !compatFirst, c.missingSecond, true},
				{"complete", true, true, "", false},
			}
			for _, p := range prefixes {
				anchors, events := c.with(p.compat, p.event)
				rep, err := Run(Options{Ledger: &fakeLedger{anchors: anchors, events: events}})
				require.NoError(t, err)
				if !p.halfWritten {
					assert.Empty(t, rep.Findings, "%s: a completed pair is clean", p.name)
					continue
				}
				f := requireOnly(t, rep.Findings, ruleAnchorPairHalfWritten)
				assert.Equal(t, SeverityError, f.Severity, p.name)
				assert.Equal(t, anchorEventsFilePath, f.Path)
				assert.Equal(t,
					"anchorev_2026_04_02_a: half-written pair — "+p.missing+"; retry "+c.retry,
					f.Message, p.name)
				assert.False(t, rep.OK(), "%s: a half-written pair fails the sweep", p.name)
			}
		})
	}

	t.Run("the retry names the anchor's current label", func(t *testing.T) {
		c := pairCases()[0]
		anchors, events := c.with(false, false)
		anchors.History = append(anchors.History,
			anchorDef(anchorSobriety, "steady days", "2026-01-01", "2026-04-03T09:00:00Z"))
		f := requireOnly(t, CheckAnchorFiles(&fakeLedger{anchors: anchors, events: events}), ruleAnchorPairHalfWritten)
		assert.Contains(t, f.Message, "retry lucid anchor slip 'steady days' --day 2026-04-01")
	})

	t.Run("an add --kind whose reserved id is already recorded is a warning", func(t *testing.T) {
		c := pairCases()[3]
		claimed := anchorDef(anchorSnacks, "first-gate", "2026-04-05", "2026-04-05T09:00:00Z")
		for _, tc := range []struct {
			name, consequence string
			compat, event     bool
		}{
			{"event written", "its kind event is ignored and nothing reads it", false, true},
			{"intent only", "it recorded no fact and nothing reads it", false, false},
		} {
			anchors, events := c.with(tc.compat, tc.event)
			anchors.History = append(anchors.History, claimed)
			rep, err := Run(Options{Ledger: &fakeLedger{anchors: anchors, events: events}})
			require.NoError(t, err)
			f := requireOnly(t, rep.Findings, ruleAnchorPairAbandoned)
			assert.Equal(t, SeverityWarn, f.Severity, tc.name)
			assert.Contains(t, f.Message, `anchorev_2026_04_02_a: unfinished anchor add --kind for "late-night-snacks" `+
				"can no longer be finished — anchor id "+anchorSnacks+" is already recorded, so "+tc.consequence)
			assert.True(t, rep.OK(), "%s: the warning never fails the sweep", tc.name)
		}
	})

	t.Run("an add --kind whose definition landed without its kind event is a warning", func(t *testing.T) {
		c := pairCases()[3]
		anchors, events := c.with(true, false)
		f := requireOnly(t, CheckAnchorFiles(&fakeLedger{anchors: anchors, events: events}), ruleAnchorPairAbandoned)
		assert.Equal(t, SeverityWarn, f.Severity)
		assert.Contains(t, f.Message,
			"reclassify it with lucid anchor kind late-night-snacks limit --allowance 4 --window 90")
	})

	t.Run("a dangling add --kind event is named once, by its pair", func(t *testing.T) {
		c := pairCases()[3]
		anchors, events := c.with(false, true)
		requireOnly(t, CheckAnchorFiles(&fakeLedger{anchors: anchors, events: events}), ruleAnchorPairHalfWritten)
	})

	t.Run("a reserved id holding a different event can't be retried", func(t *testing.T) {
		c := pairCases()[0]
		anchors, events := c.with(false, false)
		other := c.intent.Event
		other.Day = "2026-03-20"
		events.Events = append(events.Events, other)
		f := requireOnly(t, CheckAnchorFiles(&fakeLedger{anchors: anchors, events: events}), ruleAnchorPairMismatch)
		assert.Equal(t, SeverityError, f.Severity)
		assert.Contains(t, f.Message, "the event recorded under its id is not the one its intent planned")
		assert.NotContains(t, f.Message, "retry lucid")
	})

	t.Run("a malformed intent is named, and reads as no pair", func(t *testing.T) {
		c := pairCases()[0]
		anchors, events := c.with(false, false)
		events.PairIntents[0].Compat.ID = anchorWalk
		f := requireOnly(t, CheckAnchorFiles(&fakeLedger{anchors: anchors, events: events}), ruleAnchorPairMalformed)
		assert.Contains(t, f.Message, "pair_intents[0]: pair intent anchorev_2026_04_02_a pairs an event for "+anchorSobriety)
	})

	t.Run("a second intent reserving the same id is a duplicate", func(t *testing.T) {
		c := pairCases()[0]
		anchors, events := c.with(true, true)
		events.PairIntents = append(events.PairIntents, c.intent)
		f := requireOnly(t, CheckAnchorFiles(&fakeLedger{anchors: anchors, events: events}), ruleAnchorPairDuplicate)
		assert.Contains(t, f.Message, "pair_intents[1]: anchorev_2026_04_02_a is already reserved")
	})
}

// TestShellWord quotes a label only when a shell would not pass it through
// unchanged.
func TestShellWord(t *testing.T) {
	assert.Equal(t, "late-night-snacks", shellWord("late-night-snacks"))
	assert.Equal(t, "'morning walk'", shellWord("morning walk"))
	assert.Equal(t, `'it'\''s fine'`, shellWord("it's fine"))
	assert.Equal(t, "'a&b'", shellWord("a&b"))
	assert.Equal(t, "''", shellWord(""))
}

// TestRetryCommandFallback: an intent of a type that is never written as a
// pair has no verb to name (a malformed intent never reaches this path).
func TestRetryCommandFallback(t *testing.T) {
	p := engine.AnchorPairIntent{Event: anchorEvent("anchorev_2026_04_02_a", anchorSobriety, engine.AnchorEventMiss, "2026-04-02")}
	assert.Equal(t, "the original command", retryCommand(p, "sobriety"))
}
