package storage

import (
	"os"
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

// TestAnchorDetailPartsReceiptAndLegacyReads: a multi-message weekly report's
// frozen parts — payload, message id, read-back — round-trip whole through the
// receipt and report their progress; and a legacy single-message receipt (as
// written before parts existed, byte for byte) still reads, with no parts and
// nothing pending.
func TestAnchorDetailPartsReceiptAndLegacyReads(t *testing.T) {
	a := newEngineAdapter(t)

	pending := WitnessReportReceipt{
		Week:      "2026-W29",
		ChannelID: "user",
		Parts: []WitnessReportPart{
			{
				Embed: WitnessReportEmbed{
					Title: "Weekly witness report · Week 2026-W29", Description: "A steady week.", Color: 0x57F287,
					Fields: []WitnessReportField{{Name: "⛓️ Streak & adherence", Value: "7-day streak", Inline: true}},
					Footer: "footer",
				},
				MessageID: "msg-1", Verified: true,
			},
			{Embed: WitnessReportEmbed{Title: "⚓ Anchor detail · part 2 of 3", Fields: []WitnessReportField{{Name: "⚓ Anchor detail · 4", Value: "• sobriety (quit) — 2026-07-08 · slip · line one\nline two 🌊"}}}, MessageID: "msg-2"},
			{Embed: WitnessReportEmbed{Title: "⚓ Anchor detail · part 3 of 3"}},
		},
	}
	require.NoError(t, a.WriteWitnessReportReceipt(pending))
	got, ok, err := a.ReadWitnessReportReceipt()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, pending, got, "the frozen batch round-trips whole")
	assert.True(t, got.Pending(), "a batch not yet fully verified is pending")
	assert.Equal(t, 1, got.PartsVerified())

	done := got
	for i := range done.Parts {
		done.Parts[i].Verified = true
	}
	done.MessageID, done.Verified = "msg-1", true
	require.NoError(t, a.WriteWitnessReportReceipt(done))
	got, _, err = a.ReadWitnessReportReceipt()
	require.NoError(t, err)
	assert.False(t, got.Pending(), "every part verified and the week marked is delivered")
	assert.Equal(t, 3, got.PartsVerified())

	legacy := []byte(`{
  "week": "2026-W28",
  "message_id": "msg-legacy",
  "channel_id": "witness",
  "verified": true,
  "delivered_at": "2026-07-06T09:00:00Z"
}
`)
	require.NoError(t, os.WriteFile(a.witnessReportReceiptPath(), legacy, 0o600))
	got, ok, err = a.ReadWitnessReportReceipt()
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, WitnessReportReceipt{
		Week: "2026-W28", MessageID: "msg-legacy", ChannelID: "witness", Verified: true, DeliveredAt: "2026-07-06T09:00:00Z",
	}, got)
	assert.Nil(t, got.Parts)
	assert.False(t, got.Pending(), "a legacy receipt is a finished single-message delivery")

	raw, err := marshalJSON(got)
	require.NoError(t, err)
	assert.Equal(t, string(legacy), string(raw), "a single-message receipt still writes its legacy shape")
}
