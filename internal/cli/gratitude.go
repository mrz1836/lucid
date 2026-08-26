package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// newGratitudeCmd wires `lucid gratitude` (gratitude.md §3, §6): the
// accumulating nightly-gratitude tally. It is a thin dispatch group over two
// subcommands — deterministic, agent-free, no LLM in any path (architecture P9),
// modeled on `lucid reframe`:
//
//	lucid gratitude add "my morning coffee"
//	lucid gratitude add "a walk outside" --day @yesterday
//	lucid gratitude list --json
//
// add tallies one occurrence (creating the entry or bumping a canonical-key
// match) and prints the receipt id; list shows the tally sorted by count then
// recency with a stable id per entry so a later `--into`/`merge` can target one.
func newGratitudeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gratitude",
		Short: "Keep an accumulating tally of what you're grateful for",
	}
	cmd.AddCommand(newGratitudeAddCmd(), newGratitudeListCmd())
	return cmd
}

// newGratitudeAddCmd wires `lucid gratitude add <thing> [--day <date>]`. The
// thing is joined from the trailing args (the obs/injury precedent) and stored
// verbatim; the canonical key auto-matches an existing entry or starts a new
// one. `--day` is the strict backdating tier — a bad token or a future day is a
// clean refusal that writes nothing, printed to stderr. `--json` emits the
// receipt and the resulting tally where it fits the verb.
func newGratitudeAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <thing>",
		Short: "Tally one thing you're grateful for",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			day, _ := cmd.Flags().GetString(flagDay)
			res, err := r.AddGratitude(router.AddGratitudeRequest{
				Thing:  strings.Join(args, " "),
				DayArg: day,
				Now:    clockNow(),
			})
			if err != nil {
				// The root silences returned errors, so a rejected --day or an empty
				// thing would otherwise be a bare exit code. emitErr prints the reason
				// (a DayRejectedError's Error is its accepted-forms message) and returns
				// the error unchanged so the exit code still travels.
				return emitErr(cmd, err)
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), gratitudeAddViewOf(res))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	registerDayFlag(cmd)
	return cmd
}

// newGratitudeListCmd wires `lucid gratitude list [--json]`: the live tally
// (tombstones omitted), sorted by count then recency, human-first by default
// with the structured list under `--json` (ADR-0007). It writes nothing.
func newGratitudeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the gratitude tally, most-returned-to first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.GratitudeList()
			if err != nil {
				return err
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
}

// gratitudeAddView is the machine-readable projection of an `add` under --json:
// the receipt id of this write, the stable entry id, and the resulting tally.
// Built CLI-side with stable snake_case names so a harness branches on fields
// rather than parsing the ack prose.
type gratitudeAddView struct {
	Receipt string `json:"receipt"`
	ID      string `json:"id"`
	Thing   string `json:"thing"`
	Count   int    `json:"count"`
	First   string `json:"first"`
	Last    string `json:"last"`
	Created bool   `json:"created"`
}

// gratitudeAddViewOf projects a router result into the stable --json shape.
func gratitudeAddViewOf(res router.GratitudeWriteResult) gratitudeAddView {
	return gratitudeAddView{
		Receipt: res.Receipt,
		ID:      res.Key,
		Thing:   res.Thing,
		Count:   res.Count,
		First:   res.First,
		Last:    res.Last,
		Created: res.Created,
	}
}
