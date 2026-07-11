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
	links, capacity, tag, journal, err := parseCompactArgs(r, args)
	if err != nil {
		return err
	}
	res, err := r.Backfill(router.BackfillRequest{
		Now: time.Now(), Target: target, Yesterday: yesterday, Links: links, Capacity: capacity,
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
