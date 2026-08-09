package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/deploy"
	"github.com/mrz1836/lucid/internal/storage"
)

// backupFlagOut is the `--out` archive-destination flag shared by the command
// and its tests.
const backupFlagOut = "out"

// backupJSON is the `--json` payload for `lucid backup`: the archive path, its
// compressed size in bytes, and the count of files written.
type backupJSON struct {
	Command string `json:"command"`
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	Files   int    `json:"files"`
}

// newBackupCmd wires `lucid backup` — write the must-keep Ledger set (the
// ADR-0002 backup manifest, shared with scripts/backup.sh via
// deploy.BackupManifest) to a single deterministic .tar.gz. The archive sink is
// the CLI's concern (an external path outside ~/.lucid); the ~/.lucid walk and
// read live entirely in the storage adapter, so the Sanctuary boundary holds.
func newBackupCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "backup [--out <file>]",
		Short: "Write the must-keep Ledger trees to a single .tar.gz archive",
		Long: `backup writes the must-keep Ledger set — the ADR-0002 backup manifest
that exists nowhere else: raw/, media/, observations/, registries/, links/,
secrets/, engine/ (minus its derived status.json), and
projections/exports.log — to one
gzip-compressed tar archive. Rebuildable trees and the reconstructable indexes
(processed/, insights/, reflections/, engine/status.json, people/, sessions/,
lucid.json) are omitted. Everything primary is included — there is no opt-out flag.

Covering media/ and links/ means a restore yields a coherent Ledger: the
association ledger and the binaries its links point at survive together. It also
has an honest cost — media/ holds already-compressed formats (photos, scanned
PDFs), so the gzip stream shrinks them very little and archives are materially
larger than a metadata-only backup.

It reads ~/.lucid/ and writes one archive; it makes no network call and never
mutates the Ledger. The destination must be outside ~/.lucid/ (a backup is never
written into the tree it copies), and an existing file is never clobbered.`,
		Args: cobra.NoArgs,
		Example: `  # Write a timestamped archive to the current directory.
  lucid backup

  # Choose the destination.
  lucid backup --out /mnt/usb/lucid-2026-07-29.tar.gz`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return emitErr(cmd, runBackup(cmd, out))
		},
	}
	cmd.Flags().StringVar(&out, backupFlagOut, "", "Destination archive path (default: lucid-backup-<UTC timestamp>.tar.gz in the current directory)")
	return cmd
}

// runBackup resolves the sink, guards it against the Ledger home (P8), creates it
// with O_EXCL so a same-second re-run fails loudly rather than clobbering, and
// streams the manifest through the storage adapter. A mid-backup failure removes
// the partial archive so a broken file is never left behind.
func runBackup(cmd *cobra.Command, outFlag string) error {
	store, err := storage.Open()
	if err != nil {
		return fmt.Errorf("lucid backup: resolve home: %w", err)
	}
	out := outFlag
	if out == "" {
		out = defaultBackupName(clockNow())
	}
	inside, err := pathInsideHome(store.Home(), out)
	if err != nil {
		return fmt.Errorf("lucid backup: %w", err)
	}
	if inside {
		return fmt.Errorf("lucid backup: refusing to write the archive inside the Ledger home (%s) — choose a path outside ~/.lucid", store.Home())
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) //nolint:gosec // out is an operator-named sink guarded outside ~/.lucid
	if err != nil {
		return fmt.Errorf("lucid backup: create %q: %w", out, err)
	}
	res, backupErr := store.BackupTo(f, toStorageEntries(deploy.BackupManifest()))
	closeErr := f.Close()
	if backupErr != nil {
		_ = os.Remove(out) // we created it with O_EXCL, so removing the partial is safe
		return fmt.Errorf("lucid backup: %w", backupErr)
	}
	if closeErr != nil {
		return fmt.Errorf("lucid backup: close %q: %w", out, closeErr)
	}
	return emit(cmd, backupPayload(out, res), backupLines(out, res))
}

// toStorageEntries translates the deploy backup manifest into storage's own
// BackupEntry type so the Sanctuary package never imports deploy — the CLI is the
// junction that owns that dependency.
func toStorageEntries(manifest []deploy.BackupEntry) []storage.BackupEntry {
	out := make([]storage.BackupEntry, len(manifest))
	for i, e := range manifest {
		out[i] = storage.BackupEntry{Path: e.Path, IsDir: e.IsDir, Exclude: e.Exclude}
	}
	return out
}

// defaultBackupName is the CWD-relative archive name when --out is unset: a UTC
// timestamp keeps a day's backups distinct and sorts them chronologically. It
// reuses the CLI's clockNow seam so a test pins the name deterministically.
func defaultBackupName(now time.Time) string {
	return "lucid-backup-" + now.UTC().Format("20060102-150405") + ".tar.gz"
}

// pathInsideHome reports whether path resolves to (or under) the Ledger home, so
// backup can refuse to write into the very tree it copies (P8). An unrelatable
// pair (e.g. distinct Windows volumes) is treated as outside.
func pathInsideHome(home, path string) (bool, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("resolve %q: %w", path, err)
	}
	absHome, err := filepath.Abs(home)
	if err != nil {
		return false, fmt.Errorf("resolve home: %w", err)
	}
	rel, err := filepath.Rel(absHome, absPath)
	if err != nil {
		//nolint:nilerr // an unrelatable path pair (e.g. distinct volumes) is outside the home, not a failure
		return false, nil
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// backupPayload builds the --json document for a completed backup.
func backupPayload(path string, res storage.BackupResult) backupJSON {
	return backupJSON{Command: "backup", Path: path, Bytes: res.Bytes, Files: len(res.Files)}
}

// backupLines renders the human summary, ending by naming what was written and
// where.
func backupLines(path string, res storage.BackupResult) []string {
	return []string{
		fmt.Sprintf("Backed up %d file(s) (%d bytes) to %s", len(res.Files), res.Bytes, path),
	}
}
