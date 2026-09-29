package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/router"
)

// Flag names for the gratitude verbs (gratitude.md §3, §4, §5).
const (
	gratitudeIntoFlag  = "into"
	gratitudeNewFlag   = "new"
	gratitudeCountFlag = "count"
	gratitudeFirstFlag = "first"
	gratitudeLastFlag  = "last"
)

// gratitudeTier3UnavailableNote is the one-line stderr note an add prints when
// the by-meaning judge was needed but could not be reached or trusted, so the
// phrase was matched on tiers 1–2 alone (gratitude.md §7.6).
const gratitudeTier3UnavailableNote = "meaning match unavailable — matched by wording only"

// newGratitudeCmd wires `lucid gratitude` (gratitude.md §3–§6): the accumulating
// nightly-gratitude tally. It is a thin dispatch group over four subcommands —
// deterministic and agent-free (architecture P9) apart from `add`'s optional
// tier-3 by-meaning judge, which degrades to the model-free tiers — modeled on
// `lucid reframe` and `lucid person`:
//
//	lucid gratitude add "my morning coffee"
//	lucid gratitude add "my house" --into gratitude_a-river
//	lucid gratitude add "the walk home" --new
//	lucid gratitude add "a walk outside" --day @yesterday
//	lucid gratitude list --json
//	lucid gratitude merge gratitude_b-stone gratitude_a-river
//	lucid gratitude import "clean drinking water" --count 22 --first 2025-11-02 --last 2026-08-20
//
// add tallies one occurrence (creating the entry, bumping the entry the match
// tiers land it on, or bumping a specific entry with --into) and prints the
// receipt id; an ambiguous match writes nothing and names the candidates; list shows
// the tally sorted by count then recency with a stable id per entry; merge folds
// an accidental duplicate; import seeds a pre-counted row (the one-time migration
// path). Every mutation returns its own receipt id, distinct from the stable id.
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
	)
	return cmd
}

// newGratitudeAddCmd wires `lucid gratitude add <thing> [--into <id> | --new]
// [--day <date>]` and the seed alias `add <thing> --count N --first <date> --last
// <date>`. The thing is joined from the trailing args (the obs/injury precedent)
// and stored verbatim. Without flags the match tiers decide (gratitude.md §7): the
// canonical key, a clear token match, or a clear by-meaning match (the optional
// tier-3 judge, built through the buildProvider seam) bumps an existing entry,
// nothing close starts a new one, and an ambiguous match writes nothing and
// prints the candidates to stderr (exit 1) for the caller to resolve. When the
// judge was needed but unreachable the add still completes on tiers 1–2 and says
// so on stderr (gratitude.md §7.6); `--into <id>` bumps
// that specific stable entry regardless of wording; `--new` starts a new entry
// (tier 1 still applies); `--count` routes to the one-time seed/import path (an
// explicit Count/First/Last, gratitude.md §5). `--day` is the strict backdating
// tier — a bad token or a future day is a clean refusal that writes nothing,
// printed to stderr. `--json` emits the receipt and the resulting tally.
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

			// `add --count …` is the documented alias for the one-time seed/import
			// path; it carries explicit Count/First/Last, so it never mixes with a
			// targeted bump or a matching answer.
			if cmd.Flags().Changed(gratitudeCountFlag) {
				if strings.TrimSpace(into) != "" {
					return emitErr(cmd, fmt.Errorf("gratitude add: --into and --count cannot be combined; nothing was saved"))
				}
				if forceNew {
					return emitErr(cmd, fmt.Errorf("gratitude add: --new and --count cannot be combined; nothing was saved"))
				}
				return runGratitudeImport(cmd, r, thing)
			}

			day, _ := cmd.Flags().GetString(flagDay)
			res, err := r.AddGratitude(cmd.Context(), router.AddGratitudeRequest{
				Thing:    thing,
				DayArg:   day,
				Into:     into,
				ForceNew: forceNew,
				Provider: gratitudeJudge(r.Config()),
				Now:      clockNow(),
			})
			noteGratitudeTier3(cmd.ErrOrStderr(), res.Tier3, err)
			if err != nil {
				// The root silences returned errors, so a rejected --day, an empty
				// thing, an unknown --into id, or an ambiguous-band suggestion would
				// otherwise be a bare exit code. emitErr prints the reason (a
				// DayRejectedError's Error is its accepted-forms message; a
				// GratitudeSuggestionError's names the candidates and how to resolve
				// them) and returns the error unchanged so the exit code still travels.
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
	registerGratitudeSeedFlags(cmd)
	registerBodyFileFlag(cmd, "thing you're grateful for")
	return cmd
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
// (tombstones omitted), sorted by count then recency, human-first by default
// with the structured list under `--json` (ADR-0007). It writes nothing.
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
// the occurrence landed by an automatic match (gratitude.md §7.4), and tier3 only
// when the by-meaning judge was needed (gratitude.md §7.6), so a tier-1,
// `--into`, or first-entry write keeps exactly its v1 shape.
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
	Tier3      string  `json:"tier3,omitempty"`
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
		Tier3:      string(res.Tier3),
	}
}
