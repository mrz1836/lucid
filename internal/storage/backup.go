package storage

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxBackupFileSize caps any single file read from a restore archive at 500 MiB,
// mirroring the upgrade extractor's zip-bomb ceiling: the Ledger's files are tiny
// today, so the cap is far above realistic growth and far below the danger zone.
const maxBackupFileSize = 500 * 1024 * 1024

// gzipUnknownOS is the gzip OS byte meaning "unknown" (RFC 1952 §2.3.1). Pinning
// it — alongside an empty name/comment and a zero ModTime — keeps the archive
// container from leaking the wall clock or the host OS, so two backups of one
// unchanged tree are byte-identical.
const gzipUnknownOS = 255

// backupGzipLevel pins the DEFLATE level so the compressed stream is
// reproducible: determinism needs a fixed level, not a particular one.
const backupGzipLevel = gzip.BestCompression

// Sentinel errors for the backup/restore path, matched with errors.Is by the CLI
// layer. Each carries the package prefix so a wrap adds detail, never a second
// "storage:".
var (
	// ErrHomeOccupied is returned when a restore targets a home that already
	// holds record data and --force was not given.
	ErrHomeOccupied = errors.New("storage: restore target already holds data")
	// ErrDisallowedRoot is returned when an archive entry's top-level component
	// is not one of the backup-set roots the caller allowed.
	ErrDisallowedRoot = errors.New("storage: archive entry outside the backup roots")
	// ErrPathTraversal is returned when an archive entry is absolute or escapes
	// the home via "..": the Zip-Slip defense boundary.
	ErrPathTraversal = errors.New("storage: path traversal attempt in archive")
	// ErrFileTooLarge is returned when a single archived file exceeds
	// [maxBackupFileSize].
	ErrFileTooLarge = errors.New("storage: archived file exceeds the maximum size")
)

// BackupEntry is one path in a backup set, relative to the Ledger home. It
// mirrors deploy.BackupEntry field-for-field so the CLI can translate the
// canonical manifest into this type and this Sanctuary package never imports
// deploy. IsDir is advisory only — [Adapter.BackupTo] branches on the path's
// actual on-disk type; Exclude names home-relative sub-paths pruned from a
// directory walk (the one case is engine/ minus its derived status.json).
type BackupEntry struct {
	Path    string
	IsDir   bool
	Exclude []string
}

// BackupResult reports what a backup wrote: the home-relative slash paths that
// were archived (sorted) and the compressed byte count the caller's writer
// received.
type BackupResult struct {
	Files []string
	Bytes int64
}

// RestoreOptions controls a restore. Force overlays an occupied home;
// AllowedRoots is the set of top-level components an archive entry may live
// under (any other is rejected as [ErrDisallowedRoot]).
type RestoreOptions struct {
	Force        bool
	AllowedRoots []string
}

// RestoreResult reports what a restore wrote: the home-relative slash paths
// restored and the total uncompressed bytes written into the Ledger.
type RestoreResult struct {
	Files []string
	Bytes int64
}

// countingWriter tallies the bytes forwarded to an underlying writer so a backup
// can report the compressed size the caller actually received.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// BackupTo streams the given backup set to w as a deterministic gzip-compressed
// tar archive: the same unchanged tree produces byte-identical output within one
// Go toolchain. It reads only under this adapter's home and never mutates it.
func (a *Adapter) BackupTo(w io.Writer, entries []BackupEntry) (BackupResult, error) {
	files, err := a.collectEntries(entries)
	if err != nil {
		return BackupResult{}, err
	}
	cw := &countingWriter{w: w}
	gz, err := gzip.NewWriterLevel(cw, backupGzipLevel)
	if err != nil {
		return BackupResult{}, fmt.Errorf("storage: gzip writer: %w", err)
	}
	// Empty name/comment, zero ModTime, OS=unknown: the container leaks no
	// wall-clock or hostname, so two backups of one tree match byte-for-byte.
	gz.Header = gzip.Header{OS: gzipUnknownOS}
	tw := tar.NewWriter(gz)
	for _, rel := range files {
		if werr := a.writeTarEntry(tw, rel); werr != nil {
			return BackupResult{}, werr
		}
	}
	// Close the tar before the gzip so the final block and trailer both flush.
	if err := tw.Close(); err != nil {
		return BackupResult{}, fmt.Errorf("storage: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return BackupResult{}, fmt.Errorf("storage: close gzip: %w", err)
	}
	return BackupResult{Files: files, Bytes: cw.n}, nil
}

// collectEntries resolves the backup set into the sorted, home-relative slash
// paths of every regular file to archive. It branches on each entry's actual
// on-disk type (not BackupEntry.IsDir), skipping an absent tree, a symlink at the
// root, and any irregular file — so a manifest/disk mismatch degrades safely
// instead of erroring. Split out from BackupTo to fit the funlen budget.
func (a *Adapter) collectEntries(entries []BackupEntry) ([]string, error) {
	var files []string
	for _, e := range entries {
		abs := filepath.Join(a.home, filepath.FromSlash(e.Path))
		info, err := os.Lstat(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // an absent tree is skipped, mirroring scripts/backup.sh
		case err != nil:
			return nil, fmt.Errorf("storage: stat backup entry %q: %w", e.Path, err)
		case info.Mode()&fs.ModeSymlink != 0:
			continue // never follow a symlink out of ~/.lucid
		case info.IsDir():
			walked, werr := a.walkBackupDir(e)
			if werr != nil {
				return nil, werr
			}
			files = append(files, walked...)
		case info.Mode().IsRegular():
			files = append(files, e.Path)
		default:
			continue // FIFO, device, socket — never in a backup (P2)
		}
	}
	sort.Strings(files)
	return files, nil
}

// walkBackupDir walks one directory entry and returns the home-relative slash
// paths of its regular files, pruning any Exclude sub-path. Sub-directories,
// symlinks, and other irregular files are skipped: empty dirs are recreated on
// demand at restore, and the .keep markers that hold scaffolded dirs are regular
// files that ride along. Split out to fit the funlen/gocognit budgets.
func (a *Adapter) walkBackupDir(e BackupEntry) ([]string, error) {
	exclude := make(map[string]bool, len(e.Exclude))
	for _, x := range e.Exclude {
		exclude[x] = true
	}
	root := filepath.Join(a.home, filepath.FromSlash(e.Path))
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, rerr := filepath.Rel(a.home, p)
		if rerr != nil {
			return rerr
		}
		if slash := filepath.ToSlash(rel); !exclude[slash] {
			files = append(files, slash)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("storage: walk backup dir %q: %w", e.Path, err)
	}
	return files, nil
}

// writeTarEntry writes one home-relative regular file into the tar stream with a
// normalized, deterministic header: zero uid/gid and owner names, a
// second-truncated UTC ModTime, and zero access/change times (so a PAX writer
// emits no wall-clock records). Split out to fit the funlen budget.
func (a *Adapter) writeTarEntry(tw *tar.Writer, rel string) error {
	abs := filepath.Join(a.home, filepath.FromSlash(rel))
	fi, err := os.Stat(abs) // Stat, not Lstat: collectEntries already excluded symlinks
	if err != nil {
		return fmt.Errorf("storage: stat %q: %w", rel, err)
	}
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     rel,
		Mode:     int64(fi.Mode().Perm()),
		Size:     fi.Size(),
		ModTime:  fi.ModTime().Truncate(time.Second).UTC(),
	}
	if werr := tw.WriteHeader(hdr); werr != nil {
		return fmt.Errorf("storage: tar header %q: %w", rel, werr)
	}
	f, err := os.Open(abs) //nolint:gosec // abs is a home-relative manifest path under a.home
	if err != nil {
		return fmt.Errorf("storage: open %q: %w", rel, err)
	}
	defer func() { _ = f.Close() }()
	if _, cerr := io.Copy(tw, f); cerr != nil {
		return fmt.Errorf("storage: copy %q: %w", rel, cerr)
	}
	return nil
}

// RestoreFrom reads a gzip-compressed tar archive from r and overlays its files
// into this adapter's home. It refuses an occupied home unless opts.Force, and
// rejects any entry that escapes the home (Zip-Slip) or lands outside the
// allowed backup roots. It writes and overwrites the archive's files; it never
// deletes anything already present.
func (a *Adapter) RestoreFrom(r io.Reader, opts RestoreOptions) (RestoreResult, error) {
	if err := a.checkOccupied(opts); err != nil {
		return RestoreResult{}, err
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("storage: gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()
	return a.untar(tar.NewReader(gz), opts)
}

// checkOccupied refuses a restore into an occupied home unless opts.Force. A home
// is occupied when any allowed root holds a non-.keep regular file; a
// scaffolded-but-empty home (only lucid.json + .keep markers) reads as empty.
func (a *Adapter) checkOccupied(opts RestoreOptions) error {
	if opts.Force {
		return nil
	}
	for _, root := range opts.AllowedRoots {
		occupied, err := a.rootHasContent(root)
		if err != nil {
			return err
		}
		if occupied {
			return fmt.Errorf("%w (%s); pass --force to overlay a backup", ErrHomeOccupied, root)
		}
	}
	return nil
}

// rootHasContent reports whether a backup-set root holds any non-.keep regular
// file. An absent root is empty, not an error; the first real file settles it.
func (a *Adapter) rootHasContent(root string) (bool, error) {
	dir := filepath.Join(a.home, filepath.FromSlash(root))
	found := false
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return filepath.SkipDir // an absent root is empty
			}
			return walkErr
		}
		if d.Type().IsRegular() && d.Name() != keepFile {
			found = true
			return filepath.SkipAll // one real file is enough
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("storage: scan restore root %q: %w", root, err)
	}
	return found, nil
}

// untar walks every regular entry in tr, writing each into the home after the
// path and root checks. Irregular entries (dirs, symlinks) are skipped: parents
// are created on demand per file, so a hostile dir entry can never widen the
// surface.
func (a *Adapter) untar(tr *tar.Reader, opts RestoreOptions) (RestoreResult, error) {
	roots := make(map[string]bool, len(opts.AllowedRoots))
	for _, root := range opts.AllowedRoots {
		roots[root] = true
	}
	var res RestoreResult
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return res, nil
		}
		if err != nil {
			return res, fmt.Errorf("storage: read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		n, rel, err := a.restoreOneFile(tr, hdr.Name, roots)
		if err != nil {
			return res, err
		}
		res.Files = append(res.Files, rel)
		res.Bytes += n
	}
}

// restoreOneFile validates one archive entry's path (Zip-Slip guard + backup-root
// allowlist), then writes it under the home. It returns the bytes written and the
// home-relative slash path. Split out to fit the funlen budget.
func (a *Adapter) restoreOneFile(tr io.Reader, name string, roots map[string]bool) (int64, string, error) {
	destPath, err := validateArchivePath(a.home, name)
	if err != nil {
		return 0, "", err
	}
	rel, err := filepath.Rel(a.home, destPath)
	if err != nil {
		return 0, "", fmt.Errorf("storage: relativize %q: %w", name, err)
	}
	slash := filepath.ToSlash(rel)
	if !roots[firstComponent(slash)] {
		return 0, "", fmt.Errorf("%w: %s", ErrDisallowedRoot, slash)
	}
	if derr := ensureDir(filepath.Dir(destPath), "restore"); derr != nil {
		return 0, "", derr
	}
	written, err := writeCapped(tr, destPath, slash, maxBackupFileSize)
	if err != nil {
		return 0, "", err
	}
	return written, slash, nil
}

// writeCapped copies at most limit bytes from r into destPath at the owner-only
// file mode, removing the file and erroring if the cap is hit. The mode is 0o600
// unconditionally (no chmod up, unlike the upgrade extractor): a tighter umask
// only clears bits, so the result is never wider than requested. limit is a
// parameter (production passes [maxBackupFileSize]) so the cap is exercisable
// without a multi-hundred-MiB fixture.
func writeCapped(r io.Reader, destPath, slash string, limit int64) (int64, error) {
	// destPath validated by the Zip-Slip guard + manifest-root allowlist.
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, filePerm) //nolint:gosec // destPath validated above
	if err != nil {
		return 0, fmt.Errorf("storage: create %q: %w", slash, err)
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, limit))
	closeErr := f.Close()
	if n >= limit {
		_ = os.Remove(destPath)
		return 0, fmt.Errorf("%w: %s exceeds %d bytes", ErrFileTooLarge, slash, limit)
	}
	if copyErr != nil {
		return 0, fmt.Errorf("storage: extract %q: %w", slash, copyErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("storage: close %q: %w", slash, closeErr)
	}
	return n, nil
}

// validateArchivePath joins an archive entry name with destDir and returns the
// cleaned absolute path only if it stays inside destDir. Absolute entries and any
// ".." traversal return [ErrPathTraversal] — the Zip-Slip defense, mirroring the
// upgrade extractor.
//
// The guard is a prefix check of the cleaned, joined target against the cleaned
// destination plus a trailing separator: this is the form CodeQL's go/zipslip
// query recognizes as a sanitizer barrier (a filepath.Rel + ".." check is
// equivalent but the analyzer does not model it).
func validateArchivePath(destDir, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("%w: absolute path %q", ErrPathTraversal, name)
	}
	cleanDest := filepath.Clean(destDir)
	target := filepath.Join(cleanDest, name) // Join cleans the result.
	if !strings.HasPrefix(target, cleanDest+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrPathTraversal, name)
	}
	return target, nil
}

// firstComponent returns the first slash-separated component of a home-relative
// path — the top-level backup root the restore allowlist checks.
func firstComponent(slash string) string {
	first, _, _ := strings.Cut(slash, "/")
	return first
}
