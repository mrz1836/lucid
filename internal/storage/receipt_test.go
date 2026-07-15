package storage

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompanionReceipt_MissingIsNotFound: a window that has never fired reads as
// (zero, false, nil) so the companion fires fresh rather than erroring.
func TestCompanionReceipt_MissingIsNotFound(t *testing.T) {
	a := newEngineAdapter(t)
	got, found, err := a.ReadCompanionReceipt("morning")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, CompanionReceipt{}, got)
}

// TestCompanionReceipt_RoundTrip: a written receipt reads back byte-identical,
// and rewriting the same window overwrites (one receipt per window, not history).
func TestCompanionReceipt_RoundTrip(t *testing.T) {
	a := newEngineAdapter(t)
	want := CompanionReceipt{
		Date:        "2026-07-14",
		Window:      "morning",
		MessageID:   "1526225086254682172",
		ChannelID:   "1525218650058129571",
		Verified:    true,
		DeliveredAt: "2026-07-14T06:00:11-04:00",
	}
	require.NoError(t, a.WriteCompanionReceipt(want))

	got, found, err := a.ReadCompanionReceipt("morning")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, want, got)

	// The night window is independent — it has its own receipt file.
	_, found, err = a.ReadCompanionReceipt("night")
	require.NoError(t, err)
	assert.False(t, found)

	// Rewriting morning overwrites rather than appending a second record.
	next := want
	next.Date = "2026-07-15"
	next.MessageID = "1600000000000000000"
	require.NoError(t, a.WriteCompanionReceipt(next))
	got, _, err = a.ReadCompanionReceipt("morning")
	require.NoError(t, err)
	assert.Equal(t, next, got)
}

// TestCompanionReceipt_InvalidWindowRejected: an empty or path-bearing window is
// rejected on both read and write so a receipt can never escape the engine tree.
func TestCompanionReceipt_InvalidWindowRejected(t *testing.T) {
	a := newEngineAdapter(t)
	for _, w := range []string{"", "../evil", "a/b", `a\b`} {
		_, _, err := a.ReadCompanionReceipt(w)
		require.Errorf(t, err, "read window %q should be rejected", w)
		require.Errorf(t, a.WriteCompanionReceipt(CompanionReceipt{Window: w}), "write window %q should be rejected", w)
	}
}

// TestCompanionReceipt_CorruptErrors: a malformed receipt file surfaces a parse
// error rather than silently reading as a fresh (never-delivered) window.
func TestCompanionReceipt_CorruptErrors(t *testing.T) {
	a := newEngineAdapter(t)
	require.NoError(t, os.MkdirAll(a.companionDir(), dirPerm))
	path, err := a.companionReceiptPath("morning")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("{bad"), 0o600))

	_, _, err = a.ReadCompanionReceipt("morning")
	assert.Error(t, err)
}

// TestCompanionReceipt_WriteCreatesDir: the first write creates
// engine/companion/ even if the scaffold never made it.
func TestCompanionReceipt_WriteCreatesDir(t *testing.T) {
	a := newEngineAdapter(t)
	_, statErr := os.Stat(a.companionDir())
	require.True(t, os.IsNotExist(statErr), "companion dir should not exist before the first write")

	require.NoError(t, a.WriteCompanionReceipt(CompanionReceipt{Window: "night", Date: "2026-07-14"}))
	info, err := os.Stat(a.companionDir())
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

// TestCompanionReceipt_WriteFailureSurfaced: a read-only engine tree fails the
// receipt write rather than silently dropping it.
func TestCompanionReceipt_WriteFailureSurfaced(t *testing.T) {
	skipIfRoot(t)
	a := newEngineAdapter(t)
	require.NoError(t, os.Chmod(a.engineDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(a.engineDir(), 0o700) })
	assert.Error(t, a.WriteCompanionReceipt(CompanionReceipt{Window: "morning", Date: "2026-07-14"}))
}
