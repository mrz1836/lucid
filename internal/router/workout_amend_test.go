package router

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// seedWorkout logs one session through the structured path and returns its
// result, defaulting Now to the shared EDT fixture instant.
func seedWorkout(t *testing.T, r *Router, req WorkoutLogRequest) WorkoutLogResult {
	t.Helper()
	if req.Now.IsZero() {
		req.Now = nowEDT()
	}
	res, err := r.WorkoutLog(req)
	require.NoError(t, err)
	require.NotEmpty(t, res.WorkoutID)
	return res
}

// foldedWorkout reads every workout event, folds the corrections, and returns
// the current (folded) state of one base session — what every reader sees.
func foldedWorkout(t *testing.T, r *Router, id string) observations.Event {
	t.Helper()
	evs, err := r.Store().ReadObservationsKind(observations.KindWorkout)
	require.NoError(t, err)
	for _, e := range observations.FoldWorkoutAmendments(evs) {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("workout %s not found after fold", id)
	return observations.Event{}
}

// countFoldedWorkouts reports how many sessions the folded workout read holds,
// so a test can prove an amend never adds a second session.
func countFoldedWorkouts(t *testing.T, r *Router) int {
	t.Helper()
	evs, err := r.Store().ReadObservationsKind(observations.KindWorkout)
	require.NoError(t, err)
	return len(observations.FoldWorkoutAmendments(evs))
}

// obsSnapshot returns the bytes of every observation day file under the Ledger,
// keyed by path, so a refusal test can assert the Ledger is byte-identical
// before and after — "nothing was saved" proven on disk, not inferred.
func obsSnapshot(t *testing.T, r *Router) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(r.Store().Home(), "observations")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[path] = string(b)
		return nil
	})
	require.NoError(t, err)
	return out
}

// TestAmendWorkout_AppendsOneCorrection: an amend appends exactly one new
// workout event whose refs.corrects names the session, carrying only the
// changed field and a nanosecond recorded_at; the session's own event is
// untouched and the result reports the change and both ids.
func TestAmendWorkout_AppendsOneCorrection(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	base := seedWorkout(t, r, WorkoutLogRequest{Type: "climbing", DurationMin: 60})
	original := eventByID(t, r, base.LogicalDate, base.WorkoutID)

	before, err := r.Store().ReadObservationsKind(observations.KindWorkout)
	require.NoError(t, err)

	now := nowEDT().Add(30*time.Minute + 123456789*time.Nanosecond)
	res, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: base.WorkoutID, RPE: 4, RPEChanged: true, Now: now})
	require.NoError(t, err)
	assert.False(t, res.Rejected)
	assert.Equal(t, base.WorkoutID, res.TargetID)
	assert.NotEqual(t, base.WorkoutID, res.EventID, "the correction gets its own id")
	assert.Equal(t, base.LogicalDate, res.LogicalDate, "a plain amend files under the session's day")
	assert.Equal(t, base.WorkoutID, res.Refs[observations.RefCorrects])
	assert.NotContains(t, res.Refs, observations.RefRedate, "a plain amend never re-dates")
	assert.Contains(t, res.Ack, base.WorkoutID)
	assert.Contains(t, res.Ack, res.EventID)
	require.Contains(t, res.Changes, "rpe")
	assert.Nil(t, res.Changes["rpe"].From, "the session had no RPE before")
	assert.Equal(t, 4, res.Changes["rpe"].To)
	assert.Len(t, res.Changes, 1, "only the stated field is reported")

	after, err := r.Store().ReadObservationsKind(observations.KindWorkout)
	require.NoError(t, err)
	require.Len(t, after, len(before)+1, "exactly one event is appended")

	corr := eventByID(t, r, res.LogicalDate, res.EventID)
	require.NoError(t, corr.Validate())
	assert.Equal(t, observations.KindWorkout, corr.Kind)
	assert.Equal(t, base.WorkoutID, corr.Refs[observations.RefCorrects])
	assert.Equal(t, map[string]any{"rpe": float64(4)}, corr.Payload, "only the changed field is carried")
	assert.Equal(t, now.Format(time.RFC3339Nano), corr.RecordedAt, "recorded_at keeps nanosecond precision")
	assert.Equal(t, original.OccurredAt, corr.OccurredAt, "colocated with the session's occurrence")
	assert.Equal(t, original.OccurredAtPrecision, corr.OccurredAtPrecision)

	assert.Equal(t, original, eventByID(t, r, base.LogicalDate, base.WorkoutID),
		"the session's own event is never rewritten")
}

// TestAmendWorkout_OnlyStatedFieldsChange: amending the RPE alone leaves the
// session's duration and type exactly as logged, and the session still counts
// once on the folded read.
func TestAmendWorkout_OnlyStatedFieldsChange(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	base := seedWorkout(t, r, WorkoutLogRequest{Type: "climbing", DurationMin: 60})

	_, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: base.WorkoutID, RPE: 4, RPEChanged: true, Now: nowEDT()})
	require.NoError(t, err)

	folded := foldedWorkout(t, r, base.WorkoutID)
	assert.EqualValues(t, 4, folded.Payload["rpe"], "the RPE folds onto the session")
	assert.Equal(t, "climbing", folded.Payload["type"], "type is untouched")
	assert.EqualValues(t, 60, folded.Payload["duration_min"], "duration is untouched")
	assert.Equal(t, 1, countFoldedWorkouts(t, r), "one session, never a second")
}

// TestAmendWorkout_EachField: every amendable field folds onto the session, the
// list fields replace the whole list, and the changes report the folded prior.
func TestAmendWorkout_EachField(t *testing.T) {
	logged := WorkoutLogRequest{
		Type:        "push",
		Movements:   []string{"bench", "ohp"},
		DurationMin: 45,
		RPE:         intPtr(7),
		BodyParts:   []string{"chest", "shoulders"},
		Notes:       "felt strong",
	}

	tests := []struct {
		name  string
		req   WorkoutAmendRequest
		field string
		want  any
		from  any
	}{
		{"type", WorkoutAmendRequest{Type: " pull ", TypeChanged: true}, "type", "pull", "push"},
		{
			"movements replace",
			WorkoutAmendRequest{Movements: []string{"rows", " ", "pullups"}, MovementsChanged: true},
			"movements",
			[]any{"rows", "pullups"},
			[]any{"bench", "ohp"},
		},
		{"duration", WorkoutAmendRequest{DurationMin: 50, DurationChanged: true}, "duration_min", float64(50), float64(45)},
		{"duration zero", WorkoutAmendRequest{DurationMin: 0, DurationChanged: true}, "duration_min", float64(0), float64(45)},
		{"rpe", WorkoutAmendRequest{RPE: 9, RPEChanged: true}, "rpe", float64(9), float64(7)},
		{
			"parts replace",
			WorkoutAmendRequest{BodyParts: []string{"back"}, BodyPartsChanged: true},
			"body_parts",
			[]any{"back"},
			[]any{"chest", "shoulders"},
		},
		{
			"notes",
			WorkoutAmendRequest{Notes: "  grip gave out; `echo $HOME` & \"quoted\"\nsecond line  ", NotesChanged: true},
			"note", "grip gave out; `echo $HOME` & \"quoted\"\nsecond line", "felt strong",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := bootedWorkout(t, observations.KindWorkout)
			base := seedWorkout(t, r, logged)

			req := tc.req
			req.ObsID = base.WorkoutID
			req.Now = nowEDT()
			res, err := r.AmendWorkout(req)
			require.NoError(t, err)

			folded := foldedWorkout(t, r, base.WorkoutID)
			assert.Equal(t, tc.want, folded.Payload[tc.field])
			require.Contains(t, res.Changes, tc.field)
			assert.Equal(t, tc.from, res.Changes[tc.field].From, "the change reports the folded prior value")
			assert.Len(t, res.Changes, 1)

			// Every other field keeps its logged value.
			for _, other := range []string{"type", "movements", "duration_min", "rpe", "body_parts", "note"} {
				if other == tc.field {
					continue
				}
				assert.Equal(t, eventByID(t, r, base.LogicalDate, base.WorkoutID).Payload[other], folded.Payload[other],
					"%s is untouched", other)
			}
		})
	}
}

// TestAmendWorkout_ChainLastWriteWins: two RPE amends in a row — the second
// value wins on read, its change reports the first as the prior, and both
// corrections stay on disk as history.
func TestAmendWorkout_ChainLastWriteWins(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	base := seedWorkout(t, r, WorkoutLogRequest{Type: "climbing"})

	first, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: base.WorkoutID, RPE: 4, RPEChanged: true, Now: nowEDT().Add(time.Minute)})
	require.NoError(t, err)
	second, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: base.WorkoutID, RPE: 6, RPEChanged: true, Now: nowEDT().Add(2 * time.Minute)})
	require.NoError(t, err)

	assert.EqualValues(t, 4, second.Changes["rpe"].From, "the prior is the first amend's value")
	assert.EqualValues(t, 6, foldedWorkout(t, r, base.WorkoutID).Payload["rpe"], "the latest amend wins")

	all, err := r.Store().ReadObservationsKind(observations.KindWorkout)
	require.NoError(t, err)
	assert.Len(t, all, 3, "the session and both corrections remain")
	assert.Equal(t, base.WorkoutID, eventByID(t, r, first.LogicalDate, first.EventID).Refs[observations.RefCorrects])
	assert.Equal(t, base.WorkoutID, eventByID(t, r, second.LogicalDate, second.EventID).Refs[observations.RefCorrects])
	assert.Equal(t, 1, countFoldedWorkouts(t, r))
}

// TestAmendWorkout_CorrectionResolvesToBase: passing a correction's id amends
// the session that correction names — refs.corrects always points at the base,
// the result names the base, and both corrections fold onto the one session.
// This holds for a re-dated correction too, whose id carries its new day.
func TestAmendWorkout_CorrectionResolvesToBase(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	base := seedWorkout(t, r, WorkoutLogRequest{Type: "climbing", DurationMin: 60})

	first, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: base.WorkoutID, RPE: 4, RPEChanged: true, Now: nowEDT().Add(time.Minute)})
	require.NoError(t, err)

	second, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: first.EventID, Notes: "slab day", NotesChanged: true, Now: nowEDT().Add(2 * time.Minute)})
	require.NoError(t, err)
	assert.Equal(t, base.WorkoutID, second.TargetID, "the correction id resolves to its session")
	assert.Equal(t, base.WorkoutID, second.Refs[observations.RefCorrects], "never a correction of a correction")

	redate, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: second.EventID, DayArg: "2026-06-30", Now: nowEDT().Add(3 * time.Minute)})
	require.NoError(t, err)
	third, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: redate.EventID, DurationMin: 55, DurationChanged: true, Now: nowEDT().Add(4 * time.Minute)})
	require.NoError(t, err)
	assert.Equal(t, base.WorkoutID, third.TargetID, "a re-dated correction's id resolves to the session too")

	folded := foldedWorkout(t, r, base.WorkoutID)
	assert.EqualValues(t, 4, folded.Payload["rpe"])
	assert.Equal(t, "slab day", folded.Payload["note"])
	assert.EqualValues(t, 55, folded.Payload["duration_min"])
	assert.Equal(t, "2026-06-30", folded.LogicalDate)
	assert.Equal(t, 1, countFoldedWorkouts(t, r))
}

// TestAmendWorkout_RedateMovesSession: a --day amend carries refs.redate and the
// new date trio, resolved exactly as `workout log --day` resolves it, so the
// folded session's occurred_at, precision, and logical_date move together; the
// change reports both date moves and the id files under the new day.
func TestAmendWorkout_RedateMovesSession(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	base := seedWorkout(t, r, WorkoutLogRequest{Type: "climbing", DurationMin: 60})
	original := eventByID(t, r, base.LogicalDate, base.WorkoutID)

	res, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: base.WorkoutID, DayArg: "2026-06-30", Now: nowEDT().Add(time.Minute)})
	require.NoError(t, err, "a re-date alone is a complete amend")
	assert.Equal(t, "2026-06-30", res.LogicalDate)
	assert.True(t, strings.HasPrefix(res.EventID, "obs_2026_06_30_"), "the correction files under the new day")
	assert.Equal(t, true, res.Refs[observations.RefRedate])
	assert.Equal(t, base.WorkoutID, res.Refs[observations.RefCorrects])

	// The same resolution `workout log --day 2026-06-30` would have stamped.
	logged := seedWorkout(t, bootedWorkout(t, observations.KindWorkout),
		WorkoutLogRequest{Type: "climbing", DayArg: "2026-06-30", Now: nowEDT().Add(time.Minute)})
	corr := eventByID(t, r, res.LogicalDate, res.EventID)
	assert.Empty(t, corr.Payload, "a re-date carries no payload fields")
	assert.Equal(t, logged.LogicalDate, corr.LogicalDate)

	folded := foldedWorkout(t, r, base.WorkoutID)
	assert.Equal(t, "2026-06-30", folded.LogicalDate)
	assert.Equal(t, corr.OccurredAt, folded.OccurredAt)
	assert.Equal(t, corr.OccurredAtPrecision, folded.OccurredAtPrecision)
	assert.Equal(t, "climbing", folded.Payload["type"], "payload is untouched by a re-date")
	assert.EqualValues(t, 60, folded.Payload["duration_min"])

	assert.Equal(t, WorkoutFieldChange{From: base.LogicalDate, To: "2026-06-30"}, res.Changes["logical_date"])
	assert.Equal(t, WorkoutFieldChange{From: original.OccurredAt, To: corr.OccurredAt}, res.Changes["occurred_at"])
	assert.Equal(t, 1, countFoldedWorkouts(t, r))
}

// TestAmendWorkout_FieldEditAfterRedateKeepsNewDay: after a re-date, a plain
// amend files under the session's *current* folded day and never reverts it,
// and a second re-date reports the first re-date's day as its prior.
func TestAmendWorkout_FieldEditAfterRedateKeepsNewDay(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	base := seedWorkout(t, r, WorkoutLogRequest{Type: "climbing"})

	_, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: base.WorkoutID, DayArg: "2026-06-30", Now: nowEDT().Add(time.Minute)})
	require.NoError(t, err)

	edit, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: base.WorkoutID, RPE: 5, RPEChanged: true, Now: nowEDT().Add(2 * time.Minute)})
	require.NoError(t, err)
	assert.Equal(t, "2026-06-30", edit.LogicalDate, "a plain amend files under the session's current day")
	assert.NotContains(t, edit.Refs, observations.RefRedate)
	assert.NotContains(t, edit.Changes, "logical_date", "a plain amend reports no date move")

	folded := foldedWorkout(t, r, base.WorkoutID)
	assert.Equal(t, "2026-06-30", folded.LogicalDate, "the later field edit keeps the re-date")
	assert.EqualValues(t, 5, folded.Payload["rpe"])

	again, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: base.WorkoutID, DayArg: "2026-06-29", Now: nowEDT().Add(3 * time.Minute)})
	require.NoError(t, err)
	assert.Equal(t, "2026-06-30", again.Changes["logical_date"].From, "the prior is the folded day, not the logged one")
	assert.Equal(t, "2026-06-29", foldedWorkout(t, r, base.WorkoutID).LogicalDate)
}

// TestAmendWorkout_PartialSessionAmendable: a bare "I trained" capture (the
// partial path, no fields) is a session, so it can be filled in afterwards.
func TestAmendWorkout_PartialSessionAmendable(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	base := seedWorkout(t, r, WorkoutLogRequest{})

	_, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: base.WorkoutID, RPE: 4, RPEChanged: true, Now: nowEDT()})
	require.NoError(t, err)
	assert.EqualValues(t, 4, foldedWorkout(t, r, base.WorkoutID).Payload["rpe"])
}

// TestAmendWorkout_AnchorOnlyRefused: an anchor-only capture is not a session,
// so amend refuses it with the documented reason and writes nothing — while a
// capture that logged a session alongside the anchor stays amendable.
func TestAmendWorkout_AnchorOnlyRefused(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	anchor := seedWorkout(t, r, WorkoutLogRequest{Anchor: true, AnchorItems: []AnchorCount{{Name: "squats", Count: 55, HasCount: true}}})

	before := obsSnapshot(t, r)
	_, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: anchor.WorkoutID, RPE: 4, RPEChanged: true, Now: nowEDT()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amend corrects logged sessions; anchors aren't amendable")
	assert.Contains(t, err.Error(), "nothing was saved")
	assert.Equal(t, before, obsSnapshot(t, r), "a refused amend writes nothing")

	withSession := seedWorkout(t, r, WorkoutLogRequest{Anchor: true, Type: "mobility"})
	_, err = r.AmendWorkout(WorkoutAmendRequest{ObsID: withSession.WorkoutID, RPE: 3, RPEChanged: true, Now: nowEDT()})
	require.NoError(t, err, "an anchor that rode along with a session does not block amending the session")
}

// TestAmendWorkout_Refusals pins every strict-tier refusal: each returns a bare
// reason ending "nothing was saved" (the CLI adds the verb prefix) and leaves
// the Ledger byte-identical.
func TestAmendWorkout_Refusals(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout, observations.KindBodyState)
	base := seedWorkout(t, r, WorkoutLogRequest{
		Type:       "legs",
		BodyStates: []BodyStateInput{{Part: "quads", Soreness: intPtr(5)}},
	})
	require.Len(t, base.BodyStateIDs, 1)

	tests := []struct {
		name    string
		req     WorkoutAmendRequest
		want    string
		dayRejc bool
	}{
		{
			"unknown id",
			WorkoutAmendRequest{ObsID: "obs_2026_07_02_009", RPE: 4, RPEChanged: true},
			`workout "obs_2026_07_02_009" not found`, false,
		},
		{
			"malformed id",
			WorkoutAmendRequest{ObsID: "yesterday's climb", RPE: 4, RPEChanged: true},
			"not found", false,
		},
		{"empty id", WorkoutAmendRequest{RPE: 4, RPEChanged: true}, "not found", false},
		{
			"non-workout id",
			WorkoutAmendRequest{ObsID: base.BodyStateIDs[0], RPE: 4, RPEChanged: true},
			"is a body_state observation, not a workout session", false,
		},
		{"no fields", WorkoutAmendRequest{ObsID: base.WorkoutID}, "no fields to amend", false},
		{"blank day only", WorkoutAmendRequest{ObsID: base.WorkoutID, DayArg: "  "}, "no fields to amend", false},
		{"rpe above range", WorkoutAmendRequest{ObsID: base.WorkoutID, RPE: 11, RPEChanged: true}, "--rpe must be 0-10", false},
		{"rpe below range", WorkoutAmendRequest{ObsID: base.WorkoutID, RPE: -1, RPEChanged: true}, "--rpe must be 0-10", false},
		{
			"negative duration",
			WorkoutAmendRequest{ObsID: base.WorkoutID, DurationMin: -5, DurationChanged: true},
			"--duration must be zero or more", false,
		},
		{
			"empty type",
			WorkoutAmendRequest{ObsID: base.WorkoutID, Type: " ", TypeChanged: true},
			"--type needs a value — amend corrects a field, it doesn't clear it", false,
		},
		{
			"empty movements",
			WorkoutAmendRequest{ObsID: base.WorkoutID, Movements: []string{" "}, MovementsChanged: true},
			"--movements needs a value", false,
		},
		{"empty parts", WorkoutAmendRequest{ObsID: base.WorkoutID, BodyPartsChanged: true}, "--parts needs a value", false},
		{"empty notes", WorkoutAmendRequest{ObsID: base.WorkoutID, NotesChanged: true}, "--notes needs a value", false},
		{"future day", WorkoutAmendRequest{ObsID: base.WorkoutID, DayArg: "2026-07-09"}, "has not happened yet", true},
		{
			"unreadable day",
			WorkoutAmendRequest{ObsID: base.WorkoutID, DayArg: "someday", RPE: 4, RPEChanged: true},
			"could not read the day", true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := obsSnapshot(t, r)
			req := tc.req
			req.Now = nowEDT()
			res, err := r.AmendWorkout(req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.True(t, strings.HasSuffix(err.Error(), "nothing was saved"), "every refusal says nothing was saved: %q", err)
			assert.NotContains(t, err.Error(), "lucid workout amend:", "the router returns the bare reason")
			var refused *DayRejectedError
			assert.Equal(t, tc.dayRejc, errors.As(err, &refused), "a refused --day is a DayRejectedError")
			assert.Empty(t, res.EventID)
			assert.Equal(t, before, obsSnapshot(t, r), "a refused amend writes nothing")
		})
	}
}

// TestAmendWorkout_CorrectionWithMissingBaseRefused: a correction whose session
// is absent (a hand-edited Ledger) resolves to nothing, so amending it is "not
// found" rather than a correction aimed at a session no reader can see.
func TestAmendWorkout_CorrectionWithMissingBaseRefused(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	orphan, err := r.Store().AppendObservation(observations.Event{
		Schema:              observations.Schema,
		Kind:                observations.KindWorkout,
		RecordedAt:          nowEDT().Format(time.RFC3339Nano),
		OccurredAt:          nowEDT().Format(time.RFC3339),
		OccurredAtPrecision: observations.PrecisionExact,
		LogicalDate:         "2026-07-02",
		Source:              observations.SourceMicrolog,
		Payload:             map[string]any{"rpe": 4},
		Refs:                map[string]any{observations.RefCorrects: "obs_2026_07_01_001"},
	})
	require.NoError(t, err)

	before := obsSnapshot(t, r)
	_, err = r.AmendWorkout(WorkoutAmendRequest{ObsID: orphan.ID, RPE: 5, RPEChanged: true, Now: nowEDT()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Equal(t, before, obsSnapshot(t, r))
}

// TestAmendWorkout_DisabledKindRejected: with the workout kind disabled, amend
// behaves exactly like log — the enable hint, a nil error, nothing written —
// even for an id that names a real session.
func TestAmendWorkout_DisabledKindRejected(t *testing.T) {
	r := bootedWorkout(t, observations.KindWorkout)
	base := seedWorkout(t, r, WorkoutLogRequest{Type: "climbing"})

	cfg, err := r.Store().ReadObservationsConfig()
	require.NoError(t, err)
	cfg.KindsEnabled = nil
	require.NoError(t, r.Store().SaveObservationsConfig(cfg))

	before := obsSnapshot(t, r)
	res, err := r.AmendWorkout(WorkoutAmendRequest{ObsID: base.WorkoutID, RPE: 4, RPEChanged: true, Now: nowEDT()})
	require.NoError(t, err, "a disabled kind is a graceful skip, not a failure")
	assert.True(t, res.Rejected)
	assert.Empty(t, res.EventID)
	assert.Equal(t, observations.EnableHint(observations.KindWorkout), res.Ack)
	assert.Equal(t, before, obsSnapshot(t, r), "a gated amend writes nothing")
}
