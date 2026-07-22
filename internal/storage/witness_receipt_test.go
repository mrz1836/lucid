package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWitnessReportReceipt_MissingIsZero: a Ledger whose weekly report has never
// fired reads (zero, false, nil) — the cue to fire fresh — and a written receipt
// round-trips whole.
func TestWitnessReportReceipt_MissingIsZero(t *testing.T) {
	a := newEngineAdapter(t)

	_, ok, err := a.ReadWitnessReportReceipt()
	require.NoError(t, err)
	assert.False(t, ok, "a never-fired weekly report has no receipt")

	want := WitnessReportReceipt{
		Week:        "2026-W29",
		MessageID:   "msg-123",
		ChannelID:   "witness",
		Verified:    true,
		DeliveredAt: "2026-07-13T09:00:00Z",
	}
	require.NoError(t, a.WriteWitnessReportReceipt(want))

	got, ok, err := a.ReadWitnessReportReceipt()
	require.NoError(t, err)
	require.True(t, ok, "a written receipt reads back")
	assert.Equal(t, want, got)
}

// TestWitnessReportReceipt_OverwritesNotAppends: writing a second week's receipt
// overwrites the first — the file is the last-delivery memory, never a history.
func TestWitnessReportReceipt_OverwritesNotAppends(t *testing.T) {
	a := newEngineAdapter(t)

	require.NoError(t, a.WriteWitnessReportReceipt(WitnessReportReceipt{Week: "2026-W29", MessageID: "old"}))
	require.NoError(t, a.WriteWitnessReportReceipt(WitnessReportReceipt{Week: "2026-W30", MessageID: "new"}))

	got, ok, err := a.ReadWitnessReportReceipt()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "2026-W30", got.Week, "the latest write wins")
	assert.Equal(t, "new", got.MessageID)
}
