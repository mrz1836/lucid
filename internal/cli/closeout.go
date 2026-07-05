package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/engine"
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
//
// It scaffolds the Ledger on first use so capture never blocks on setup.
func newCloseoutCmd() *cobra.Command {
	return &cobra.Command{
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
}

// dispatchCloseout parses the leading sub-word and routes to the matching
// router call.
func dispatchCloseout(cmd *cobra.Command, r *router.Router, args []string) error {
	if len(args) > 0 && args[0] == "skip" {
		return runCloseout(cmd, r, router.CloseoutRequest{Now: time.Now(), Skip: true, Source: sourceCLI, Harness: sourceCLI})
	}
	if len(args) > 0 && args[0] == "backfill" {
		return runBackfill(cmd, r, args[1:])
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
		Now: time.Now(), Links: links, Capacity: capacity, LimiterTag: tag, Journal: journal,
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

// runBackfill parses an optional target then executes the backfill.
func runBackfill(cmd *cobra.Command, r *router.Router, args []string) error {
	var target *time.Time
	if len(args) > 0 {
		if t, ok := parseBackfillTarget(args[0]); ok {
			target = t
			args = args[1:]
		}
	}
	links, capacity, tag, journal, err := parseCompactArgs(r, args)
	if err != nil {
		return err
	}
	res, err := r.Backfill(router.BackfillRequest{
		Now: time.Now(), Target: target, Links: links, Capacity: capacity, LimiterTag: tag,
		Journal: journal, Source: sourceCLI, Harness: sourceCLI,
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// parseBackfillTarget resolves the optional target token: "yesterday" or a
// YYYY-MM-DD date. Anything else is treated as the start of the compact
// form (ok=false).
func parseBackfillTarget(tok string) (*time.Time, bool) {
	if tok == "yesterday" {
		y := time.Now().AddDate(0, 0, -1)
		return &y, true
	}
	// Parse an explicit backfill date in the host's own zone (data-model.md
	// trusts the host clock) so it aligns with the "yesterday" branch.
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
			"lucid closeout: supply the compact form, e.g. `closeout dfx 3/wrist <journal line>`")
	}
	chain, err := r.Chain()
	if err != nil {
		return nil, 0, "", "", err
	}
	return engine.ParseCompact(chain, joined)
}
