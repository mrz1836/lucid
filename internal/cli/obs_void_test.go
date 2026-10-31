package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// obsVoidAck matches the void's inventory ack, capturing the voided reading,
// its day, and the void's own id.
var obsVoidAck = regexp.MustCompile(
	"^Voided measurement `(obs_[^`]+)` \\((\\d{4}-\\d{2}-\\d{2})\\) as `(obs_[^`]+)` — it no longer counts " +
		"in the measurement export or the packet; both records are kept\\.\n$",
)

// captureReading logs one observation through `lucid obs --json` and returns
// its receipt id and logical day, so the void tests hold a real reading.
func captureReading(t *testing.T, tokens ...string) (id, logicalDate string) {
	t.Helper()
	args := append(append([]string{"obs"}, tokens...), "--json")
	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
	require.NoError(t, err, stderr)
	var receipt writeReceiptView
	require.NoError(t, json.Unmarshal([]byte(out), &receipt), out)
	require.NotEmpty(t, receipt.ReceiptID)
	return receipt.ReceiptID, receipt.LogicalDate
}

// voidReading runs `lucid obs void <id> --json` and returns the void's own
// receipt id and logical day.
func voidReading(t *testing.T, id string) (voidID, logicalDate string) {
	t.Helper()
	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "void", id, "--json")
	require.NoError(t, err, stderr)
	var receipt writeReceiptView
	require.NoError(t, json.Unmarshal([]byte(out), &receipt), out)
	require.NotEmpty(t, receipt.ReceiptID)
	return receipt.ReceiptID, receipt.LogicalDate
}

// appendObsFixture hand-appends one minimal event of kind, so a refusal test
// holds a workout or memory id without going through those verbs.
func appendObsFixture(t *testing.T, home string, kind observations.Kind, payload map[string]any) string {
	t.Helper()
	at := time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC)
	ev, err := storage.New(home).AppendObservation(observations.Event{
		Schema:              observations.Schema,
		Kind:                kind,
		RecordedAt:          at.Format(time.RFC3339),
		OccurredAt:          at.Format(time.RFC3339),
		OccurredAtPrecision: observations.PrecisionExact,
		LogicalDate:         "2026-07-02",
		Source:              observations.SourceMicrolog,
		Payload:             payload,
	})
	require.NoError(t, err)
	return ev.ID
}

// obsFiles snapshots every file under the observations tree, keyed by path,
// so a refusal test proves the file list and every byte are unchanged.
func obsFiles(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(filepath.Join(home, "observations"), func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[p] = string(b)
		return nil
	})
	require.NoError(t, err)
	return out
}

// TestObsVoid_CLI_VoidsMeasurementReceipt: the human run prints the inventory
// ack naming the reading, its day and the void, and --json emits exactly the
// {receipt_id, logical_date} receipt for the void.
func TestObsVoid_CLI_VoidsMeasurementReceipt(t *testing.T) {
	home := enableMeasurementCapture(t)
	id, date := captureReading(t, "measurement", "weight", "70.5", "kg")

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "void", id)
	require.NoError(t, err, stderr)
	assert.Empty(t, stderr)
	m := obsVoidAck.FindStringSubmatch(out)
	require.Len(t, m, 4, "the ack is inventory only: %q", out)
	assert.Equal(t, id, m[1])
	assert.Equal(t, date, m[2])
	assert.NotContains(t, out, "70.5", "the ack never names the value")

	voidEv := eventWithID(t, home, m[3])
	target, isVoid := observations.MeasurementVoidTarget(voidEv)
	require.True(t, isVoid)
	assert.Equal(t, id, target)

	second, _ := captureReading(t, "measurement", "bp-systolic", "121", "mmHg")
	jsonOut, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "void", second, "--json")
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &payload), jsonOut)
	assert.Len(t, payload, 2, "exactly the receipt keys: %v", payload)
	rid, _ := payload["receipt_id"].(string)
	assert.True(t, strings.HasPrefix(rid, "obs_"), "receipt_id is the void's own event id")
	assert.NotEqual(t, second, rid)
	assert.NotEmpty(t, payload["logical_date"])
	voided, _ := observations.MeasurementVoidTarget(eventWithID(t, home, rid))
	assert.Equal(t, second, voided)
}

// TestObsVoid_CLI_RefusalsExitNonZeroNoWrite: each strict-tier refusal
// (Ov-1..Ov-5) exits non-zero with the `lucid obs void:` reason on stderr,
// nothing on stdout, and the observations tree byte-identical.
func TestObsVoid_CLI_RefusalsExitNonZeroNoWrite(t *testing.T) {
	home := enableMeasurementCapture(t)
	captureReading(t, "measurement", "bp-systolic", "121", "mmHg")
	retracted, _ := captureReading(t, "measurement", "bp-diastolic", "79", "mmHg")
	pain, _ := captureReading(t, "pain", "6", "knee")
	voidID, _ := voidReading(t, retracted)
	session := appendObsFixture(t, home, observations.KindWorkout, map[string]any{"type": "push"})
	story := appendObsFixture(t, home, observations.KindMemory, map[string]any{"text": "a walk by the river"})

	tests := []struct {
		name, id, reason string
	}{
		{"ov1 unparseable", "not-an-id", `no observation "not-an-id"; nothing was saved`},
		{"ov1 missing", "obs_2026_07_02_009", `no observation "obs_2026_07_02_009"; nothing was saved`},
		{"ov2 void of a void", voidID, `"` + voidID + `" is itself a void — a void can't be voided`},
		{"ov3 already voided", retracted, `"` + retracted + `" is already voided (by "` + voidID + `")`},
		{"ov4 workout", session, `is a workout observation — correct it with lucid workout amend; nothing was saved`},
		{"ov4 memory", story, `is a memory observation — correct it with lucid memory amend; nothing was saved`},
		{"ov5 pain", pain, `"` + pain + `" is a pain observation — only measurement readings can be voided yet`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := obsFiles(t, home)
			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "void", tc.id)
			require.Error(t, err)
			assert.Equal(t, ExitErr, exitCodeForError(err))
			assert.Empty(t, stdout)
			assert.True(t, strings.HasPrefix(stderr, "lucid obs void: "), "stderr: %q", stderr)
			assert.Contains(t, stderr, tc.reason)
			assert.Equal(t, 1, strings.Count(stderr, "lucid obs void:"), "the reason prints once")
			assert.Equal(t, before, obsFiles(t, home), "a refused void writes nothing")
		})
	}
}

// TestObsVoid_CLI_DisabledKindPrintsHint: with the measurement kind disabled,
// void prints the enable hint, exits 0 and writes nothing (Ov-6).
func TestObsVoid_CLI_DisabledKindPrintsHint(t *testing.T) {
	home := enableAllObsKinds(t)
	pain, _ := captureReading(t, "pain", "6", "knee")
	before := obsFiles(t, home)

	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "void", pain)
	require.NoError(t, err, stderr)
	assert.Equal(t, observations.EnableHint(observations.KindMeasurement)+"\n", out)
	assert.Equal(t, before, obsFiles(t, home))
}

// TestObsVoid_CLI_ArgsAndDayFlagRejected: no id, two ids, and --day are usage
// errors (Ov-7) — exit 2 with the help pointer — and nothing is written.
func TestObsVoid_CLI_ArgsAndDayFlagRejected(t *testing.T) {
	home := enableMeasurementCapture(t)
	id, _ := captureReading(t, "measurement", "weight", "70.5", "kg")

	for name, args := range map[string][]string{
		"no id":    {"obs", "void"},
		"two ids":  {"obs", "void", id, id},
		"day flag": {"obs", "void", id, "--day", "@yesterday"},
	} {
		t.Run(name, func(t *testing.T) {
			before := obsFiles(t, home)
			stdout, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, args...)
			require.Error(t, err)
			assert.Equal(t, ExitUsage, exitCodeForError(err))
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "Run 'lucid obs void --help' for usage.")
			assert.Equal(t, before, obsFiles(t, home), "a usage error writes nothing")
		})
	}
}

// TestObsVoid_CLI_VoidThenRelogWithDay: the documented fix — void the wrong
// reading, re-log the right value on its day — leaves exactly one row, the
// re-logged value, on that day in the measurement export.
func TestObsVoid_CLI_VoidThenRelogWithDay(t *testing.T) {
	home := enableMeasurementCapture(t)
	day := time.Now().AddDate(0, 0, -3).Format("2006-01-02")

	wrong, wrongDate := captureReading(t, "measurement", "weight", "70.5", "kg", "--day", "@"+day)
	require.Equal(t, day, wrongDate)
	voidReading(t, wrong)
	right, rightDate := captureReading(t, "measurement", "weight", "70.3", "kg", "--day", "@"+day)
	require.Equal(t, day, rightDate)

	_, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "export", "measurements")
	require.NoError(t, err, stderr)
	body, err := os.ReadFile(filepath.Join(home, "projections", "measurements.csv"))
	require.NoError(t, err)
	assert.Equal(t, "logical_date,metric,value,unit,id\n"+day+",weight,70.3,kg,"+right+"\n", string(body))
}

// TestObsVoid_CLI_DayKeepsBoth: `lucid day` is raw history — the voided reading
// stays on its own day and the void shows as a void on the day it was filed;
// when both fall on one day, both lines show.
func TestObsVoid_CLI_DayKeepsBoth(t *testing.T) {
	t.Run("reading and void on different days", func(t *testing.T) {
		enableMeasurementCapture(t)
		day := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
		id, _ := captureReading(t, "measurement", "weight", "70.5", "kg", "--day", "@"+day)
		voidID, voidDate := voidReading(t, id)
		require.NotEqual(t, day, voidDate)

		readingDay := runDay(t, day)
		assert.Contains(t, readingDay, "measurement metric=weight, unit=kg, value=70.5 ("+id+")")
		assert.NotContains(t, readingDay, "void of")

		voidDay := runDay(t, voidDate)
		assert.Contains(t, voidDay, "measurement void of "+id+" ("+voidID+")")
		assert.NotContains(t, voidDay, "value=70.5")
	})

	t.Run("reading and void on one day", func(t *testing.T) {
		enableMeasurementCapture(t)
		id, date := captureReading(t, "measurement", "bp-systolic", "121", "mmHg")
		voidID, voidDate := voidReading(t, id)
		require.Equal(t, date, voidDate)

		out := runDay(t, date)
		assert.Contains(t, out, "measurement metric=bp-systolic, unit=mmHg, value=121 ("+id+")")
		assert.Contains(t, out, "measurement void of "+id+" ("+voidID+")")
	})
}

// TestObs_CLI_CaptureUnaffectedByVoidSubcommand: adding the void subcommand
// leaves capture routing intact — `obs <kind> …` still logs — and no kind or
// alias resolves from "void", so the subcommand shadows nothing.
func TestObs_CLI_CaptureUnaffectedByVoidSubcommand(t *testing.T) {
	home := enableMeasurementCapture(t)

	out, _, err := runRoot(t, BuildInfo{Version: "dev"}, "obs", "pain", "6", "knee")
	require.NoError(t, err)
	assert.Contains(t, out, "Logged pain as `obs_")
	out, _, err = runRoot(t, BuildInfo{Version: "dev"}, "obs", "measurement", "weight", "70.5", "kg")
	require.NoError(t, err)
	assert.Contains(t, out, "Logged measurement as `obs_")

	events := readObsEvents(t, home)
	require.Len(t, events, 2)
	kinds := []observations.Kind{events[0].Kind, events[1].Kind}
	assert.ElementsMatch(t, []observations.Kind{observations.KindPain, observations.KindMeasurement}, kinds)

	_, _, ok := observations.ResolveVerb("void")
	assert.False(t, ok, "no observation kind or alias is named void")

	root := newRootCmd(BuildInfo{Version: "dev"})
	voidCmd, _, err := root.Find([]string{"obs", "void", "obs_2026_07_02_001"})
	require.NoError(t, err)
	assert.Equal(t, "void", voidCmd.Name())
	captureCmd, rest, err := root.Find([]string{"obs", "pain", "6"})
	require.NoError(t, err)
	assert.Equal(t, "obs", captureCmd.Name())
	assert.Equal(t, []string{"pain", "6"}, rest)
}

// runDay renders `lucid day <date>` and returns stdout.
func runDay(t *testing.T, date string) string {
	t.Helper()
	out, stderr, err := runRoot(t, BuildInfo{Version: "dev"}, "day", date)
	require.NoError(t, err, stderr)
	return out
}

// eventWithID returns the on-disk event holding id.
func eventWithID(t *testing.T, home, id string) observations.Event {
	t.Helper()
	for _, ev := range readObsEvents(t, home) {
		if ev.ID == id {
			return ev
		}
	}
	t.Fatalf("event %s not found", id)
	return observations.Event{}
}
