package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// personCandidateDateLayout formats a candidate's first-seen date in the
// disambiguation view. It matches the byte-stable date-only rendering the
// router uses in the prose §P-2 line, so the --json shape stays consistent
// with the human output.
const personCandidateDateLayout = "2006-01-02"

// personCandidateView is one row of the §P-2 disambiguation list under --json:
// the record key, its display name, and its first-seen date.
type personCandidateView struct {
	PersonKey   string `json:"person_key"`
	DisplayName string `json:"display_name"`
	FirstSeenAt string `json:"first_seen_at"`
}

// personContactView is the person view's check-in summary under --json
// (usage/commands.md §person; ADR-0014 §6): the cadence state (active, paused,
// retired, or none for contact logged with no cadence), the most recent window
// as typed and its length — left out when no window was ever set — and the
// last non-voided contact day, or null.
type personContactView struct {
	State       string  `json:"state"`
	Cadence     string  `json:"cadence,omitempty"`
	CadenceDays int     `json:"cadence_days,omitempty"`
	LastContact *string `json:"last_contact"`
}

// personView is the machine-readable projection of a [router.PersonResult]
// under --json (harness-integration.md §C). It is built CLI-side with stable
// snake_case names so a shelling harness branches on the outcome fields —
// matched (§P-0), multiple_matches (§P-2), off_limits (§P-3), or neither (§P-1
// no-match) — rather than parsing the prose; the router package stays untouched.
// contact is present only when the person has contact events, so a person with
// none projects exactly as before.
type personView struct {
	Query           string                `json:"query"`
	Matched         bool                  `json:"matched"`
	MultipleMatches bool                  `json:"multiple_matches"`
	Candidates      []personCandidateView `json:"candidates"`
	OffLimits       bool                  `json:"off_limits"`
	PersonKey       string                `json:"person_key"`
	Contact         *personContactView    `json:"contact,omitempty"`
	Text            string                `json:"text"`
}

// personViewFrom projects a router result into the CLI --json view, always
// rendering candidates as a (possibly empty) array rather than null so a
// harness can index it unconditionally.
func personViewFrom(res router.PersonResult) personView {
	cands := make([]personCandidateView, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		cands = append(cands, personCandidateView{
			PersonKey:   c.PersonKey,
			DisplayName: c.DisplayName,
			FirstSeenAt: c.FirstSeenAt.Format(personCandidateDateLayout),
		})
	}
	return personView{
		Query:           res.Query,
		Matched:         res.Matched,
		MultipleMatches: res.MultipleMatches,
		Candidates:      cands,
		OffLimits:       res.OffLimits,
		PersonKey:       res.PersonKey,
		Contact:         personContactViewFrom(res.Contact),
		Text:            res.Text,
	}
}

// personContactViewFrom projects a person's check-in summary, or nil when they
// have none.
func personContactViewFrom(pc *router.PersonContact) *personContactView {
	if pc == nil {
		return nil
	}
	v := &personContactView{State: pc.State, Cadence: pc.Window.Text, CadenceDays: pc.Window.Days}
	if pc.LastContact != "" {
		last := pc.LastContact
		v.LastContact = &last
	}
	return v
}

// newPersonCmd wires `lucid person <name>` (data-model.md §"People references";
// error-states.md §P-1/§P-2/§P-3): the deterministic, no-LLM join over the
// people record, its mention counts, the accepted insights citing it, and its
// dominance share. It is a thin dispatch over Router.Person — a pure read that
// never writes and never calls a model. A name that may contain spaces is
// joined from the trailing args (the obs/closeout precedent). Human-first prose
// by default; the personView shape under --json.
//
// A read never "fails": match, no-match (§P-1), multiple-match (§P-2), and
// off-limits (§P-3) are all legitimate outcomes carried in the result fields, so
// the command exits 0 for every one of them. Only a genuine boot/read failure
// (a real I/O error) surfaces as a non-zero exit.
func newPersonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "person <name>",
		Short: "Look up a person you've mentioned, or curate the people records",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.Person(router.PersonRequest{Name: strings.Join(args, " ")})
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), personViewFrom(res))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Text)
			return nil
		},
	}
	// The read stays the parent's own RunE (so `lucid person <name>` and the
	// bare-arg exit-2 contract are unchanged); the curation verbs hang beneath.
	cmd.AddCommand(newPersonCreateCmd())
	cmd.AddCommand(newPersonMergeCmd())
	cmd.AddCommand(newPersonAliasCmd())
	cmd.AddCommand(newPersonRenameCmd())
	cmd.AddCommand(newPersonSetCmd())
	cmd.AddCommand(newPersonOffLimitsCmd())
	cmd.AddCommand(newPersonReconcileCmd())
	return cmd
}
