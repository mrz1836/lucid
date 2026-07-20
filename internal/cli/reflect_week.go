package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	lucid "github.com/mrz1836/lucid"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/frameworks"
	"github.com/mrz1836/lucid/internal/router"
)

// reflectWeekEmpty is the calm copy printed when the deep-dive has nothing to
// read yet — an empty or thin week where no narrative and no pattern surfaced.
const reflectWeekEmpty = "Nothing to reflect on yet this week — capture a few entries and come back."

// reflectWeekPatternView is the machine-readable projection of the surfaced
// candidate: the Safety-gated text, its shape_tag, and the raw-entry-id
// citations that back it.
type reflectWeekPatternView struct {
	ProposalText       string   `json:"proposal_text"`
	ShapeTag           string   `json:"shape_tag"`
	SupportingEntryIDs []string `json:"supporting_entry_ids"`
}

// reflectWeekView is the --json projection of a [router.ReflectWeekResult],
// built CLI-side with stable snake_case names so a harness (the personal Sunday
// driver) branches on fields rather than parsing prose. The read-only surface
// writes nothing, so there is no record id or wrote flag.
type reflectWeekView struct {
	ISOWeek     string                  `json:"iso_week"`
	Summary     string                  `json:"summary"`
	Wins        []string                `json:"wins"`
	Misses      []string                `json:"misses"`
	BodyPain    []string                `json:"body_pain"`
	HabitChange []string                `json:"habit_change"`
	NextWeek    []string                `json:"next_week"`
	Pattern     *reflectWeekPatternView `json:"pattern"`
	AppliedLens string                  `json:"applied_lens,omitempty"`
}

// newReflectWeekCmd wires `lucid reflect week`: the read-only weekly deep-dive
// over the past week's projection-only bundle. It is provider-backed (the
// lucid.json `provider` block) and strictly read-only — nothing under ~/.lucid/
// changes. The router assembles the sanctuary-safe week bundle, runs the
// deep-dive framed through the active consented lens (resolved from the binary's
// embedded framework registry), gates every surfaced line through Safety, and
// returns the Discord-friendly narrative plus at most one Safety-cleared,
// cited candidate pattern. It never proposes-and-persists — routing a confirmed
// pattern back through the resonance gate is the separate apply path.
func newReflectWeekCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "week",
		Short: "Read-only weekly deep-dive over the past week (never writes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			p, err := buildProvider(r.Config().Provider)
			if err != nil {
				return err
			}
			res, err := r.ReflectWeek(cmd.Context(), router.ReflectWeekRequest{
				Now:        clockNow(),
				Provider:   p,
				ActiveLens: resolveActiveLens(r.Config()),
			})
			if err != nil {
				return err
			}
			return renderReflectWeek(cmd, res)
		},
	}
}

// resolveActiveLens resolves the consented lens that frames this run, or nil for
// the baseline voice. It short-circuits when no framework is consented (the
// default), so the embedded registry is only loaded when a lens is actually in
// play; any registry error degrades to the baseline voice rather than failing
// the read.
func resolveActiveLens(cfg config.Config) *frameworks.Lens {
	if _, ok := cfg.ActiveFramework(); !ok {
		return nil
	}
	reg, err := frameworks.NewRegistryFS(lucid.FrameworksFS())
	if err != nil {
		return nil
	}
	lens, ok := reg.ActiveLens(cfg)
	if !ok {
		return nil
	}
	return &lens
}

// renderReflectWeek prints the deep-dive: the --json view, or Discord-friendly
// prose (no markdown tables — bullets and Key: Value only, per the Discord
// output rules). An empty week prints the calm fallback line.
func renderReflectWeek(cmd *cobra.Command, res router.ReflectWeekResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), reflectWeekViewOf(res))
	}

	out := cmd.OutOrStdout()
	if isEmptyWeek(res) {
		_, _ = fmt.Fprintln(out, reflectWeekEmpty)
		return nil
	}

	_, _ = fmt.Fprintf(out, "Week %s\n", res.ISOWeek)
	if res.Summary != "" {
		_, _ = fmt.Fprintln(out, res.Summary)
	}
	writeSection(out, "Wins", res.Wins)
	writeSection(out, "Misses", res.Misses)
	writeSection(out, "Body & pain", res.BodyPain)
	writeSection(out, "Habit changes", res.HabitChange)
	writeSection(out, "Next week", res.NextWeek)

	if res.Pattern != nil {
		_, _ = fmt.Fprintln(out)
		header := "Pattern — " + res.Pattern.ShapeTag
		if res.AppliedLens != "" {
			header += " (lens: " + res.AppliedLens + ")"
		}
		_, _ = fmt.Fprintln(out, header+":")
		_, _ = fmt.Fprintln(out, res.Pattern.ProposalText)
		if len(res.Pattern.SupportingEntryIDs) > 0 {
			_, _ = fmt.Fprintf(out, "Cites: %s\n", strings.Join(res.Pattern.SupportingEntryIDs, ", "))
		}
	}
	return nil
}

// writeSection prints one bulleted narrative section, skipping it entirely when
// it has no surviving lines.
func writeSection(out io.Writer, label string, lines []string) {
	if len(lines) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "%s:\n", label)
	for _, l := range lines {
		_, _ = fmt.Fprintf(out, "• %s\n", l)
	}
}

// isEmptyWeek reports whether the deep-dive surfaced nothing — no summary, no
// section line, and no pattern — the empty / thin-week fallback.
func isEmptyWeek(res router.ReflectWeekResult) bool {
	return res.Summary == "" &&
		len(res.Wins) == 0 && len(res.Misses) == 0 && len(res.BodyPain) == 0 &&
		len(res.HabitChange) == 0 && len(res.NextWeek) == 0 && res.Pattern == nil
}

// reflectWeekViewOf projects a router result into the stable --json shape.
func reflectWeekViewOf(res router.ReflectWeekResult) reflectWeekView {
	view := reflectWeekView{
		ISOWeek:     res.ISOWeek,
		Summary:     res.Summary,
		Wins:        nonNil(res.Wins),
		Misses:      nonNil(res.Misses),
		BodyPain:    nonNil(res.BodyPain),
		HabitChange: nonNil(res.HabitChange),
		NextWeek:    nonNil(res.NextWeek),
		AppliedLens: res.AppliedLens,
	}
	if res.Pattern != nil {
		view.Pattern = &reflectWeekPatternView{
			ProposalText:       res.Pattern.ProposalText,
			ShapeTag:           res.Pattern.ShapeTag,
			SupportingEntryIDs: nonNil(res.Pattern.SupportingEntryIDs),
		}
	}
	return view
}

// nonNil normalizes a nil slice to an empty one so the --json arrays render as
// [] rather than null.
func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
