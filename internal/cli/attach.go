package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// attachLinkedViews projects the links an attach applied into the shared
// per-subject JSON shape, so `attach --json` and the link verbs report a link
// the same way.
func attachLinkedViews(applied []router.LinkApplied) []linkAppliedView {
	if len(applied) == 0 {
		return nil
	}
	out := make([]linkAppliedView, len(applied))
	for i, a := range applied {
		out[i] = linkAppliedView{Kind: a.Kind, Key: a.Key, EventID: a.EventID, NoOp: a.NoOp}
	}
	return out
}

// flagCaption is the attach-only flag name: the optional verbatim
// description stored with the media. The logical-day selector (flagDay) and
// the provenance cluster (flagSource/flagHarness/flagChannel) ride the accept
// surface the capture verbs share, declared in log.go — `--day` reuses the
// one rollover grammar log and attach both resolve through, and provenance
// lets a relaying harness attribute an attach through flags or LUCID_* env.
const flagCaption = "caption"

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
	// Linked names the subjects the capture linked at attach time, one per
	// --to; omitted when no --to was given so a bare attach's shape is unchanged.
	Linked []linkAppliedView `json:"linked,omitempty"`
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
			subjects, _ := cmd.Flags().GetStringArray(flagTo)

			res, err := r.Attach(router.AttachRequest{
				Path:      args[0],
				Caption:   caption,
				DayArg:    day,
				Now:       time.Now(),
				Source:    flagOrEnv(cmd, flagSource, envSource, sourceCLI),
				Harness:   flagOrEnv(cmd, flagHarness, envHarness, sourceCLI),
				ChannelID: flagOrEnv(cmd, flagChannel, envChannel, sourceCLI),
				Subjects:  subjects,
			})
			if err != nil {
				// A refused --day prints through emitRefusedDay; any other
				// refusal (an invalid --to subject, a bad source) is a plain
				// error that must still reach the user, since the root silences
				// returned errors.
				var refusedDay *router.DayRejectedError
				if errors.As(err, &refusedDay) {
					return emitRefusedDay(cmd, err)
				}
				return emitErr(cmd, err)
			}

			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), attachJSON{
					StoredPath: res.StoredPath,
					SHA256:     res.SHA256,
					Day:        res.Day,
					RawID:      res.RawID,
					Caption:    res.Caption,
					Linked:     attachLinkedViews(res.Linked),
				})
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	cmd.Flags().String(flagCaption, "", "Optional caption/alt-text stored verbatim with the attachment")
	// The same strict --day the other capture verbs carry: attach resolves through
	// the shared resolver, so it accepts every form that grammar accepts.
	registerDayFlag(cmd)
	// The three provenance fields router.Attach consumes, declared through the
	// capture verbs' shared flag > env > default accept surface so a relaying
	// harness can attribute an attach while a bare terminal call stays cli.
	f := cmd.Flags()
	f.String(flagSource, "", "Harness source token recorded on the attachment (overrides "+envSource+")")
	f.String(flagHarness, "", "Surface that hosted the capture (overrides "+envHarness+")")
	f.String(flagChannel, "", "Channel the capture came in through (overrides "+envChannel+")")
	// Repeatable --to links the attachment to its subjects at capture time, as
	// <kind>:<key>. StringArray (not StringSlice) so a key with a comma is one
	// token; a malformed or unresolvable subject leaves nothing on disk.
	f.StringArray(flagTo, nil, "Subject to link this attachment to, as <kind>:<key> (repeatable)")
	return cmd
}
