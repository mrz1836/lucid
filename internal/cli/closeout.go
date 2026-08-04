package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/storage"
)

// bootedRouter opens the Ledger, scaffolds it idempotently, and boots the
// router so config clip warnings surface once — the shared setup every
// stateful command runs before dispatch.
func bootedRouter(cmd *cobra.Command) (*router.Router, error) {
	store, err := storage.Open()
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}
	if _, err = store.Scaffold(); err != nil {
		return nil, err
	}
	r := router.New(store)
	warnings, err := r.Boot()
	if err != nil {
		return nil, err
	}
	for _, w := range warnings {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
	}
	return r, nil
}

// newCloseoutCmd wires `lucid closeout` (engine-module.md §"/closeout
// sequence"). The CLI is the deterministic compact-form entry point; the
// guided multi-prompt flow belongs to a chat harness. Sub-forms:
//
//	lucid closeout dfx 3/wrist <journal>   compact close-out
//	lucid closeout today dfx 3 <journal>   force current-logical-day
//	lucid closeout skip                    record an honest miss
//	lucid closeout backfill [yesterday|<YYYY-MM-DD>] dfx 3/tag <journal>
//	lucid closeout --day @yesterday dfx 3/tag <journal>
//
// It scaffolds the Ledger on first use so capture never blocks on setup.
//
// `--day` is an additive alias for the backfill target: it routes onto the same
// path as the positional form, so the window, the `backfilled: true` stamp, and
// the future/today rejection are whatever the router already enforces. The
// positional form is untouched.
func newCloseoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "closeout [today|skip|backfill] [compact form...]",
		Short: "Record the day's committed practice",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			return dispatchCloseout(cmd, r, args)
		},
	}
	cmd.Flags().String(
		flagDay, "",
		"Backfill target day, e.g. @yesterday or YYYY-MM-DD "+
			"(same window as `closeout backfill`; today and future days are rejected)",
	)
	return cmd
}

// dispatchCloseout parses the leading sub-word and routes to the matching
// router call. `--day` outranks the sub-words that name their own day, and
// combining the two is a usage error rather than a silent pick between them.
func dispatchCloseout(cmd *cobra.Command, r *router.Router, args []string) error {
	now := clockNow()
	if dayArg, _ := cmd.Flags().GetString(flagDay); strings.TrimSpace(dayArg) != "" {
		return runFlagBackfill(cmd, r, args, dayArg, now)
	}
	if len(args) > 0 && args[0] == "skip" {
		return runCloseout(cmd, r, router.CloseoutRequest{Now: now, Skip: true, Source: sourceCLI, Harness: sourceCLI})
	}
	if len(args) > 0 && args[0] == "backfill" {
		return runPositionalBackfill(cmd, r, args[1:], now)
	}
	forceToday := len(args) > 0 && args[0] == "today"
	if forceToday {
		args = args[1:]
	}
	links, capacity, tag, journal, err := parseCompactArgs(r, args)
	if err != nil {
		return err
	}
	return runCloseout(cmd, r, router.CloseoutRequest{
		Now: now, Links: links, Capacity: capacity, LimiterTag: tag, Journal: journal,
		ForceToday: forceToday, Source: sourceCLI, Harness: sourceCLI,
	})
}

// runCloseout executes a close-out request and prints its ack.
func runCloseout(cmd *cobra.Command, r *router.Router, req router.CloseoutRequest) error {
	res, err := r.Closeout(req)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// runPositionalBackfill parses an optional leading target then executes the
// backfill — the `closeout backfill [yesterday|<date>] …` form, unchanged.
func runPositionalBackfill(cmd *cobra.Command, r *router.Router, args []string, now time.Time) error {
	var target *time.Time
	var yesterday bool
	if len(args) > 0 {
		switch args[0] {
		case "yesterday":
			// The router resolves "yesterday" against the logical day (the
			// rollover boundary), so the CLI just forwards the intent — a naive
			// calendar date computed here would collide with the in-progress day
			// before the rollover and read as out-of-window.
			yesterday = true
			args = args[1:]
		default:
			if t, ok := parseBackfillDate(args[0]); ok {
				target = t
				args = args[1:]
			}
		}
	}
	return runBackfill(cmd, r, args, target, yesterday, now)
}

// runFlagBackfill executes the `--day` alias. It rejects every combination that
// would name a second day — `skip` and `today` are their own targets, and a
// positional backfill target alongside the flag is two targets for one intent —
// rather than quietly preferring one of them. The bare `backfill` sub-word is
// allowed through as redundant-but-consistent: it names the path the flag
// already routes onto, without naming a day.
func runFlagBackfill(cmd *cobra.Command, r *router.Router, args []string, dayArg string, now time.Time) error {
	if len(args) > 0 {
		switch args[0] {
		case "skip", "today":
			return fmt.Errorf(
				"lucid closeout: --day names a backfill target, so it cannot be combined with `%s`", args[0],
			)
		case "backfill":
			args = args[1:]
			if len(args) > 0 && positionalBackfillTarget(args[0]) {
				return fmt.Errorf(
					"lucid closeout: --day and `backfill %s` both name a target — give one or the other", args[0],
				)
			}
		}
	}
	target, yesterday, err := resolveCloseoutDay(dayArg, now)
	if err != nil {
		// The fixed reason on stderr, then the error for its exit code: Execute
		// renders no returned error, so a refusal nobody printed is a bare
		// non-zero exit the user has to guess at.
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
		return err
	}
	return runBackfill(cmd, r, args, target, yesterday, now)
}

// positionalBackfillTarget reports whether tok is the target slot of the
// positional form rather than the head of the compact form.
func positionalBackfillTarget(tok string) bool {
	if tok == "yesterday" {
		return true
	}
	_, ok := parseBackfillDate(tok)
	return ok
}

// resolveCloseoutDay reads `--day` through the shared grammar on the strict
// tier: an unreadable token or a future day is a clean error and nothing is
// written (error-states.md §B-1, §B-2).
//
// A *relative* token forwards the `yesterday` intent rather than a date
// computed here, so the router still resolves it against the rollover boundary
// — the same reason the positional keyword does. Computing a calendar date
// CLI-side would collide with the in-progress day before the rollover and read
// as out-of-window. An explicit or partial date is literal, so it can be passed
// as the target directly.
func resolveCloseoutDay(dayArg string, now time.Time) (target *time.Time, yesterday bool, err error) {
	res, err := observations.ResolveDay(dayArg, now, observations.DayOptions{AllowPartial: true})
	switch {
	case errors.Is(err, observations.ErrDayFuture):
		// The ceiling stays in ResolveDay; this second pass only recovers the
		// day the value resolved to, so the refusal can name it.
		future, _ := observations.ResolveDay(dayArg, now, observations.DayOptions{
			AllowFuture: true, AllowPartial: true,
		})
		return nil, false, fmt.Errorf(
			"lucid closeout: cannot backfill %s — that day has not happened yet; nothing was saved",
			future.LogicalDate,
		)
	case err != nil:
		return nil, false, fmt.Errorf(
			"lucid closeout: could not read the day %q (want %s); nothing was saved",
			dayArg, observations.AcceptedDayForms,
		)
	}
	if res.Relative {
		return nil, true, nil
	}
	occurred := res.OccurredAt
	return &occurred, false, nil
}

// runBackfill executes a resolved backfill and prints its ack — the shared tail
// of the positional form and the `--day` alias, so the two cannot drift.
func runBackfill(
	cmd *cobra.Command, r *router.Router, args []string,
	target *time.Time, yesterday bool, now time.Time,
) error {
	links, capacity, tag, journal, err := parseCompactArgs(r, args)
	if err != nil {
		return err
	}
	res, err := r.Backfill(router.BackfillRequest{
		Now: now, Target: target, Yesterday: yesterday, Links: links, Capacity: capacity,
		LimiterTag: tag, Journal: journal, Source: sourceCLI, Harness: sourceCLI,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// parseBackfillDate resolves an explicit YYYY-MM-DD backfill target in the
// host's own zone (data-model.md trusts the host clock). Anything else is
// treated as the start of the compact form (ok=false); the "yesterday"
// keyword is handled by the caller and resolved in the router.
func parseBackfillDate(tok string) (*time.Time, bool) {
	if d, err := time.ParseInLocation("2006-01-02", tok, time.Now().Location()); err == nil {
		return &d, true
	}
	return nil, false
}

// parseCompactArgs joins the remaining args and parses the compact form.
// Empty args (a bare `lucid closeout`) yield no links — a caller that
// wants a real record supplies the compact form.
func parseCompactArgs(r *router.Router, args []string) (links map[string]string, capacity int, tag, journal string, err error) {
	joined := strings.TrimSpace(strings.Join(args, " "))
	if joined == "" {
		return nil, 0, "", "", fmt.Errorf(
			"lucid closeout: supply the compact form, e.g. `closeout dfx 3/wrist <journal line>`",
		)
	}
	chain, err := r.Chain()
	if err != nil {
		return nil, 0, "", "", err
	}
	return engine.ParseCompact(chain, joined)
}
