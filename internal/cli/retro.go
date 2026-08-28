package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/retro"
	"github.com/mrz1836/lucid/internal/router"
)

// Retro verb flag names. --source sets the item's verbatim provenance; --all and
// --resolved switch the list view; --file points the hidden import at its JSON
// array; --day comes from the shared registerDayFlag, --json from the persistent
// root flag.
const (
	flagRetroSource   = "source"
	flagRetroAll      = "all"
	flagRetroResolved = "resolved"
	flagRetroFile     = "file"
)

// newRetroCmd wires `lucid retro` (retro.md §3–§6): the R-NNN parking lot — the
// single auditable home for anything parked to revisit at the weekly Retro or a
// Gate. It is a thin dispatch group — deterministic, agent-free, no LLM in any
// path (architecture P9), modeled on `lucid focus`:
//
//	lucid retro park "Revisit whether the Sunday walk opens with deferred items"
//	lucid retro park "Try a shorter Gate cadence next quarter" --source chat
//	lucid retro park "Experiment with a two-column weekly layout" --day @yesterday
//	lucid retro list                 # the Sunday walk: open + deferred
//	lucid retro list --all --json    # every item, structured
//	lucid retro show R-001
//	lucid retro resolve R-001 "Adopted it — the walk now opens with deferred items"
//	lucid retro defer   R-014 "Someday — revisit after the quarter closes"
//
// park appends one immutable item and prints its minted R-NNN plus the per-write
// receipt (the echo contract, §0); list/show fold the stream into current items
// (a pure read); resolve/defer append a transition that references an existing
// item without consuming a new R-NNN. The hidden one-time `import` (retro.md §6)
// reproduces a pre-existing queue exactly and never appears in `--help`.
func newRetroCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retro",
		Short: "Park and track the R-NNN retro parking lot",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(
		newRetroParkCmd(),
		newRetroListCmd(),
		newRetroShowCmd(),
		newRetroResolveCmd(),
		newRetroDeferCmd(),
		newRetroImportCmd(),
	)
	return cmd
}

// newRetroParkCmd wires `lucid retro park <item> [--source <value>] [--day
// <date>] [--json]`. The item is required and stored verbatim (all positionals
// are joined so an unquoted park works alongside a quoted one); --source sets the
// provenance verbatim (default `retro`, never synthesized). --day is the strict
// backdating tier — a bad token or a future day is a clean refusal that writes
// nothing, printed to stderr like the observation micro-log's --day. Unlike a
// plain capture, park honors --json (retro.md §3): the write result — the minted
// R-NNN, its receipt, and the folded item — as a structured object.
func newRetroParkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "park <item>",
		Short: "Park an item for the weekly retro (mints an R-NNN id)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			day, _ := cmd.Flags().GetString(flagDay)
			source, _ := cmd.Flags().GetString(flagRetroSource)
			res, err := r.ParkRetro(router.ParkRetroRequest{
				Item:   strings.Join(args, " "),
				Source: source,
				DayArg: day,
				Now:    time.Now(),
			})
			if err != nil {
				return emitRefusedDay(cmd, err)
			}
			return emit(cmd, res.View, []string{res.Ack})
		},
	}
	cmd.Flags().String(flagRetroSource, "", "Where the item came from, stored verbatim (default retro)")
	registerDayFlag(cmd)
	return cmd
}

// newRetroListCmd wires `lucid retro list [--all] [--resolved] [--json]`: the
// folded parking lot, ascending R-NNN. The default view is the Sunday walk — open
// ∪ deferred — so a deferred item stays visible with its reason. --all appends the
// resolved audit trail after the open/deferred section; --resolved shows only the
// resolved items (and wins over --all). Human-first by default; the structured
// list under --json. It writes nothing.
func newRetroListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List parked items (open + deferred by default; --all adds resolved)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			all, _ := cmd.Flags().GetBool(flagRetroAll)
			resolved, _ := cmd.Flags().GetBool(flagRetroResolved)
			res, err := r.ListRetro(router.ListRetroRequest{All: all, Resolved: resolved})
			if err != nil {
				return emitErr(cmd, err)
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
	cmd.Flags().Bool(flagRetroAll, false, "Include resolved items (the full audit view)")
	cmd.Flags().Bool(flagRetroResolved, false, "Show only the resolved audit trail")
	return cmd
}

// newRetroShowCmd wires `lucid retro show <R-NNN>`: a single folded item with all
// its fields (the verbatim body shown in full). Human-first by default; the folded
// item as a structured object under --json. An unknown id is a clean error printed
// to stderr with a non-zero exit.
func newRetroShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <R-NNN>",
		Short: "Show a single parked item with all its fields",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.ShowRetro(args[0])
			if err != nil {
				return emitErr(cmd, err)
			}
			return emit(cmd, res.View, res.Lines)
		},
	}
}

// newRetroResolveCmd wires `lucid retro resolve <R-NNN> <resolution>`: append a
// resolve transition that records what we did and never deletes the original park
// line. The id is the first positional; the rest is joined into the verbatim
// resolution (so an unquoted resolution works alongside a quoted one). It
// acknowledges the affected R-NNN and its fresh receipt; --json returns both plus
// the item's new folded state. An unknown id, or an empty resolution, is a clean
// error and nothing is appended.
func newRetroResolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve <R-NNN> <resolution>",
		Short: "Resolve a parked item (kept as the audit trail; never deleted)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.ResolveRetro(router.ResolveRetroRequest{
				ID:         args[0],
				Resolution: strings.Join(args[1:], " "),
				Now:        time.Now(),
			})
			if err != nil {
				return emitErr(cmd, err)
			}
			return emit(cmd, res.View, []string{res.Ack})
		},
	}
}

// newRetroDeferCmd wires `lucid retro defer <R-NNN> <reason>`: append a defer
// transition that sets the first-class deferred status and keeps the item in the
// default list. The id is the first positional; the rest is joined into the
// verbatim reason. It acknowledges the affected R-NNN and its fresh receipt;
// --json returns both plus the item's new folded state. An unknown id, or an empty
// reason, is a clean error and nothing is appended.
func newRetroDeferCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "defer <R-NNN> <reason>",
		Short: "Defer a parked item (stays visible in the default list with its reason)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			res, err := r.DeferRetro(router.DeferRetroRequest{
				ID:     args[0],
				Reason: strings.Join(args[1:], " "),
				Now:    time.Now(),
			})
			if err != nil {
				return emitErr(cmd, err)
			}
			return emit(cmd, res.View, []string{res.Ack})
		},
	}
}

// newRetroImportCmd wires the hidden one-time `lucid retro import --file <json>`
// (retro.md §6): the migration cutover from a hand-maintained queue into the
// Ledger. Modeled on the hidden `pet migrate-self`, it carries Hidden: true and
// never appears in `lucid retro --help` — it is a migration tool, not one of the
// five everyday subcommands. It reads a JSON array of folded items (the same shape
// `retro list --all --json` emits) and reproduces each one exactly: the supplied
// R-NNN, the backdated parked-date, the verbatim source and text, and any
// resolve/defer transition. It is NOT idempotent — a second run duplicates ids —
// so a failed run is recovered by restoring the pre-migration backup and
// replaying, never by re-running on top.
func newRetroImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "import",
		Short:  "One-time migration: reproduce a pre-existing queue from a JSON file",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			path, _ := cmd.Flags().GetString(flagRetroFile)
			items, err := readRetroImportFile(path)
			if err != nil {
				return emitErr(cmd, err)
			}
			res, err := r.ImportRetro(router.ImportRetroRequest{Items: items, Now: time.Now()})
			if err != nil {
				return emitErr(cmd, err)
			}
			return emit(cmd, res.View, []string{res.Ack})
		},
	}
	cmd.Flags().String(flagRetroFile, "", "Path to the JSON array of items to import (required)")
	_ = cmd.MarkFlagRequired(flagRetroFile)
	return cmd
}

// readRetroImportFile reads and parses the import file — a JSON array of folded
// [retro.Item] values. A missing path, an unreadable file, or a body that is not a
// JSON array is a clean error that imports nothing.
func readRetroImportFile(path string) ([]retro.Item, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("a --file path is required for retro import; nothing was imported")
	}
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied migration file, read once at cutover
	if err != nil {
		return nil, fmt.Errorf("could not read the import file %s: %w", path, err)
	}
	var items []retro.Item
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("could not parse %s as a JSON array of retro items: %w", path, err)
	}
	return items, nil
}
