package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Era write-verb flag names (mvp/life-archive.md §4): a named life chapter with
// an optional, backdate-aware start/end range. flagNote is shared with
// `lucid injury` (declared in injury.go).
const (
	flagStart = "start"
	flagEnd   = "end"
)

// newEraCmd wires `lucid era <name> [--start …] [--end …] [--note …]`: the
// registry-write verb for a life chapter (mvp/life-archive.md §4). Stories
// attach to an era via refs.era so the past becomes browsable by chapter rather
// than by a date no one remembers. Either bound may be approximate; an open end
// is a still-running chapter. Dispatch-only over [router.WriteEra] —
// deterministic and agent-free — reusing the same append-only merge path as
// `lucid injury`. A name may contain spaces (joined from the trailing args).
func newEraCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "era <name>",
		Short: "Record or amend a life chapter (era)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			req := router.EraWriteRequest{
				Name: strings.Join(args, " "),
				Now:  clockNow(),
			}
			req.Start, _ = f.GetString(flagStart)
			req.End, _ = f.GetString(flagEnd)
			req.Note, _ = f.GetString(flagNote)

			res, err := r.WriteEra(req)
			if err != nil {
				return err
			}
			return renderRegistryWrite(cmd, res)
		},
	}
	f := cmd.Flags()
	f.String(flagStart, "", "When the chapter began: @yesterday, YYYY-MM-DD, or an approximate value like 2014")
	f.String(flagEnd, "", "When it ended (omit for a still-running chapter)")
	f.String(flagNote, "", "A free-text note kept verbatim")
	return cmd
}
