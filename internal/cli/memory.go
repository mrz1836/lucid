package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// Memory story-capture flag names (mvp/life-archive.md §3). flagDay and
// flagCaption are shared with `lucid attach` (declared in attach.go); the rest
// are the story convention keys. Every flag is optional — a bare
// `lucid memory "…"` is a valid text-only story.
const (
	flagCertainty = "certainty"
	flagEra       = "era"
	flagPlace     = "place"
	flagPeople    = "people"
	flagTone      = "tone"
	flagWhy       = "why"
	flagFollowup  = "followup"
	flagAttach    = "attach"
)

// memoryWriteView is the machine-readable projection of a `lucid memory --json`
// turn: the appended event id, its logical day, whether it took the partial
// path, whether it was rejected (memory kind disabled), and the resolved
// relational refs (era/place/person/entry). Built CLI-side with stable
// snake_case names so a harness branches on fields, not prose.
type memoryWriteView struct {
	EventID     string         `json:"event_id"`
	LogicalDate string         `json:"logical_date"`
	Partial     bool           `json:"partial"`
	Rejected    bool           `json:"rejected"`
	Refs        map[string]any `json:"refs"`
}

// renderMemoryWrite prints a story-capture result: the --json view (refs always
// a non-nil object so a harness can index it), or the inventory ack prose.
func renderMemoryWrite(cmd *cobra.Command, res router.MemoryWriteResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		refs := res.Refs
		if refs == nil {
			refs = map[string]any{}
		}
		return writeJSON(cmd.OutOrStdout(), memoryWriteView{
			EventID:     res.EventID,
			LogicalDate: res.LogicalDate,
			Partial:     res.Partial,
			Rejected:    res.Rejected,
			Refs:        refs,
		})
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// newMemoryCmd wires `lucid memory <text> [flags]`: the story-capture verb of
// the life-archive module (mvp/life-archive.md §3). It writes one KindMemory
// event on the frozen bitemporal envelope, backdated to when the story happened,
// linked to the era it sits in, the place and people in it, and — when --attach
// is present — a photo reused through `lucid attach` (a text-only story simply
// omits the media and is never gated). It is dispatch-only over
// [router.WriteMemory] — deterministic and agent-free (architecture P9); the
// Ledger scaffolds on first use so capture never blocks on setup
// (product-principles.md P10). The memory kind is enable-gated: a disabled kind
// is reported with the enable hint, nothing written.
func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory <text>",
		Short: "Record a story from your past — backdated, linked, kept",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			f := cmd.Flags()
			req := router.MemoryWriteRequest{
				Text: strings.Join(args, " "),
				Now:  clockNow(),
			}
			req.Certainty, _ = f.GetString(flagCertainty)
			req.Era, _ = f.GetString(flagEra)
			req.Place, _ = f.GetString(flagPlace)
			req.People, _ = f.GetStringSlice(flagPeople)
			req.Tone, _ = f.GetString(flagTone)
			req.WhyItMatters, _ = f.GetString(flagWhy)
			req.FollowUp, _ = f.GetString(flagFollowup)
			req.Day, _ = f.GetString(flagDay)

			// Optional media reuses `lucid attach`: attach first, then reference
			// the returned raw entry id from refs.entry. A text-only story skips
			// this entirely — media never gates a story.
			if path, _ := f.GetString(flagAttach); strings.TrimSpace(path) != "" {
				if err = r.Store().ScaffoldMedia(); err != nil {
					return fmt.Errorf("lucid memory: %w", err)
				}
				caption, _ := f.GetString(flagCaption)
				ares, aerr := r.Attach(router.AttachRequest{
					Path:    path,
					Caption: caption,
					DayArg:  req.Day,
					Now:     req.Now,
				})
				if aerr != nil {
					return aerr
				}
				req.EntryRef = ares.RawID
			}

			res, err := r.WriteMemory(req)
			if err != nil {
				return err
			}
			return renderMemoryWrite(cmd, res)
		},
	}
	f := cmd.Flags()
	f.String(flagCertainty, "", "How you recall it: vivid | hazy | reconstructed")
	f.String(flagEra, "", "The era (life chapter) key this story sits in")
	f.String(flagPlace, "", "Where it happened (a place name, registered like a location)")
	f.StringSlice(flagPeople, nil, "Who was there (repeatable, or comma-separated)")
	f.String(flagTone, "", "The emotional color, one phrase")
	f.String(flagWhy, "", "Why it still matters")
	f.String(flagFollowup, "", "The thread to pull next time")
	f.String(flagDay, "", "When it happened: @yesterday or YYYY-MM-DD (approximate backdating)")
	f.String(flagAttach, "", "Optional photo/media file to attach and link to this story")
	f.String(flagCaption, "", "Caption for the attached media")
	return cmd
}
