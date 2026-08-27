package router

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/retro"
)

// bootedRetro returns a booted router over a fresh scaffolded Ledger (day1/day2/
// edt live in reframe_test.go — same package).
func bootedRetro(t *testing.T) *Router {
	t.Helper()
	r := New(newScaffolded(t))
	_, err := r.Boot()
	require.NoError(t, err)
	return r
}

// TestRetroParkAppendsAndReturnsReceipt: park appends one event and returns a
// unique per-write receipt (retro_event_…) distinct from the parked-item id; a
// second park returns a different receipt and the next R-NNN (AC-3).
func TestRetroParkAppendsAndReturnsReceipt(t *testing.T) {
	r := bootedRetro(t)

	first, err := r.ParkRetro(ParkRetroRequest{Item: "Revisit the Sunday walk opener", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "R-001", first.View.ID)
	assert.True(t, strings.HasPrefix(first.View.EventID, "retro_event_"), "the receipt is a per-write id")
	assert.NotEqual(t, first.View.ID, first.View.EventID, "the parked-item id and the receipt are distinct identities")
	assert.Equal(t, retro.StatusOpen, first.View.Item.Status)
	assert.Equal(t, "Revisit the Sunday walk opener", first.View.Item.Item)
	assert.Contains(t, first.Ack, "R-001")
	assert.Contains(t, first.Ack, first.View.EventID, "the ack names both the item id and the receipt")

	second, err := r.ParkRetro(ParkRetroRequest{Item: "Try a shorter Gate cadence", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "R-002", second.View.ID, "the next park mints the next global id")
	assert.NotEqual(t, first.View.EventID, second.View.EventID, "each write returns a fresh receipt")
}

// TestRetroParkReturnsRID: park mints and returns a real R-NNN id (the echo
// contract holds structurally); an empty item is a loud failure that parks
// nothing (AC-4).
func TestRetroParkReturnsRID(t *testing.T) {
	r := bootedRetro(t)

	res, err := r.ParkRetro(ParkRetroRequest{Item: "Experiment with a two-column weekly layout", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "R-001", res.View.ID, "park returns the minted R-NNN")
	seq, ok := retro.ParseSeq(res.View.ID)
	assert.True(t, ok, "the returned id is a well-formed R-NNN")
	assert.Equal(t, 1, seq)

	// An empty item fails loudly and parks nothing — no id is echoed.
	_, err = r.ParkRetro(ParkRetroRequest{Item: "   ", Now: day1()})
	require.Error(t, err, "an empty item is a loud failure, never a silent no-op")

	// The failed park did not advance the counter: the next real park is still R-002.
	next, err := r.ParkRetro(ParkRetroRequest{Item: "A second real item", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "R-002", next.View.ID)
}

// TestRetroPark_SourceAndBackdating: --source is stored verbatim (default retro),
// and a strict --day backdates the parked date; a bad token parks nothing.
func TestRetroPark_SourceAndBackdating(t *testing.T) {
	r := bootedRetro(t)

	res, err := r.ParkRetro(ParkRetroRequest{Item: "From chat", Source: "chat", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "chat", res.View.Item.Source)

	// No --source defaults to the documented `retro` provenance.
	def, err := r.ParkRetro(ParkRetroRequest{Item: "Default source", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, retro.SourceRetro, def.View.Item.Source)

	// --day backdates the parked date to a literal civil day.
	back, err := r.ParkRetro(ParkRetroRequest{Item: "Backdated", DayArg: "2026-06-01", Now: day1()})
	require.NoError(t, err)
	assert.Equal(t, "2026-06-01", back.View.Item.ParkedDate)

	// A --day the grammar cannot read is a clean refusal that parks nothing.
	_, err = r.ParkRetro(ParkRetroRequest{Item: "Bad day", DayArg: "@yesterdya", Now: day1()})
	require.Error(t, err)
	var refused *DayRejectedError
	assert.ErrorAs(t, err, &refused, "a bad --day is a DayRejectedError")
}
