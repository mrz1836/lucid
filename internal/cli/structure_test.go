package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// The structure tests drive the real cobra tree offline: the Ledger is an
// isolated LUCID_HOME under t.TempDir, the clock is pinned, and the model is a
// scripted [provider.Fake] injected through the package-level buildProvider
// seam by withServeProvider (restored in t.Cleanup), so no test spawns a real
// vendor CLI or opens a socket (ADR-0006).

// structureExtraction is a well-formed extraction reply mentioning nobody, so a
// pass exercises the processed write without also exercising the People
// routine.
const structureExtraction = `{"emotions":[{"name":"calm","rationale":"'quiet'"}],"themes":[],"people":[],"notes":null}`

// structurePassDay is the civil day every structure test seeds into, so a
// window over it is written out literally rather than computed.
const structurePassDay = "2026-07-05"

// structureNow is the pinned clock: an evening on the pass day, so an omitted
// --until closes the window on the same day the entries were seeded.
func structureNow() time.Time { return time.Date(2026, time.July, 5, 18, 0, 0, 0, time.UTC) }

// structureAt returns an instant on the pass day, so seeded entries land on
// distinct minute-precision ids inside one window.
func structureAt(hour int) time.Time {
	return time.Date(2026, time.July, 5, hour, 0, 0, 0, time.UTC)
}

// structureExchange is one scripted extraction completion.
func structureExchange() provider.Exchange { return provider.Exchange{Content: structureExtraction} }

// seedStructureRaw scaffolds the isolated Ledger (idempotently) and writes one
// raw entry captured by verb, returning its id. The verb is a parameter because
// the pass must reach every raw entry whatever captured it.
func seedStructureRaw(t *testing.T, home, verb, body string, at time.Time) string {
	t.Helper()
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	res, err := a.WriteRaw(storage.RawEntry{
		RecordedAt: at, OccurredAt: at, OccurredAtPrecision: storage.PrecisionExact,
		Source: "cli", Command: verb, Body: body,
	})
	require.NoError(t, err)
	return res.RawID
}

// runStructureCmd executes `lucid structure [args...]`, returning stdout,
// stderr, and the command error.
func runStructureCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runRoot(t, BuildInfo{Version: "dev"}, append([]string{"structure"}, args...)...)
}

// processedPath is the on-disk artifact a structured entry writes.
func processedPath(home, id string) string {
	return filepath.Join(home, "processed", id+".json")
}

// TestStructure_SingleEntryWritesArtifact is the base case: an entry this run
// did not capture is structured on demand, and single-entry mode says what
// became of it without emitting a progress line.
func TestStructure_SingleEntryWritesArtifact(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	id := seedStructureRaw(t, home, "/log", "Dinner with M. I pushed back then dropped it.", structureAt(9))

	fake := &provider.Fake{Script: []provider.Exchange{structureExchange()}}
	withServeProvider(t, fake)

	out, errOut, err := runStructureCmd(t, id)
	require.NoError(t, err)

	assert.Contains(t, out, id+" → written")
	assert.Contains(t, out, "Structured 1 of 1 entry — 1 written, 0 skipped, 0 degraded, 0 failed.")
	assert.NotContains(t, errOut, "[1/", "single-entry mode emits no progress line")
	assert.FileExists(t, processedPath(home, id))
	assert.Equal(t, 1, fake.Calls())
}

// TestStructure_SkipsAlreadyProcessed proves the default that makes an
// interrupted window run resumable: an entry with an artifact costs no model
// call, exits 0, and names the flag that overrides it.
func TestStructure_SkipsAlreadyProcessed(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	id := seedStructureRaw(t, home, "/log", "Dinner with M.", structureAt(9))

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{structureExchange()}})
	_, _, err := runStructureCmd(t, id)
	require.NoError(t, err)

	// No script at all: an over-call would fail loudly rather than hang.
	fake := &provider.Fake{}
	withServeProvider(t, fake)

	out, _, err := runStructureCmd(t, id)
	require.NoError(t, err)
	assert.Contains(t, out, "already structured")
	assert.Contains(t, out, "--force")
	assert.Contains(t, out, "0 written, 1 skipped")
	assert.Equal(t, 0, fake.Calls(), "a skip must never reach the model")
}

// TestStructure_ForceRestructures proves --force spends the model call the
// default skip saves, rewriting the artifact.
func TestStructure_ForceRestructures(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	id := seedStructureRaw(t, home, "/log", "Dinner with M.", structureAt(9))

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{structureExchange()}})
	_, _, err := runStructureCmd(t, id)
	require.NoError(t, err)

	fake := &provider.Fake{Script: []provider.Exchange{structureExchange()}}
	withServeProvider(t, fake)

	out, _, err := runStructureCmd(t, id, "--force")
	require.NoError(t, err)
	assert.Contains(t, out, id+" → written")
	assert.Contains(t, out, "1 written, 0 skipped")
	assert.Equal(t, 1, fake.Calls())
	assert.FileExists(t, processedPath(home, id))
}

// TestStructure_RangedCoversEveryCommand proves a window reaches every raw
// entry whatever captured it, skips the one already structured, and reports the
// counts.
func TestStructure_RangedCoversEveryCommand(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	logged := seedStructureRaw(t, home, "/log", "Quiet morning.", structureAt(9))
	checked := seedStructureRaw(t, home, "/checkin", "Talked it through.", structureAt(10))
	closed := seedStructureRaw(t, home, "/closeout", "Held the promise.", structureAt(11))

	// Structure one of the three up front so the window has something to skip.
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{structureExchange()}})
	_, _, err := runStructureCmd(t, logged)
	require.NoError(t, err)

	fake := &provider.Fake{Script: []provider.Exchange{structureExchange(), structureExchange()}}
	withServeProvider(t, fake)

	out, _, err := runStructureCmd(t, "--since", structurePassDay, "--until", structurePassDay)
	require.NoError(t, err)

	assert.Contains(t, out, "Structured 2 of 3 entries — 2 written, 1 skipped, 0 degraded, 0 failed.")
	assert.Equal(t, 2, fake.Calls())
	assert.FileExists(t, processedPath(home, checked))
	assert.FileExists(t, processedPath(home, closed))
}

// TestStructure_RangedJSONShape proves the --json projection carries every
// scalar plus a non-null entries array, and that an omitted --until closes the
// window on the clock's day.
func TestStructure_RangedJSONShape(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	first := seedStructureRaw(t, home, "/log", "Quiet morning.", structureAt(9))
	second := seedStructureRaw(t, home, "/closeout", "Held the promise.", structureAt(10))

	fake := &provider.Fake{Script: []provider.Exchange{structureExchange(), structureExchange()}}
	withServeProvider(t, fake)

	out, _, err := runStructureCmd(t, "--since", structurePassDay, "--json")
	require.NoError(t, err)

	var view structureView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, structureModeRange, view.Mode)
	assert.Equal(t, structurePassDay, view.Since)
	assert.Equal(t, structurePassDay, view.Until, "an omitted --until closes on the clock's day")
	assert.False(t, view.Force)
	assert.Equal(t, 2, view.Attempted)
	assert.Equal(t, 2, view.Written)
	assert.Equal(t, 0, view.Skipped)
	assert.Equal(t, 0, view.Degraded)
	assert.Equal(t, 0, view.Failed)
	require.Len(t, view.Entries, 2)
	assert.Equal(t, first, view.Entries[0].RawID)
	assert.Equal(t, router.StructureOutcomeWrote, view.Entries[0].Outcome)
	assert.Equal(t, second, view.Entries[1].RawID)
}

// TestStructure_SingleEntryJSONHasNoWindow proves single-entry mode is labeled
// as such and reports no window bounds.
func TestStructure_SingleEntryJSONHasNoWindow(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	id := seedStructureRaw(t, home, "/log", "Quiet morning.", structureAt(9))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{structureExchange()}})

	out, _, err := runStructureCmd(t, id, "--force", "--json")
	require.NoError(t, err)

	var view structureView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, structureModeEntry, view.Mode)
	assert.Empty(t, view.Since)
	assert.Empty(t, view.Until)
	assert.True(t, view.Force)
	require.Len(t, view.Entries, 1)
	assert.Equal(t, id, view.Entries[0].RawID)
}

// TestStructure_ProgressGoesToStderrOnly proves a long window shows forward
// motion without putting anything ahead of the --json object on stdout.
func TestStructure_ProgressGoesToStderrOnly(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	seedStructureRaw(t, home, "/log", "Quiet morning.", structureAt(9))
	seedStructureRaw(t, home, "/log", "Afternoon walk.", structureAt(10))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{structureExchange(), structureExchange()}})

	out, errOut, err := runStructureCmd(t, "--since", structurePassDay, "--json")
	require.NoError(t, err)

	var view structureView
	require.NoError(t, json.Unmarshal([]byte(out), &view), "stdout stays parseable JSON")
	assert.NotContains(t, out, "[1/", "no progress line reaches stdout")
	assert.Contains(t, errOut, "[1/2] ")
	assert.Contains(t, errOut, "[2/2] ")
	assert.Contains(t, errOut, "→ "+router.StructureOutcomeWrote)
}

// TestStructure_EmptyWindow proves a window with nothing in it prints the calm
// fallback, exits 0, and costs no model call.
func TestStructure_EmptyWindow(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	_, err := storage.New(home).Scaffold()
	require.NoError(t, err)

	fake := &provider.Fake{}
	withServeProvider(t, fake)

	out, _, err := runStructureCmd(t, "--since", "2026-01-01", "--until", "2026-01-07")
	require.NoError(t, err)
	assert.Contains(t, out, structureEmpty)
	assert.Equal(t, 0, fake.Calls())
}

// TestStructure_UsageErrors is the arg-vs-flag matrix: every ambiguous or
// malformed invocation is a usage error (exit 2), not a runtime failure.
func TestStructure_UsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"id and window together", []string{"raw_2026_07_05_09_00", "--since", structurePassDay}},
		{"neither form", nil},
		{"until without since", []string{"--until", structurePassDay}},
		{"unparseable since", []string{"--since", "2026-7-5"}},
		{"unparseable until", []string{"--since", structurePassDay, "--until", "07/12/2026"}},
		{"out of range day", []string{"--since", "2026-01-32"}},
		{"since after until", []string{"--since", "2026-07-08", "--until", structurePassDay}},
		{"blank id", []string{"   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolatedHome(t)
			withClock(t, structureNow())
			fake := &provider.Fake{}
			withServeProvider(t, fake)

			_, _, err := runStructureCmd(t, tc.args...)
			require.Error(t, err)
			assert.Equal(t, ExitUsage, exitCodeForError(err))
			assert.Equal(t, 0, fake.Calls(), "a rejected invocation never reaches the model")
		})
	}
}

// TestStructure_TooManyArgs proves cobra's own positional validator still maps
// to a usage error.
func TestStructure_TooManyArgs(t *testing.T) {
	isolatedHome(t)
	withServeProvider(t, &provider.Fake{})
	_, _, err := runStructureCmd(t, "raw_a", "raw_b")
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestStructure_MissingEntryIsRuntimeError proves a read fault on a single
// entry surfaces as a runtime failure (exit 1) rather than misuse.
func TestStructure_MissingEntryIsRuntimeError(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	_, err := storage.New(home).Scaffold()
	require.NoError(t, err)
	withServeProvider(t, &provider.Fake{})

	_, _, err = runStructureCmd(t, "raw_2026_07_05_09_00")
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err))
}

// TestStructure_FailedEntryInWindowExitsNonZero proves a window finishes its
// remaining entries after one fails, reports the failure in the counts, and
// still exits non-zero — a failure is a failure, but it must not cost the rest
// of the window.
func TestStructure_FailedEntryInWindowExitsNonZero(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	good := seedStructureRaw(t, home, "/log", "Afternoon walk.", structureAt(10))

	// A well-formed id whose document has no frontmatter fence: enumeration
	// reads names, so it is listed, and the read fails when the pass opens it.
	broken := "raw_2026_07_05_21_00"
	brokenPath := filepath.Join(home, "raw", "2026", "07", broken+".md")
	require.NoError(t, os.WriteFile(brokenPath, []byte("no frontmatter here\n"), 0o600))

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{structureExchange()}})

	out, _, err := runStructureCmd(t, "--since", structurePassDay)
	require.Error(t, err)
	assert.Equal(t, ExitErr, exitCodeForError(err))
	assert.Contains(t, out, "1 written, 0 skipped, 0 degraded, 1 failed.")
	assert.FileExists(t, processedPath(home, good), "the rest of the window still ran")
	assert.NoFileExists(t, processedPath(home, broken))
}

// TestStructure_SingleEntryDegradedStillWrites is the ordinary degrade shape:
// the extraction fails and its stricter retry fails too, so an artifact still
// lands. The short form says so rather than reporting a clean write.
func TestStructure_SingleEntryDegradedStillWrites(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	id := seedStructureRaw(t, home, "/log", "Quiet morning.", structureAt(9))

	fake := &provider.Fake{Script: []provider.Exchange{
		{Content: "garbage"}, {Content: "still garbage"},
	}}
	withServeProvider(t, fake)

	out, _, err := runStructureCmd(t, id)
	require.NoError(t, err, "a degrade is not a failure")
	assert.Contains(t, out, id+" → written from a degraded extraction")
	assert.Contains(t, out, "1 written, 0 skipped, 1 degraded, 0 failed.")
	assert.Equal(t, 2, fake.Calls(), "the stricter retry is the second call")
	assert.FileExists(t, processedPath(home, id))
}

// TestStructure_SingleEntryDegradedWritesNothing is the other degrade shape
// (error-states.md §St-3): the artifact cannot be written at all, so the short
// form must not claim one landed.
func TestStructure_SingleEntryDegradedWritesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	home := isolatedHome(t)
	withClock(t, structureNow())
	id := seedStructureRaw(t, home, "/log", "a quiet, personless day", structureAt(9))

	procDir := filepath.Join(home, "processed")
	require.NoError(t, os.Chmod(procDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(procDir, 0o700) })

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{structureExchange()}})

	out, _, err := runStructureCmd(t, id)
	require.NoError(t, err, "a degrade is not a failure")
	assert.Contains(t, out, id+" → degraded; nothing written")
	assert.Contains(t, out, "0 written, 0 skipped, 1 degraded, 0 failed.")
}

// TestStructure_ProviderBuildErrorSurfaces proves a provider that cannot be
// built fails the verb rather than reporting a pass that never ran.
func TestStructure_ProviderBuildErrorSurfaces(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, structureNow())
	id := seedStructureRaw(t, home, "/log", "Quiet morning.", structureAt(9))
	withFailingProvider(t)

	_, _, err := runStructureCmd(t, id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unavailable")
}

// TestStructure_BootError proves an unscaffoldable home fails the verb at boot,
// before any model call.
func TestStructure_BootError(t *testing.T) {
	unscaffoldableHome(t)
	withServeProvider(t, &provider.Fake{})
	_, _, err := runStructureCmd(t, "--since", structurePassDay)
	require.Error(t, err)
}

// TestStructure_Help proves the verb documents both modes where a user looks
// for them.
func TestStructure_Help(t *testing.T) {
	out, _, err := runStructureCmd(t, "--help")
	require.NoError(t, err)
	for _, want := range []string{"--" + structureFlagSince, "--" + structureFlagUntil, "--" + structureFlagForce} {
		assert.Containsf(t, out, want, "help must document %s", want)
	}
}
