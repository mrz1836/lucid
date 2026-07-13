package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Flag names for `lucid attach`. Caption is the optional verbatim
// description stored with the media; day is the optional logical-day
// selector (`@yesterday` / `@YYYY-MM-DD`) reusing the shared rollover
// grammar. Provenance rides the same accept surface the capture verbs
// share (flagSource/flagHarness/flagChannel, declared in log.go), so a
// relaying harness can attribute an attach through flags or LUCID_* env.
const (
	flagCaption = "caption"
	flagDay     = "day"
)

// attachJSON is the machine-readable result emitted by `lucid attach --json`
// (data-model.md / commands.md §attach): the stored path, its sha256, the
// resolved logical day, the linked raw entry id, and the caption. It mirrors
// the fields on [router.AttachResult] a script needs to locate and verify the
// stored binary. Caption is omitted when empty (the frictionless "drop it" path).
type attachJSON struct {
	StoredPath string `json:"stored_path"`
	SHA256     string `json:"sha256"`
	Day        string `json:"day"`
	RawID      string `json:"raw_id"`
	Caption    string `json:"caption,omitempty"`
}

// newAttachCmd wires `lucid attach <path> [--caption …] [--day @yesterday]`:
// the deterministic, agent-free verb that files any binary artifact into the
// ~/.lucid/media/ store and emits one linked immutable raw entry so the Mirror
// can find it (data-model.md §"Media attachments"). It is dispatch-only over
// [router.Attach] — no model runs here or downstream in the write path
// (architecture P3). The Ledger and its media/ tree scaffold on first use so a
// capture never blocks on setup (product-principles.md P10).
func newAttachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <path>",
		Short: "Attach a media file to a logical day",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			if err = r.Store().ScaffoldMedia(); err != nil {
				return fmt.Errorf("lucid attach: %w", err)
			}

			caption, _ := cmd.Flags().GetString(flagCaption)
			day, _ := cmd.Flags().GetString(flagDay)

			res, err := r.Attach(router.AttachRequest{
				Path:      args[0],
				Caption:   caption,
				DayArg:    day,
				Now:       time.Now(),
				Source:    flagOrEnv(cmd, flagSource, envSource, sourceCLI),
				Harness:   flagOrEnv(cmd, flagHarness, envHarness, sourceCLI),
				ChannelID: flagOrEnv(cmd, flagChannel, envChannel, sourceCLI),
			})
			if err != nil {
				return err
			}

			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), attachJSON{
					StoredPath: res.StoredPath,
					SHA256:     res.SHA256,
					Day:        res.Day,
					RawID:      res.RawID,
					Caption:    res.Caption,
				})
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	cmd.Flags().String(flagCaption, "", "Optional caption/alt-text stored verbatim with the attachment")
	cmd.Flags().String(flagDay, "", "Attribute to a logical day, e.g. @yesterday or @YYYY-MM-DD (04:00 rollover aware)")
	// The three provenance fields router.Attach consumes, declared through the
	// capture verbs' shared flag > env > default accept surface so a relaying
	// harness can attribute an attach while a bare terminal call stays cli.
	f := cmd.Flags()
	f.String(flagSource, "", "Harness source token recorded on the attachment (overrides "+envSource+")")
	f.String(flagHarness, "", "Surface that hosted the capture (overrides "+envHarness+")")
	f.String(flagChannel, "", "Channel the capture came in through (overrides "+envChannel+")")
	return cmd
}
