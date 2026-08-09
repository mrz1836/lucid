package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// newPetCmd wires `lucid pet <name> [flags]`: the registry-write verb for a
// named companion (mvp/life-archive.md §8). It stores the documented species, a
// free-text note, and a backdate-aware start/end life-span, recording any
// lifecycle transition on the shared append-only status history. --start/--end
// read the same strict date grammar as `lucid era`, so a companion from years
// ago is placeable in time. Dispatch is deterministic and agent-free. A name may
// contain spaces, joined from the trailing args.
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
			req.Start, _ = f.GetString(flagStart)
			req.End, _ = f.GetString(flagEnd)
			req.Note, _ = f.GetString(flagNote)

			res, err := r.WritePet(req)
			if err != nil {
				// The root silences returned errors, so a rejected status or
				// --start/--end date would otherwise be a bare exit code. A
				// rejection has to say what it wanted (error-states.md §St-1,
				// §B-3, §B-4).
				return emitErr(cmd, err)
			}
			return renderRegistryWrite(cmd, res)
		},
	}
	f := cmd.Flags()
	f.String(flagSpecies, "", "Species, in your words (free text)")
	f.String(flagStatus, "", "Status transition: active | rehomed | passed")
	f.String(flagStart, "", "When they came into your life: @yesterday, YYYY-MM-DD, or a partial date like 2005 or 2005-06")
	f.String(flagEnd, "", "When they passed or were rehomed, same forms as --start (omit for a companion still with you)")
	f.String(flagNote, "", "A free-text note kept verbatim")
	cmd.AddCommand(newPetMigrateCmd())
	return cmd
}

type petMigrateView struct {
	Migrated   bool   `json:"migrated"`
	PetKey     string `json:"pet_key"`
	RetiredKey string `json:"retired_key"`
	Skipped    bool   `json:"skipped"`
	Reason     string `json:"reason"`
}

func newPetMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "migrate-self",
		Short:  "Move the legacy misc.pets self fact into the pet registry",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.MigratePetsFromSelf(clockNow())
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), petMigrateView{
					Migrated: res.Migrated, PetKey: res.PetKey, RetiredKey: res.RetiredKey,
					Skipped: res.Skipped, Reason: res.Reason,
				})
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}
