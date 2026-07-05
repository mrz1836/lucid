package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// stubSpec describes a spine command whose behavior arrives in a later
// build stage. Registering the whole command spine now (ADR-0003) keeps
// the surface discoverable via `lucid --help` from Stage 0, while each
// stub honestly reports that its stage has not shipped yet rather than
// pretending success.
type stubSpec struct {
	name            string
	short           string
	stage           string
	machineReadable bool // ADR-0007 script-facing commands emit JSON under --json
}

// newStubCmd builds a placeholder command from spec. It returns
// [errNotImplemented] (mapping to a non-zero exit) so a script never
// mistakes an unbuilt verb for a successful no-op; under --json the
// machine-readable commands still emit a structured, parseable body.
func newStubCmd(spec stubSpec) *cobra.Command {
	return &cobra.Command{
		Use:   spec.name,
		Short: spec.short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			asJSON, _ := cmd.Flags().GetBool(jsonFlag)
			if asJSON && spec.machineReadable {
				_ = writeJSON(cmd.OutOrStdout(), map[string]string{
					"command": spec.name,
					"status":  "not_implemented",
					"stage":   spec.stage,
				})
			} else {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"lucid %s: not implemented yet — arrives in %s\n", spec.name, spec.stage)
			}
			return errNotImplemented
		},
	}
}

// The feature spine. Behavior for each lands in its build stage; the
// verbs exist now so the command tree matches the documented set and
// no undocumented verb ever appears (ADR-0007 hard rule). `init` is
// real as of Stage 1 — its command lives in init.go.

func newLogCmd() *cobra.Command {
	return newStubCmd(stubSpec{name: "log", short: "Capture an immutable raw entry", stage: "Stage 1"})
}

func newCloseoutCmd() *cobra.Command {
	return newStubCmd(stubSpec{name: "closeout", short: "Record the day's committed practice", stage: "Stage 2"})
}

func newModeCmd() *cobra.Command {
	return newStubCmd(stubSpec{name: "mode", short: "Declare today's Engine mode (green|yellow|red)", stage: "Stage 2"})
}

func newStatusCmd() *cobra.Command {
	return newStubCmd(stubSpec{name: "status", short: "Show the derived Engine status", stage: "Stage 2", machineReadable: true})
}

func newDayCmd() *cobra.Command {
	return newStubCmd(stubSpec{name: "day", short: "Show a day's joined record", stage: "Stage 4", machineReadable: true})
}

func newValidateCmd() *cobra.Command {
	return newStubCmd(stubSpec{name: "validate", short: "Validate the Ledger and boundary invariants", stage: "Stage 5", machineReadable: true})
}

func newExportCmd() *cobra.Command {
	return newStubCmd(stubSpec{name: "export", short: "Export a series or clinician packet", stage: "Stage 4", machineReadable: true})
}
