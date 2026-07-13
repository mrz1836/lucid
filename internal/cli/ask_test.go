package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/reflection"
	"github.com/mrz1836/lucid/internal/agents/safety"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// withFailingProvider injects a buildProvider seam that always errors, so a
// provider-backed verb can be driven down its "no backend" path offline. Shared
// by the reflect and ask tests.
func withFailingProvider(t *testing.T) {
	t.Helper()
	prev := buildProvider
	buildProvider = func(config.ProviderConfig) (provider.Provider, error) {
		return nil, errors.New("provider backend unavailable")
	}
	t.Cleanup(func() { buildProvider = prev })
}

// askAnswerReply builds a valid grounded-answer completion citing one insight id.
func askAnswerReply(id string) provider.Exchange {
	return provider.Exchange{Content: fmt.Sprintf(
		`{"outcome":"answer","answer_text":"Based on your history, you tend to go quiet.","citations":[{"kind":"insight","id":%q}]}`,
		id,
	)}
}

// homeTreeHash is a stable digest of every file under home, so a read-only
// command can be proven to leave ~/.lucid/ byte-identical.
func homeTreeHash(t *testing.T, home string) string {
	t.Helper()
	var files []string
	require.NoError(t, filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	}))
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		_, _ = fmt.Fprintf(h, "%s\x00%x\x00", f, sha256.Sum256(b))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// runAsk executes `lucid ask [args...]`, capturing stdout and the command error
// (stderr is discarded — no ask test asserts on it).
func runAsk(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "dev"})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"ask"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), err
}

// TestAsk_Registered proves `ask` is on the cobra root (AC-6).
func TestAsk_Registered(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	found := slices.ContainsFunc(root.Commands(), func(c *cobra.Command) bool { return c.Name() == "ask" })
	assert.True(t, found, "ask must be registered on the root command")
}

// TestAsk_AnswersWithCitationsReadOnly proves /ask over a populated store prints
// the grounded answer plus its in-slice citations, and writes nothing under
// ~/.lucid/ (read-only — AC-6).
func TestAsk_AnswersWithCitationsReadOnly(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	id := seedAcceptedInsight(t, home, reflectNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	before := homeTreeHash(t, home)
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{askAnswerReply(id)}})

	out, err := runAsk(t, "how", "do", "I", "act", "in", "groups?")
	require.NoError(t, err)

	assert.Contains(t, out, "you tend to go quiet")
	assert.Contains(t, out, "Sources: insight:"+id, "in-slice citations are printed")
	assert.Equal(t, before, homeTreeHash(t, home), "/ask writes nothing")
}

// TestAsk_JSON proves the --json view carries the outcome, message, and in-slice
// citations for a passed answer.
func TestAsk_JSON(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	id := seedAcceptedInsight(t, home, reflectNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{askAnswerReply(id)}})

	out, err := runAsk(t, "--json", "how do I act in groups?")
	require.NoError(t, err)

	var view askView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.Equal(t, string(reflection.OutcomeAnswer), view.Outcome)
	assert.False(t, view.Blocked)
	require.Len(t, view.Citations, 1)
	assert.Equal(t, id, view.Citations[0].ID)
	assert.Equal(t, reflection.CitationInsight, view.Citations[0].Kind)
}

// TestAsk_OutOfSliceCitationBlockedToFallback proves an answer citing an id not
// in the slice is Safety-blocked to the calm fallback with no citations printed
// (AC-7 — Safety on the live ask path).
func TestAsk_OutOfSliceCitationBlockedToFallback(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	seedAcceptedInsight(t, home, reflectNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	before := homeTreeHash(t, home)
	// The model cites an out-of-slice id twice, so the agent's retry cannot
	// recover and the answer reaches Safety, which blocks it (Sf-7).
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{
		askAnswerReply("i_2026_99_99_z"),
		askAnswerReply("i_2026_99_99_z"),
	}})

	out, err := runAsk(t, "--json", "q?")
	require.NoError(t, err)

	var view askView
	require.NoError(t, json.Unmarshal([]byte(out), &view))
	assert.True(t, view.Blocked, "an out-of-slice citation is blocked")
	assert.Equal(t, string(safety.Block), view.Decision)
	assert.Equal(t, string(safety.ReasonUnverifiedClaim), view.ReasonCode)
	assert.Empty(t, view.Citations, "a blocked answer surfaces no citations")
	assert.Equal(t, before, homeTreeHash(t, home))
}

// TestAsk_BlockedProseHasNoSources proves the prose (non-JSON) block path prints
// the fixed fallback and no Sources line.
func TestAsk_BlockedProseHasNoSources(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	seedAcceptedInsight(t, home, reflectNow().Add(-2*24*time.Hour), "I go quiet in groups.")

	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{
		askAnswerReply("i_2026_99_99_z"),
		askAnswerReply("i_2026_99_99_z"),
	}})

	out, err := runAsk(t, "q?")
	require.NoError(t, err)
	assert.NotContains(t, out, "Sources:", "a blocked answer prints no citations")
	assert.NotEmpty(t, out, "the calm fallback is still printed")
}

// TestAsk_EmptyStoreInsufficient proves /ask over an empty store returns the
// insufficient copy (pointing at /checkin or /log) without a model call, and
// touches nothing.
func TestAsk_EmptyStoreInsufficient(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	// Scaffold an empty ledger so the tree hash is stable across the read.
	a := storage.New(home)
	_, err := a.Scaffold()
	require.NoError(t, err)

	before := homeTreeHash(t, home)
	fake := &provider.Fake{}
	withServeProvider(t, fake)

	out, err := runAsk(t, "anything?")
	require.NoError(t, err)
	assert.Contains(t, out, "/checkin")
	assert.Contains(t, out, "/log")
	assert.Equal(t, 0, fake.Calls(), "no model call over an empty store")
	assert.Equal(t, before, homeTreeHash(t, home))
}

// TestAsk_RequiresQuestion proves a bare `lucid ask` is a usage error.
func TestAsk_RequiresQuestion(t *testing.T) {
	isolatedHome(t)
	withServeProvider(t, &provider.Fake{})

	_, err := runAsk(t)
	require.Error(t, err)
	assert.Equal(t, ExitUsage, exitCodeForError(err))
}

// TestAsk_ProviderBuildErrorSurfaces proves a provider that cannot be built
// fails the verb rather than printing a false answer.
func TestAsk_ProviderBuildErrorSurfaces(t *testing.T) {
	home := isolatedHome(t)
	withClock(t, reflectNow())
	seedAcceptedInsight(t, home, reflectNow().Add(-2*24*time.Hour), "I go quiet in groups.")
	withFailingProvider(t)

	_, err := runAsk(t, "q?")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unavailable")
}

// TestAsk_BootError proves an unscaffoldable home fails the verb at boot, before
// any model call.
func TestAsk_BootError(t *testing.T) {
	unscaffoldableHome(t)
	withServeProvider(t, &provider.Fake{})
	_, err := runAsk(t, "q?")
	require.Error(t, err)
}
