package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// witnessSlotReply is a well-formed, witness-safe model reply that fills the four
// labeled compose slots. It carries no private-detail marker, so it passes the
// witness-safe output scan and the composed prose lands on the report.
const witnessSlotReply = "%%FAULTS%%\n" +
	"The chain held — nothing to flag this week.\n\n" +
	"%%PROGRESS%%\n" +
	"Steady, consistent showing up.\n\n" +
	"%%NARRATIVE%%\n" +
	"A calm, on-track week. Keep the momentum into next week.\n\n" +
	"%%ASKS%%\n" +
	"- Ask me how the week went."

// fakeEmbedDeliverer is an embedDeliverer that captures the resolved channel and
// the embed it was handed, so a delivery test asserts preview→user / auto→witness
// routing without a live Discord token. A failing test injects sendErr.
type fakeEmbedDeliverer struct {
	sends     int
	channel   string
	embed     notify.Embed
	id        string
	verified  []string
	sendErr   error
	verifyErr error
}

func (f *fakeEmbedDeliverer) SendEmbedReturningID(channel string, e notify.Embed) (string, error) {
	f.sends++
	f.channel = channel
	f.embed = e
	if f.sendErr != nil {
		return "", f.sendErr
	}
	if f.id == "" {
		f.id = "msg-100000000000000009"
	}
	return f.id, nil
}

func (f *fakeEmbedDeliverer) VerifyPresent(channel, messageID string) error {
	f.verified = append(f.verified, channel+"/"+messageID)
	return f.verifyErr
}

// withWitnessDeliverer injects a fake deliverer for the duration of a test so the
// witness-report delivery path never needs a real Discord token, mirroring
// withServeProvider/withClock.
func withWitnessDeliverer(t *testing.T, d embedDeliverer) {
	t.Helper()
	prev := newWitnessDeliverer
	newWitnessDeliverer = func() (embedDeliverer, error) { return d, nil }
	t.Cleanup(func() { newWitnessDeliverer = prev })
}

// seedWitnessHome scaffolds an isolated Ledger and writes a lucid.json whose
// witness_report block is configured (enabled/mode per the args) and points at two
// real opaque prompt files under a separate temp dir. The isolated home is wired
// through LUCID_HOME, so a test drives the CLI without capturing the path.
func seedWitnessHome(t *testing.T, enabled bool, mode string) {
	t.Helper()
	home := isolatedHome(t)

	store := storage.New(home)
	_, err := store.Scaffold()
	require.NoError(t, err)

	dir := t.TempDir()
	sys := filepath.Join(dir, "system_prompt.md")
	tmpl := filepath.Join(dir, "template.md")
	require.NoError(t, os.WriteFile(sys, []byte("WITNESS VOICE"), 0o600))
	require.NoError(t, os.WriteFile(tmpl, []byte("WITNESS TEMPLATE"), 0o600))

	cfg := config.Default()
	cfg.WitnessReport = config.WitnessReportConfig{
		Enabled:      enabled,
		Mode:         mode,
		Time:         "09:00",
		Weekday:      1,
		SystemPrompt: sys,
		Template:     tmpl,
	}
	b, err := cfg.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.ConfigPath(), b, 0o600))
}

// runWitness executes `lucid witness [args...]`, capturing stdout, stderr, and the
// command error.
func runWitness(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newRootCmd(BuildInfo{Version: "dev"})
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"witness"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), errBuf.String(), err
}

// TestWitness_TreeExposesReport proves the `witness` group and its `report` child
// are registered, and that `report` carries the deliver/dry-run flags.
func TestWitness_TreeExposesReport(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})

	wit, _, err := root.Find([]string{"witness"})
	require.NoError(t, err)
	assert.Equal(t, "witness", wit.Name())

	reportCmd, _, err := root.Find([]string{"witness", "report"})
	require.NoError(t, err)
	assert.Equal(t, "report", reportCmd.Name())
	assert.NotNil(t, reportCmd.Flags().Lookup(witnessFlagDeliver))
	assert.NotNil(t, reportCmd.Flags().Lookup(witnessFlagDryRun))
}

// TestWitness_RegisteredOnSpine proves `witness` is on the cobra root.
func TestWitness_RegisteredOnSpine(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "witness" {
			found = true
		}
	}
	assert.True(t, found, "witness must be registered on the root command")
}

// TestWitnessReport_DryRun_ComposesNoSideEffect: a dry-run composes the report
// through the (faked) provider, prints the structured-markdown render, and never
// touches the deliverer — the meaningful zero-side-effect guarantee (a fake
// deliverer that would fail the test if called is never called).
func TestWitnessReport_DryRun_ComposesNoSideEffect(t *testing.T) {
	seedWitnessHome(t, true, config.WitnessReportModePreview)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: witnessSlotReply}}})

	deliverer := &fakeEmbedDeliverer{}
	withWitnessDeliverer(t, deliverer)

	out, _, err := runWitness(t, "report")
	require.NoError(t, err)

	assert.Contains(t, out, "Weekly witness report")
	assert.Contains(t, out, "dry-run")
	assert.Contains(t, out, "How friends can help", "the rendered markdown carries the friend-asks section")
	assert.Zero(t, deliverer.sends, "a dry-run never delivers")
}

// TestWitnessReport_DryRun_JSON: `--dry-run --json` prints the report data model
// (the deterministic scaffold) rather than the rendered markdown — the Phase-4
// checkpoint shape.
func TestWitnessReport_DryRun_JSON(t *testing.T) {
	seedWitnessHome(t, true, config.WitnessReportModePreview)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: witnessSlotReply}}})

	out, _, err := runWitness(t, "report", "--dry-run", "--json")
	require.NoError(t, err)

	// The data-model keys, not the rendered markdown, prove --json emits the report
	// struct a harness reads.
	assert.Contains(t, out, `"iso_week"`)
	assert.Contains(t, out, `"streak"`)
	assert.Contains(t, out, `"adherence"`)
	assert.Contains(t, out, `"asks"`)
	assert.NotContains(t, out, "── weekly witness report", "the --json branch skips the human banner")
}

// TestWitnessReport_QuietWeek_StillRenders (AC-13): a fresh Ledger with nothing
// logged is a quiet week — the report still renders honestly, surfacing the thin
// logging as its own watch-out rather than suppressing the report.
func TestWitnessReport_QuietWeek_StillRenders(t *testing.T) {
	seedWitnessHome(t, true, config.WitnessReportModePreview)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: witnessSlotReply}}})

	out, _, err := runWitness(t, "report", "--dry-run", "--json")
	require.NoError(t, err)
	assert.Contains(t, out, `"low_signal": true`, "a quiet week is flagged low-signal")
	assert.Contains(t, out, "accountability risk", "the thin logging is surfaced as a watch-out")
}

// TestWitnessReport_Deliver_PreviewRoutesToUser (AC-12): mode preview + --deliver
// posts to the operator's own user channel and read-back-verifies the message.
func TestWitnessReport_Deliver_PreviewRoutesToUser(t *testing.T) {
	seedWitnessHome(t, true, config.WitnessReportModePreview)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: witnessSlotReply}}})

	deliverer := &fakeEmbedDeliverer{}
	withWitnessDeliverer(t, deliverer)

	out, _, err := runWitness(t, "report", "--deliver")
	require.NoError(t, err)

	assert.Equal(t, 1, deliverer.sends)
	assert.Equal(t, engine.ChannelUser, deliverer.channel, "preview mode routes to the user channel")
	require.Len(t, deliverer.verified, 1, "the delivered message is read-back-verified")
	assert.Contains(t, deliverer.verified[0], engine.ChannelUser)
	assert.Contains(t, out, "delivered to the user channel")
	assert.NotEmpty(t, deliverer.embed.Title, "a rich embed is delivered, not plain text")
}

// TestWitnessReport_Deliver_AutoRoutesToWitness (AC-1, AC-12): mode auto +
// --deliver posts to the friend-facing witness channel.
func TestWitnessReport_Deliver_AutoRoutesToWitness(t *testing.T) {
	seedWitnessHome(t, true, config.WitnessReportModeAuto)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: witnessSlotReply}}})

	deliverer := &fakeEmbedDeliverer{}
	withWitnessDeliverer(t, deliverer)

	out, _, err := runWitness(t, "report", "--deliver")
	require.NoError(t, err)

	assert.Equal(t, 1, deliverer.sends)
	assert.Equal(t, engine.ChannelWitness, deliverer.channel, "auto mode routes to the witness channel")
	assert.Contains(t, out, "delivered to the witness channel")
}

// TestWitnessReport_Deliver_DisabledNoOp: with the feature disabled a --deliver is
// a clean no-op — nothing is composed or sent, and a warning explains why.
func TestWitnessReport_Deliver_DisabledNoOp(t *testing.T) {
	seedWitnessHome(t, false, config.WitnessReportModePreview)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))

	deliverer := &fakeEmbedDeliverer{}
	withWitnessDeliverer(t, deliverer)

	out, stderr, err := runWitness(t, "report", "--deliver")
	require.NoError(t, err)

	assert.Zero(t, deliverer.sends, "a disabled feature never delivers")
	assert.Empty(t, out, "the no-op prints nothing to stdout")
	assert.Contains(t, stderr, "witness_report.enabled is false")
	assert.Contains(t, stderr, "nothing delivered")
}

// TestWitnessReport_DisabledDryRunWarns: with the feature disabled a dry-run still
// composes a preview (the "preview the voice before enabling" use case), but warns
// that the scheduler will not post it automatically.
func TestWitnessReport_DisabledDryRunWarns(t *testing.T) {
	seedWitnessHome(t, false, config.WitnessReportModePreview)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: witnessSlotReply}}})

	out, stderr, err := runWitness(t, "report")
	require.NoError(t, err)
	assert.Contains(t, out, "Weekly witness report")
	assert.Contains(t, stderr, "witness_report.enabled is false")
	assert.Contains(t, stderr, "will not post this automatically")
}

// TestWitnessReport_Deliver_Fallback: when the provider is unreachable the report
// still delivers — the deterministic metrics-only report lands, flagged as a
// fallback, so only the narrative warmth is lost, never the report.
func TestWitnessReport_Deliver_Fallback(t *testing.T) {
	seedWitnessHome(t, true, config.WitnessReportModePreview)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Err: provider.ErrUnavailable}}})

	deliverer := &fakeEmbedDeliverer{}
	withWitnessDeliverer(t, deliverer)

	out, _, err := runWitness(t, "report", "--deliver")
	require.NoError(t, err)
	assert.Equal(t, 1, deliverer.sends, "a provider outage still delivers the deterministic report")
	assert.Contains(t, out, "deterministic fallback")
}

// TestWitnessReport_DeliverAndDryRun_MutuallyExclusive: the two flags cannot both
// be set.
func TestWitnessReport_DeliverAndDryRun_MutuallyExclusive(t *testing.T) {
	seedWitnessHome(t, true, config.WitnessReportModePreview)
	_, _, err := runWitness(t, "report", "--deliver", "--dry-run")
	require.Error(t, err)
}

// TestWitnessReport_RejectsArgs guards that `report` is a no-args verb (a stray
// positional is a usage error), matching the rest of the spine.
func TestWitnessReport_RejectsArgs(t *testing.T) {
	root := newRootCmd(BuildInfo{Version: "dev"})
	root.SetArgs([]string{"witness", "report", "extra"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	err := root.Execute()
	require.Error(t, err)
}

// TestWitnessReport_DryRun_SafetyTripped (AC-6 surfacing): when the model prose
// carries a private-detail marker the witness-safe scan fails closed — the dry-run
// names the trip and shows the deterministic metrics-only report, never the flagged
// prose.
func TestWitnessReport_DryRun_SafetyTripped(t *testing.T) {
	seedWitnessHome(t, true, config.WitnessReportModePreview)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	// A NARRATIVE that names a private relationship trips the witness-safe scan.
	tripped := "%%FAULTS%%\nfine\n\n%%PROGRESS%%\nfine\n\n%%NARRATIVE%%\n" +
		"had a hard week with my therapist\n\n%%ASKS%%\n- check in"
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: tripped}}})

	out, _, err := runWitness(t, "report")
	require.NoError(t, err)
	assert.Contains(t, out, "witness-safe scan tripped")
	assert.NotContains(t, out, "my therapist", "the flagged prose is never shown")
}

// TestWitnessReport_Deliver_SendError surfaces a transport failure loudly rather
// than reporting a delivery that never landed.
func TestWitnessReport_Deliver_SendError(t *testing.T) {
	seedWitnessHome(t, true, config.WitnessReportModePreview)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: witnessSlotReply}}})
	withWitnessDeliverer(t, &fakeEmbedDeliverer{sendErr: assertAnErr()})

	_, _, err := runWitness(t, "report", "--deliver")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deliver")
}

// TestWitnessReport_Deliver_VerifyError: a message that posts but fails read-back
// verification is a loud error — a delivery is never recorded as verified when the
// message is not really there.
func TestWitnessReport_Deliver_VerifyError(t *testing.T) {
	seedWitnessHome(t, true, config.WitnessReportModePreview)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: witnessSlotReply}}})
	withWitnessDeliverer(t, &fakeEmbedDeliverer{verifyErr: assertAnErr()})

	_, _, err := runWitness(t, "report", "--deliver")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read-back verify")
}

// TestWitnessReport_Deliver_TransportBuildError: a failure building the Discord
// transport (e.g. the token/channel env is unset) surfaces as a clear error.
func TestWitnessReport_Deliver_TransportBuildError(t *testing.T) {
	seedWitnessHome(t, true, config.WitnessReportModePreview)
	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: witnessSlotReply}}})

	prev := newWitnessDeliverer
	newWitnessDeliverer = func() (embedDeliverer, error) { return nil, assertAnErr() }
	t.Cleanup(func() { newWitnessDeliverer = prev })

	_, _, err := runWitness(t, "report", "--deliver")
	require.Error(t, err)
}

// TestWitnessReport_MissingPromptFile_Errors: an enabled report whose configured
// prompt path does not exist is a loud compose error, not a silent empty report.
func TestWitnessReport_MissingPromptFile_Errors(t *testing.T) {
	home := isolatedHome(t)
	store := storage.New(home)
	_, err := store.Scaffold()
	require.NoError(t, err)

	cfg := config.Default()
	cfg.WitnessReport = config.WitnessReportConfig{
		Enabled:      true,
		Mode:         config.WitnessReportModePreview,
		Time:         "09:00",
		Weekday:      1,
		SystemPrompt: filepath.Join(t.TempDir(), "does-not-exist.md"),
		Template:     filepath.Join(t.TempDir(), "also-missing.md"),
	}
	b, err := cfg.Marshal()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(store.ConfigPath(), b, 0o600))

	withClock(t, time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC))
	withServeProvider(t, &provider.Fake{Script: []provider.Exchange{{Content: witnessSlotReply}}})

	_, _, runErr := runWitness(t, "report")
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "system prompt")
}

// TestWitnessChannelForMode unit-covers the mode → logical-channel mapping,
// including the defensive default branch config validation normally forecloses on
// an enabled report.
func TestWitnessChannelForMode(t *testing.T) {
	user, err := witnessChannelForMode(config.WitnessReportModePreview)
	require.NoError(t, err)
	assert.Equal(t, engine.ChannelUser, user)

	wit, err := witnessChannelForMode(config.WitnessReportModeAuto)
	require.NoError(t, err)
	assert.Equal(t, engine.ChannelWitness, wit)

	_, err = witnessChannelForMode("broadcast")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown witness_report.mode")
}

// assertAnErr returns a non-nil error for the transport-failure tests.
func assertAnErr() error { return errWitnessTest }

// errWitnessTest is a fixed sentinel for the delivery-failure tests.
var errWitnessTest = errors.New("witness transport failure")
