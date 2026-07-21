package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// excavateEmpty is the calm copy printed when there is nothing to excavate yet —
// no injury with gaps and no era to open a story from.
const excavateEmpty = "Nothing to excavate yet — record an injury or an era first, then come back."

// excavateView is the --json projection of a [router.ExcavateResult], built
// CLI-side with stable snake_case names so the personal excavation driver
// branches on fields rather than parsing prose (mvp/life-archive.md §5). The
// surface is read-only, so there is no id or wrote flag. `found` is false and the
// arrays are empty on an empty/thin store.
type excavateView struct {
	Found       bool     `json:"found"`
	Track       string   `json:"track"`
	Key         string   `json:"key"`
	DisplayName string   `json:"display_name"`
	Reason      string   `json:"reason"`
	Gaps        []string `json:"gaps"`
	Prompts     []string `json:"prompts"`
}

// newExcavateCmd wires `lucid excavate`: the read-only excavation-selection
// surface (mvp/life-archive.md §5–§6). It picks the next injury or story cluster
// to excavate and prints its generic prompt templates, mirroring the read-only
// `lucid reflect week` shape. It is strictly read-only — nothing under ~/.lucid/
// changes — and agent-free (no model runs). The personal driver reads `--json`,
// holds the one-cluster-at-a-time conversation on its own surface, and calls the
// write verbs (`lucid injury`/`era`/`memory`) to persist the answers; the binary
// owns only the deterministic selection half (life-archive.md §5).
func newExcavateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "excavate",
		Short: "Read-only: select the next memory cluster to excavate (never writes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.Excavate(clockNow())
			if err != nil {
				return err
			}
			return renderExcavate(cmd, res)
		},
	}
}

// renderExcavate prints the selection: the --json view, or Discord-friendly
// prose (no markdown tables — bullets and Key: Value only, per the Discord
// output rules). An empty/thin store prints the calm fallback line.
func renderExcavate(cmd *cobra.Command, res router.ExcavateResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), excavateViewOf(res))
	}

	out := cmd.OutOrStdout()
	if !res.Found {
		_, _ = fmt.Fprintln(out, excavateEmpty)
		return nil
	}

	_, _ = fmt.Fprintf(out, "Next cluster — %s (%s track)\n", res.DisplayName, res.Track)
	if res.Reason != "" {
		_, _ = fmt.Fprintln(out, res.Reason)
	}
	if len(res.Gaps) > 0 {
		_, _ = fmt.Fprintf(out, "Gaps: %s\n", strings.Join(res.Gaps, ", "))
	}
	for _, p := range res.Prompts {
		_, _ = fmt.Fprintf(out, "• %s\n", p)
	}
	return nil
}

// excavateViewOf projects a router result into the stable --json shape,
// normalizing nil slices to [] so a harness can index them unconditionally.
func excavateViewOf(res router.ExcavateResult) excavateView {
	return excavateView{
		Found:       res.Found,
		Track:       res.Track,
		Key:         res.Key,
		DisplayName: res.DisplayName,
		Reason:      res.Reason,
		Gaps:        nonNil(res.Gaps),
		Prompts:     nonNil(res.Prompts),
	}
}
