package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// ── Batch 1a: the durable whole-file writers are atomic ─────────────────────

// TestAtomicWriters_PreservePriorRecordOnFailure proves the record families
// moved onto writeFileAtomic keep the crash-durability contract: when the write
// cannot complete, the record already on disk is byte-identical to before and
// no staging file is left behind. A bare os.WriteFile would have truncated the
// record before failing. The failure is induced by a read-only parent dir (the
// atomic temp file cannot be staged there); a read-only target file alone would
// simply be replaced by the rename — which is exactly why the change was safe.
func TestAtomicWriters_PreservePriorRecordOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}

	t.Run("gratitude entry", func(t *testing.T) {
		a := newObsStore(t)
		key := addGratitudeOccurrence(t, a, "a roof over my head", "2026-07-01")
		path, err := a.gratitudePath(key)
		require.NoError(t, err)
		assertAtomicWritePreserves(t, a.gratitudeDir(), path, func() error {
			ev := observations.GratitudeEvent{
				Type: observations.GratitudeEventOccurrence, Date: "2026-07-02",
				Source: observations.GratitudeSourceGratitude,
			}
			_, _, e := a.AppendGratitudeEvent(key, "a roof over my head", "2026-07-02", true, ev, mergeNow())
			return e
		})
	})

	t.Run("registry record", func(t *testing.T) {
		a := newObsStore(t)
		_, err := a.UpdateRegistry(observations.RegistryInjury, "injury_a-cedar",
			observations.RegistryPatch{DisplayName: "left knee", At: "2026-07-01T09:00:00Z"})
		require.NoError(t, err)
		dir := filepath.Join(a.registriesDir(), "injuries")
		path := filepath.Join(dir, "injury_a-cedar.json")
		assertAtomicWritePreserves(t, dir, path, func() error {
			_, e := a.UpdateRegistry(observations.RegistryInjury, "injury_a-cedar",
				observations.RegistryPatch{Status: "healed", At: "2026-07-02T09:00:00Z"})
			return e
		})
	})

	t.Run("person record", func(t *testing.T) {
		a := newPeopleAdapter(t)
		res, err := a.UpdatePerson(PersonMention{DisplayName: "Hazel", RawEntryID: "raw_a", At: personAt(1)})
		require.NoError(t, err)
		path, err := a.personPath(res.PersonKey)
		require.NoError(t, err)
		assertAtomicWritePreserves(t, a.peopleDir(), path, func() error {
			_, e := a.UpdatePerson(PersonMention{DisplayName: "Hazel", RawEntryID: "raw_b", At: personAt(2)})
			return e
		})
	})

	t.Run("off-limits registry", func(t *testing.T) {
		a := New(t.TempDir())
		require.NoError(t, a.WriteOffLimitsPersonKeys([]string{"person_a-cedar"}))
		path := filepath.Join(a.home, offLimitsFile)
		assertAtomicWritePreserves(t, a.home, path, func() error {
			return a.WriteOffLimitsPersonKeys([]string{"person_a-cedar", "person_b-oak"})
		})
	})

	t.Run("observations config", func(t *testing.T) {
		a := newObsStore(t)
		cfg, err := a.ReadObservationsConfig()
		require.NoError(t, err)
		assertAtomicWritePreserves(t, a.observationsDir(), a.obsConfigPath(), func() error {
			return a.SaveObservationsConfig(cfg)
		})
	})
}

// assertAtomicWritePreserves runs write with dir made read-only, asserting the
// write fails, the file at path is byte-identical to before the attempt, and no
// staging temp file was left in dir.
func assertAtomicWritePreserves(t *testing.T, dir, path string, write func() error) {
	t.Helper()
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	require.Error(t, write(), "a write into a read-only dir must fail")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the prior record survives a write that could not complete")
	assert.Empty(t, stagedTempFiles(t, dir), "a failed write leaves no staging temp behind")
}

// ── Batch 1b: the gratitude and registry paths reject a traversal key ───────

// TestGratitudePath_RejectsTraversalKey: a separator-bearing or empty gratitude
// key is refused on both read and write, before any filesystem access — closing
// the `lucid gratitude add --into ../../..` read-oracle.
func TestGratitudePath_RejectsTraversalKey(t *testing.T) {
	a := newObsStore(t)
	for _, bad := range []string{"", "../escape", "a/b", `a\b`} {
		_, _, err := a.ReadGratitude(bad)
		require.Error(t, err, "ReadGratitude(%q) must reject", bad)
		assert.Contains(t, err.Error(), "invalid gratitude key")

		ev := observations.GratitudeEvent{
			Type: observations.GratitudeEventOccurrence, Date: "2026-07-02",
			Source: observations.GratitudeSourceGratitude,
		}
		_, _, werr := a.AppendGratitudeEvent(bad, "x", "2026-07-02", true, ev, mergeNow())
		require.Error(t, werr, "AppendGratitudeEvent(%q) must reject before any write", bad)
	}
}

// TestRegistryPath_RejectsTraversalKey: a separator-bearing or empty registry
// key is refused on both read and write — closing the `lucid link --to
// injury:../../../foo` read-oracle.
func TestRegistryPath_RejectsTraversalKey(t *testing.T) {
	a := newObsStore(t)
	for _, bad := range []string{"", "../escape", "a/b", `a\b`} {
		_, _, err := a.ReadRegistry(observations.RegistryInjury, bad)
		require.Error(t, err, "ReadRegistry(%q) must reject", bad)
		assert.Contains(t, err.Error(), "invalid registry key")

		_, werr := a.UpdateRegistry(observations.RegistryInjury, bad,
			observations.RegistryPatch{DisplayName: "x", At: "2026-07-02T09:00:00Z"})
		require.Error(t, werr, "UpdateRegistry(%q) must reject before any write", bad)
	}
}

// ── Batch 1c: the day-file paths reject a malformed logical_date ────────────

// TestDayPaths_RejectMalformedDate: a logical_date that is not a real three-part
// YYYY-MM-DD can never build a day-file path outside the record tree, and the
// rejection surfaces through the append and read entry points, not just the raw
// helper (defense-in-depth for the storage day path).
func TestDayPaths_RejectMalformedDate(t *testing.T) {
	a := newObsStore(t)
	require.NoError(t, a.ScaffoldFocus())
	require.NoError(t, a.ScaffoldReframes())

	for _, bad := range []string{"", "2026", "2026-07", "../../etc", "2026-07-02/../x", `2026\07\02`} {
		_, oerr := a.obsDayPath(bad)
		require.Error(t, oerr, "obsDayPath(%q)", bad)
		_, ferr := a.focusDayPath(bad)
		require.Error(t, ferr, "focusDayPath(%q)", bad)
		_, rerr := a.reframeDayPath(bad)
		require.Error(t, rerr, "reframeDayPath(%q)", bad)
	}

	_, aerr := a.AppendObservation(observations.Event{
		Schema: observations.Schema, Kind: observations.KindMood,
		RecordedAt: "x", OccurredAt: "x", OccurredAtPrecision: observations.PrecisionExact,
		LogicalDate: "../../etc", Source: observations.SourceMicrolog,
	})
	require.Error(t, aerr, "AppendObservation must reject a malformed logical_date before any write")
	_, _, rderr := a.ReadObservationsDay("../../etc")
	require.Error(t, rderr, "ReadObservationsDay must reject a malformed date before any read")
}

// ── Batch 1d: the two wrapped write errors carry the storage: prefix ────────

// TestAppendProposal_WriteError_HasStoragePrefix: a failed proposal append
// surfaces with the package's "storage: write <label>" shape, matching its
// sibling writers rather than returning the raw os error.
func TestAppendProposal_WriteError_HasStoragePrefix(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a := New(t.TempDir())
	id, path := seedProcessed(t, a)
	require.NoError(t, os.Chmod(path, 0o400)) // read-only target: os.WriteFile's truncating open fails
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	err := a.AppendRejectedProposal(id, RejectedProposal{ShapeTag: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage: write proposals")
}

// TestRewriteInsight_WriteError_HasStoragePrefix: a failed recall-status rewrite
// surfaces with the "storage: write insight" shape rather than the raw os error.
func TestRewriteInsight_WriteError_HasStoragePrefix(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	a := New(t.TempDir())
	id := writeAcceptedInsight(t, a, insightNow(), "When M. is in the room, I test an idea once and back off.")
	path, err := a.insightPath(id)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0o400))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	err = a.UpdateInsightStatus(id, RecallConfirmed, insightNow())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage: write insight")
}
