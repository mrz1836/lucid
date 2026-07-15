package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/companion"
	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/scheduler"
	"github.com/mrz1836/lucid/internal/storage"
)

// Flags on `lucid companion fire`. --deliver actually sends; the default is a
// dry-run capture with zero side effect. --dry-run is accepted explicitly so a
// script can be unambiguous; it is mutually exclusive with --deliver.
const (
	companionFlagMode    = "mode"
	companionFlagDeliver = "deliver"
	companionFlagDryRun  = "dry-run"
)

// noopNotifier is a do-nothing [scheduler.Notifier]. The companion reads the
// tripwire verdict send-free (it renders, it never delivers), so the scheduler
// it reads through needs a notifier only to construct — this one is never asked
// to send.
type noopNotifier struct{}

// Send discards: the send-free verdict read never calls it.
func (noopNotifier) Send(_, _ string) error { return nil }

// newCompanionCmd wires the `companion` verb group and its on-demand `fire`
// child. The scheduled companion runs inside `lucid scheduler run`; this verb is
// the operator's way to compose (and optionally deliver) one message now — to
// preview the voice, prove the pipeline end to end, or re-send after a miss.
func newCompanionCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "companion",
		Short: "Compose and deliver the daily companion messages",
		Long: `companion groups the Mirror-side daily companion: the morning and
night messages Lucid composes through its model provider from your own opaque
prompt files and the chain's honest live numbers. The two messages fire
automatically inside ` + "`lucid scheduler run`" + ` on the chain's bell and
tripwire marks; ` + "`companion fire`" + ` composes one on demand.`,
		Args: cobra.NoArgs,
	}
	parent.AddCommand(newCompanionFireCmd())
	return parent
}

// newCompanionFireCmd builds `lucid companion fire --mode morning|night
// [--dry-run|--deliver]`. A dry-run composes and prints the message with zero
// side effect (no delivery, no receipt); --deliver sends one idempotent,
// read-back-verified message to the user channel.
func newCompanionFireCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fire",
		Short: "Compose one companion message now (dry-run by default)",
		Long: `fire composes one companion message for a window immediately. By
default it is a dry-run: it composes and prints the message and touches nothing
(no send, no delivery receipt). Pass --deliver to actually post one idempotent,
read-back-verified message to the user channel — the same path the scheduled
companion takes, so a delivered test fire honors the missed-fire window and the
delivery receipt exactly as a real fire would.`,
		Args: cobra.NoArgs,
		Example: `  # Preview the morning message without sending it.
  lucid companion fire --mode morning

  # Actually deliver one night message now.
  lucid companion fire --mode night --deliver`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			modeStr, _ := cmd.Flags().GetString(companionFlagMode)
			deliver, _ := cmd.Flags().GetBool(companionFlagDeliver)
			return runCompanionFire(cmd, modeStr, deliver)
		},
	}
	cmd.Flags().String(companionFlagMode, "", "Which window to compose: morning|night (required)")
	cmd.Flags().Bool(companionFlagDeliver, false, "Deliver the message (default: dry-run capture with no side effect)")
	cmd.Flags().Bool(companionFlagDryRun, false, "Compose and print without delivering (the default)")
	cmd.MarkFlagsMutuallyExclusive(companionFlagDeliver, companionFlagDryRun)
	return cmd
}

// runCompanionFire resolves the window, boots the Ledger, builds the compose
// dependencies (numbers from the router, the send-free verdict from the
// scheduler, the opaque prompt files + provider from lucid.json), and either
// captures a dry-run or delivers one idempotent message.
func runCompanionFire(cmd *cobra.Command, modeStr string, deliver bool) error {
	mode, err := companionModeFromFlag(modeStr)
	if err != nil {
		return err
	}

	store, r, err := bootCompanion(cmd)
	if err != nil {
		return err
	}
	cfg := r.Config()
	if !cfg.Companion.Enabled {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: companion.enabled is false — the scheduler will not fire this automatically")
	}

	deps := companion.Deps{
		Companion: cfg.Companion,
		Provider:  cfg.Provider,
		Numbers:   r,
		Verdict:   scheduler.New(store, noopNotifier{}),
		Chain:     store,
		Build:     buildProvider,
	}

	if !deliver {
		res, cErr := companion.New(deps).Compose(cmd.Context(), mode, clockNow())
		if cErr != nil {
			return cErr
		}
		return renderCompanionDryRun(cmd.OutOrStdout(), res)
	}

	// A real delivery needs the env-injected Discord transport (the credential-
	// dumb notifier — token + channel come from the environment only).
	discord, err := notify.NewDiscordFromEnv()
	if err != nil {
		return fmt.Errorf("lucid companion fire: %w", err)
	}
	out, err := companion.NewRunner(deps, discord, store).Fire(cmd.Context(), mode, clockNow())
	if err != nil {
		return err
	}
	return renderCompanionFire(cmd.OutOrStdout(), out)
}

// companionModeFromFlag resolves the required --mode flag to a companion window,
// rejecting a missing or unknown value so a typo never composes the wrong
// window.
func companionModeFromFlag(s string) (companion.Mode, error) {
	switch s {
	case string(companion.ModeMorning):
		return companion.ModeMorning, nil
	case string(companion.ModeNight):
		return companion.ModeNight, nil
	case "":
		return "", fmt.Errorf("lucid companion fire: --mode is required (morning|night)")
	default:
		return "", fmt.Errorf("lucid companion fire: unknown mode %q — use morning|night", s)
	}
}

// bootCompanion opens the Ledger, scaffolds it, and boots the router (surfacing
// config clip warnings once) — returning both the store (for the send-free
// scheduler and the delivery receipts) and the booted router (for the config
// and the honest live numbers).
func bootCompanion(cmd *cobra.Command) (*storage.Adapter, *router.Router, error) {
	store, err := storage.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve home: %w", err)
	}
	if _, err = store.Scaffold(); err != nil {
		return nil, nil, err
	}
	r := router.New(store)
	warnings, err := r.Boot()
	if err != nil {
		return nil, nil, err
	}
	for _, w := range warnings {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
	}
	return store, r, nil
}

// renderCompanionDryRun prints a composed message for a person to read, naming
// the deterministic-fallback and miss-day paths when they fired so a preview is
// never mistaken for the model's warm output when it was not.
func renderCompanionDryRun(out io.Writer, res companion.Result) error {
	_, _ = fmt.Fprintf(out, "── companion %s (dry-run — not delivered) ──\n", res.Mode)
	if res.Fallback {
		_, _ = fmt.Fprintln(out, "[deterministic fallback — the provider was unreachable; only warmth is lost]")
	}
	if res.MissDay {
		_, _ = fmt.Fprintln(out, "[miss-day — the Engine verdict is appended verbatim below]")
	}
	_, _ = fmt.Fprintln(out, res.Text)
	return nil
}

// renderCompanionFire reports how a real delivery resolved: a skip (idempotent
// or past the cut-off) or a delivered message id, noting a late-note or
// fallback delivery.
func renderCompanionFire(out io.Writer, o companion.Outcome) error {
	switch {
	case o.Skipped:
		_, _ = fmt.Fprintf(out, "companion %s skipped (%s).\n", o.Mode, o.SkipReason)
	case o.Delivered:
		note := ""
		if o.Late {
			note += " (late note prepended)"
		}
		if o.Fallback {
			note += " (deterministic fallback)"
		}
		_, _ = fmt.Fprintf(out, "companion %s delivered%s — message %s.\n", o.Mode, note, o.MessageID)
	}
	return nil
}
