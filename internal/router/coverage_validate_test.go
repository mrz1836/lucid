package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/storage"
)

// validPauseState is a well-formed, un-paused proposal-pause file — valid enough
// for proposalPauseActive to read, so a subsequent write is what fails when the
// file is frozen read-only.
const validPauseState = `{"consecutive_unanswered":0,"paused_until":null}`

// TestValidate_PersistPauseWriteFailuresSurface proves each answered-proposal
// path surfaces a proposal-pause write failure rather than dropping the counter
// update silently after the insight/rejection/unanswered record already landed.
func TestValidate_PersistPauseWriteFailuresSurface(t *testing.T) {
	if isRoot() {
		t.Skip("chmod permission bits are a no-op as root")
	}
	for _, tc := range []struct {
		name string
		resp ProposalResponse
	}{
		{"accepted", ProposalResponse{Kind: RespAccepted, Text: "Yes."}},
		{"rejected", ProposalResponse{Kind: RespRejected, Text: "No."}},
		{"unanswered", ProposalResponse{Kind: RespUnanswered}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, a, home := newBootedRouter(t)
			seedWindow(t, a)
			require.NoError(t, writeFileHelper(home, "proposal_pause.json", validPauseState))
			chmodPath(t, home, "proposal_pause.json", 0o400)

			resp := &scriptValidation{proposal: tc.resp, rule: RuleResponse{}}
			_, err := r.Validate(context.Background(), validateReq(proposalFake(), resp, false))
			require.Error(t, err)
		})
	}
}

// TestValidate_ClearExpiredPauseWriteFails proves an expired pause whose clearing
// write fails surfaces the error rather than resuming over a stuck counter.
func TestValidate_ClearExpiredPauseWriteFails(t *testing.T) {
	if isRoot() {
		t.Skip("chmod permission bits are a no-op as root")
	}
	r, a, home := newBootedRouter(t)
	seedWindow(t, a)
	// An expired pause: paused_until is in the past, so proposalPauseActive tries
	// to clear it — a write the frozen file refuses.
	require.NoError(t, writeFileHelper(home, "proposal_pause.json",
		`{"consecutive_unanswered":3,"paused_until":"2020-01-01T00:00:00Z"}`))
	chmodPath(t, home, "proposal_pause.json", 0o400)

	_, err := r.Validate(context.Background(), validateReq(proposalFake(), acceptResp(), false))
	require.Error(t, err)
}

// TestValidate_OffLimitsPersonRedactedFromSlice proves an off-limits person on
// the current artifact is dropped from the agent-bound view while a visible
// person on the same artifact is kept (toView's redact-continue and visible
// append, validate.go:398) — and the turn still writes the insight.
func TestValidate_OffLimitsPersonRedactedFromSlice(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedProc(t, a, priorID, fixedNow(), nil, "voice-not-heard")
	seedProc(t, a, curr, fixedNow(), []storage.ProcessedPerson{
		{DisplayName: "Redacted", PersonKey: "person_redacted"},
		{DisplayName: "Visible", PersonKey: "person_visible"},
	}, "voice-not-heard")
	require.NoError(t, a.WriteOffLimitsPersonKeys([]string{"person_redacted"}))

	res, err := r.Validate(context.Background(), validateReq(proposalFake(), acceptResp(), false))
	require.NoError(t, err)
	assert.True(t, res.Wrote, "the turn still persists the insight over a partly-redacted slice")
}

// TestValidate_UnknownResponseKindTreatedAsUnanswered proves an out-of-vocabulary
// proposal response routes through the default arm to the unanswered path
// (validate.go:207) rather than being lost.
func TestValidate_UnknownResponseKindTreatedAsUnanswered(t *testing.T) {
	r, a, _ := newBootedRouter(t)
	seedWindow(t, a)
	resp := &scriptValidation{proposal: ProposalResponse{Kind: ResponseKind("maybe")}}

	res, err := r.Validate(context.Background(), validateReq(proposalFake(), resp, false))
	require.NoError(t, err)
	assert.True(t, res.Unanswered, "an unrecognized response is recorded as unanswered")
}
