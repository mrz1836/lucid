package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/safety"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// serveInstant is the pinned clock for the serve tests, so the /checkin raw id
// (raw_YYYY_MM_DD_HH_MM) is deterministic and can be asserted.
func serveInstant() time.Time { return time.Date(2026, 7, 5, 18, 41, 39, 0, time.UTC) }

// wantRawID is the raw entry id produced at serveInstant (one entry per
// session, so no same-minute collision suffix).
const wantRawID = "raw_2026_07_05_18_41"

// withServeProvider injects a scripted provider for the duration of a test so
// serve never spawns a real vendor CLI (ADR-0006), mirroring withClock.
func withServeProvider(t *testing.T, p provider.Provider) {
	t.Helper()
	prev := buildProvider
	buildProvider = func(config.ProviderConfig) (provider.Provider, error) { return p, nil }
	t.Cleanup(func() { buildProvider = prev })
}

// runServe runs `lucid serve` with the given stdin, returning the decoded
// server frames and the command error.
func runServe(t *testing.T, stdin string) (frames []serveOut, err error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "dev"})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs([]string{"serve"})
	err = root.ExecuteContext(context.Background())
	return decodeFrames(t, out.String()), err
}

// decodeFrames parses the newline-delimited JSON server frames on stdout.
func decodeFrames(t *testing.T, out string) []serveOut {
	t.Helper()
	var frames []serveOut
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var f serveOut
		if decErr := dec.Decode(&f); decErr != nil {
			if errors.Is(decErr, io.EOF) {
				break
			}
			require.NoError(t, decErr)
		}
		frames = append(frames, f)
	}
	return frames
}

// clientLines marshals client frames into the newline-delimited stdin the
// serve loop reads.
func clientLines(t *testing.T, in ...serveIn) string {
	t.Helper()
	var b strings.Builder
	for _, f := range in {
		line, err := json.Marshal(f)
		require.NoError(t, err)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// frameTypes projects the ordered frame types for a sequence assertion.
func frameTypes(frames []serveOut) []string {
	types := make([]string, 0, len(frames))
	for _, f := range frames {
		types = append(types, f.Type)
	}
	return types
}

// seedPriorProcessed scaffolds the Ledger and writes one prior processed
// artifact so reflection.propose has a non-empty recent window and can fire (an
// empty window short-circuits to no_pattern with no model call — §R-3). A serve
// session only ever adds its own new artifact, so a live proposal needs a prior
// one seeded before the run.
func seedPriorProcessed(t *testing.T, home string) {
	t.Helper()
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	require.NoError(t, a.WriteProcessed(storage.ProcessedArtifact{
		ID:           "raw_2026_05_03_21_10",
		EntryID:      "raw_2026_05_03_21_10",
		ProducedAt:   time.Date(2026, 5, 3, 21, 10, 0, 0, time.UTC),
		AgentVersion: "structuring-2026.05.0",
		Emotions:     []storage.ProcessedItem{{Name: "annoyed", Rationale: "user said 'annoyed'"}},
		Themes:       []storage.ProcessedItem{{Name: "voice-not-heard", Rationale: "grounded in the entry"}},
	}))
}

// countHomeFiles counts the non-.keep files under home/sub.
func countHomeFiles(t *testing.T, home, sub string) int {
	t.Helper()
	var n int
	err := filepath.WalkDir(filepath.Join(home, sub), func(_ string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && d.Name() != ".keep" {
			n++
		}
		return nil
	})
	require.NoError(t, err)
	return n
}

// ─── provider script builders (mirror the router test fixtures) ───

func decideAsk(q string) provider.Exchange {
	return provider.Exchange{Content: fmt.Sprintf(`{"done":false,"question":%q}`, q)}
}

func decideDone() provider.Exchange { return provider.Exchange{Content: `{"done":true}`} }

// bundleReplyEx returns the intake bundling reply for the shared /checkin
// fixture (the bundle is the opening plus both answers, verbatim, so it clears
// the ≥90% user-authored floor).
func bundleReplyEx() provider.Exchange {
	return provider.Exchange{Content: `{"bundled_text":"Rough dinner. I pushed back and dropped it. Annoyed and embarrassed."}`}
}

// extractEx returns one clean structuring extraction (one emotion, no people so
// the People routine needs no wordlist).
func extractEx() provider.Exchange {
	return provider.Exchange{Content: `{"emotions":[{"name":"annoyed","rationale":"the user said they were annoyed and embarrassed"}],"themes":[],"people":[],"notes":null}`}
}

func proposeEx(text, tag string, ids ...string) provider.Exchange {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	body := fmt.Sprintf(`{"outcome":"proposal","proposal_text":%q,"shape_tag":%q,"supporting_entry_ids":[%s]}`,
		text, tag, strings.Join(quoted, ","))
	return provider.Exchange{Content: body}
}

// TestServe_Registered proves `serve` is on the cobra root.
func TestServe_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	found := slices.ContainsFunc(root.Commands(), func(c *cobra.Command) bool { return c.Name() == "serve" })
	assert.True(t, found, "serve must be registered on the root command")
}

// TestServe_CheckinToValidatedInsight drives the full interactive /checkin over
// the stdin/JSON protocol: two Intake follow-ups, a Safety-gated proposal, the
// resonance accept, and the rule prompt — landing a validated insight with full
// provenance. The proposal the user is shown is the Safety *rewrite* of an
// overclaim, proving safety.Evaluate runs on the live serve path (AC-7), and the
// stored insight body is the clean rewrite (AC-5).
func TestServe_CheckinToValidatedInsight(t *testing.T) {
	home := isolatedHome(t)
	seedPriorProcessed(t, home)
	withClock(t, serveInstant())

	rewritten := "I noticed a possible pattern: when M. is in the room, you tend to fold. Does this resonate?"
	fake := &provider.Fake{Script: []provider.Exchange{
		decideAsk("What happened?"),
		decideAsk("How did it land?"),
		decideDone(),
		bundleReplyEx(),
		extractEx(),
		proposeEx("You always fold when M. is in the room.", "voice-fold-when-m", wantRawID),
		{Content: rewritten}, // Safety rewrites the overclaim ("always") on the live path
	}}
	withServeProvider(t, fake)

	stdin := clientLines(
		t,
		serveIn{Type: frameControl, Command: controlStart, Opening: "Rough dinner."},
		serveIn{Type: frameAnswer, Text: "I pushed back and dropped it."},
		serveIn{Type: frameAnswer, Text: "Annoyed and embarrassed."},
		serveIn{Type: frameResonance, Kind: string(router.RespAccepted), Text: "Yes, that fits."},
		serveIn{Type: frameRuleAnswer, Answered: false},
	)

	frames, err := runServe(t, stdin)
	require.NoError(t, err)

	// Protocol frames: two questions, the surfaced proposal, the rule prompt,
	// then the terminal ack.
	require.Equal(t, []string{frameQuestion, frameQuestion, frameProposal, frameRule, frameAck}, frameTypes(frames))

	// The user was shown the Safety rewrite, not the raw overclaim.
	assert.Equal(t, rewritten, frames[2].Text, "the proposal is Safety-gated on the live path")

	ack := frames[4]
	assert.True(t, ack.Wrote)
	assert.Equal(t, wantRawID, ack.RawID)
	require.NotEmpty(t, ack.InsightID)

	// The validated insight is on disk with full provenance and clean text.
	a := storage.New(home)
	ins, err := a.ReadInsight(ack.InsightID)
	require.NoError(t, err)
	assert.Equal(t, storage.InsightStatusAccepted, ins.Status)
	assert.Equal(t, rewritten, ins.Body)
	assert.False(t, safety.MatchesBlocklist(ins.Body), "the stored insight carries no blocklist phrase")
	assert.Equal(t, wantRawID, ins.Provenance.ProcessedArtifactID)
	assert.Equal(t, []string{wantRawID}, ins.Provenance.RawEntryIDs)
	assert.Equal(t, 1, countHomeFiles(t, home, "insights"))
}

// TestServe_SafetyBlockWritesNothing proves a proposal that Safety blocks (an
// external-action attempt) surfaces the calm fallback and writes nothing — the
// blocked proposal never reaches the user, so no resonance turn is read (AC-7).
func TestServe_SafetyBlockWritesNothing(t *testing.T) {
	home := isolatedHome(t)
	seedPriorProcessed(t, home)
	withClock(t, serveInstant())

	fake := &provider.Fake{Script: []provider.Exchange{
		decideAsk("What happened?"),
		decideAsk("How did it land?"),
		decideDone(),
		bundleReplyEx(),
		extractEx(),
		proposeEx("I'll send M. a follow-up message tonight.", "voice-fold", wantRawID),
	}}
	withServeProvider(t, fake)

	stdin := clientLines(
		t,
		serveIn{Type: frameControl, Command: controlStart, Opening: "Rough dinner."},
		serveIn{Type: frameAnswer, Text: "I pushed back and dropped it."},
		serveIn{Type: frameAnswer, Text: "Annoyed and embarrassed."},
	)

	frames, err := runServe(t, stdin)
	require.NoError(t, err)

	require.Equal(t, []string{frameQuestion, frameQuestion, frameAck}, frameTypes(frames))
	ack := frames[2]
	assert.False(t, ack.Wrote, "a blocked proposal writes no insight")
	assert.Equal(t, "I held that response — let me ask differently.", ack.Message)
	assert.Empty(t, ack.InsightID)
	assert.Equal(t, 0, countHomeFiles(t, home, "insights"))
}

// TestServe_CancelWritesNothing proves a /cancel control turn during Intake ends
// the session with "nothing saved" and no raw entry — the interactive exit
// signals ride the same protocol.
func TestServe_CancelWritesNothing(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, serveInstant())

	fake := &provider.Fake{Script: []provider.Exchange{decideAsk("What happened?")}}
	withServeProvider(t, fake)

	stdin := clientLines(
		t,
		serveIn{Type: frameControl, Command: controlStart, Opening: "Rough dinner."},
		serveIn{Type: frameAnswer, Control: controlCancel},
	)

	frames, err := runServe(t, stdin)
	require.NoError(t, err)

	require.Equal(t, []string{frameQuestion, frameAck}, frameTypes(frames))
	ack := frames[1]
	assert.False(t, ack.Wrote)
	assert.Equal(t, "Stopped — nothing saved.", ack.Message)
	assert.Equal(t, 0, countHomeFiles(t, home, "raw"))
	assert.Equal(t, 0, countHomeFiles(t, home, "sessions"))
}

// TestServe_UnexpectedFirstFrameReported proves a non-start opening frame is
// reported (so a harness can resend a proper start) rather than silently
// dropped, and the loop continues to a clean EOF.
func TestServe_UnexpectedFirstFrameReported(t *testing.T) {
	isolatedHome(t)
	withServeProvider(t, &provider.Fake{})

	stdin := clientLines(t, serveIn{Type: frameAnswer, Text: "no session yet"})
	frames, err := runServe(t, stdin)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.Equal(t, frameError, frames[0].Type)
	assert.Contains(t, frames[0].Message, "start")
}

// TestServe_MalformedFrameSurfacesError proves malformed stdin ends serve with
// an error frame and a non-zero command error rather than a false success.
func TestServe_MalformedFrameSurfacesError(t *testing.T) {
	isolatedHome(t)
	withServeProvider(t, &provider.Fake{})

	frames, err := runServe(t, "{not json\n")
	require.Error(t, err)
	require.NotEmpty(t, frames)
	assert.Equal(t, frameError, frames[len(frames)-1].Type)
}

// TestServe_EmptyStdinCleanExit proves a serve process given no input exits
// cleanly (an empty session stream is not an error).
func TestServe_EmptyStdinCleanExit(t *testing.T) {
	isolatedHome(t)
	withServeProvider(t, &provider.Fake{})

	frames, err := runServe(t, "")
	require.NoError(t, err)
	assert.Empty(t, frames)
}

// cleanProposalScript is the six-exchange script for a clean (non-rewritten)
// proposal: two follow-ups, done, bundle, extract, one hypothesis-framed
// proposal that Safety passes unchanged.
func cleanProposalScript() []provider.Exchange {
	return []provider.Exchange{
		decideAsk("What happened?"),
		decideAsk("How did it land?"),
		decideDone(),
		bundleReplyEx(),
		extractEx(),
		proposeEx("One possible pattern: you test an idea once, then back off.", "voice-fold-when-m", wantRawID),
	}
}

// checkinPrelude is the three opening client frames (start + two answers) shared
// by the resonance-gate tests.
func checkinPrelude() []serveIn {
	return []serveIn{
		{Type: frameControl, Command: controlStart, Opening: "Rough dinner."},
		{Type: frameAnswer, Text: "I pushed back and dropped it."},
		{Type: frameAnswer, Text: "Annoyed and embarrassed."},
	}
}

// TestServe_NuancedInsightUsesRefinement proves a nuanced resonance answer over
// the serve surface makes the user's refinement the canonical insight body.
func TestServe_NuancedInsightUsesRefinement(t *testing.T) {
	home := isolatedHome(t)
	seedPriorProcessed(t, home)
	withClock(t, serveInstant())
	withServeProvider(t, &provider.Fake{Script: cleanProposalScript()})

	refine := "Mostly yes — it's more when M. is in the room."
	stdin := clientLines(t, append(
		checkinPrelude(),
		serveIn{Type: frameResonance, Kind: string(router.RespNuanced), Text: refine},
		serveIn{Type: frameRuleAnswer, Answered: false},
	)...)

	frames, err := runServe(t, stdin)
	require.NoError(t, err)
	require.Equal(t, []string{frameQuestion, frameQuestion, frameProposal, frameRule, frameAck}, frameTypes(frames))

	ack := frames[4]
	require.True(t, ack.Wrote)
	ins, err := storage.New(home).ReadInsight(ack.InsightID)
	require.NoError(t, err)
	assert.True(t, ins.NuancedFromProposal)
	assert.Equal(t, refine, ins.Body, "the canonical statement is the user's refinement")
}

// TestServe_RejectedRecordsNoInsight proves a rejected resonance answer writes
// no insight and asks no rule prompt (only accept/nuance reach the rule).
func TestServe_RejectedRecordsNoInsight(t *testing.T) {
	home := isolatedHome(t)
	seedPriorProcessed(t, home)
	withClock(t, serveInstant())
	withServeProvider(t, &provider.Fake{Script: cleanProposalScript()})

	stdin := clientLines(t, append(
		checkinPrelude(),
		serveIn{Type: frameResonance, Kind: string(router.RespRejected), Text: "No — that doesn't fit."},
	)...)

	frames, err := runServe(t, stdin)
	require.NoError(t, err)
	require.Equal(t, []string{frameQuestion, frameQuestion, frameProposal, frameAck}, frameTypes(frames))
	assert.False(t, frames[3].Wrote)
	assert.Empty(t, frames[3].InsightID)
	assert.Equal(t, 0, countHomeFiles(t, home, "insights"))
}

// TestServe_StructureDegradeAcksCapture proves that when Structuring degrades
// honestly (here: an unwritable processed/ dir), serve acks the held capture
// and skips validation — the raw entry is captured but unprocessed, so no
// proposal is surfaced (error-states.md §St-3).
func TestServe_StructureDegradeAcksCapture(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod permission bits are a no-op as root")
	}
	home := isolatedHome(t)
	withClock(t, serveInstant())

	// Scaffold, then make processed/ unwritable so Structuring's write fails.
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)
	procDir := filepath.Join(home, "processed")
	require.NoError(t, os.Chmod(procDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(procDir, 0o700) })

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{
		decideAsk("What happened?"),
		decideAsk("How did it land?"),
		decideDone(),
		bundleReplyEx(),
		extractEx(),
	}})

	stdin := clientLines(t, checkinPrelude()...)
	frames, err := runServe(t, stdin)
	require.NoError(t, err)

	require.Equal(t, []string{frameQuestion, frameQuestion, frameAck}, frameTypes(frames))
	ack := frames[2]
	assert.True(t, ack.Wrote, "the capture is held even when structuring defers")
	assert.Equal(t, wantRawID, ack.RawID)
	assert.Empty(t, ack.InsightID)
	assert.Equal(t, 0, countHomeFiles(t, home, "insights"))
}

// TestServe_ProviderBuildErrorSurfaces proves a provider that cannot be built
// (e.g. a misconfigured backend) ends the session with an error frame and a
// non-zero command error rather than a false ack.
func TestServe_ProviderBuildErrorSurfaces(t *testing.T) {
	isolatedHome(t)
	prev := buildProvider
	buildProvider = func(config.ProviderConfig) (provider.Provider, error) {
		return nil, errors.New("provider backend unavailable")
	}
	t.Cleanup(func() { buildProvider = prev })

	stdin := clientLines(t, serveIn{Type: frameControl, Command: controlStart, Opening: "Rough dinner."})
	frames, err := runServe(t, stdin)
	require.Error(t, err)
	require.NotEmpty(t, frames)
	last := frames[len(frames)-1]
	assert.Equal(t, frameError, last.Type)
	assert.Contains(t, last.Message, "unavailable")
}
