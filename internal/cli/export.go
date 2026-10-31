package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// metricFlag is the `lucid export measurements --metric <slug>` filter.
const metricFlag = "metric"

// newExportCmd wires `lucid export` (observations-module.md §Commands,
// observations.md §7): the projection exports. Three forms, matching the
// documented set exactly:
//
//	lucid export series                                      # pain/mood/capacity CSV
//	lucid export measurements [@date|all] [--metric <slug>]  # long-format measurement CSV
//	lucid export packet clinician [@date|all]                # the clinician packet
//
// On the chat surface `/packet clinician` maps here (Phase 14 skill). Only the
// written path is posted; the packet body excludes notes, location, and
// weather by default, and the measurement CSV carries no notes or tags. Under
// --json the machine-readable path/window is emitted (ADR-0007); no projection
// body ever rides the chat surface. A malformed measurement window, a second
// window argument, a bad --metric, and --metric on another form are usage
// errors decided before anything is written (error-states.md Mx-1..Mx-4).
func newExportCmd() *cobra.Command {
	var metric string
	cmd := &cobra.Command{
		Use:   "export [series | measurements [@date|all] [--metric <slug>] | packet clinician [@date|all]]",
		Short: "Export a series, the measurements, or a clinician packet",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("lucid export: name what to export — use `series`, `measurements` or `packet clinician`")
			}
			metricSet := cmd.Flags().Changed(metricFlag)
			switch {
			case metricSet && (args[0] == "series" || args[0] == "packet"):
				return newUsageError(cmd, errors.New("--metric applies only to `lucid export measurements`"))
			case args[0] == "measurements" && len(args) > 2:
				return newUsageError(cmd, errors.New("lucid export measurements takes at most one window argument"))
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
			case "measurements":
				return runMeasurements(cmd, r, args[1:], metric, metricSet)
			case "packet":
				return runPacket(cmd, r, args[1:])
			default:
				return fmt.Errorf("lucid export: unknown export %q — use `series`, `measurements` or `packet clinician`", args[0])
			}
		},
	}
	cmd.Flags().StringVar(&metric, metricFlag, "", "Only this metric slug — exact, case-sensitive (measurements only)")
	return cmd
}

// runMeasurements dispatches `export measurements [@date|all] [--metric
// <slug>]`. The caller has already refused a second window argument; the
// router refuses a malformed window or --metric, and that refusal is the
// caller's to fix, so it is typed as a usage error (exit 2) — raised before
// any projection or disclosure line is written.
func runMeasurements(cmd *cobra.Command, r *router.Router, args []string, metric string, metricSet bool) error {
	window := ""
	if len(args) > 0 {
		window = args[0]
	}
	out, err := r.MeasurementsExport(window, metric, metricSet, time.Now())
	var rejected *router.MeasurementsRejectedError
	if errors.As(err, &rejected) {
		return newUsageError(cmd, err)
	}
	if err != nil {
		return err
	}
	return emitExport(cmd, out)
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
// (--json). The posted surface is only the path — never projection content. A
// filtered measurement export adds its `metric`; every other export keeps the
// exact five metadata keys.
func emitExport(cmd *cobra.Command, out router.ExportOutcome) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		payload := map[string]string{
			"command":      "export",
			"what":         out.What,
			"path":         out.Path,
			"window_start": out.WindowStart,
			"window_end":   out.WindowEnd,
		}
		if out.Metric != "" {
			payload["metric"] = out.Metric
		}
		return writeJSON(cmd.OutOrStdout(), payload)
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), out.Message)
	return nil
}
