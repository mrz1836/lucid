package router

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// foldedMemory reads every memory event, folds the amendments, and returns the
// current (folded) state of one base story — the effective values every read
// surface will show after an amend.
func foldedMemory(t *testing.T, r *Router, id string) observations.Event {
	t.Helper()
	evs, err := r.store.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)
	for _, e := range observations.FoldMemoryAmendments(evs) {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("memory %s not found after fold", id)
	return observations.Event{}
}

// readEventByID reads one event by id from its logical day.
func readEventByID(t *testing.T, r *Router, date, id string) observations.Event {
	t.Helper()
	evs, _, err := r.store.ReadObservationsDay(date)
	require.NoError(t, err)
	for _, e := range evs {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("event %s not found in %s", id, date)
	return observations.Event{}
}

// seedMemory writes one base story and returns its capture result.
func seedMemory(t *testing.T, r *Router, req MemoryWriteRequest) MemoryWriteResult {
	t.Helper()
	if req.Now.IsZero() {
		req.Now = fixedNow()
	}
	res, err := r.WriteMemory(req)
	require.NoError(t, err)
	require.NotEmpty(t, res.EventID)
	return res
}

// TestAmendMemory_EachFieldIndividually proves a single-field amend folds onto
// the base story field-by-field, leaving the rest of the story intact.
func TestAmendMemory_EachFieldIndividually(t *testing.T) {
	t.Run("certainty", func(t *testing.T) {
		r, _, _ := bootedMemoryRouter(t)
		mem := seedMemory(t, r, MemoryWriteRequest{Text: "the drive to the coast", Certainty: "hazy"})

		_, err := r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, Certainty: "vivid", CertaintyChanged: true, Now: fixedNow()})
		require.NoError(t, err)

		folded := foldedMemory(t, r, mem.EventID)
		assert.Equal(t, "vivid", folded.Payload[observations.MemoryFieldCertainty], "certainty folds to the amended value")
		assert.Equal(t, "the drive to the coast", folded.Payload[observations.MemoryFieldText], "text is untouched")
	})

	t.Run("body text", func(t *testing.T) {
		r, _, _ := bootedMemoryRouter(t)
		mem := seedMemory(t, r, MemoryWriteRequest{Text: "old text"})

		_, err := r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, Body: "corrected text", BodyChanged: true, Now: fixedNow()})
		require.NoError(t, err)

		assert.Equal(t, "corrected text", foldedMemory(t, r, mem.EventID).Payload[observations.MemoryFieldText])
	})

	t.Run("followup", func(t *testing.T) {
		r, _, _ := bootedMemoryRouter(t)
		mem := seedMemory(t, r, MemoryWriteRequest{Text: "a memory"})

		_, err := r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, Followup: "ask about the boat", FollowupChanged: true, Now: fixedNow()})
		require.NoError(t, err)

		assert.Equal(t, "ask about the boat", foldedMemory(t, r, mem.EventID).Payload[observations.MemoryFieldFollowUp])
	})
}

// TestAmendMemory_MultipleFieldsInOneCall proves one amend can carry several
// changed fields at once, all folding onto the base.
func TestAmendMemory_MultipleFieldsInOneCall(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)
	era, err := r.WriteEra(EraWriteRequest{Name: "wild summer", Now: fixedNow()})
	require.NoError(t, err)
	mem := seedMemory(t, r, MemoryWriteRequest{Text: "the night we drove", Certainty: "reconstructed"})

	_, err = r.AmendMemory(AmendMemoryRequest{
		ObsID:            mem.EventID,
		Certainty:        "vivid",
		CertaintyChanged: true,
		Followup:         "who drove?",
		FollowupChanged:  true,
		Era:              era.Key,
		EraChanged:       true,
		Now:              fixedNow(),
	})
	require.NoError(t, err)

	folded := foldedMemory(t, r, mem.EventID)
	assert.Equal(t, "vivid", folded.Payload[observations.MemoryFieldCertainty])
	assert.Equal(t, "who drove?", folded.Payload[observations.MemoryFieldFollowUp])
	assert.Equal(t, era.Key, folded.Refs[observations.RefEra])
}

// TestAmendMemory_EraRefileToLaterMintedEra proves the headline use case: file a
// story under a chapter minted *after* it was created. The era key takes the same
// token create stores, validated for existence.
func TestAmendMemory_EraRefileToLaterMintedEra(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)
	// The memory exists first, with no era.
	mem := seedMemory(t, r, MemoryWriteRequest{Text: "sometime that autumn", Day: "2014-09-01"})
	assert.NotContains(t, readMemoryEvent(t, r.store, mem).Refs, "era")

	// The chapter is minted later, then the memory is re-filed under it.
	era, err := r.WriteEra(EraWriteRequest{Name: "the Lisbon years", Start: "2014", Now: fixedNow()})
	require.NoError(t, err)

	_, err = r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, Era: era.Key, EraChanged: true, Now: fixedNow()})
	require.NoError(t, err)

	assert.Equal(t, era.Key, foldedMemory(t, r, mem.EventID).Refs[observations.RefEra], "the story is now filed under the later-minted era")
}

// TestAmendMemory_ClearFollowup proves the explicit clear removes the follow-up
// field on the folded story (distinct from setting it empty).
func TestAmendMemory_ClearFollowup(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)
	mem := seedMemory(t, r, MemoryWriteRequest{Text: "a memory", FollowUp: "the old thread"})
	require.Equal(t, "the old thread", readMemoryEvent(t, r.store, mem).Payload[observations.MemoryFieldFollowUp])

	_, err := r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, ClearFollowup: true, Now: fixedNow()})
	require.NoError(t, err)

	assert.NotContains(t, foldedMemory(t, r, mem.EventID).Payload, observations.MemoryFieldFollowUp, "the follow-up is cleared on read")
}

// TestAmendMemory_RejectsUnknownID proves an id that names no memory errors with
// "not found" and appends nothing.
func TestAmendMemory_RejectsUnknownID(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)

	_, err := r.AmendMemory(AmendMemoryRequest{ObsID: "obs_2099_01_01_001", Certainty: "vivid", CertaintyChanged: true, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "nothing was saved")

	mems, err := a.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)
	assert.Empty(t, mems, "a rejected amend leaves nothing on disk")
}

// TestAmendMemory_RejectsMalformedID proves an unparseable id is refused the same
// way (there is no memory to amend), writing nothing.
func TestAmendMemory_RejectsMalformedID(t *testing.T) {
	r, _, _ := bootedMemoryRouter(t)

	_, err := r.AmendMemory(AmendMemoryRequest{ObsID: "not-an-obs-id", Certainty: "vivid", CertaintyChanged: true, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestAmendMemory_RejectsNonMemoryID proves an id that names an event of another
// kind is not amendable and writes nothing.
func TestAmendMemory_RejectsNonMemoryID(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)

	pain, err := a.AppendObservation(observations.Event{
		Schema:              observations.Schema,
		Kind:                observations.KindPain,
		RecordedAt:          fixedNow().Format(time.RFC3339),
		OccurredAt:          fixedNow().Format(time.RFC3339),
		OccurredAtPrecision: observations.PrecisionExact,
		LogicalDate:         "2026-07-05",
		Source:              observations.SourceMicrolog,
		Payload:             map[string]any{"note": "left knee"},
	})
	require.NoError(t, err)

	_, err = r.AmendMemory(AmendMemoryRequest{ObsID: pain.ID, Certainty: "vivid", CertaintyChanged: true, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// The pain event is the only event on that day and is untouched.
	mems, err := a.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)
	assert.Empty(t, mems)
}

// TestAmendMemory_RejectsAmendmentID proves a correction-event id is not an
// amendable base — a correction-of-a-correction the fold would never apply is
// refused, and nothing new is written.
func TestAmendMemory_RejectsAmendmentID(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)
	mem := seedMemory(t, r, MemoryWriteRequest{Text: "a memory"})

	first, err := r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, Certainty: "vivid", CertaintyChanged: true, Now: fixedNow()})
	require.NoError(t, err)

	before, err := a.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)

	_, err = r.AmendMemory(AmendMemoryRequest{ObsID: first.EventID, Certainty: "hazy", CertaintyChanged: true, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amendment")

	after, err := a.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)
	assert.Len(t, after, len(before), "amending an amendment writes nothing")
}

// TestAmendMemory_NoFieldsIsNoOp proves an amend with no changed fields is a
// documented error that writes nothing.
func TestAmendMemory_NoFieldsIsNoOp(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)
	mem := seedMemory(t, r, MemoryWriteRequest{Text: "a memory"})

	_, err := r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no fields to amend")

	mems, err := a.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)
	assert.Len(t, mems, 1, "a no-op amend appends nothing")
}

// TestAmendMemory_RejectsBadCertainty proves an out-of-enum certainty errors with
// the create-path wording and writes nothing.
func TestAmendMemory_RejectsBadCertainty(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)
	mem := seedMemory(t, r, MemoryWriteRequest{Text: "a memory"})

	_, err := r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, Certainty: "sure", CertaintyChanged: true, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown certainty")
	assert.Contains(t, err.Error(), "nothing was saved")

	mems, err := a.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)
	assert.Len(t, mems, 1, "a rejected certainty appends nothing")
}

// TestAmendMemory_RejectsMissingEra proves an era key that resolves to no chapter
// errors clearly ("era ... not found") and writes nothing — the amend-only
// existence check.
func TestAmendMemory_RejectsMissingEra(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)
	mem := seedMemory(t, r, MemoryWriteRequest{Text: "a memory"})

	_, err := r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, Era: "era_does-not-exist", EraChanged: true, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "era")
	assert.Contains(t, err.Error(), "not found")

	mems, err := a.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)
	assert.Len(t, mems, 1, "a missing era appends nothing")
}

// TestAmendMemory_RejectsSetAndClearFollowup proves setting and clearing the
// follow-up in one call is contradictory and writes nothing.
func TestAmendMemory_RejectsSetAndClearFollowup(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)
	mem := seedMemory(t, r, MemoryWriteRequest{Text: "a memory"})

	_, err := r.AmendMemory(AmendMemoryRequest{
		ObsID: mem.EventID, Followup: "x", FollowupChanged: true, ClearFollowup: true, Now: fixedNow(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")

	mems, err := a.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)
	assert.Len(t, mems, 1)
}

// TestAmendMemory_RejectsAmbiguousEmptyFollowup proves a changed-but-empty
// --followup is ambiguous (set-empty vs clear) and is rejected, pointing at
// --clear-followup, with nothing written.
func TestAmendMemory_RejectsAmbiguousEmptyFollowup(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)
	mem := seedMemory(t, r, MemoryWriteRequest{Text: "a memory"})

	_, err := r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, Followup: "", FollowupChanged: true, Now: fixedNow()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clear-followup")

	mems, err := a.ReadObservationsKind(observations.KindMemory)
	require.NoError(t, err)
	assert.Len(t, mems, 1)
}

// TestAmendMemory_OriginalLineByteIdentical proves the base story's line is never
// rewritten: after an amend the day file still begins with the original bytes,
// and the amendment files in the target's own day.
func TestAmendMemory_OriginalLineByteIdentical(t *testing.T) {
	r, _, home := bootedMemoryRouter(t)
	mem := seedMemory(t, r, MemoryWriteRequest{Text: "the summer everything changed", Certainty: "hazy", Day: "1999-06-01"})
	before := readDayFileBytes(t, home, "1999-06-01")

	res, err := r.AmendMemory(AmendMemoryRequest{ObsID: mem.EventID, Certainty: "vivid", CertaintyChanged: true, Now: fixedNow()})
	require.NoError(t, err)
	assert.Equal(t, "1999-06-01", res.LogicalDate, "the amendment files under the target's own day")

	after := readDayFileBytes(t, home, "1999-06-01")
	assert.True(t, bytes.HasPrefix(after, before), "the original line stays byte-identical — the amendment is a new appended line")
	assert.Greater(t, len(after), len(before), "the amendment adds a new line")
}

// TestAmendMemory_AmendmentCopiesOccurrenceAndSource proves the amendment reuses
// the target's occurrence stamp (occurred_at, precision, and a range end), carries
// its own recorded_at, and is sourced as excavation like the base story.
func TestAmendMemory_AmendmentCopiesOccurrenceAndSource(t *testing.T) {
	r, a, _ := bootedMemoryRouter(t)

	// A synthetic range-precision base memory exercises the occurred_at_end copy
	// (the capture path never mints a range memory).
	end := "2010-07-20T00:00:00Z"
	base, err := a.AppendObservation(observations.Event{
		Schema:              observations.Schema,
		Kind:                observations.KindMemory,
		RecordedAt:          fixedNow().Format(time.RFC3339),
		OccurredAt:          "2010-07-15T00:00:00Z",
		OccurredAtPrecision: observations.PrecisionRange,
		OccurredAtEnd:       &end,
		LogicalDate:         "2010-07-15",
		Source:              observations.SourceExcavation,
		Payload:             map[string]any{observations.MemoryFieldText: "the trip"},
	})
	require.NoError(t, err)

	amendAt := fixedNow().Add(48 * time.Hour)
	res, err := r.AmendMemory(AmendMemoryRequest{ObsID: base.ID, Certainty: "vivid", CertaintyChanged: true, Now: amendAt})
	require.NoError(t, err)

	amendment := readEventByID(t, r, "2010-07-15", res.EventID)
	assert.Equal(t, observations.KindMemory, amendment.Kind)
	assert.Equal(t, observations.SourceExcavation, amendment.Source, "an amendment carries the same excavation provenance as the base")
	assert.Equal(t, base.OccurredAt, amendment.OccurredAt, "the amendment reuses the target's occurred_at")
	assert.Equal(t, observations.PrecisionRange, amendment.OccurredAtPrecision, "the amendment copies the target's precision")
	require.NotNil(t, amendment.OccurredAtEnd)
	assert.Equal(t, end, *amendment.OccurredAtEnd, "the amendment copies the target's range end")
	assert.Equal(t, base.ID, amendment.Refs[observations.RefCorrects], "refs.corrects keys the amendment to its base")
	assert.Equal(t, amendAt.Format(time.RFC3339), amendment.RecordedAt, "the amendment carries its own recorded_at timestamp")
}
