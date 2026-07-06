package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// newExportCmd wires `lucid export` (observations-module.md §Commands,
// observations.md §7): the projection exports. Two forms, matching the
// documented set exactly:
//
//	lucid export series                        # pain/mood/capacity CSV
//	lucid export packet clinician [@date|all]  # the clinician packet
//
// On the chat surface `/packet clinician` maps here (Phase 14 skill). Only the
// written path is posted; the packet body excludes notes, location, and
// weather by default. Under --json the machine-readable path/window is emitted
// (ADR-0007); the packet body itself never rides the chat surface.
func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export [series | packet clinician [@date|all]]",
		Short: "Export a series or clinician packet",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("lucid export: name what to export — `series` or `packet clinician`")
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			switch args[0] {
			case "series":
				out, serr := r.SeriesExport(time.Now())
				if serr != nil {
					return serr
				}
				return emitExport(cmd, out)
			case "packet":
				return runPacket(cmd, r, args[1:])
			default:
				return fmt.Errorf("lucid export: unknown export %q — use `series` or `packet clinician`", args[0])
			}
		},
	}
	return cmd
}

// runPacket dispatches `export packet clinician [@date|all]`. Only the
// clinician packet is documented in the MVP; any other packet type is refused.
func runPacket(cmd *cobra.Command, r *router.Router, args []string) error {
	if len(args) == 0 || args[0] != "clinician" {
		return fmt.Errorf("lucid export packet: only `clinician` is available")
	}
	window := ""
	if len(args) > 1 {
		window = args[1]
	}
	out, err := r.ClinicianPacket(window, time.Now())
	if err != nil {
		return err
	}
	return emitExport(cmd, out)
}

// emitExport prints an export outcome: the path (human) or a structured body
// (--json). The posted surface is only the path — never packet content.
func emitExport(cmd *cobra.Command, out router.ExportOutcome) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), map[string]string{
			"command":      "export",
			"what":         out.What,
			"path":         out.Path,
			"window_start": out.WindowStart,
			"window_end":   out.WindowEnd,
		})
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), out.Message)
	return nil
}
