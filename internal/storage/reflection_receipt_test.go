package storage

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReflectionReceipt_MissingIsZeroAndCreatesNothing is the load-bearing case:
// the weekly deep-dive reads this cursor on every invocation and is read-only by
// contract, so a miss must return (zero, false, nil) AND leave the tree
// untouched. Asserting the return values alone would pass for a reader that
// silently mkdir'd engine/reflection/ on the way past, which would break the
// read-only guarantee the moment the deep-dive wired this in — so the directory's
// absence is asserted on disk.
func TestReflectionReceipt_MissingIsZeroAndCreatesNothing(t *testing.T) {
	a := newEngineAdapter(t)

	got, ok, err := a.ReadReflectionReceipt()
	require.NoError(t, err, "a never-closed reflection is not an error")
	assert.False(t, ok, "a Ledger with no closed reflection has no cursor")
	assert.Equal(t, ReflectionReceipt{}, got)

	_, statErr := os.Stat(a.reflectionDir())
	assert.True(t, os.IsNotExist(statErr), "reading the cursor must create no directory")
	_, statErr = os.Stat(a.reflectionReceiptPath())
	assert.True(t, os.IsNotExist(statErr), "reading the cursor must create no file")
}

// TestReflectionReceipt_RoundTrip proves a stamped cursor survives whole, and
// that the write creates engine/reflection/ lazily rather than depending on the
// scaffold having reserved it.
func TestReflectionReceipt_RoundTrip(t *testing.T) {
	a := newEngineAdapter(t)

	want := ReflectionReceipt{
		ReflectedThrough: "2026-07-26T20:00:00Z",
		Source:           "close",
		WrittenAt:        "2026-07-26T20:00:00Z",
	}
	require.NoError(t, a.WriteReflectionReceipt(want))

	got, ok, err := a.ReadReflectionReceipt()
	require.NoError(t, err)
	require.True(t, ok, "a written cursor reads back")
	assert.Equal(t, want, got)
}

// TestReflectionReceipt_OverwritesNotAppends: the file is a cursor, not a
// history. A second close replaces the first, the same discipline the witness
// receipt follows — otherwise the window's start would depend on parsing a
// growing log.
func TestReflectionReceipt_OverwritesNotAppends(t *testing.T) {
	a := newEngineAdapter(t)

	require.NoError(t, a.WriteReflectionReceipt(ReflectionReceipt{
		ReflectedThrough: "2026-07-19T20:00:00Z",
		Source:           "close",
	}))
	require.NoError(t, a.WriteReflectionReceipt(ReflectionReceipt{
		ReflectedThrough: "2026-07-26T21:30:00Z",
		Source:           "apply",
	}))

	got, ok, err := a.ReadReflectionReceipt()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "2026-07-26T21:30:00Z", got.ReflectedThrough, "the latest write wins")
	assert.Equal(t, "apply", got.Source)
}

// TestReflectionReceipt_CorruptBodySurfaces: a hand-mangled cursor is a parse
// error, not a silent reset. Resetting would hand the caller a first-run window
// and re-read the entire corpus without saying why.
func TestReflectionReceipt_CorruptBodySurfaces(t *testing.T) {
	a := newEngineAdapter(t)

	require.NoError(t, ensureDir(a.reflectionDir(), "reflection"))
	require.NoError(t, os.WriteFile(a.reflectionReceiptPath(), []byte("{"), filePerm))

	_, ok, err := a.ReadReflectionReceipt()
	require.Error(t, err, "a corrupt cursor surfaces rather than silently resetting")
	assert.False(t, ok)
}
