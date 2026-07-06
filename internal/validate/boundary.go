package validate

import "regexp"

// boundaryRule is one forbidden repo pattern: a private-integration identity
// or internal reference that must never appear in this public repo
// (product-principles.md §10; the external-repo privacy invariant). The
// patterns are written in character-class form (z[a]i, not the literal) so
// this source file — which the sweep also scans everywhere except its own
// package — never trips its own rule; the character class matches the identity
// at runtime while the file's bytes never spell it out.
type boundaryRule struct {
	rule string
	re   *regexp.Regexp
	msg  string
}

// boundaryRules is the S-7 pattern set. It is deliberately narrow: the tokens
// here (the private agent identity, an internal todo id) do not occur in
// ordinary English or Go, so a whole-repo scan stays free of false positives
// while catching a real leak. The sanctioned harness/dependency names
// (OpenClaw, Hermes, hush, go-flywheel, go-foundation) are named openly in the
// ADRs and are not forbidden — only the private integration layer is.
//
//nolint:gochecknoglobals // fixed, read-only compiled boundary rules (product-principles.md §10)
var boundaryRules = []boundaryRule{
	{
		rule: "internal-identity",
		re:   regexp.MustCompile(`(?i)\bz[a]i\b`),
		msg:  "private-integration identity leaked into the public repo",
	},
	{
		rule: "internal-todo-id",
		re:   regexp.MustCompile(`\bT-[0-9]{3,}\b`),
		msg:  "internal todo id leaked into the public repo",
	},
}

// CheckPublicBoundaryTree scans every text file under root for the S-7
// forbidden patterns and returns one error-severity finding per hit. The
// validate package's own tree is skipped by the walker ([skipDirs]) so its
// pattern definitions and detection fixtures do not self-report.
func CheckPublicBoundaryTree(root string) ([]Finding, error) {
	var findings []Finding
	err := walkTextFiles(root, func(rel string, content []byte) error {
		for i, line := range splitLines(content) {
			for _, br := range boundaryRules {
				if br.re.MatchString(line) {
					findings = append(findings, Finding{
						Check:    CheckPublicBoundary,
						Severity: SeverityError,
						Path:     rel,
						Line:     i + 1,
						Rule:     br.rule,
						Message:  br.msg,
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
