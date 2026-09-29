package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/router"
)

// Flag names for the gratitude verbs (gratitude.md §3, §4, §5, §7.9, §8).
const (
	gratitudeIntoFlag   = "into"
	gratitudeNewFlag    = "new"
	gratitudeCountFlag  = "count"
	gratitudeFirstFlag  = "first"
	gratitudeLastFlag   = "last"
	gratitudeApplyFlag  = "apply"
	gratitudePersonFlag = "person"
)

// gratitudeTier3UnavailableNote is the one-line stderr note an add prints when
// the by-meaning judge was needed but could not be reached or trusted, so the
// phrase was matched on tiers 1–2 alone (gratitude.md §7.6).
const gratitudeTier3UnavailableNote = "meaning match unavailable — matched by wording only"

// gratitudeSuggestionStatus is the "status" an ambiguous-band add's --json
// payload carries (gratitude.md §7.4), telling it apart from a write's view.
const gratitudeSuggestionStatus = "suggestion"

// gratitudeSuggestionResolve lists the two explicit answers that resolve a
// deferred suggestion, as the --json payload's "resolve" field names them. It
// returns a fresh slice, so no caller can alter another's payload.
func gratitudeSuggestionResolve() []string {
	return []string{"--into <id>", "--new"}
}

// errGratitudeAskCanceled is an interactive add whose "did you mean …?" was
// answered q, or met the end of input: nothing is written and the add exits 1,
// like any other deferred choice (gratitude.md §7.4).
var errGratitudeAskCanceled = errors.New("gratitude add canceled; nothing was saved")

// gratitudeStdinIsTerminal reports whether an add's stdin is a terminal a person
// can answer on — the one case an ambiguous-band add asks rather than refuses
// (gratitude.md §7.4). It is a package var so tests can stand in for a terminal;
// the cli test package defaults it to false, so no test ever waits on a real one.
//
//nolint:gochecknoglobals // one injected terminal seam so the ambiguous-band question is testable without a TTY
var gratitudeStdinIsTerminal = stdinIsInteractive

// newGratitudeCmd wires `lucid gratitude` (gratitude.md §3–§8): the accumulating
// nightly-gratitude tally. It is a thin dispatch group over six subcommands —
// deterministic and agent-free (architecture P9) apart from the optional tier-3
// by-meaning judge `add` and a `reconcile` dry run consult, which degrades to
// the model-free tiers — modeled on `lucid reframe` and `lucid person`:
//
//	lucid gratitude add "my morning coffee"
//	lucid gratitude add "my house" --into gratitude_a-river
//	lucid gratitude add "the walk home" --new
//	lucid gratitude add "a walk outside" --day @yesterday
//	lucid gratitude add "coffee with Sam on the porch" --person person_a-river
//	lucid gratitude list --json
//	lucid gratitude merge gratitude_b-stone gratitude_a-river
//	lucid gratitude import "clean drinking water" --count 22 --first 2025-11-02 --last 2026-08-20
//	lucid gratitude reconcile --apply
//	lucid gratitude thank gratitude_a-river --person person_a-river
//
// add tallies one occurrence (creating the entry, bumping the entry the match
// tiers land it on, or bumping a specific entry with --into) and prints the
// receipt id; an ambiguous match asks "did you mean …?" on a terminal and
// otherwise writes nothing and names the candidates; list shows
// the tally sorted by count then recency with a stable id per entry; merge folds
// an accidental duplicate; import seeds a pre-counted row (the one-time migration
// path); reconcile proposes folds for duplicates already in the tally and, with
// --apply, folds the clear ones through merge; thank links a person and notes,
// privately and tally-neutrally, that you told them. Every mutation returns its
// own receipt id, distinct from the stable id.
func newGratitudeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gratitude",
		Short: "Keep an accumulating tally of what you're grateful for",
	}
	cmd.AddCommand(
		newGratitudeAddCmd(),
		newGratitudeListCmd(),
		newGratitudeMergeCmd(),
		newGratitudeImportCmd(),
		newGratitudeReconcileCmd(),
		newGratitudeThankCmd(),
	)
	return cmd
}

// newGratitudeAddCmd wires `lucid gratitude add <thing> [--into <id> | --new]
// [--person <subject>] [--day <date>]` and the seed alias `add <thing> --count N
// --first <date> --last <date>`. The thing is joined from the trailing args (the
// obs/injury precedent) and stored verbatim. Without flags the match tiers
// decide (gratitude.md §7): the canonical key, a clear token match, or a clear
// by-meaning match (the optional tier-3 judge, built through the buildProvider
// seam) bumps an existing entry, and nothing close starts a new one. An ambiguous match never guesses
// ([resolveGratitudeSuggestion]): on a terminal it asks "did you mean …?" and
// writes only on the answer; under --json, or off a terminal, it writes nothing
// and exits 1 with the suggestion — the --json payload, or a sentence on stderr —
// for the caller to resolve with --into or --new. When the judge was needed but
// unreachable the add still completes on tiers 1–2 and says so on stderr
// (gratitude.md §7.6); `--into <id>` bumps that specific stable entry regardless
// of wording; `--new` starts a new entry (tier 1 still applies); `--person` links
// a person onto the entry the add lands on (link-only, validated before any
// write, gratitude.md §8); `--count` routes to the one-time seed/import path (an
// explicit Count/First/Last, gratitude.md §5) and takes none of those. `--day`
// is the strict backdating tier — a bad token or a future day is a clean refusal
// that writes nothing, printed to stderr. `--json` emits the receipt and the
// resulting tally.
func newGratitudeAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <thing>",
		Short: "Tally one thing you're grateful for",
		Args:  requireTextArgs(0, 1, "body-file"),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			// --body-file supplies the thing off the command line; the trailing
			// positional words are the fallback source.
			thing, err := resolvePrimaryText(cmd, "gratitude add", "body-file", strings.Join(args, " "))
			if err != nil {
				return emitErr(cmd, err)
			}
			into, _ := cmd.Flags().GetString(gratitudeIntoFlag)
			forceNew, _ := cmd.Flags().GetBool(gratitudeNewFlag)
			person, _ := cmd.Flags().GetString(gratitudePersonFlag)
			if err = checkGratitudeAddFlags(cmd, into, forceNew, person); err != nil {
				return emitErr(cmd, err)
			}
			// `add --count …` is the documented alias for the one-time seed/import
			// path (its flag conflicts were refused above).
			if cmd.Flags().Changed(gratitudeCountFlag) {
				return runGratitudeImport(cmd, r, thing)
			}

			day, _ := cmd.Flags().GetString(flagDay)
			req := router.AddGratitudeRequest{
				Thing:    thing,
				DayArg:   day,
				Into:     into,
				ForceNew: forceNew,
				Person:   person,
				Provider: gratitudeJudge(r.Config()),
				Now:      clockNow(),
			}
			res, err := r.AddGratitude(cmd.Context(), req)
			noteGratitudeTier3(cmd.ErrOrStderr(), res.Tier3, err)
			var sugg *router.GratitudeSuggestionError
			if errors.As(err, &sugg) {
				return resolveGratitudeSuggestion(cmd, r, req, sugg)
			}
			if err != nil {
				// The root silences returned errors, so a rejected --day, an empty
				// thing, or an unknown --into id would otherwise be a bare exit code.
				// emitErr prints the reason (a DayRejectedError's Error is its
				// accepted-forms message) and returns the error unchanged so the exit
				// code still travels.
				return emitErr(cmd, err)
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), gratitudeAddViewOf(res))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	registerDayFlag(cmd)
	cmd.Flags().String(gratitudeIntoFlag, "", "Bump a specific entry by its stable id, regardless of wording")
	cmd.Flags().Bool(gratitudeNewFlag, false, "Start a new entry instead of matching an existing one by wording")
	cmd.Flags().String(gratitudePersonFlag, "", "Link a person (name or person_key) to the entry this lands on")
	registerGratitudeSeedFlags(cmd)
	registerBodyFileFlag(cmd, "thing you're grateful for")
	return cmd
}

// checkGratitudeAddFlags refuses the flag combinations `add` cannot honor,
// before anything is read or written: an empty --person, and --into, --new, or
// --person alongside the --count seed — `add --count …` is the alias for the
// one-time seed/import path, carrying explicit Count/First/Last, so it never
// mixes with a targeted bump, a matching answer, or a person link (gratitude.md
// §3, §5).
func checkGratitudeAddFlags(cmd *cobra.Command, into string, forceNew bool, person string) error {
	if cmd.Flags().Changed(gratitudePersonFlag) && strings.TrimSpace(person) == "" {
		return errors.New("gratitude add: --person needs a person name or key; nothing was saved")
	}
	if !cmd.Flags().Changed(gratitudeCountFlag) {
		return nil
	}
	switch {
	case strings.TrimSpace(into) != "":
		return errors.New("gratitude add: --into and --count cannot be combined; nothing was saved")
	case forceNew:
		return errors.New("gratitude add: --new and --count cannot be combined; nothing was saved")
	case strings.TrimSpace(person) != "":
		return errors.New("gratitude add: --person and --count cannot be combined; nothing was saved")
	default:
		return nil
	}
}

// gratitudeJudge builds the optional tier-3 by-meaning judge for a plain `add`
// (gratitude.md §7.5) through the buildProvider seam every provider-backed verb
// shares: the provider block with the gratitude.match tier3_backend / tier3_model
// / tier3_timeout_seconds overrides. It returns nil when tier 3 is disabled — the
// router then reports it as such — or when no backend can be built, which the
// router treats as unreachable: the add completes on tiers 1–2 either way.
func gratitudeJudge(cfg config.Config) provider.Provider {
	m := cfg.Gratitude.Match.OrDefault()
	if !m.Tier3Enabled {
		return nil
	}
	p, err := buildProvider(m.Tier3ProviderConfig(cfg.Provider))
	if err != nil {
		return nil
	}
	return p
}

// noteGratitudeTier3 prints the degraded-path note (gratitude.md §7.6) when the
// by-meaning judge was needed but unavailable — on a completed add (status) or an
// ambiguous-band refusal (the suggestion error carries its own status). It goes
// to stderr so a `--json` stdout stays machine-clean.
func noteGratitudeTier3(w io.Writer, status router.GratitudeTier3Status, err error) {
	var sugg *router.GratitudeSuggestionError
	if errors.As(err, &sugg) {
		status = sugg.Tier3
	}
	if status == router.GratitudeTier3Unavailable {
		_, _ = fmt.Fprintln(w, gratitudeTier3UnavailableNote)
	}
}

// resolveGratitudeSuggestion handles an add that landed in the ambiguous band
// (gratitude.md §7.4): the router wrote nothing and returned the suggestion.
// Under --json it refuses and defers — the structured suggestion is the stdout
// payload and the add exits 1, for the caller to resolve by re-running with
// --into <id> or --new. Off a terminal (or with the phrase read from stdin by
// --body-file -) it is the same refusal, as the suggestion's sentence on stderr.
// On a terminal it asks "did you mean …?" and re-runs the add exactly as the
// answer's explicit flag would: a chosen candidate is bumped as `--into <id>`
// bumps it (no match_tier stamp — the person made the call), "no" starts a new
// entry as `--new` does, and canceling writes nothing and exits 1.
func resolveGratitudeSuggestion(
	cmd *cobra.Command, r *router.Router, req router.AddGratitudeRequest, sugg *router.GratitudeSuggestionError,
) error {
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		if err := writeJSON(cmd.OutOrStdout(), gratitudeSuggestionViewOf(sugg)); err != nil {
			return err
		}
		return sugg // the payload is the explanation; the error carries exit 1
	}
	if !gratitudeCanAsk(cmd, sugg) {
		return emitErr(cmd, sugg)
	}
	answer, err := askGratitudeSuggestion(cmd.InOrStdin(), cmd.ErrOrStderr(), sugg.Candidates)
	if err != nil {
		return emitErr(cmd, err)
	}
	// The event's `at` is the real write time, so the answered add reads the
	// clock again rather than reusing the moment the question was asked.
	req.Into, req.ForceNew, req.Now = answer.into, answer.forceNew, clockNow()
	res, err := r.AddGratitude(cmd.Context(), req)
	if err != nil {
		return emitErr(cmd, err)
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// gratitudeCanAsk reports whether an ambiguous-band add may put its question to
// a person (gratitude.md §7.4): stdin is a terminal and was not already read for
// the phrase itself — `--body-file -` is therefore always non-interactive — and
// there is a candidate to ask about. The caller has already ruled out --json: a
// machine caller is never asked.
func gratitudeCanAsk(cmd *cobra.Command, sugg *router.GratitudeSuggestionError) bool {
	if len(sugg.Candidates) == 0 {
		return false
	}
	if path, _ := cmd.Flags().GetString("body-file"); cmd.Flags().Changed("body-file") && path == stdinPath {
		return false
	}
	return gratitudeStdinIsTerminal(cmd.InOrStdin())
}

// gratitudeAnswer is one answer to the ambiguous-band question: the candidate id
// to bump (into), a new entry (forceNew), or cancel.
type gratitudeAnswer struct {
	into     string
	forceNew bool
	cancel   bool
}

// askGratitudeSuggestion puts the "did you mean …?" question to a person on w
// (stderr, so stdout carries only the ack) and reads answers from in, one per
// line, until one is recognized ([parseGratitudeAnswer]); an unrecognized answer
// names the choices and asks again. q, or the end of input, cancels with
// [errGratitudeAskCanceled]. cands is never empty ([gratitudeCanAsk]).
func askGratitudeSuggestion(
	in io.Reader, w io.Writer, cands []router.GratitudeSuggestionCandidate,
) (gratitudeAnswer, error) {
	_, _ = fmt.Fprint(w, gratitudeQuestion(cands))
	lines := bufio.NewScanner(in)
	for lines.Scan() {
		answer, ok := parseGratitudeAnswer(lines.Text(), cands)
		switch {
		case !ok:
			_, _ = fmt.Fprintln(w, gratitudeAnswerHint(len(cands)))
		case answer.cancel:
			return gratitudeAnswer{}, errGratitudeAskCanceled
		default:
			return answer, nil
		}
	}
	if err := lines.Err(); err != nil {
		return gratitudeAnswer{}, fmt.Errorf("gratitude add: could not read the answer; nothing was saved: %w", err)
	}
	return gratitudeAnswer{}, errGratitudeAskCanceled
}

// gratitudeQuestion renders the ambiguous-band question (gratitude.md §7.4): the
// best candidate named in the question, then the choices — bump it, start a new
// entry, pick another listed candidate by its number, or cancel.
func gratitudeQuestion(cands []router.GratitudeSuggestionCandidate) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Did you mean to bump %s: %q?\n", cands[0].ID, cands[0].Thing)
	b.WriteString("  [y] bump it   [n] start a new entry")
	for i, c := range cands[1:] {
		_, _ = fmt.Fprintf(&b, "   [%d] %s: %q", i+2, c.ID, c.Thing)
	}
	b.WriteString("   [q] cancel\n")
	return b.String()
}

// parseGratitudeAnswer reads one answer line, case- and space-insensitively: y
// (or yes, or 1) bumps the first candidate, a listed number picks that
// candidate, n (or no) starts a new entry, and q (or quit, or cancel) cancels.
// ok is false for anything else, including a blank line — the question is never
// answered by default.
func parseGratitudeAnswer(line string, cands []router.GratitudeSuggestionCandidate) (gratitudeAnswer, bool) {
	switch a := strings.ToLower(strings.TrimSpace(line)); a {
	case "y", "yes":
		return gratitudeAnswer{into: cands[0].ID}, true
	case "n", "no":
		return gratitudeAnswer{forceNew: true}, true
	case "q", "quit", "cancel":
		return gratitudeAnswer{cancel: true}, true
	default:
		n, err := strconv.Atoi(a)
		if err != nil || n < 1 || n > len(cands) {
			return gratitudeAnswer{}, false
		}
		return gratitudeAnswer{into: cands[n-1].ID}, true
	}
}

// gratitudeAnswerHint names the accepted answers after an unrecognized one.
func gratitudeAnswerHint(candidates int) string {
	switch candidates {
	case 1:
		return "Answer y, n, or q."
	case 2:
		return "Answer y, n, 2, or q."
	default:
		return fmt.Sprintf("Answer y, n, a number from 2 to %d, or q.", candidates)
	}
}

// newGratitudeMergeCmd wires `lucid gratitude merge <src> <dst>` (gratitude.md
// §4): fold an accidental duplicate's whole count + first/last span into the
// canonical entry and rewrite the source as a redirect tombstone — omitted from
// the active tally but auditably kept, never deleted. Both arguments are stable
// gratitude ids (shown by `list`). A self-merge or a missing/tombstoned entry is
// a clean error that changes nothing.
func newGratitudeMergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge <src> <dst>",
		Short: "Fold a duplicate gratitude entry into a canonical one (redirect, never delete)",
		Long: `merge collapses a gratitude entry that was accidentally split into two — a
short wording and a long descriptive phrase for the same thing — onto one
canonical entry. The target absorbs the source's whole count and first/last span
(via a merge event) and its wordings; the source becomes a redirect tombstone
that forwards to the target and is omitted from the active tally. Nothing is
deleted. Merging an entry into itself, or onto/from a missing or already-merged
entry, is rejected. Both arguments are stable gratitude ids (shown by list).`,
		Args: cobra.ExactArgs(2),
		Example: `  lucid gratitude merge gratitude_b-stone gratitude_a-river
  lucid gratitude merge gratitude_b-stone gratitude_a-river --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.MergeGratitude(router.GratitudeMergeRequest{
				Source: args[0],
				Target: args[1],
				Now:    clockNow(),
			})
			if err != nil {
				return emitErr(cmd, err)
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), gratitudeAddViewOf(res))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}

// newGratitudeReconcileCmd wires `lucid gratitude reconcile [--apply]`
// (gratitude.md §7.9): scan the live tally for likely duplicates and propose
// folds. It is dry-run by default and writes nothing; --apply is the explicit
// confirmation that folds exactly the pairs the dry run marks "fold" — the
// deterministic tier-2 clear matches — each through the ordinary merge path, one
// receipt per fold. A dry run also asks the optional tier-3 by-meaning judge
// (built through the buildProvider seam, like `add`) for pairs; those are always
// advisory, printed with their merge command and never applied, and --apply
// never consults a model at all. `--json` emits the proposals, the tier-3
// status, and the applied folds.
func newGratitudeReconcileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Propose folds for duplicate gratitude entries (dry run unless --apply)",
		Long: `reconcile scans the live tally for entries that name the same thing and
proposes folding one into the other — the lower count into the higher. It is a
dry run by default and writes nothing. A pair marked "fold" is a clear match by
wording (tier 2): each entry is the other's best match by a clear margin.
--apply folds exactly those pairs through the ordinary merge path (a merge event
plus a redirect tombstone), one receipt per fold. A pair marked "look" — a
closer call by wording, or any match by meaning from the optional tier-3 judge —
is never folded for you; its merge command is printed for you to run if it is
the same thing. Take a lucid backup before --apply: a fold has no unmerge, and
restoring that backup is how a wrong one is undone.`,
		Args: cobra.NoArgs,
		Example: `  lucid gratitude reconcile
  lucid gratitude reconcile --apply --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			apply, _ := cmd.Flags().GetBool(gratitudeApplyFlag)
			req := router.ReconcileGratitudeRequest{Apply: apply, Now: clockNow()}
			if !apply {
				// An apply acts on the deterministic tier-2 set alone, so only a
				// dry run reaches for the optional judge.
				req.Provider = gratitudeJudge(r.Config())
			}
			res, err := r.ReconcileGratitude(cmd.Context(), req)
			if err != nil {
				return emitErr(cmd, err)
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
	cmd.Flags().Bool(gratitudeApplyFlag, false, "Fold the pairs marked fold (clear matches by wording) through merge")
	return cmd
}

// newGratitudeThankCmd wires `lucid gratitude thank <id> --person <subject>
// [--day <date>]` (gratitude.md §8): note that you told a person you were
// grateful. It links the person onto the entry (if not already linked) and
// appends one tally-neutral `expressed` event with its own receipt — Count,
// First, and Last never move. The id must name a live entry, and the subject is
// resolved like every person write verb's — both validated before anything is
// written, so an unknown id or an unknown or ambiguous person is a clean error
// on stderr that saves nothing. `--day` is the strict backdating tier for "I told
// them yesterday". Lucid sends nothing: the person did the telling. `--json`
// emits the receipt, the entry, the unchanged count, the person, and the date.
func newGratitudeThankCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "thank <id>",
		Short: "Note that you told someone you're grateful (never moves the tally)",
		Long: `thank keeps a private note that you told a person about a gratitude — Lucid
never sends anything; you do the telling, however you choose. It links the person
onto the entry (if not already linked) and records the day you told them, with its
own receipt. It never moves the tally: the count, first, and last stay as they
are. <id> is a stable gratitude id (shown by list); --person is a name or a
person_key, resolved like the person write verbs resolve a subject. A person
marked off-limits can be thanked — it is your own record — but is never named in
list's gentle reminders.`,
		Args: cobra.ExactArgs(1),
		Example: `  lucid gratitude thank gratitude_a-river --person person_a-river
  lucid gratitude thank gratitude_a-river --person "Sam Rivera" --day @yesterday --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			person, _ := cmd.Flags().GetString(gratitudePersonFlag)
			day, _ := cmd.Flags().GetString(flagDay)
			res, err := r.ThankGratitude(router.ThankGratitudeRequest{
				ID:     args[0],
				Person: person,
				DayArg: day,
				Now:    clockNow(),
			})
			if err != nil {
				return emitErr(cmd, err)
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), gratitudeThankViewOf(res))
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	registerDayFlag(cmd)
	cmd.Flags().String(gratitudePersonFlag, "", "Whom you told — a person name or person_key (required)")
	return cmd
}

// newGratitudeImportCmd wires `lucid gratitude import <thing> --count N --first
// <date> --last <date>` (gratitude.md §5): the one-time seed/migration path. It
// writes one entry carrying a single seed event with the explicit Count/First/
// Last and fabricates no per-occurrence dates — distinct from the nightly `add`.
// It is deliberately NOT idempotent (re-running double-counts).
func newGratitudeImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <thing>",
		Short: "Seed a pre-counted tally row (one-time migration path)",
		Long: `import seeds an existing tally row — a thing you already know you were grateful
for N times between two dates — as one entry carrying a single seed event with
the explicit --count, --first, and --last. It fabricates no per-occurrence dates
(the raw per-night truth already lives in the separate #gratitude logs) and is
the one-time migration path, distinct from the nightly add. It is NOT idempotent:
re-running double-counts, so a migration is a single pass and a retry restores the
pre-migration backup first.`,
		Args: cobra.MinimumNArgs(1),
		Example: `  lucid gratitude import "clean drinking water" --count 22 --first 2025-11-02 --last 2026-08-20
  lucid gratitude import "my morning coffee" --count 8 --first 2026-01-04 --last 2026-08-19 --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			return runGratitudeImport(cmd, r, strings.Join(args, " "))
		},
	}
	registerGratitudeSeedFlags(cmd)
	return cmd
}

// runGratitudeImport is the shared seed/import tail behind `import` and the
// `add --count …` alias: it reads the explicit Count/First/Last flags, runs the
// router seed path, and renders the receipt-bearing ack (or --json view).
func runGratitudeImport(cmd *cobra.Command, r *router.Router, thing string) error {
	count, _ := cmd.Flags().GetInt(gratitudeCountFlag)
	first, _ := cmd.Flags().GetString(gratitudeFirstFlag)
	last, _ := cmd.Flags().GetString(gratitudeLastFlag)
	res, err := r.ImportGratitude(router.ImportGratitudeRequest{
		Thing: thing,
		Count: count,
		First: first,
		Last:  last,
		Now:   clockNow(),
	})
	if err != nil {
		return emitErr(cmd, err)
	}
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), gratitudeAddViewOf(res))
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// registerGratitudeSeedFlags adds the seed/import flags shared by `import` and
// the `add --count …` alias.
func registerGratitudeSeedFlags(cmd *cobra.Command) {
	cmd.Flags().Int(gratitudeCountFlag, 0, "Seed the entry with an explicit count (the one-time migration path)")
	cmd.Flags().String(gratitudeFirstFlag, "", "Seed first date (civil YYYY-MM-DD)")
	cmd.Flags().String(gratitudeLastFlag, "", "Seed last date (civil YYYY-MM-DD)")
}

// newGratitudeListCmd wires `lucid gratitude list [--json]`: the live tally
// (tombstones omitted), sorted by count then recency, each row naming its linked
// people and when you last told them, then at most three gentle "You might tell"
// lines — never a count (gratitude.md §6, §8). Human-first by default with the
// structured list under `--json` (ADR-0007). It writes nothing.
func newGratitudeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the gratitude tally, most-returned-to first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.GratitudeList()
			if err != nil {
				return err
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
}

// gratitudeAddView is the machine-readable projection of an `add` under --json:
// the receipt id of this write, the stable entry id, and the resulting tally.
// Built CLI-side with stable snake_case names so a harness branches on fields
// rather than parsing the ack prose. match_tier and match_score appear only when
// the occurrence landed by an automatic match (gratitude.md §7.4), tier3 only
// when the by-meaning judge was needed (gratitude.md §7.6), and person_key only
// when `--person` linked someone (gratitude.md §8), so a tier-1, `--into`, or
// first-entry write keeps exactly its v1 shape.
type gratitudeAddView struct {
	Receipt    string  `json:"receipt"`
	ID         string  `json:"id"`
	Thing      string  `json:"thing"`
	Count      int     `json:"count"`
	First      string  `json:"first"`
	Last       string  `json:"last"`
	Created    bool    `json:"created"`
	MatchTier  int     `json:"match_tier,omitempty"`
	MatchScore float64 `json:"match_score,omitempty"`
	PersonKey  string  `json:"person_key,omitempty"`
	Tier3      string  `json:"tier3,omitempty"`
}

// gratitudeThankView is the --json payload of a `thank` (gratitude.md §8;
// commands.md `### gratitude`): the expressed event's receipt, the stable entry
// id and wording, the count — shown unchanged, since expressing gratitude never
// moves the tally — the canonical person key, and the logical date recorded.
type gratitudeThankView struct {
	Receipt   string `json:"receipt"`
	ID        string `json:"id"`
	Thing     string `json:"thing"`
	Count     int    `json:"count"`
	PersonKey string `json:"person_key"`
	Date      string `json:"date"`
}

// gratitudeThankViewOf projects a thank result into its stable --json shape.
func gratitudeThankViewOf(res router.GratitudeThankResult) gratitudeThankView {
	return gratitudeThankView{
		Receipt:   res.Receipt,
		ID:        res.Key,
		Thing:     res.Thing,
		Count:     res.Count,
		PersonKey: res.PersonKey,
		Date:      res.Date,
	}
}

// gratitudeSuggestionView is the --json payload of an ambiguous-band add — the
// refuse-and-defer answer (gratitude.md §7.4): nothing was saved, and the
// candidates (id, wording, score; best first, at most three) with the band, the
// tier whose band produced it, and the two explicit ways to resolve it. tier3
// says what became of the by-meaning judge, omitted when it was not needed.
// Candidates and resolve are always arrays, never null.
type gratitudeSuggestionView struct {
	Status     string                                `json:"status"`
	Thing      string                                `json:"thing"`
	Band       string                                `json:"band"`
	MatchTier  int                                   `json:"match_tier"`
	Candidates []router.GratitudeSuggestionCandidate `json:"candidates"`
	Resolve    []string                              `json:"resolve"`
	Saved      bool                                  `json:"saved"`
	Tier3      string                                `json:"tier3,omitempty"`
}

// gratitudeSuggestionViewOf projects an ambiguous-band suggestion into its
// stable --json shape.
func gratitudeSuggestionViewOf(sugg *router.GratitudeSuggestionError) gratitudeSuggestionView {
	cands := sugg.Candidates
	if cands == nil {
		cands = []router.GratitudeSuggestionCandidate{}
	}
	return gratitudeSuggestionView{
		Status:     gratitudeSuggestionStatus,
		Thing:      sugg.Thing,
		Band:       string(sugg.Band),
		MatchTier:  sugg.MatchTier,
		Candidates: cands,
		Resolve:    gratitudeSuggestionResolve(),
		Saved:      false,
		Tier3:      string(sugg.Tier3),
	}
}

// gratitudeAddViewOf projects a router result into the stable --json shape.
func gratitudeAddViewOf(res router.GratitudeWriteResult) gratitudeAddView {
	return gratitudeAddView{
		Receipt:    res.Receipt,
		ID:         res.Key,
		Thing:      res.Thing,
		Count:      res.Count,
		First:      res.First,
		Last:       res.Last,
		Created:    res.Created,
		MatchTier:  res.MatchTier,
		MatchScore: res.MatchScore,
		PersonKey:  res.PersonKey,
		Tier3:      string(res.Tier3),
	}
}
