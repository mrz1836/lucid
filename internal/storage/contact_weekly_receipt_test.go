package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContactWeeklyReceipt_RoundTripAndAbsent: a Monday post that has never
// fired reads (zero, false, nil) without creating anything; a written receipt
// round-trips whole at contacts/weekly_receipt.json; a second week overwrites
// the first (it is not a history) and leaves no temp residue; and a corrupt
// body is a loud error, not an empty receipt.
func TestContactWeeklyReceipt_RoundTripAndAbsent(t *testing.T) {
	home := t.TempDir()
	a := New(home)

	_, ok, err := a.ReadContactWeeklyReceipt()
	require.NoError(t, err)
	assert.False(t, ok, "a never-fired post has no receipt")
	assert.NoDirExists(t, filepath.Join(home, "contacts"), "a read creates nothing")

	want := ContactWeeklyReceipt{
		Week:        "2026-W45",
		MessageID:   "msg-123",
		ChannelID:   "user",
		Verified:    true,
		DeliveredAt: "2026-11-02T09:30:00-05:00",
	}
	require.NoError(t, a.WriteContactWeeklyReceipt(want), "a bare run creates contacts/ itself")
	assert.FileExists(t, filepath.Join(home, "contacts", "weekly_receipt.json"))

	got, ok, err := a.ReadContactWeeklyReceipt()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, want, got)

	require.NoError(t, a.WriteContactWeeklyReceipt(ContactWeeklyReceipt{Week: "2026-W46", MessageID: "msg-456"}))
	got, ok, err = a.ReadContactWeeklyReceipt()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "2026-W46", got.Week, "the latest write wins")
	assert.Equal(t, "msg-456", got.MessageID)
	assert.False(t, got.Verified)

	entries, err := os.ReadDir(filepath.Join(home, "contacts"))
	require.NoError(t, err)
	require.Len(t, entries, 1, "the atomic write leaves no temp file behind")
	assert.Equal(t, "weekly_receipt.json", entries[0].Name())

	events, err := a.ReadContactEvents()
	require.NoError(t, err)
	assert.Empty(t, events, "the receipt is not a contact event")

	require.NoError(t, os.WriteFile(filepath.Join(home, "contacts", "weekly_receipt.json"), []byte("{not json"), 0o600))
	_, _, err = a.ReadContactWeeklyReceipt()
	require.Error(t, err, "a corrupt receipt surfaces, never reads as absent")
}
