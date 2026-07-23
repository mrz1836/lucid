package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/witnessreport"
)

// Flags on `lucid witness report`. --deliver actually posts; the default is a
// dry-run compose with zero side effect. --dry-run is accepted explicitly so a
// script can be unambiguous; it is mutually exclusive with --deliver. The
// report data model is emitted with the persistent --json flag.
const (
	witnessFlagDeliver = "deliver"
	witnessFlagDryRun  = "dry-run"
)

// embedDeliverer is the minimal transport seam the witness-report CLI delivers
// through: a channel-addressed rich-embed send with read-back verification. It is
// exactly the subset of *notify.Discord the report needs. Phase 5's weekly node
// layers per-week receipt idempotency and missed-fire handling over the same
// SendEmbedReturningID + VerifyPresent shape; the CLI proves the
// compose → render → post → read-back chain end to end for a manual fire and a
// preview proof. It is an interface so a test injects a fake that captures the
// resolved channel without a live Discord token.
type embedDeliverer interface {
	SendEmbedReturningID(channel string, e notify.Embed) (string, error)
	VerifyPresent(channel, messageID string) error
}

// newWitnessDeliverer is the seam that builds the concrete Discord transport from
// the injected environment (the credential-dumb notifier — token + channel ids
// come from the environment only). Tests override it with a fake so a delivery
// test never needs a real Discord token, mirroring the buildProvider/clockNow
// seams.
//
//nolint:gochecknoglobals // one injected deliverer seam so the witness-report delivery path stays testable offline
var newWitnessDeliverer = func() (embedDeliverer, error) {
	return notify.NewDiscordFromEnv()
}

// newWitnessCmd wires the `witness` verb group and its on-demand `report` child.
// The scheduled weekly report runs inside `lucid scheduler run` (Phase 5); this
// verb is the operator's way to compose (and optionally deliver) one report now —
// to preview the voice, prove the pipeline end to end in preview mode, or re-post
// after a miss.
func newWitnessCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "witness",
		Short: "Compose and deliver the weekly witness report",
		Long: `witness groups the Mirror-side weekly witness report: the friend-facing
accountability report Lucid composes each week from the chain's honest live
numbers and your own opaque prompt files. Every streak, adherence, and miss
figure is copied from the engine projection — never fabricated — while the model
fills only the warm framing, the faults, and the friend-asks. The report fires
automatically inside ` + "`lucid scheduler run`" + ` on the weekly mark;
` + "`witness report`" + ` composes one on demand.`,
		Args: cobra.NoArgs,
	}
	parent.AddCommand(newWitnessReportCmd())
	return parent
}

// newWitnessReportCmd builds `lucid witness report [--dry-run|--deliver]`. A
// dry-run composes and prints the report (structured markdown, or the report data
// model with --json) with zero side effect; --deliver posts one read-back-verified
// rich embed to the channel selected by witness_report.mode — preview to the
// operator's own user channel, auto to the friend-facing witness channel — so
// flipping preview → auto is a config change, not code.
func newWitnessReportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Compose the weekly witness report now (dry-run by default)",
		Long: `report composes one weekly witness report immediately. By default it is a
dry-run: it composes and prints the report and touches nothing (no send). Add
--json to print the report data model instead of the rendered markdown. Pass
--deliver to actually post one read-back-verified rich embed to the channel
witness_report.mode selects — preview posts to your own user channel (the safe
default during the trust-building period), auto posts to the friend-facing
witness channel. A quiet, low-signal week still renders honestly: the thin
logging is surfaced as its own watch-out rather than suppressed.`,
		Args: cobra.NoArgs,
		Example: `  # Preview this week's report without sending it.
  lucid witness report

  # Print the deterministic report data model.
  lucid witness report --dry-run --json

  # Deliver one report now to the channel witness_report.mode selects.
  lucid witness report --deliver`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deliver, _ := cmd.Flags().GetBool(witnessFlagDeliver)
			return runWitnessReport(cmd, deliver)
		},
	}
	cmd.Flags().Bool(witnessFlagDeliver, false, "Deliver the report per witness_report.mode (default: dry-run compose with no side effect)")
	cmd.Flags().Bool(witnessFlagDryRun, false, "Compose and print without delivering (the default)")
	cmd.MarkFlagsMutuallyExclusive(witnessFlagDeliver, witnessFlagDryRun)
	return cmd
}

// runWitnessReport boots the Ledger + router, composes the weekly report through
// the shared witnessreport.Composer (honest numbers from the router, the day
// records for the 7-day window, the opaque prompt files + provider from
// lucid.json), and either captures a dry-run or delivers one report per
// witness_report.mode. A --deliver on a disabled feature is a no-op — the
// scheduler will not post it and a manual fire should not either — short-circuited
// before compose so an unconfigured Ledger never errors trying to build one.
func runWitnessReport(cmd *cobra.Command, deliver bool) error {
	r, err := bootedRouter(cmd)
	if err != nil {
		return err
	}
	cfg := r.Config()
	wr := cfg.WitnessReport

	// Deliver on a disabled feature is a clean no-op, short-circuited before any
	// compose or provider build so a Ledger with no witness prompt files never
	// errors on a stray --deliver.
	if deliver && !wr.Enabled {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: witness_report.enabled is false — nothing delivered")
		return nil
	}
	if !wr.Enabled {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: witness_report.enabled is false — the scheduler will not post this automatically")
	}

	deps := witnessreport.Deps{
		SystemPrompt: wr.SystemPrompt,
		Template:     wr.Template,
		AsksFile:     wr.AsksFile,
		Model:        wr.Model,
		Provider:     cfg.Provider,
		Numbers:      r,
		Records:      r.Store(),
		Build:        buildProvider,
	}

	report, err := witnessreport.New(deps).Compose(cmd.Context(), clockNow())
	if err != nil {
		return err
	}

	if !deliver {
		return renderWitnessDryRun(cmd, report)
	}
	return deliverWitnessReport(cmd, wr.Mode, report)
}

// renderWitnessDryRun prints a composed report for a person to read: with --json
// it writes the report data model (the deterministic scaffold plus any composed
// prose), otherwise the structured-markdown render under a dry-run banner. The
// fallback and witness-safe-trip paths are named so a preview is never mistaken
// for the model's warm output when it was not.
func renderWitnessDryRun(cmd *cobra.Command, r witnessreport.Report) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), r)
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, "── weekly witness report (dry-run — not delivered) ──")
	if r.Fallback {
		_, _ = fmt.Fprintln(out, "[deterministic fallback — the provider was unreachable or returned nothing usable; only the narrative warmth is lost]")
	}
	if r.SafetyTripped {
		_, _ = fmt.Fprintln(out, "[witness-safe scan tripped — the model prose was discarded; the deterministic metrics-only report is shown]")
	}
	_, _ = fmt.Fprintln(out, witnessreport.RenderMarkdown(r))
	return nil
}

// deliverWitnessReport renders the composed report as a rich embed and posts it to
// the channel witness_report.mode selects — preview to the operator's own user
// channel, auto to the friend-facing witness channel — then read-back-verifies the
// created message so a delivery is never reported without proof it landed. This is
// the minimal manual delivery path; Phase 5's weekly node wraps the same
// SendEmbedReturningID + VerifyPresent shape with per-week receipt idempotency and
// missed-fire handling.
func deliverWitnessReport(cmd *cobra.Command, mode string, r witnessreport.Report) error {
	channel, err := witnessChannelForMode(mode)
	if err != nil {
		return err
	}
	d, err := newWitnessDeliverer()
	if err != nil {
		return fmt.Errorf("lucid witness report: %w", err)
	}
	id, err := d.SendEmbedReturningID(channel, witnessreport.RenderEmbed(r))
	if err != nil {
		return fmt.Errorf("lucid witness report: deliver: %w", err)
	}
	if err := d.VerifyPresent(channel, id); err != nil {
		return fmt.Errorf("lucid witness report: read-back verify: %w", err)
	}
	note := ""
	if r.Fallback {
		note = " (deterministic fallback)"
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "witness report delivered to the %s channel%s — message %s.\n", channel, note, id)
	return nil
}

// witnessChannelForMode resolves the delivery mode to a logical channel: preview
// posts to the operator's own user channel, auto posts to the friend-facing
// witness channel. An unknown mode is a hard error (config validation already
// rejects one on an enabled report, so this only guards a hand-driven call) rather
// than a mis-send to the wrong audience.
func witnessChannelForMode(mode string) (string, error) {
	switch mode {
	case config.WitnessReportModePreview:
		return engine.ChannelUser, nil
	case config.WitnessReportModeAuto:
		return engine.ChannelWitness, nil
	default:
		return "", fmt.Errorf("lucid witness report: unknown witness_report.mode %q — use %q|%q",
			mode, config.WitnessReportModePreview, config.WitnessReportModeAuto)
	}
}
