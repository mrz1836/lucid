package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// newPetCmd wires `lucid pet <name> [flags]`: the registry-write verb for a
// named companion (mvp/life-archive.md §8). It stores only the documented
// species and note Fields, with lifecycle transitions recorded on the shared
// append-only status history. Dispatch is deterministic and agent-free. A name
// may contain spaces, joined from the trailing args.
func newPetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pet <name>",
		Short: "Record or amend a companion in your pet registry",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			req := router.PetWriteRequest{
				Name: strings.Join(args, " "),
				Now:  clockNow(),
			}
			req.Species, _ = f.GetString(flagSpecies)
			req.Status, _ = f.GetString(flagStatus)
			req.Note, _ = f.GetString(flagNote)

			res, err := r.WritePet(req)
			if err != nil {
				return err
			}
			return renderRegistryWrite(cmd, res)
		},
	}
	f := cmd.Flags()
	f.String(flagSpecies, "", "Species, in your words (free text)")
	f.String(flagStatus, "", "Status transition: active | rehomed | passed")
	f.String(flagNote, "", "A free-text note kept verbatim")
	return cmd
}
