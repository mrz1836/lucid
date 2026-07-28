package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	lucid "github.com/mrz1836/lucid"
	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/frameworks"
	"github.com/mrz1836/lucid/internal/isoweek"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/router"
)

// reflectWeekEmpty is the calm copy printed when the deep-dive has nothing to
// read yet — an empty or thin week where no narrative and no pattern surfaced.
const reflectWeekEmpty = "Nothing to reflect on yet this week — capture a few entries and come back."

// The three range-override flags. They are mutually exclusive, so each names one
// complete window rule rather than combining into a fourth.
const (
	reflectWeekFlagWeek  = "week"
	reflectWeekFlagSince = "since"
	reflectWeekFlagDays  = "days"
)

// reflectWeekSinceLayout is the civil-date form `--since` speaks, named in the
// parse error so a rejected value says what shape it wanted.
const reflectWeekSinceLayout = "YYYY-MM-DD"

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
//
// The window fields describe exactly what was read: window_start and window_end
// are inclusive logical days, days_covered counts them, and capped /
// uncovered_days state any shortfall the catch-up cap deferred.
//
// iso_week is RETAINED — same key, same position, never null — and on a
// multi-week catch-up read it labels the window's END day. An existing harness
// that keys on it keeps working; the additions are strictly additive.
type reflectWeekView struct {
	ISOWeek       string                  `json:"iso_week"`
	WindowStart   string                  `json:"window_start"`
	WindowEnd     string                  `json:"window_end"`
	DaysCovered   int                     `json:"days_covered"`
	Capped        bool                    `json:"capped"`
	UncoveredDays int                     `json:"uncovered_days"`
	Summary       string                  `json:"summary"`
	Wins          []string                `json:"wins"`
	Misses        []string                `json:"misses"`
	BodyPain      []string                `json:"body_pain"`
	HabitChange   []string                `json:"habit_change"`
	NextWeek      []string                `json:"next_week"`
	Pattern       *reflectWeekPatternView `json:"pattern"`
	AppliedLens   string                  `json:"applied_lens,omitempty"`
}

// newReflectWeekCmd wires `lucid reflect week`: the read-only weekly deep-dive
// over a projection-only bundle. It is provider-backed (the lucid.json
// `provider` block) and strictly read-only — nothing under ~/.lucid/ changes.
// The router resolves the window, assembles the sanctuary-safe bundle, runs the
// deep-dive framed through the active consented lens (resolved from the binary's
// embedded framework registry), gates every surfaced line through Safety, and
// returns the Discord-friendly narrative plus at most one Safety-cleared,
// cited candidate pattern. It never proposes-and-persists — routing a confirmed
// pattern back through the resonance gate is the separate apply path.
//
// The default window is the catch-up read: everything since the last
// reflection, so a skipped week is read on the next run rather than dropped.
// The three range flags override it and are mutually exclusive.
func newReflectWeekCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "week",
		Short: "Read-only weekly deep-dive since your last reflection (never writes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			now := clockNow()
			// Resolved before the router boots so a malformed range is rejected
			// on its own terms, rather than after a scaffold and a provider build.
			window, err := reflectWindowOptions(cmd, now)
			if err != nil {
				return err
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			p, err := buildProvider(r.Config().Provider)
			if err != nil {
				return err
			}
			res, err := r.ReflectWeek(cmd.Context(), router.ReflectWeekRequest{
				Now:        now,
				Provider:   p,
				ActiveLens: resolveActiveLens(r.Config()),
				Window:     window,
			})
			if err != nil {
				return err
			}
			return renderReflectWeek(cmd, res)
		},
	}
	cmd.Flags().Bool(reflectWeekFlagWeek, false,
		"Read only the ISO week containing today (the behavior before catch-up windows)")
	cmd.Flags().String(reflectWeekFlagSince, "",
		"Read from an explicit "+reflectWeekSinceLayout+" day through the end of today")
	cmd.Flags().Int(reflectWeekFlagDays, 0,
		"Read the last N logical days, today inclusive")
	// Cobra rejects a combination outright. A hand-rolled precedence chain would
	// silently pick one and read a window the user never asked for — and they
	// would have no way to notice.
	cmd.MarkFlagsMutuallyExclusive(reflectWeekFlagWeek, reflectWeekFlagSince, reflectWeekFlagDays)

	// `apply` is the write path: it routes a candidate + the user's response back
	// through the existing resonance gate. `close` is the other write path: it
	// stamps the reflected-through cursor. Both are leaves under the read-only
	// `week` surface so the deep-dive itself stays side-effect-free (AC-2) and
	// every write is an explicit, separately-invoked verb.
	cmd.AddCommand(newReflectWeekApplyCmd())
	cmd.AddCommand(newReflectWeekCloseCmd())
	return cmd
}

// reflectWindowOptions maps the parsed range flags onto the router's window
// request. Cobra has already rejected any combination, so at most one branch is
// live and the zero value — the catch-up default — is what an unflagged run
// gets.
//
// An explicitly-passed flag is honored even when its value is out of range:
// `--days 0` resolves to the days mode and surfaces the router's `>= 1`
// rejection rather than silently degrading to the default window.
func reflectWindowOptions(cmd *cobra.Command, now time.Time) (router.ReflectWindowOptions, error) {
	flags := cmd.Flags()

	if week, _ := flags.GetBool(reflectWeekFlagWeek); week {
		return router.ReflectWindowOptions{Mode: router.ReflectWindowWeek}, nil
	}

	if flags.Changed(reflectWeekFlagSince) {
		raw, _ := flags.GetString(reflectWeekFlagSince)
		// Parsed with the same date parser the rest of the CLI uses, in the
		// clock's own location, so a start date means the day the user meant.
		day, err := observations.ParseDate(strings.TrimSpace(raw), now.Location())
		if err != nil {
			// Phrased in cobra's own invalid-argument dialect so a bad date reads
			// like every other flag-parse failure and maps to ExitUsage rather
			// than to a runtime failure.
			return router.ReflectWindowOptions{}, fmt.Errorf(
				"invalid argument %q for %q flag: needs a %s date",
				raw, "--"+reflectWeekFlagSince, reflectWeekSinceLayout,
			)
		}
		return router.ReflectWindowOptions{Mode: router.ReflectWindowSince, Since: day}, nil
	}

	if flags.Changed(reflectWeekFlagDays) {
		days, _ := flags.GetInt(reflectWeekFlagDays)
		return router.ReflectWindowOptions{Mode: router.ReflectWindowDays, Days: days}, nil
	}

	return router.ReflectWindowOptions{}, nil
}

// reflectWeekApplyResponse is the user's answer to the surfaced candidate:
// `accepted` | `nuanced` | `rejected` | `unanswered`, with the verbatim text (a
// nuance's refinement becomes canonical; an accept's affirmation and a rejection's
// reason are recorded; ignored for an unanswered pass).
type reflectWeekApplyResponse struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

// reflectWeekApplyRule is the optional answer to the fixed one-line rule prompt
// asked once after an accept/nuance. Answered false (the default) leaves the
// insight ruleless and the prompt never returns.
type reflectWeekApplyRule struct {
	Answered bool   `json:"answered"`
	Rule     string `json:"rule,omitempty"`
}

// reflectWeekApplyEnvelope is the stdin/JSON payload the Sunday driver pipes to
// `lucid reflect week apply`: the candidate the read-only pass surfaced (echoed
// back verbatim, including its raw-entry-id citations), the "<id> v<version>"
// lens label that framed it, the user's response, and the optional rule answer.
type reflectWeekApplyEnvelope struct {
	Candidate reflectWeekPatternView   `json:"candidate"`
	Framework string                   `json:"framework,omitempty"`
	Response  reflectWeekApplyResponse `json:"response"`
	Rule      reflectWeekApplyRule     `json:"rule,omitempty"`
}

// reflectWeekApplyView is the --json projection of an apply turn: the outcome and
// the durable effect (an insight id + wrote on accept/nuance; the rejected /
// unanswered flags otherwise), plus the Safety decision so a harness can tell a
// block from a persist. proposal_paused reports the silent pause state.
type reflectWeekApplyView struct {
	Outcome        string `json:"outcome"`
	Message        string `json:"message,omitempty"`
	Decision       string `json:"decision,omitempty"`
	ReasonCode     string `json:"reason_code,omitempty"`
	InsightID      string `json:"insight_id,omitempty"`
	Wrote          bool   `json:"wrote"`
	Rejected       bool   `json:"rejected"`
	Unanswered     bool   `json:"unanswered"`
	ProposalPaused bool   `json:"proposal_paused"`
	RuleSet        bool   `json:"rule_set"`
}

// newReflectWeekApplyCmd wires `lucid reflect week apply`: the resonance-gated
// write path for a weekly deep-dive candidate. It reads one JSON envelope from
// stdin (the candidate + the user's response), re-gates the candidate through
// Safety, and — on an accept/nuance — persists a tracked insight stamped with
// the lens's provenance.framework, or records the rejection / unanswered shape
// and advances the silent proposal pause. It reuses the same persist machinery
// `/checkin` does; there is no parallel writer.
func newReflectWeekApplyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "apply",
		Short: "Persist a weekly deep-dive candidate through the resonance gate (reads JSON on stdin)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := readReflectWeekApply(cmd.InOrStdin())
			if err != nil {
				return err
			}
			kind, err := reflectWeekResponseKind(env.Response.Kind)
			if err != nil {
				return err
			}
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			p, err := buildProvider(r.Config().Provider)
			if err != nil {
				return err
			}
			res, err := r.ApplyWeekProposal(cmd.Context(), router.ApplyWeekProposalRequest{
				Now:      clockNow(),
				Provider: p,
				Candidate: router.ReflectWeekPattern{
					ProposalText:       env.Candidate.ProposalText,
					ShapeTag:           env.Candidate.ShapeTag,
					SupportingEntryIDs: env.Candidate.SupportingEntryIDs,
				},
				Framework: env.Framework,
				Response:  router.ProposalResponse{Kind: kind, Text: env.Response.Text},
				Rule:      router.RuleResponse{Answered: env.Rule.Answered, Rule: env.Rule.Rule},
			})
			if err != nil {
				return err
			}
			return renderReflectWeekApply(cmd, res)
		},
	}
}

// reflectWeekCloseView is the --json projection of a close: the instant the
// cursor now reflects through, which act stamped it, and that a write landed.
type reflectWeekCloseView struct {
	ReflectedThrough string `json:"reflected_through"`
	Source           string `json:"source"`
	Wrote            bool   `json:"wrote"`
}

// newReflectWeekCloseCmd wires `lucid reflect week close`: the explicit act of
// finishing a sit-down. It stamps the reflected-through cursor so the next
// deep-dive starts where this one ended — one of exactly two writers of that
// cursor (the other is a completed apply). Reading never advances it, so a
// sit-down opened and abandoned re-reads those days rather than marking them
// done.
//
// It builds NO provider: closing is pure storage with no model call, so it works
// on a host with no provider configured at all.
func newReflectWeekCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close",
		Short: "Stamp the reflected-through cursor so the next deep-dive starts here",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.CloseReflectWeek(clockNow())
			if err != nil {
				return err
			}
			return renderReflectWeekClose(cmd, res)
		},
	}
}

// renderReflectWeekClose reports what the close stamped: the --json view, or a
// one-line human confirmation naming the day the cursor now reflects through.
func renderReflectWeekClose(cmd *cobra.Command, res router.ReflectionCloseResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), reflectWeekCloseView{
			ReflectedThrough: res.ReflectedThrough.Format(time.RFC3339),
			Source:           res.Source,
			Wrote:            true,
		})
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Reflected through %s.\n", engine.DateString(res.ReflectedThrough))
	return nil
}

// readReflectWeekApply decodes the single stdin/JSON apply envelope. Unlike the
// read-only recall batch, apply is an explicit write with a required payload, so
// an empty or interactive stdin is an error rather than a silent no-op.
func readReflectWeekApply(in io.Reader) (reflectWeekApplyEnvelope, error) {
	var env reflectWeekApplyEnvelope
	if err := json.NewDecoder(in).Decode(&env); err != nil {
		return reflectWeekApplyEnvelope{}, fmt.Errorf("lucid reflect week apply: decode payload: %w", err)
	}
	if strings.TrimSpace(env.Candidate.ProposalText) == "" || strings.TrimSpace(env.Candidate.ShapeTag) == "" {
		return reflectWeekApplyEnvelope{}, fmt.Errorf("lucid reflect week apply: payload needs a candidate with proposal_text and shape_tag")
	}
	return env, nil
}

// reflectWeekResponseKind maps the envelope's response string to a router
// response kind, rejecting an unrecognized value so an explicit apply never
// silently degrades to `unanswered` on a typo.
func reflectWeekResponseKind(kind string) (router.ResponseKind, error) {
	switch router.ResponseKind(strings.TrimSpace(kind)) {
	case router.RespAccepted:
		return router.RespAccepted, nil
	case router.RespNuanced:
		return router.RespNuanced, nil
	case router.RespRejected:
		return router.RespRejected, nil
	case router.RespUnanswered:
		return router.RespUnanswered, nil
	default:
		return "", fmt.Errorf("lucid reflect week apply: unknown response kind %q — use accepted|nuanced|rejected|unanswered", kind)
	}
}

// renderReflectWeekApply prints the apply outcome: the --json view, or a short
// human line naming what landed (a persisted insight, a recorded rejection, or
// an unanswered shape).
func renderReflectWeekApply(cmd *cobra.Command, res router.ValidateResult) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), reflectWeekApplyViewOf(res))
	}
	out := cmd.OutOrStdout()
	switch {
	case res.Wrote:
		_, _ = fmt.Fprintf(out, "Tracked insight %s.\n", res.InsightID)
		if res.RuleSet {
			_, _ = fmt.Fprintln(out, "Rule attached.")
		}
	case res.Rejected:
		_, _ = fmt.Fprintln(out, "Recorded — not a fit; nothing tracked.")
	case res.Unanswered:
		_, _ = fmt.Fprintln(out, "Left it open — nothing tracked.")
	default:
		if res.Message != "" {
			_, _ = fmt.Fprintln(out, res.Message)
		}
	}
	return nil
}

// reflectWeekApplyViewOf projects the router result into the stable --json shape.
func reflectWeekApplyViewOf(res router.ValidateResult) reflectWeekApplyView {
	return reflectWeekApplyView{
		Outcome:        string(res.Outcome),
		Message:        res.Message,
		Decision:       string(res.Decision),
		ReasonCode:     string(res.ReasonCode),
		InsightID:      res.InsightID,
		Wrote:          res.Wrote,
		Rejected:       res.Rejected,
		Unanswered:     res.Unanswered,
		ProposalPaused: res.ProposalPaused,
		RuleSet:        res.RuleSet,
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

	_, _ = fmt.Fprintln(out, reflectWeekHeader(res))
	if res.Capped {
		// The cap is only acceptable because it is honest: the shortfall is
		// stated under the header, never quietly read past.
		_, _ = fmt.Fprintf(out,
			"Covering the last %d of %d un-reflected days — pass --%s to read the full span.\n",
			res.DaysCovered, res.DaysCovered+res.UncoveredDays, reflectWeekFlagSince)
	}
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

// reflectWeekHeader renders the range the read actually covered. A window whose
// ends fall in the same ISO week keeps the familiar one-week line verbatim; a
// catch-up window spanning more names both ends and the day count, so a
// multi-week read is never presented as a single week.
//
// The closing label is res.ISOWeek — the same value --json emits — so the header
// and the payload can never disagree about which week the window ended in.
func reflectWeekHeader(res router.ReflectWeekResult) string {
	start, end := isoweek.Label(res.WindowStart), res.ISOWeek
	if start == end {
		return "Week " + end
	}
	// A span with two distinct ISO labels is at least two days wide, so the day
	// count is always plural here.
	return fmt.Sprintf("Weeks %s → %s (%d days)", start, trimWeekLabelYear(start, end), res.DaysCovered)
}

// trimWeekLabelYear drops the repeated ISO year from the closing label when both
// ends share it — "2026-W29 → W30" — and keeps the label whole across a year
// boundary, where the year is the only thing telling the two weeks apart.
func trimWeekLabelYear(start, end string) string {
	startYear, _, ok := strings.Cut(start, "-")
	if !ok {
		return end
	}
	endYear, week, ok := strings.Cut(end, "-")
	if !ok || startYear != endYear {
		return end
	}
	return week
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
		ISOWeek:       res.ISOWeek,
		WindowStart:   engine.DateString(res.WindowStart),
		WindowEnd:     engine.DateString(res.WindowEnd),
		DaysCovered:   res.DaysCovered,
		Capped:        res.Capped,
		UncoveredDays: res.UncoveredDays,
		Summary:       res.Summary,
		Wins:          nonNil(res.Wins),
		Misses:        nonNil(res.Misses),
		BodyPain:      nonNil(res.BodyPain),
		HabitChange:   nonNil(res.HabitChange),
		NextWeek:      nonNil(res.NextWeek),
		AppliedLens:   res.AppliedLens,
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
