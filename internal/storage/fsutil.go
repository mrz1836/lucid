package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// ensureDir creates dir (and any missing parents) if absent, wrapping a failure
// with the standard "prepare <label> dir" message the record writers share.
// The Ledger tree is scaffolded at init, so at runtime this is an idempotent
// guard that a record family's directory exists before a write — and the single
// place the write path's MkdirAll wording (and, later, its rooting) lives.
func ensureDir(dir, label string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("storage: prepare %s dir: %w", label, err)
	}
	return nil
}

// writeFileAtomic writes content to path such that a failed or interrupted
// write can never leave a partial file in its place: it writes a temp file in
// the target's own directory, syncs it, and renames it over the target. A
// rename within one directory is atomic, so a reader sees either the whole
// previous file or the whole new one — never a truncated one — and a write that
// fails leaves the previous file exactly as it was. label names the record for
// the error messages, matching this package's "storage: <verb> <label>" shape.
//
// The scope is deliberately narrow, and saying so is part of the contract: this
// exists for the self-facts store, whose file is years of semantic memory where
// a torn write costs the user their whole self-profile rather than a handful of
// recoverable records. Every other writer in the adapter still calls
// os.WriteFile, and making them all atomic is a separate change — the helper
// takes exactly os.WriteFile's arguments plus a label so that change would be
// one line per call site.
//
//nolint:unparam // label is kept explicit so a second caller is a one-line change, not a signature change
func writeFileAtomic(path string, content []byte, label string) error {
	dir := filepath.Dir(path)

	// The temp file must live in the target's OWN directory. os.Rename is only
	// atomic within a filesystem, so a temp file staged elsewhere (TMPDIR, say)
	// would degrade the rename into a cross-device copy and silently lose the
	// only guarantee this function exists to provide.
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("storage: create temp for %s: %w", label, err)
	}
	tmp := f.Name()
	// discard removes the staged file so a failure leaves no residue beside the
	// record it failed to replace.
	discard := func(wrapped error) error {
		_ = os.Remove(tmp)
		return wrapped
	}

	if _, err = f.Write(content); err != nil {
		_ = f.Close()
		return discard(fmt.Errorf("storage: write %s: %w", label, err))
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return discard(fmt.Errorf("storage: sync %s: %w", label, err))
	}
	if err = f.Close(); err != nil {
		return discard(fmt.Errorf("storage: close %s: %w", label, err))
	}
	// CreateTemp opens at 0600, which is filePerm today. Setting the mode
	// explicitly makes the renamed file's permissions independent of both that
	// coincidence and the process umask, so the Ledger's owner-only rule holds
	// however the writing process was launched.
	if err = os.Chmod(tmp, filePerm); err != nil {
		return discard(fmt.Errorf("storage: set mode on %s: %w", label, err))
	}
	if err = os.Rename(tmp, path); err != nil {
		return discard(fmt.Errorf("storage: replace %s: %w", label, err))
	}

	// Best-effort: fsync the directory so the rename itself is durable, not
	// merely the bytes it published. The record is already in place by now, so a
	// filesystem that will not open or sync a directory is not a write failure —
	// hence the dropped errors, matching the package's existing `_ =` idiom.
	if d, dirErr := os.Open(dir); dirErr == nil { //nolint:gosec // adapter-internal path derived from the resolved Ledger home
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
