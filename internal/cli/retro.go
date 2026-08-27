package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Retro verb flag names. --source sets the item's verbatim provenance; --day
// comes from the shared registerDayFlag, --json from the persistent root flag.
const flagRetroSource = "source"

// newRetroCmd wires `lucid retro` (retro.md §3–§6): the R-NNN parking lot — the
// single auditable home for anything parked to revisit at the weekly Retro or a
// Gate. It is a thin dispatch group — deterministic, agent-free, no LLM in any
// path (architecture P9), modeled on `lucid focus`:
//
//	lucid retro park "Revisit whether the Sunday walk opens with deferred items"
//	lucid retro park "Try a shorter Gate cadence next quarter" --source chat
//	lucid retro park "Experiment with a two-column weekly layout" --day @yesterday
//
// park appends one immutable item and prints its minted R-NNN plus the per-write
// receipt (the echo contract, §0). The read (`list`/`show`) and transition
// (`resolve`/`defer`) subcommands, and the hidden one-time `import`, land in
// later build stages.
func newRetroCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retro",
		Short: "Park and track the R-NNN retro parking lot",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newRetroParkCmd())
	return cmd
}

// newRetroParkCmd wires `lucid retro park <item> [--source <value>] [--day
// <date>] [--json]`. The item is required and stored verbatim (all positionals
// are joined so an unquoted park works alongside a quoted one); --source sets the
// provenance verbatim (default `retro`, never synthesized). --day is the strict
// backdating tier — a bad token or a future day is a clean refusal that writes
// nothing, printed to stderr like the observation micro-log's --day. Unlike a
// plain capture, park honors --json (retro.md §3): the write result — the minted
// R-NNN, its receipt, and the folded item — as a structured object.
func newRetroParkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "park <item>",
		Short: "Park an item for the weekly retro (mints an R-NNN id)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			day, _ := cmd.Flags().GetString(flagDay)
			source, _ := cmd.Flags().GetString(flagRetroSource)
			res, err := r.ParkRetro(router.ParkRetroRequest{
				Item:   strings.Join(args, " "),
				Source: source,
				DayArg: day,
				Now:    time.Now(),
			})
			if err != nil {
				return emitRefusedDay(cmd, err)
			}
			return emit(cmd, res.View, []string{res.Ack})
		},
	}
	cmd.Flags().String(flagRetroSource, "", "Where the item came from, stored verbatim (default retro)")
	registerDayFlag(cmd)
	return cmd
}
