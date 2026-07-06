package validate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// agentsRel is the repo-relative package tree that holds the model-facing
// agent prompts. The diagnostic and sanctuary source scans read only this
// tree: it is where the four required-now agents (Intake, Structuring,
// Reflection, Safety/Consent) compose the strings a model or the user sees.
const agentsRel = "internal/agents"

// diagnosticPatterns is the diagnostic / labeling subset of the phrase
// blocklist (product-principles.md §6). The S-8 sweep applies exactly this
// subset — clinical labels, certainty overclaims, and performance markers — to
// shipped agent prompt strings. The external-action and coaching categories
// are intentionally excluded here: their common verbs ("call", "post") occur
// in ordinary prompt prose, and they are enforced at runtime by the Safety
// gate, not by prompt hygiene.
//
//nolint:gochecknoglobals // fixed, read-only compiled diagnostic subset (product-principles.md §6)
var diagnosticPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\byou (always|never)\b`),
	regexp.MustCompile(`(?i)\byou ('?re|have) (an? )?(anxious|avoidant|secure|disorganized)\b`),
	regexp.MustCompile(`(?i)\b(anxious|avoidant|secure|disorganized) (attach\w*|tendenc\w*|style|type|behavior)\b`),
	regexp.MustCompile(`(?i)\b(clearly|obviously)\b`),
	regexp.MustCompile(`(?i)\b(i (diagnos\w*|am diagnosing)|you'?re suffering from)\b`),
	regexp.MustCompile(`(?i)\b(attachment style|trauma response|narcissist|borderline)\b`),
	regexp.MustCompile(`(?i)(!{2,}|\bOMG\b|\bYay!|\byasss?\b)`),
}

// literal is one Go string literal extracted from a source file: its cleaned
// value plus the repo-relative path and 1-indexed line it came from.
type literal struct {
	path  string
	line  int
	value string
}

// CheckDiagnosticLanguage extracts every string literal from the agent prompt
// packages and reports any that carries a diagnostic / labeling phrase (S-8).
// Comments are excluded (the AST drops them) and regex-shaped literals are
// skipped, so the scan sees prose the agent actually emits — not the
// blocklist definitions those same files carry. If the agent tree is absent
// (validate run outside the repo) it returns no findings.
func CheckDiagnosticLanguage(root string) ([]Finding, error) {
	lits, err := agentStringLiterals(root)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, lit := range lits {
		if looksLikeRegex(lit.value) {
			continue
		}
		for _, re := range diagnosticPatterns {
			if re.MatchString(lit.value) {
				findings = append(findings, Finding{
					Check:    CheckDiagnostic,
					Severity: SeverityError,
					Path:     lit.path,
					Line:     lit.line,
					Rule:     "diagnostic-phrase",
					Message:  "agent prompt carries a diagnostic / labeling phrase",
				})
				break
			}
		}
	}
	return findings, nil
}

// CheckSanctuaryTree enforces the bidirectional sanctuary boundary (P3) two
// ways. First, structurally: the supplied denylist must name the three runtime
// subtrees no agent slice may include (engine/, observations/, registries/) —
// a missing one is a finding. Second, by scan: no agent prompt string literal
// may reference a path into those subtrees. Together they assert the
// agent-reachable surface has no route into the Engine, observations, or
// registries.
func CheckSanctuaryTree(root string, denylist []string) ([]Finding, error) {
	var findings []Finding
	for _, want := range []string{"engine/", "observations/", "registries/"} {
		if !containsString(denylist, want) {
			findings = append(findings, Finding{
				Check:    CheckSanctuary,
				Severity: SeverityError,
				Path:     "router.SanctuaryDenylist",
				Rule:     "denylist-coverage",
				Message:  "sanctuary denylist does not cover " + want,
			})
		}
	}

	lits, err := agentStringLiterals(root)
	if err != nil {
		return nil, err
	}
	for _, lit := range lits {
		if looksLikeRegex(lit.value) {
			continue
		}
		for _, deny := range []string{"engine/", "observations/", "registries/"} {
			if strings.Contains(lit.value, deny) {
				findings = append(findings, Finding{
					Check:    CheckSanctuary,
					Severity: SeverityError,
					Path:     lit.path,
					Line:     lit.line,
					Rule:     "sanctuary-path",
					Message:  "agent prompt references the sanctuary subtree " + deny,
				})
				break
			}
		}
	}
	return findings, nil
}

// agentStringLiterals parses every non-test .go file under <root>/internal/
// agents and returns the string literals it holds. An absent agent tree yields
// no literals (and no error) so validate degrades cleanly when run outside a
// checkout.
func agentStringLiterals(root string) ([]literal, error) {
	dir := filepath.Join(root, filepath.FromSlash(agentsRel))
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var lits []literal
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		fileLits, ferr := fileStringLiterals(root, p)
		if ferr != nil {
			return ferr
		}
		lits = append(lits, fileLits...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lits, nil
}

// fileStringLiterals parses one Go file and returns its string literals with
// repo-relative paths and 1-indexed line numbers. Parsing is syntax-only (no
// type checking), so it does not need the file's imports resolved.
func fileStringLiterals(root, path string) ([]literal, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	rel, err := relSlash(root, path)
	if err != nil {
		return nil, err
	}

	var lits []literal
	ast.Inspect(file, func(n ast.Node) bool {
		bl, ok := n.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return true
		}
		val, uerr := strconv.Unquote(bl.Value)
		if uerr != nil {
			return true // an unparseable literal is not our concern here
		}
		lits = append(lits, literal{
			path:  rel,
			line:  fset.Position(bl.Pos()).Line,
			value: val,
		})
		return true
	})
	return lits, nil
}

// looksLikeRegex reports whether a string literal is a compiled-pattern
// definition rather than prose. Regex metacharacters that never appear in a
// natural-language prompt (an anchor class, a word-boundary, an inline flag)
// mark the diagnostic-vocabulary and phrase-blocklist patterns those same
// agent files carry, so the prompt scans skip them and see only real prose.
func looksLikeRegex(s string) bool {
	for _, marker := range []string{`(?i)`, `\b`, `(?:`, `[a-z`, `[0-9`, `\w`, `\s`, `\d`, `^[`} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// containsString reports whether xs contains x.
func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
