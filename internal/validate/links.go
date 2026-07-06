package validate

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// markdownLink matches an inline Markdown link's target: the text in the
// parentheses of [label](target). It is intentionally simple — the doc set
// uses plain inline links — and it ignores image/reference syntax the MVP docs
// do not use.
var markdownLink = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

// CheckDocLinks verifies that relative Markdown links in the Lucid-authored
// doc set — the `docs/` tree and the repo-root Markdown files
// (claude-code-workflow.md names "docs/mvp/*.md, docs/, and the root
// README.md") — resolve to a file on disk. External links (http/https/mailto)
// and pure in-page anchors (#section) are not checked, and the vendored CI
// template under `.github/` is out of scope (its links target a full
// go-template layout this repo does not carry). A broken link is a warning,
// never an error: link hygiene is surfaced but never fails the sweep.
func CheckDocLinks(root string) ([]Finding, error) {
	var findings []Finding
	err := walkTextFiles(root, func(rel string, content []byte) error {
		if !strings.HasSuffix(rel, ".md") || !isDocFile(rel) {
			return nil
		}
		for i, line := range splitLines(content) {
			for _, m := range markdownLink.FindAllStringSubmatch(line, -1) {
				target := strings.TrimSpace(m[1])
				if !isCheckableLink(target) {
					continue
				}
				if !linkResolves(root, rel, target) {
					findings = append(findings, Finding{
						Check:    CheckLinks,
						Severity: SeverityWarn,
						Path:     rel,
						Line:     i + 1,
						Rule:     "broken-link",
						Message:  "relative link target not found: " + target,
					})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

// isDocFile reports whether a repo-relative Markdown path is part of the
// Lucid-authored doc set the link check owns: anything under docs/, or a
// top-level file (README.md, CLAUDE.md, AGENTS.md). The vendored .github/
// template tree is deliberately excluded.
func isDocFile(rel string) bool {
	if strings.HasPrefix(rel, "docs/") {
		return true
	}
	return !strings.Contains(rel, "/")
}

// isCheckableLink reports whether a link target is a relative filesystem path
// worth resolving. Absolute URLs, mailto:, protocol-relative links, and pure
// anchors are skipped.
func isCheckableLink(target string) bool {
	if target == "" || strings.HasPrefix(target, "#") {
		return false
	}
	if strings.HasPrefix(target, "//") {
		return false
	}
	if i := strings.Index(target, "://"); i >= 0 {
		return false
	}
	if strings.HasPrefix(target, "mailto:") {
		return false
	}
	return true
}

// linkResolves reports whether target — a relative link from the file at
// fileRel — points at an existing path. Any trailing anchor or query is
// stripped before resolution.
func linkResolves(root, fileRel, target string) bool {
	clean := target
	if i := strings.IndexAny(clean, "#?"); i >= 0 {
		clean = clean[:i]
	}
	if clean == "" {
		return true // a bare "#anchor" resolved above; an empty path is same-file
	}
	base := path.Dir(filepath.ToSlash(fileRel))
	joined := path.Clean(path.Join(base, clean))
	abs := filepath.Join(root, filepath.FromSlash(joined))
	_, err := os.Stat(abs)
	return err == nil
}
