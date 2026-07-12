package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// askCitationView is one grounded reference under --json: the record kind
// (insight | reflection) and its id. Every id is in-slice by construction — an
// out-of-slice citation is blocked upstream by Safety before it reaches here.
type askCitationView struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// askView is the --json projection of a [router.AskResult]
// (harness-integration.md §D). Built CLI-side with stable snake_case names so a
// harness branches on the outcome/blocked fields rather than parsing the prose
// answer; the router package stays untouched. /ask writes nothing, so there is
// no id or wrote flag.
type askView struct {
	Outcome    string            `json:"outcome"`
	Message    string            `json:"message"`
	Citations  []askCitationView `json:"citations"`
	Blocked    bool              `json:"blocked"`
	Decision   string            `json:"decision,omitempty"`
	ReasonCode string            `json:"reason_code,omitempty"`
}

// newAskCmd wires `lucid ask <question...>`: grounded, cited Q&A over the user's
// validated insights and weekly reflections only. It is provider-backed (the
// lucid.json `provider` block) and strictly **read-only** — nothing under
// ~/.lucid/ changes (agent-contracts.md §3; S-6). The router builds the two
// authorized slices, runs the grounded-answer agent, and gates the answer
// through Safety: a pass prints the answer with its in-slice citations, an
// out-of-slice citation or advice is blocked to the fixed calm fallback, and an
// empty store yields the honest insufficient message with no model call.
// Trailing args are joined, so quoting the question is optional.
func newAskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ask <question...>",
		Short: "Grounded, cited Q&A over your validated insights and reflections",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			p, err := buildProvider(r.Config().Provider)
			if err != nil {
				return err
			}
			res, err := r.Ask(cmd.Context(), router.AskRequest{
				Question: strings.Join(args, " "),
				Provider: p,
			})
			if err != nil {
				return err
			}
			return renderAsk(cmd, res)
		},
	}
}

// renderAsk prints the answer: the --json view, or human-first prose (the answer
// / fallback / insufficient message, then any grounded citations on a passed
// answer).
func renderAsk(cmd *cobra.Command, res router.AskResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), askViewOf(res))
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, res.Message)
	// Citations accompany a passed answer only — a block surfaces the fallback
	// with nothing to cite, and insufficient has no grounded material.
	if !res.Blocked && len(res.Citations) > 0 {
		refs := make([]string, 0, len(res.Citations))
		for _, c := range res.Citations {
			refs = append(refs, fmt.Sprintf("%s:%s", c.Kind, c.ID))
		}
		_, _ = fmt.Fprintf(out, "Sources: %s\n", strings.Join(refs, ", "))
	}
	return nil
}

// askViewOf projects a router result into the stable --json shape. A blocked
// answer surfaces no citations — the ids it "cited" are precisely why Safety
// held it (an out-of-slice reference), so presenting them would let a harness
// mistake a blocked id for a valid grounded citation. This mirrors the prose
// path, which prints no Sources line on a block.
func askViewOf(res router.AskResult) askView {
	cites := make([]askCitationView, 0, len(res.Citations))
	if !res.Blocked {
		for _, c := range res.Citations {
			cites = append(cites, askCitationView{Kind: c.Kind, ID: c.ID})
		}
	}
	return askView{
		Outcome:    string(res.Outcome),
		Message:    res.Message,
		Citations:  cites,
		Blocked:    res.Blocked,
		Decision:   string(res.Decision),
		ReasonCode: string(res.ReasonCode),
	}
}
