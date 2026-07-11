package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// errAnchorNotRecorded maps a rejected `anchor add` to a non-zero exit. The
// fixed reason is already printed to stderr; this sentinel just keeps a script
// from reading a rejected record as success (mirrors errModeNotAccepted).
var errAnchorNotRecorded = errors.New("lucid: anchor not recorded")

// newAnchorCmd wires `lucid anchor` (engine-module.md §Commands): the parent
// group for the days-since milestone verbs. It currently exposes only `add`;
// it exists as a group so later anchor subcommands attach without reshaping the
// tree.
func newAnchorCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "anchor",
		Short: "Record days-since milestones (anchors)",
		Long: `anchor groups the days-since milestone verbs. A milestone is a labeled
civil date the metrics surface counts elapsed days from — a cessation or a
gate the practice measures against. Anchors are append-only: a correction or a
reset is a new dated record, and the latest record per label wins.`,
		Args: cobra.NoArgs,
	}
	parent.AddCommand(newAnchorAddCmd())
	return parent
}

// newAnchorAddCmd builds the `add` child: record one labeled YYYY-MM-DD
// milestone with an optional trailing note. Human-first prose ack by default;
// the recorded anchor as JSON under --json for scripts (ADR-0007). A rejected
// input (empty label or unparseable date) prints the fixed reason on stderr and
// exits non-zero without writing; the record path is model-free.
func newAnchorAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <label> <date> [note...]",
		Short: "Record a days-since anchor (a labeled YYYY-MM-DD milestone)",
		Long: `add records one days-since milestone: a label, a civil YYYY-MM-DD date,
and an optional trailing note. Dates are backdatable — any past or future civil
date is accepted. Recording the same label again appends a new record and the
latest one wins, so a mistyped date and a genuine reset are the same operation.`,
		Args: cobra.MinimumNArgs(2),
		Example: `  # Record a cessation milestone.
  lucid anchor add sobriety 2026-01-01

  # Add a note (trailing words are joined).
  lucid anchor add gate 2026-03-15 first ninety-day gate

  # Machine-readable output for a harness.
  lucid anchor add sobriety 2026-01-01 --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.AnchorAdd(router.AnchorAddRequest{
				Label: args[0],
				Date:  args[1],
				Note:  strings.Join(args[2:], " "),
				Now:   clockNow(),
			})
			if err != nil {
				if errors.Is(err, router.ErrAnchorRejected) {
					// Deterministic rejection: surface the fixed reason and exit
					// non-zero so a caller never mistakes it for a recorded anchor.
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
					return errAnchorNotRecorded
				}
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
				return writeJSON(cmd.OutOrStdout(), res.Anchor)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
}
