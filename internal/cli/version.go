package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// versionInfo is the machine-readable shape emitted by `lucid version
// --json`. Field names are stable script contract.
type versionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

// newVersionCmd wires `lucid version`, printing the build metadata
// injected via ldflags plus the compiling Go toolchain and platform.
func newVersionCmd(bi BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print lucid build metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := versionInfo{
				Version:   bi.Version,
				Commit:    bi.Commit,
				BuildDate: bi.Date,
				GoVersion: runtime.Version(),
				Platform:  runtime.GOOS + "/" + runtime.GOARCH,
			}

			asJSON, _ := cmd.Flags().GetBool(jsonFlag)
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), info)
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "lucid %s\n", info.Version)
			_, _ = fmt.Fprintf(out, "  commit:     %s\n", info.Commit)
			_, _ = fmt.Fprintf(out, "  built:      %s\n", info.BuildDate)
			_, _ = fmt.Fprintf(out, "  go:         %s\n", info.GoVersion)
			_, _ = fmt.Fprintf(out, "  platform:   %s\n", info.Platform)
			return nil
		},
	}
}
