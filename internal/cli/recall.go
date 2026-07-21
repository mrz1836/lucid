package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Recall dimension flag names (mvp/life-archive.md §7). They are mutually
// exclusive — a browse names exactly one dimension, and no flag is the bare
// index over all three.
const (
	recallFlagEra    = "era"
	recallFlagThread = "thread"
	recallFlagInjury = "injury"
)

// recallEmpty is the calm copy printed when the archive holds nothing to browse
// yet — no era, thread, or injury recorded.
const recallEmpty = "Nothing archived yet — record an injury, era, or story first, then come back."

// recallFieldView is one convention Field of a browsed referent in the --json
// projection.
type recallFieldView struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// recallReferentView is the --json projection of the browsed era/thread/injury:
// its identity, status, convention fields, and source context.
type recallReferentView struct {
	Kind               string            `json:"kind"`
	Key                string            `json:"key"`
	DisplayName        string            `json:"display_name"`
	Status             string            `json:"status"`
	Fields             []recallFieldView `json:"fields"`
	Source             string            `json:"source"`
	SupportingEntryIDs []string          `json:"supporting_entry_ids"`
}

// recallItemView is the --json projection of one surfaced item: a story filed
// under the referent, or a bare-index entry. Every item carries its source
// context (supporting_entry_ids + source) so no item is uncited.
type recallItemView struct {
	Kind               string   `json:"kind"`
	Key                string   `json:"key"`
	Title              string   `json:"title"`
	Detail             string   `json:"detail"`
	Source             string   `json:"source"`
	SupportingEntryIDs []string `json:"supporting_entry_ids"`
}

// recallView is the --json projection of a [router.RecallResult], built CLI-side
// with stable snake_case names so a harness branches on fields rather than
// parsing prose (mvp/life-archive.md §7). The read-only surface writes nothing,
// so there is no id or wrote flag. `found` is false and `items` is empty on a
// thin/missing store; `referent` is null for the bare index.
type recallView struct {
	Dimension string              `json:"dimension"`
	Key       string              `json:"key"`
	Found     bool                `json:"found"`
	Referent  *recallReferentView `json:"referent"`
	Items     []recallItemView    `json:"items"`
}

// newRecallCmd wires `lucid recall`: the read-only recall/browse surface
// (mvp/life-archive.md §7). With `--era`/`--thread`/`--injury <key>` (mutually
// exclusive) it browses that referent and the stories filed under it; with no
// flag it prints the archive index over every era, thread, and injury. Every
// surfaced item carries its source context (the raw/observation ids behind it +
// its provenance), so nothing is uncited. It is strictly read-only — nothing
// under ~/.lucid/ changes — and agent-free (no model runs), mirroring the
// read-only `lucid excavate` / `lucid reflect week` shape. The same
// projection-only reads back the weekly reflection surface, so the archive is
// consumable from either verb without a second data path.
func newRecallCmd() *cobra.Command {
	var era, thread, injury string
	cmd := &cobra.Command{
		Use:   "recall",
		Short: "Read-only: browse the archive by era, thread, or injury (never writes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dim, key := recallDimension(era, thread, injury)
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.Recall(router.RecallRequest{Dimension: dim, Key: key, Now: clockNow()})
			if err != nil {
				return err
			}
			return renderRecall(cmd, res)
		},
	}
	cmd.Flags().StringVar(&era, recallFlagEra, "", "Browse the stories filed under an era, by its key")
	cmd.Flags().StringVar(&thread, recallFlagThread, "", "Browse a thread, by its key")
	cmd.Flags().StringVar(&injury, recallFlagInjury, "", "Browse an injury's record, by its key")
	cmd.MarkFlagsMutuallyExclusive(recallFlagEra, recallFlagThread, recallFlagInjury)
	return cmd
}

// recallDimension maps the mutually-exclusive dimension flags to a (dimension,
// key) pair — the first set flag wins (cobra guarantees at most one is set), and
// no flag is the bare index (empty dimension).
func recallDimension(era, thread, injury string) (dim, key string) {
	switch {
	case strings.TrimSpace(era) != "":
		return router.RecallEra, strings.TrimSpace(era)
	case strings.TrimSpace(thread) != "":
		return router.RecallThread, strings.TrimSpace(thread)
	case strings.TrimSpace(injury) != "":
		return router.RecallInjury, strings.TrimSpace(injury)
	default:
		return "", ""
	}
}

// renderRecall prints the browse: the --json view, or Discord-friendly text (no
// markdown tables — bullets, Key: Value, and a `Cites:` line per item, per the
// Discord output rules). A keyed browse whose referent does not resolve and an
// empty index each print an honest fallback.
func renderRecall(cmd *cobra.Command, res router.RecallResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), recallViewOf(res))
	}

	out := cmd.OutOrStdout()
	switch {
	case res.Dimension != "" && !res.Found:
		_, _ = fmt.Fprintf(out, "No %s found for %q.\n", res.Dimension, res.Key)
		return nil
	case res.Dimension == "" && !res.Found:
		_, _ = fmt.Fprintln(out, recallEmpty)
		return nil
	}

	if res.Referent != nil {
		renderRecallReferent(out, res.Referent)
	} else {
		_, _ = fmt.Fprintln(out, "Archive index")
	}
	for _, it := range res.Items {
		renderRecallItem(out, it)
	}
	return nil
}

// renderRecallReferent prints the browsed era/thread/injury: a heading, its
// present convention fields as bullets, and its source-context citation.
func renderRecallReferent(out io.Writer, ref *router.RecallReferent) {
	_, _ = fmt.Fprintf(out, "%s — %s (%s)\n", ref.DisplayName, ref.Kind, ref.Status)
	for _, f := range ref.Fields {
		_, _ = fmt.Fprintf(out, "• %s: %s\n", f.Label, f.Value)
	}
	_, _ = fmt.Fprintf(out, "Cites: %s\n", recallCite(ref.SupportingEntryIDs, ref.Key, ref.Source))
}

// renderRecallItem prints one surfaced item: a story (its words, its detail, its
// citation) or a bare-index entry (its kind, name, and short detail), each with
// a non-empty `Cites:` line so nothing is uncited.
func renderRecallItem(out io.Writer, it router.RecallItem) {
	if it.Kind == "story" {
		_, _ = fmt.Fprintf(out, "• %s\n", it.Title)
		if it.Detail != "" {
			_, _ = fmt.Fprintf(out, "  %s\n", it.Detail)
		}
		_, _ = fmt.Fprintf(out, "  Cites: %s\n", recallCite(it.SupportingEntryIDs, it.Key, it.Source))
		return
	}
	line := fmt.Sprintf("• [%s] %s", it.Kind, it.Title)
	if it.Detail != "" {
		line += " — " + it.Detail
	}
	_, _ = fmt.Fprintln(out, line)
	_, _ = fmt.Fprintf(out, "  Cites: %s\n", recallCite(it.SupportingEntryIDs, it.Key, it.Source))
}

// recallCite renders a source-context citation: the supporting raw/observation
// ids when present, else the item's own key (a registry referent is primary
// source), always followed by the provenance in parentheses — so every item is
// cited.
func recallCite(ids []string, key, source string) string {
	body := strings.Join(ids, ", ")
	if body == "" {
		body = key
	}
	return fmt.Sprintf("%s (%s)", body, source)
}

// recallViewOf projects a router result into the stable --json shape,
// normalizing nil slices to [] so a harness can index them unconditionally.
func recallViewOf(res router.RecallResult) recallView {
	view := recallView{
		Dimension: res.Dimension,
		Key:       res.Key,
		Found:     res.Found,
		Items:     make([]recallItemView, 0, len(res.Items)),
	}
	if res.Referent != nil {
		ref := recallReferentView{
			Kind:               res.Referent.Kind,
			Key:                res.Referent.Key,
			DisplayName:        res.Referent.DisplayName,
			Status:             res.Referent.Status,
			Fields:             make([]recallFieldView, 0, len(res.Referent.Fields)),
			Source:             res.Referent.Source,
			SupportingEntryIDs: nonNil(res.Referent.SupportingEntryIDs),
		}
		for _, f := range res.Referent.Fields {
			ref.Fields = append(ref.Fields, recallFieldView{Label: f.Label, Value: f.Value})
		}
		view.Referent = &ref
	}
	for _, it := range res.Items {
		view.Items = append(view.Items, recallItemView{
			Kind:               it.Kind,
			Key:                it.Key,
			Title:              it.Title,
			Detail:             it.Detail,
			Source:             it.Source,
			SupportingEntryIDs: nonNil(it.SupportingEntryIDs),
		})
	}
	return view
}
