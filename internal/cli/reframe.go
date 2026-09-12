package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/router"
)

// newReframeCmd wires `lucid reframe` (reframes.md §3–§5): keep and rotate the
// self-talk "catch → flip" pairs. It is a thin dispatch group over three
// subcommands — deterministic, agent-free, no LLM in any path (architecture P9),
// modeled on `lucid obs`:
//
//	lucid reframe add "I can't do this" "I can learn this"
//	lucid reframe add "This is too hard" "This is worth the effort" --day @yesterday
//	lucid reframe list --json
//	lucid reframe surface
//
// add appends one immutable entry and prints its receipt id; list reads the
// live pool; surface returns exactly one reframe for the logical day and records
// that it was shown (rotating least-recently-surfaced, idempotent within a day).
func newReframeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reframe",
		Short: "Keep and rotate your self-talk reframes (catch → flip)",
	}
	cmd.AddCommand(newReframeAddCmd(), newReframeListCmd(), newReframeSurfaceCmd())
	return cmd
}

// newReframeAddCmd wires `lucid reframe add <catch> <flip> [--day <date>]`. Both
// positional arguments are required and stored verbatim; any trailing `#tag`
// tokens are copied into the entry's tags (obs-parity, optional). `--day` is the
// strict backdating tier — a bad token or a future day is a clean refusal that
// writes nothing, printed to stderr like the observation micro-log's `--day`.
// It ignores `--json` (a write verb, same as `log`/`obs`).
func newReframeAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <catch> <flip>",
		Short: "Append a reframe (catch → flip)",
		// The safe form supplies both catch and flip off the command line, so no
		// catch/flip positional is required then; the positional form still
		// requires both. resolveReframe enforces the both-together rule.
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("catch-file") || cmd.Flags().Changed("flip-file") {
				return nil
			}
			return cobra.MinimumNArgs(2)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			catch, flip, tags, err := resolveReframe(cmd, args)
			if err != nil {
				return emitErr(cmd, err)
			}
			day, _ := cmd.Flags().GetString(flagDay)
			res, err := r.AddReframe(router.AddReframeRequest{
				Catch:  catch,
				Flip:   flip,
				Tags:   tags,
				DayArg: day,
				Now:    time.Now(),
			})
			if err != nil {
				return emitRefusedDay(cmd, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	registerDayFlag(cmd)
	cmd.Flags().String("catch-file", "", "Read the catch from this file (or - for stdin); requires --flip-file")
	cmd.Flags().String("flip-file", "", "Read the flip from this file (or - for stdin); requires --catch-file")
	return cmd
}

// resolveReframe resolves a reframe's catch and flip from either both file flags
// or both positionals — the two required fields move together. The safe file
// form reads --catch-file and --flip-file through the shared reader (so a self-
// talk line carrying a shell metacharacter stays data) and treats every
// positional as a trailing tag; the positional form is the original
// catch/flip/tags layout, unchanged. Supplying only one file flag is a usage
// error, since a reframe needs both halves.
func resolveReframe(cmd *cobra.Command, args []string) (catch, flip string, tags []string, err error) {
	catchFile := cmd.Flags().Changed("catch-file")
	flipFile := cmd.Flags().Changed("flip-file")
	switch {
	case catchFile != flipFile:
		return "", "", nil, fmt.Errorf(
			"lucid reframe add: --catch-file and --flip-file must be given together; nothing was saved",
		)
	case catchFile && flipFile:
		if err = ensureSingleStdinFlags(cmd, "catch-file", "flip-file"); err != nil {
			return "", "", nil, err
		}
		catchPath, _ := cmd.Flags().GetString("catch-file")
		if catch, err = readBodyFile("catch-file", catchPath, cmd.InOrStdin()); err != nil {
			return "", "", nil, fmt.Errorf("lucid reframe add: %w", err)
		}
		flipPath, _ := cmd.Flags().GetString("flip-file")
		if flip, err = readBodyFile("flip-file", flipPath, cmd.InOrStdin()); err != nil {
			return "", "", nil, fmt.Errorf("lucid reframe add: %w", err)
		}
		return catch, flip, reframeTags(args), nil
	default:
		return args[0], args[1], reframeTags(args[2:]), nil
	}
}

// newReframeListCmd wires `lucid reframe list [--json]`: the live pool
// (corrections folded, superseded entries omitted), human-first by default with
// the structured list under `--json` (ADR-0007). It writes nothing.
func newReframeListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List stored reframes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.ListReframes()
			if err != nil {
				return err
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
}

// newReframeSurfaceCmd wires `lucid reframe surface [--json]`: the one reframe
// for the logical day, recording that it was shown. It is the daily one-per-day
// source the morning surface reads. Human-first by default; the single pick as a
// structured object under `--json`.
func newReframeSurfaceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "surface",
		Short: "Surface one reframe for the day",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.SurfaceReframe(time.Now())
			if err != nil {
				return err
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
}

// reframeTags copies the trailing arguments after catch/flip into tags,
// stripping an optional leading `#` from each so both `#growth` and `growth`
// land the same tag. Empty tokens are dropped; no trailing tokens means no tags
// (the entry marshals `tags` as `[]`).
func reframeTags(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	tags := make([]string, 0, len(args))
	for _, a := range args {
		t := strings.TrimPrefix(strings.TrimSpace(a), "#")
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}
