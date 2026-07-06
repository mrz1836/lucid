package validate

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// skipDirs are tree names never walked by a repo sweep: version control, the
// build cache, vendored modules, and the validate package's own directory
// (which necessarily holds the forbidden-pattern definitions and detection
// fixtures — a linter does not lint itself).
//
//nolint:gochecknoglobals // fixed, read-only skip set for the repo walker
var skipDirs = map[string]bool{
	".git":              true,
	".idea":             true,
	"vendor":            true,
	"node_modules":      true,
	"internal/validate": true,
}

// walkTextFiles calls fn for every likely-text file under root, in a stable
// lexical order, skipping [skipDirs] and any file that looks binary. Paths
// passed to fn are repo-relative and slash-separated so findings read the same
// on every host. fn receives the file's bytes; returning an error from fn
// aborts the walk.
func walkTextFiles(root string, fn func(rel string, content []byte) error) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := relSlash(root, p)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			if rel != "." && isSkippedDir(rel, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		content, readErr := os.ReadFile(p) //nolint:gosec // p is a path yielded by WalkDir over the caller-supplied root; the sweep is read-only
		if readErr != nil {
			return readErr
		}
		if looksBinary(content) {
			return nil
		}
		return fn(rel, content)
	})
}

// isSkippedDir reports whether a directory (by base name or repo-relative
// path) is one the walker never descends into.
func isSkippedDir(rel, name string) bool {
	if skipDirs[name] {
		return true
	}
	return skipDirs[rel]
}

// relSlash returns p relative to root with OS separators normalized to "/".
func relSlash(root, p string) (string, error) {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// looksBinary reports whether content is likely a binary file: a NUL byte in
// the first 8 KiB is the heuristic. Text scanners skip these so a grep never
// trips on a compiled artifact or an image.
func looksBinary(content []byte) bool {
	head := content
	if len(head) > 8192 {
		head = head[:8192]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// splitLines splits content into lines without a trailing empty element,
// trimming a trailing "\r" from each so findings report the right 1-indexed
// line on both Unix and CRLF files.
func splitLines(content []byte) []string {
	s := string(content)
	if s == "" {
		return nil
	}
	raw := strings.Split(s, "\n")
	out := make([]string, len(raw))
	for i, ln := range raw {
		out[i] = strings.TrimSuffix(ln, "\r")
	}
	// A file ending in "\n" yields a trailing "" element; drop it so line
	// counts match an editor's.
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
