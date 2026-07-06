package router

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/intake"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// scriptResponder replays a fixed list of user turns for the /checkin
// router tests.
type scriptResponder struct {
	turns []intake.Turn
	i     int
}

func (s *scriptResponder) Answer(string) (intake.Turn, error) {
	if s.i >= len(s.turns) {
		return intake.Turn{}, fmt.Errorf("responder: out of turns")
	}
	t := s.turns[s.i]
	s.i++
	return t, nil
}

func ans(text string) intake.Turn { return intake.Turn{Text: text} }

func askEx(q string) provider.Exchange {
	return provider.Exchange{Content: fmt.Sprintf(`{"done": false, "question": %q}`, q)}
}

func doneEx() provider.Exchange { return provider.Exchange{Content: `{"done": true}`} }

func bundleEx(text string) provider.Exchange {
	return provider.Exchange{Content: fmt.Sprintf(`{"bundled_text": %q}`, text)}
}

// checkinReq builds a /checkin request wired to the given provider and
// responder, at the fixed test instant.
func checkinReq(p provider.Provider, r intake.Responder, opening string) CheckinRequest {
	return CheckinRequest{
		Opening:   opening,
		Now:       fixedNow(),
		Source:    "cli",
		Harness:   "cli",
		ChannelID: "cli",
		Provider:  p,
		Responder: r,
	}
}

// TestCheckin_SatisfiedWritesRawEntry is acceptance case 3.1 at the router
// seam: two answers, satisfied, one valid raw entry with intake_questions
// of length two and the standard ack.
func TestCheckin_SatisfiedWritesRawEntry(t *testing.T) {
	r, a, home := newBootedRouter(t)
	p := &provider.Fake{Script: []provider.Exchange{
		askEx("What happened?"), askEx("How did it land?"), doneEx(),
		bundleEx("Rough dinner. I pushed back and dropped it. Annoyed and embarrassed."),
	}}
	resp := &scriptResponder{turns: []intake.Turn{ans("I pushed back and dropped it."), ans("Annoyed and embarrassed.")}}

	res, err := r.Checkin(context.Background(), checkinReq(p, resp, "Rough dinner."))
	require.NoError(t, err)
	assert.True(t, res.Wrote)
	assert.Equal(t, intake.StopSatisfied, res.StopReason)
	assert.Equal(t, fmt.Sprintf("Saved as `%s`.", res.RawID), res.Ack)

	// The raw entry exists, validates, and carries the two intake questions.
	path := filepath.Join(home, "raw", "2026", "07", res.RawID+".md")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, storage.ValidateRawFrontmatter(content))
	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "/checkin", doc.Fields["command"])
	iq, ok := doc.Fields["intake_questions"].([]any)
	require.True(t, ok, "intake_questions should be a list")
	assert.Len(t, iq, 2)

	// One session, nothing structured (Structuring lands in a later phase).
	assert.Equal(t, 1, countFiles(t, home, "sessions"))
	assert.Equal(t, 0, countFiles(t, home, "processed"))
	assert.Equal(t, 0, countFiles(t, home, "insights"))
}

// TestCheckin_MaxQuestionsAck is acceptance case 3.2: four questions, the
// cap ack, and an intake_questions length of four.
func TestCheckin_MaxQuestionsAck(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	p := &provider.Fake{Script: []provider.Exchange{
		askEx("q1"), askEx("q2"), askEx("q3"), askEx("q4"),
		bundleEx("a1. a2. a3. a4."),
	}}
	resp := &scriptResponder{turns: []intake.Turn{ans("a1"), ans("a2"), ans("a3"), ans("a4")}}

	res, err := r.Checkin(context.Background(), checkinReq(p, resp, "thin"))
	require.NoError(t, err)
	assert.True(t, res.Wrote)
	assert.Equal(t, fmt.Sprintf("I've got what I need — saved as `%s`.", res.RawID), res.Ack)

	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	iq, _ := doc.Fields["intake_questions"].([]any)
	assert.Len(t, iq, 4)
}

// TestCheckin_CancelWritesNothing is acceptance case 3.3: /cancel before an
// answer writes no raw entry and returns the "nothing saved" ack.
func TestCheckin_CancelWritesNothing(t *testing.T) {
	r, _, home := newBootedRouter(t)
	p := &provider.Fake{Script: []provider.Exchange{askEx("What happened?")}}
	resp := &scriptResponder{turns: []intake.Turn{{Control: intake.ControlCancel}}}

	res, err := r.Checkin(context.Background(), checkinReq(p, resp, "thin"))
	require.NoError(t, err)
	assert.False(t, res.Wrote)
	assert.Empty(t, res.RawID)
	assert.Equal(t, "Stopped — nothing saved.", res.Ack)
	assert.Equal(t, 0, countFiles(t, home, "raw"))
	assert.Equal(t, 0, countFiles(t, home, "sessions"))
}

// TestCheckin_DonePartialBundle is acceptance case 3.4: /done after two
// answers saves the partial bundle with the partial ack.
func TestCheckin_DonePartialBundle(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	p := &provider.Fake{Script: []provider.Exchange{
		askEx("q1"), askEx("q2"), askEx("q3"),
		bundleEx("first answer. second answer."),
	}}
	resp := &scriptResponder{turns: []intake.Turn{ans("first answer"), ans("second answer"), {Control: intake.ControlDone}}}

	res, err := r.Checkin(context.Background(), checkinReq(p, resp, "thin"))
	require.NoError(t, err)
	assert.True(t, res.Wrote)
	assert.Equal(t, intake.StopUserExit, res.StopReason)
	assert.Equal(t, fmt.Sprintf("Saved what we had as `%s`.", res.RawID), res.Ack)

	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	iq, _ := doc.Fields["intake_questions"].([]any)
	assert.Len(t, iq, 2)
}

// TestCheckin_ModelFailedApologyNoWrite is acceptance case 3.5: a malformed
// model twice apologizes and writes nothing.
func TestCheckin_ModelFailedApologyNoWrite(t *testing.T) {
	r, _, home := newBootedRouter(t)
	p := &provider.Fake{Script: []provider.Exchange{{Content: "nope"}, {Content: "still nope"}}}
	resp := &scriptResponder{}

	res, err := r.Checkin(context.Background(), checkinReq(p, resp, "thin"))
	require.NoError(t, err)
	assert.False(t, res.Wrote)
	assert.Equal(t, "I held that — let me try a different opening another time. Nothing saved.", res.Ack)
	assert.Equal(t, 0, countFiles(t, home, "raw"))
}

// TestCheckin_BundleFailureApologyNoWrite is acceptance case 3.6: a bundle
// that never clears the authorship floor downgrades to the apology with no
// write.
func TestCheckin_BundleFailureApologyNoWrite(t *testing.T) {
	r, _, home := newBootedRouter(t)
	bad := "The user felt unable to advocate for themselves, leading to a familiar avoidant pattern of folding, and reported annoyance afterward per the assistant summary."
	p := &provider.Fake{Script: []provider.Exchange{
		askEx("q1"), askEx("q2"), doneEx(),
		bundleEx(bad), bundleEx(bad),
	}}
	resp := &scriptResponder{turns: []intake.Turn{ans("I pushed back then agreed."), ans("Annoyed.")}}

	res, err := r.Checkin(context.Background(), checkinReq(p, resp, "Dinner went sideways."))
	require.NoError(t, err)
	assert.False(t, res.Wrote)
	assert.Contains(t, res.Ack, "I held that")
	assert.Equal(t, 0, countFiles(t, home, "raw"))
}

// TestCheckin_SingleAnswerExitWritesNothing guards the invariant seam: a
// user_exit with only one answer is below the floor and writes no
// (length-one) raw entry.
func TestCheckin_SingleAnswerExitWritesNothing(t *testing.T) {
	r, _, home := newBootedRouter(t)
	p := &provider.Fake{Script: []provider.Exchange{askEx("q1"), askEx("q2")}}
	resp := &scriptResponder{turns: []intake.Turn{ans("only one"), {Control: intake.ControlDone}}}

	res, err := r.Checkin(context.Background(), checkinReq(p, resp, "thin"))
	require.NoError(t, err)
	assert.False(t, res.Wrote)
	assert.Equal(t, "Stopped — nothing saved.", res.Ack)
	assert.Equal(t, 0, countFiles(t, home, "raw"))
}

// TestCheckin_NoRawEntryHasOneQuestion is the definition-of-done sweep: no
// written /checkin raw entry ever has an intake_questions length of one or
// greater than four, across every path that writes.
func TestCheckin_NoRawEntryHasForbiddenQuestionCount(t *testing.T) {
	r, a, home := newBootedRouter(t)

	// Write via satisfied (2), max (4), and partial (2) into one Ledger.
	writes := []struct {
		script []provider.Exchange
		turns  []intake.Turn
	}{
		{
			[]provider.Exchange{askEx("q1"), askEx("q2"), doneEx(), bundleEx("a1. a2.")},
			[]intake.Turn{ans("a1"), ans("a2")},
		},
		{
			[]provider.Exchange{askEx("q1"), askEx("q2"), askEx("q3"), askEx("q4"), bundleEx("a1. a2. a3. a4.")},
			[]intake.Turn{ans("a1"), ans("a2"), ans("a3"), ans("a4")},
		},
		{
			[]provider.Exchange{askEx("q1"), askEx("q2"), askEx("q3"), bundleEx("a1. a2.")},
			[]intake.Turn{ans("a1"), ans("a2"), {Control: intake.ControlDone}},
		},
	}
	for i, w := range writes {
		p := &provider.Fake{Script: w.script}
		resp := &scriptResponder{turns: w.turns}
		req := checkinReq(p, resp, "thin")
		// Same-minute ids resolve via the _SS collision suffix (raw.go).
		res, err := r.Checkin(context.Background(), req)
		require.NoErrorf(t, err, "write %d", i)
		require.Truef(t, res.Wrote, "write %d should persist", i)
	}

	// Every raw entry on disk has a permitted question count.
	rawRoot := filepath.Join(home, "raw")
	err := filepath.WalkDir(rawRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		id := d.Name()[:len(d.Name())-len(".md")]
		doc, rErr := a.ReadRaw(id)
		require.NoError(t, rErr)
		if iq, ok := doc.Fields["intake_questions"].([]any); ok {
			n := len(iq)
			assert.NotEqualf(t, 1, n, "%s has a one-question entry", id)
			assert.LessOrEqualf(t, n, 4, "%s exceeds the cap", id)
		}
		return nil
	})
	require.NoError(t, err)
}

// TestCheckin_WriteFailureSurfaces covers the raw-write failure branch: a
// read-only raw/ tree surfaces an explicit error and leaves nothing.
func TestCheckin_WriteFailureSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r, _, home := newBootedRouter(t)
	rawDir := filepath.Join(home, "raw")
	require.NoError(t, os.Chmod(rawDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(rawDir, 0o700) })

	p := &provider.Fake{Script: []provider.Exchange{
		askEx("q1"), askEx("q2"), doneEx(), bundleEx("a1. a2."),
	}}
	resp := &scriptResponder{turns: []intake.Turn{ans("a1"), ans("a2")}}

	_, err := r.Checkin(context.Background(), checkinReq(p, resp, "thin"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing was saved")
}

// TestCheckin_ResponderErrorSurfaces confirms an infrastructure fault from
// Intake (the responder itself breaking, not a user control) surfaces as a
// router error, not a silent no-op.
func TestCheckin_ResponderErrorSurfaces(t *testing.T) {
	r, _, _ := newBootedRouter(t)
	p := &provider.Fake{Script: []provider.Exchange{askEx("q1")}}
	resp := &scriptResponder{} // no turns → Answer errors

	_, err := r.Checkin(context.Background(), checkinReq(p, resp, "thin"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "intake")
}

// TestCheckin_ZeroNowUsesWallClock covers the default-clock branch: a
// request with no Now still writes a well-formed entry.
func TestCheckin_ZeroNowUsesWallClock(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	p := &provider.Fake{Script: []provider.Exchange{
		askEx("q1"), askEx("q2"), doneEx(), bundleEx("a1. a2."),
	}}
	resp := &scriptResponder{turns: []intake.Turn{ans("a1"), ans("a2")}}
	req := checkinReq(p, resp, "thin")
	req.Now = time.Time{} // force the wall-clock default

	res, err := r.Checkin(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, res.Wrote)
	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, "/checkin", doc.Fields["command"])
}

// TestCheckin_SessionWriteFailureSurfaces covers the branch where the raw
// entry lands but the session record cannot be written.
func TestCheckin_SessionWriteFailureSurfaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r, _, home := newBootedRouter(t)
	sessDir := filepath.Join(home, "sessions")
	require.NoError(t, os.Chmod(sessDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(sessDir, 0o700) })

	p := &provider.Fake{Script: []provider.Exchange{
		askEx("q1"), askEx("q2"), doneEx(), bundleEx("a1. a2."),
	}}
	resp := &scriptResponder{turns: []intake.Turn{ans("a1"), ans("a2")}}

	_, err := r.Checkin(context.Background(), checkinReq(p, resp, "thin"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session record")
}

// TestCheckin_BootstrapStampsFlag confirms a bootstrap /checkin stamps
// bootstrap: true on the raw entry (Reflection.propose is suppressed for
// these downstream).
func TestCheckin_BootstrapStampsFlag(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	p := &provider.Fake{Script: []provider.Exchange{
		askEx("q1"), askEx("q2"), doneEx(), bundleEx("a1. a2."),
	}}
	resp := &scriptResponder{turns: []intake.Turn{ans("a1"), ans("a2")}}
	req := checkinReq(p, resp, "thin")
	req.Bootstrap = true

	res, err := r.Checkin(context.Background(), req)
	require.NoError(t, err)
	doc, err := a.ReadRaw(res.RawID)
	require.NoError(t, err)
	assert.Equal(t, true, doc.Fields["bootstrap"])
}
