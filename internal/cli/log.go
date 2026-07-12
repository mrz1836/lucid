package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// sourceCLI identifies the local command-line surface in raw entries and
// session records.
const sourceCLI = "cli"

// Provenance accept-surface flag names shared by the capture commands
// (lucid log, lucid obs). Each is optional; a bare terminal call falls back to
// its LUCID_* env then the local default, so a relaying harness can attribute a
// capture through flags or env without a code change.
const (
	flagSource  = "source"
	flagHarness = "harness"
	flagAgent   = "agent"
	flagModel   = "model"
	flagChannel = "channel"
	flagThread  = "thread"
)

// Environment-variable fallbacks for the provenance accept surface, paired with
// the flags above (flag > env > default).
const (
	envSource  = "LUCID_SOURCE"
	envHarness = "LUCID_HARNESS"
	envAgent   = "LUCID_AGENT"
	envModel   = "LUCID_MODEL"
	envChannel = "LUCID_CHANNEL"
	envThread  = "LUCID_THREAD"
)

// newLogCmd wires `lucid log [text]`: capture the text as one immutable
// raw entry under ~/.lucid/raw/ with a sub-second ack. It scaffolds the
// Ledger on first use (idempotently) so capture never blocks on setup
// (product-principles.md P10). Structuring and insight work run in later
// stages; /log only captures — nothing is written under processed/ or
// insights/.
func newLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "log [text]",
		Short: "Capture an immutable raw entry",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := storage.Open()
			if err != nil {
				return fmt.Errorf("lucid log: resolve home: %w", err)
			}
			if _, err = store.Scaffold(); err != nil {
				return fmt.Errorf("lucid log: %w", err)
			}
			r := router.New(store)
			warnings, err := r.Boot()
			if err != nil {
				return fmt.Errorf("lucid log: %w", err)
			}
			for _, w := range warnings {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}

			res, err := r.Log(router.LogRequest{
				Text:      strings.Join(args, " "),
				Now:       time.Now(),
				Source:    flagOrEnv(cmd, flagSource, envSource, sourceCLI),
				Harness:   flagOrEnv(cmd, flagHarness, envHarness, sourceCLI),
				ChannelID: flagOrEnv(cmd, flagChannel, envChannel, sourceCLI),
				ThreadID:  flagOrEnv(cmd, flagThread, envThread, ""),
				Agent:     flagOrEnv(cmd, flagAgent, envAgent, ""),
				Model:     flagOrEnv(cmd, flagModel, envModel, ""),
			})
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	registerProvenanceFlags(cmd)
	return cmd
}

// registerProvenanceFlags declares the provenance accept-surface flags on a
// capture command (lucid log, lucid obs). Each flag defaults to the empty
// string; flagOrEnv applies the real default so the flag > env > default
// precedence lives in exactly one place.
func registerProvenanceFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String(flagSource, "", "Harness source token recorded on the entry, e.g. discord (overrides "+envSource+")")
	f.String(flagHarness, "", "Surface that hosted the capture (overrides "+envHarness+")")
	f.String(flagAgent, "", "Assistant/agent that relayed the capture (overrides "+envAgent+")")
	f.String(flagModel, "", "Model that relayed the capture (overrides "+envModel+")")
	f.String(flagChannel, "", "Channel the capture came in through (overrides "+envChannel+")")
	f.String(flagThread, "", "Thread the capture came in through (overrides "+envThread+")")
}

// flagOrEnv resolves a string with flag > env > default precedence: an
// explicitly-set flag wins, else a non-empty environment variable, else def. It
// backs the provenance accept surface shared by the capture commands so a
// relaying harness can attribute a capture through flags or LUCID_* env without
// a code change, while a bare terminal call keeps its local defaults.
func flagOrEnv(cmd *cobra.Command, flag, env, def string) string {
	if cmd.Flags().Changed(flag) {
		v, _ := cmd.Flags().GetString(flag)
		return v
	}
	if v := os.Getenv(env); v != "" {
		return v
	}
	return def
}
