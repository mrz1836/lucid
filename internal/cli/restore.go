package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/deploy"
	"github.com/mrz1836/lucid/internal/storage"
)

// Flag names for `lucid restore`, shared with its tests.
const (
	restoreFlagIn    = "in"
	restoreFlagForce = "force"
)

// restoreJSON is the `--json` payload for `lucid restore`: the source archive,
// the total bytes restored, and the count of files written.
type restoreJSON struct {
	Command string `json:"command"`
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	Files   int    `json:"files"`
}

// newRestoreCmd wires `lucid restore` — rebuild a Ledger from a `lucid backup`
// archive. The CLI opens the external archive; the storage adapter validates and
// writes every entry under ~/.lucid/ (Zip-Slip guard + backup-root allowlist).
// Restore is an overlay: it writes and overwrites the archive's files and never
// deletes anything already present.
func newRestoreCmd() *cobra.Command {
	var in string
	var force bool
	cmd := &cobra.Command{
		Use:   "restore [--in <file>] [--force]",
		Short: "Rebuild a Ledger from a backup archive",
		Long: `restore reads a lucid backup archive and overlays its files into
~/.lucid/. Every entry is checked against a path-traversal (Zip-Slip) guard and
the backup-set root allowlist, so a crafted archive can never plant a file
outside the record trees.

It is an overlay, not a replace: it writes and overwrites the archive's files and
never deletes anything already present (a full replace is out of scope). By
default it refuses to write into an occupied home — any backup-set root holding a
non-.keep file — and names --force; a scaffolded-but-empty home (only lucid.json
and .keep markers) counts as empty and restores without the flag.`,
		Args: cobra.MaximumNArgs(1),
		Example: `  # Restore from an explicit archive.
  lucid restore --in /tmp/b.tar.gz

  # Or pass the path positionally.
  lucid restore /mnt/usb/lucid-2026-07-29.tar.gz

  # Overlay onto a home that already holds data.
  lucid restore --in /tmp/b.tar.gz --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return emitErr(cmd, runRestore(cmd, in, args, force))
		},
	}
	cmd.Flags().StringVar(&in, restoreFlagIn, "", "Source archive path (or pass it positionally)")
	cmd.Flags().BoolVar(&force, restoreFlagForce, false, "Overlay the archive even when the target already holds data")
	return cmd
}

// runRestore resolves the input archive (flag or positional), opens it from
// outside ~/.lucid, and hands the read/write to the storage adapter. The archive
// sink lives in the CLI; all ~/.lucid writing lives in storage — the Sanctuary
// split.
func runRestore(cmd *cobra.Command, inFlag string, args []string, force bool) error {
	in, err := resolveRestoreInput(inFlag, args)
	if err != nil {
		return err
	}
	store, err := storage.Open()
	if err != nil {
		return fmt.Errorf("lucid restore: resolve home: %w", err)
	}
	f, err := os.Open(in) //nolint:gosec // in is an operator-named archive path outside ~/.lucid
	if err != nil {
		return fmt.Errorf("lucid restore: open %q: %w", in, err)
	}
	defer func() { _ = f.Close() }()
	res, err := store.RestoreFrom(f, storage.RestoreOptions{Force: force, AllowedRoots: restoreRoots()})
	if err != nil {
		return fmt.Errorf("lucid restore: %w", err)
	}
	return emit(cmd, restorePayload(in, res), restoreLines(in, res))
}

// resolveRestoreInput picks the archive path from --in or the single positional,
// refusing both-at-once and neither.
func resolveRestoreInput(inFlag string, args []string) (string, error) {
	switch {
	case inFlag != "" && len(args) > 0:
		return "", fmt.Errorf("lucid restore: pass the archive once — either --in or a positional path, not both")
	case inFlag != "":
		return inFlag, nil
	case len(args) > 0:
		return args[0], nil
	default:
		return "", fmt.Errorf("lucid restore: name the archive to restore (--in <path> or a positional path)")
	}
}

// restoreRoots is the deduped set of top-level components the backup manifest
// writes under — the allowlist RestoreFrom rejects any out-of-set archive entry
// against, so a crafted archive can never plant a file outside the record trees.
func restoreRoots() []string {
	seen := map[string]bool{}
	var roots []string
	for _, e := range deploy.BackupManifest() {
		first, _, _ := strings.Cut(e.Path, "/")
		if !seen[first] {
			seen[first] = true
			roots = append(roots, first)
		}
	}
	return roots
}

// restorePayload builds the --json document for a completed restore.
func restorePayload(path string, res storage.RestoreResult) restoreJSON {
	return restoreJSON{Command: "restore", Path: path, Bytes: res.Bytes, Files: len(res.Files)}
}

// restoreLines renders the human summary, ending by naming what was restored and
// from where.
func restoreLines(path string, res storage.RestoreResult) []string {
	if len(res.Files) == 0 {
		return []string{fmt.Sprintf("Nothing to restore from %s — the archive held no in-scope files.", path)}
	}
	return []string{
		fmt.Sprintf("Restored %d file(s) (%d bytes) from %s into the Ledger.", len(res.Files), res.Bytes, path),
	}
}
