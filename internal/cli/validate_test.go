package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
	"github.com/mrz1836/lucid/internal/validate"
)

// TestValidateCLI_CleanRepo runs `lucid validate` from the checkout with no
// Ledger: the grep checks (against the real repo and the live router denylist)
// pass and the schema check is skipped. This is the AC-14 end-to-end gate.
func TestValidateCLI_CleanRepo(t *testing.T) {
	isolatedHome(t) // LUCID_HOME points at a fresh, uninitialized tree
	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "validate")
	require.NoError(t, err)
	assert.Contains(t, out, "validate: clean")
	assert.Contains(t, out, "public-boundary")
	assert.Contains(t, out, "sanctuary")
	assert.Contains(t, errOut, "skipped: schema check")
}

// TestValidateCLI_JSON emits the machine-readable report; ok is true and the
// four grep checks ran.
func TestValidateCLI_JSON(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate", "--json")
	require.NoError(t, err)

	var payload validateJSON
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.True(t, payload.OK)
	assert.Equal(t, 0, payload.Errors)
	assert.Contains(t, payload.Ran, "public-boundary")
	assert.Contains(t, payload.Ran, "diagnostic-language")
	assert.Contains(t, payload.Ran, "sanctuary")
	assert.NotEmpty(t, payload.Repo)
}

// TestValidateCLI_SchemaError: an initialized Ledger with a corrupt processed
// record fails validate with a non-zero exit and a schema finding.
func TestValidateCLI_SchemaError(t *testing.T) {
	home := isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "processed", "bad.json"), []byte("not json"), 0o600))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate")
	require.ErrorIs(t, err, errValidationFailed)
	assert.Contains(t, out, "error schema")
	assert.Contains(t, out, "processed/bad")
}

// TestValidateCLI_JSONSchemaError: the JSON path also returns the failure
// sentinel and reports the Ledger home.
func TestValidateCLI_JSONSchemaError(t *testing.T) {
	home := isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "processed", "bad.json"), []byte("nope"), 0o600))

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate", "--json")
	require.ErrorIs(t, err, errValidationFailed)

	var payload validateJSON
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.False(t, payload.OK)
	assert.GreaterOrEqual(t, payload.Errors, 1)
	assert.Equal(t, home, payload.Ledger)
}

// TestValidateCLI_RejectsArgs: validate takes no positional args.
func TestValidateCLI_RejectsArgs(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate", "extra")
	require.Error(t, err)
}

// TestFindRepoRoot covers both branches: from the package dir it finds the
// checkout; from an unrelated dir it reports none.
func TestFindRepoRoot(t *testing.T) {
	root, ok := findRepoRoot()
	require.True(t, ok)
	_, statErr := os.Stat(filepath.Join(root, "go.mod"))
	require.NoError(t, statErr)

	t.Chdir(t.TempDir()) // a dir outside any Go module
	_, ok = findRepoRoot()
	assert.False(t, ok)
}

// TestValidateCLI_CloseoutNeedsNoModel evidences the AC-14 sanctuary bullet at
// the skill boundary: an Engine verb (`closeout`) completes with every model
// down. The binary configures no provider for closeout — the Engine is
// agent-free — so a compact close-out records the day and acks with no LLM
// anywhere in the path.
func TestValidateCLI_CloseoutNeedsNoModel(t *testing.T) {
	isolatedHome(t)
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "closeout", "dfx", "3/wrist", "the chain ran")
	require.NoError(t, err)
	assert.Contains(t, out, "streak 1")
}

// TestValidateCLI_NoRepoNoLedger: outside a checkout with no Ledger, validate
// runs nothing and is trivially clean, noting both skips.
func TestValidateCLI_NoRepoNoLedger(t *testing.T) {
	isolatedHome(t)
	t.Chdir(t.TempDir())
	out, errOut, err := runRoot(t, BuildInfo{Version: "dev"}, "validate")
	require.NoError(t, err)
	assert.Contains(t, out, "validate: clean")
	assert.Contains(t, errOut, "repo checks")
	assert.Contains(t, errOut, "schema check")
}

// TestValidateCLI_SweepsEveryRecordFamily seeds one valid insight, reflection,
// and person into an initialized Ledger, then runs validate: the schema sweep
// reads each family (ReadInsightErr / ReadReflectionErr / ReadPersonErr) and,
// because every record parses, the run stays clean. This is the read-back
// coverage of the sweep the corrupt-record tests only exercise for processed
// artifacts.
func TestValidateCLI_SweepsEveryRecordFamily(t *testing.T) {
	home := isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)

	a := storage.New(home)
	at := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	_, err = a.WriteInsight(storage.Insight{
		CreatedAt: at,
		Status:    storage.InsightStatusAccepted,
		Provenance: storage.InsightProvenance{
			RawEntryIDs:             []string{"raw_2026_07_06_09_00"},
			ProcessedArtifactID:     "raw_2026_07_06_09_00",
			ReflectionPromptVersion: "reflection-2026.05.0",
			UserResponseKind:        storage.ResponseAccepted,
			UserResponseText:        "Yes, that fits.",
		},
		Body: "A steadier week.",
	})
	require.NoError(t, err)
	_, err = a.WriteReflection(storage.Reflection{
		ID:           "reflection_2026_W28",
		ISOWeek:      "2026-W28",
		WindowStart:  at.AddDate(0, 0, -6),
		WindowEnd:    at,
		CreatedAt:    at,
		AgentVersion: "reflection-2026.05.0",
		Summary:      "A steadier week overall.",
	})
	require.NoError(t, err)
	_, err = a.UpdatePerson(storage.PersonMention{DisplayName: "Sam", RawEntryID: "raw_2026_07_06_09_00", At: at})
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate")
	require.NoError(t, err)
	assert.Contains(t, out, "validate: clean")
	assert.NotContains(t, out, "skipped: schema check", "the schema sweep ran over the seeded records")
}

// TestValidateCLI_AfterPersonCreate is the end-to-end gate for the deliberate,
// model-free person-creation path: `person create` mints a canonical record
// through the command tree, and `validate` then sweeps it clean. Unlike
// TestValidateCLI_SweepsEveryRecordFamily (which seeds via the extractive
// UpdatePerson path), this exercises the deliberate-creation command itself, so
// a never-mentioned record — empty entry_refs, a single creation-instant
// seen-window, aka:[display_name] — is proven schema-valid at the CLI boundary.
func TestValidateCLI_AfterPersonCreate(t *testing.T) {
	isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)

	// Mint a person deliberately, create-then-enrich in one call, no model.
	_, _, err = runRoot(t, BuildInfo{Version: "dev"},
		"person", "create", "Sam Rivera", "--relationship", "colleague", "--dob", "1990-04-12")
	require.NoError(t, err)

	// The schema sweep reads the deliberately-created record and finds it valid.
	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate")
	require.NoError(t, err)
	assert.Contains(t, out, "validate: clean")
	assert.NotContains(t, out, "skipped: schema check", "the schema sweep ran over the created record")

	// The record is readable back by its display name, rendered with no zero-time.
	look, _, err := runRoot(t, BuildInfo{Version: "dev"}, "person", "Sam Rivera")
	require.NoError(t, err)
	assert.Contains(t, look, "Sam Rivera")
	assert.Contains(t, look, "Mentioned in 0 entries")
}

// TestValidateCLI_HistoricalGratitudeCollisions: gratitude entries that share a
// receipt id minted under the old per-entry seq still load, and `validate`
// sweeps the Ledger clean — a historical cross-entry collision is append-only
// history, never a finding. The next `gratitude add --into` on that date returns
// a receipt above the shared one rather than repeating it.
func TestValidateCLI_HistoricalGratitudeCollisions(t *testing.T) {
	home := isolatedHome(t)
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)

	a := storage.New(home)
	require.NoError(t, a.ScaffoldObservations())
	require.NoError(t, a.ScaffoldGratitude())
	const shared = "grat_2026_04_20_001"
	phrases := []string{"a warm loaf of bread", "a quiet walk by the river"}
	keys := make([]string, 0, len(phrases))
	for _, phrase := range phrases {
		key, kerr := a.ResolveGratitudeKey(phrase)
		require.NoError(t, kerr)
		entry := observations.NewGratitudeEntry(key, phrase, "2026-04-20T21:00:00-04:00")
		entry.History = append(entry.History, observations.GratitudeEvent{
			ID:     shared,
			At:     "2026-04-20T21:00:00-04:00",
			Type:   observations.GratitudeEventOccurrence,
			Date:   "2026-04-20",
			Source: observations.GratitudeSourceGratitude,
		})
		b, merr := json.MarshalIndent(entry, "", "  ")
		require.NoError(t, merr)
		path := filepath.Join(home, "registries", "gratitude", key+".json")
		require.NoError(t, os.WriteFile(path, append(b, '\n'), 0o600))
		keys = append(keys, key)
	}
	require.NotEqual(t, keys[0], keys[1])

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate")
	require.NoError(t, err)
	assert.Contains(t, out, "validate: clean")
	assert.NotContains(t, out, "skipped: schema check", "the schema sweep ran over the Ledger")

	addOut, _, err := runRoot(t, BuildInfo{Version: "dev"},
		"gratitude", "add", "fresh bread from the oven", "--into", keys[0], "--day", "2026-04-20", "--json")
	require.NoError(t, err)
	var payload struct {
		Receipt string `json:"receipt"`
	}
	require.NoError(t, json.Unmarshal([]byte(addOut), &payload))
	assert.Equal(t, "grat_2026_04_20_002", payload.Receipt, "a new receipt lands above the shared historical id")
}

// TestValidateCLI_AnchorKindsClean: every anchor verb, written through the
// command tree, leaves a Ledger the anchors check sweeps clean — kinds,
// slips, uses, misses, confirms, a void and a graduation, two-file writes
// included. A real write never reads as a defect.
func TestValidateCLI_AnchorKindsClean(t *testing.T) {
	isolatedHome(t)
	withClock(t, afternoon())
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)

	runAnchor(t, "add", "sobriety", "2026-01-01", "--kind", "quit")
	slip := jsonObject(t, runAnchor(t, "slip", "sobriety", "--day", "2026-03-01", "--json"))
	runAnchor(t, "slip", "sobriety", "--day", "2026-05-01")
	runAnchor(t, "add", "late-night-snacks", "2026-01-01", "--kind", "limit", "--allowance", "4", "--window", "90")
	runAnchor(t, "use", "late-night-snacks", "--planned", "--day", "2026-06-01")
	runAnchor(t, "use", "late-night-snacks", "--slip", "--day", "2026-06-20")
	runAnchor(t, "add", "first-gate", "2026-02-01")
	runAnchor(t, "kind", "first-gate", "quit")
	runAnchor(t, "add", "morning-walk", "2026-06-01", "--kind", "habit")
	runAnchor(t, "miss", "morning-walk", "--day", "2026-07-02")
	runAnchor(t, "confirm", "morning-walk", "--from", "2026-06-01", "--through", "2026-07-04")
	runAnchor(t, "void", slip["receipt_id"].(string))
	runAnchor(t, "sunset", "morning-walk", "--graduated")

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate", "--json")
	require.NoError(t, err, out)
	var payload validateJSON
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.True(t, payload.OK)
	assert.Contains(t, payload.Ran, validate.CheckAnchors)
	for _, f := range payload.Findings {
		assert.NotEqual(t, validate.CheckAnchors, f.Check, "unexpected anchors finding: %+v", f)
	}
}

// TestValidateCLI_MeasurementVoidClean: a measurement reading and its void,
// written through the command tree, leave a Ledger validate sweeps clean — a
// void is an ordinary observation event, never a defect.
func TestValidateCLI_MeasurementVoidClean(t *testing.T) {
	enableMeasurementCapture(t)
	id, _ := captureReading(t, "measurement", "weight", "70.5", "kg")
	captureReading(t, "measurement", "weight", "70.3", "kg")
	voidReading(t, id)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate", "--json")
	require.NoError(t, err, out)
	var payload validateJSON
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	assert.True(t, payload.OK)
	assert.Zero(t, payload.Errors)
	for _, f := range payload.Findings {
		assert.NotEqual(t, validate.SeverityError, f.Severity, "unexpected error finding: %+v", f)
	}
}

// TestValidateCLI_HalfWrittenPairRetryFinishes is the recovery path end to
// end: a slip interrupted after its pair intent landed is reported as a
// half-written pair naming the retry, running exactly that command finishes
// the write, and the sweep is clean again. validate itself repairs nothing.
func TestValidateCLI_HalfWrittenPairRetryFinishes(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, afternoon())
	_, _, err := runRoot(t, BuildInfo{Version: "dev"}, "init")
	require.NoError(t, err)
	runAnchor(t, "add", "sobriety", "2026-01-01", "--kind", "quit")

	// The intent a slip on 2026-03-01 reserves before either half — and then
	// the crash: neither the re-date nor the slip event lands.
	store := storage.New(home)
	anchors := readAnchorLog(t, home)
	def := anchors.History[len(anchors.History)-1]
	const stamp = "2026-07-05T13:00:00Z"
	compat := def
	compat.Date, compat.RecordedAt = "2026-03-01", stamp
	event := engine.AnchorEvent{
		ID:         engine.NextAnchorEventID(readAnchorEvents(t, home), afternoon()),
		AnchorID:   def.ID,
		Type:       engine.AnchorEventSlip,
		Day:        "2026-03-01",
		RecordedAt: stamp,
	}
	require.NoError(t, store.AppendAnchorPairIntent(engine.AnchorPairIntent{
		Order: engine.AnchorPairCompatFirst, Event: event, Compat: compat,
	}))
	before, err := os.ReadFile(filepath.Join(home, "engine", "anchor_events.json"))
	require.NoError(t, err)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "validate")
	require.ErrorIs(t, err, errValidationFailed)
	want := event.ID + ": half-written pair — the re-dated record and the slip event are missing; " +
		"retry lucid anchor slip sobriety --day 2026-03-01"
	assert.Contains(t, out, "error anchors")
	assert.Contains(t, out, want)

	after, err := os.ReadFile(filepath.Join(home, "engine", "anchor_events.json"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "validate repairs nothing")
	assert.Equal(t, anchors, readAnchorLog(t, home), "validate repairs nothing")

	retry := strings.Fields(want[strings.Index(want, "retry lucid ")+len("retry lucid "):])
	retryOut, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, retry...)
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Contains(t, retryOut, event.ID, "the retry returns the original receipt")

	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "validate")
	require.NoError(t, err, out)
	assert.Contains(t, out, "validate: clean")
}
