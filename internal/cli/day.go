package cli

import (
	"time"

	"github.com/spf13/cobra"
)

// newDayCmd wires `lucid day [date|yesterday]` (observations-module.md
// §Commands): the read-only day view joining the engine day record, the
// day's observations (plus any spanning range event), and the raw entry ids
// for one logical day. Human-first prose by default; the assembled view as
// JSON under --json for scripts (ADR-0007). It writes nothing.
func newDayCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "day [date|yesterday]",
		Short: "Show a day's joined record",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			arg := ""
			if len(args) > 0 {
				arg = args[0]
			}
			res, err := r.DayView(arg, time.Now())
			if err != nil {
				return err
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
}
