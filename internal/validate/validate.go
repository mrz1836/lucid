// Package validate implements `lucid validate`: a read-only, deterministic
// sweep that makes the architecture's verification gates (architecture.md §6)
// checkable from one command. It carries five checks, none of which mutates
// anything:
//
//   - public-boundary (S-7): the repo never leaks a private-integration
//     identity or an internal todo id (product-principles.md §10; the external
//     -repo privacy invariant).
//   - diagnostic-language (S-8): shipped agent prompts carry no diagnostic /
//     labeling phrase (product-principles.md §6/§10).
//   - sanctuary: the agent-reachable surface names no path into the Engine,
//     observations, or registries subtrees (product-principles.md P3;
//     architecture.md §6 context-slice gate).
//   - schema: every on-disk Ledger record parses against its validator
//     (data-model.md); a corrupt record is reported, never rewritten.
//   - links: relative Markdown links across the doc set resolve (a warning-
//     level hygiene check, never a gate).
//
// The grep-style checks (public-boundary, diagnostic, sanctuary, links) run
// over a repo root; the schema check runs over a Ledger home through the
// storage adapter. Either half may be absent — `lucid validate` runs whatever
// is present and says what it skipped.
package validate

import "sort"

// Severity ranks a finding. An error fails the sweep (a real gate breach); a
// warning is surfaced but never fails it (hygiene the build can carry).
type Severity string

// The two severities. Only [SeverityError] fails [Report.OK].
const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
)

// Check names — stable strings a script (or a test) can branch on. They are
// the `check` field of every finding and the entries of [Report.Ran].
const (
	CheckPublicBoundary = "public-boundary"
	CheckDiagnostic     = "diagnostic-language"
	CheckSanctuary      = "sanctuary"
	CheckSchema         = "schema"
	CheckLinks          = "links"
)

// Finding is one issue the sweep surfaced. Path is repo-relative (grep checks)
// or Ledger-relative (schema check); Line is 1-indexed and 0 when a finding is
// not line-anchored. Rule names the specific pattern or sub-rule that fired.
type Finding struct {
	Check    string   `json:"check"`
	Severity Severity `json:"severity"`
	Path     string   `json:"path"`
	Line     int      `json:"line,omitempty"`
	Rule     string   `json:"rule,omitempty"`
	Message  string   `json:"message"`
}

// Report is the whole sweep's outcome: every finding plus the set of checks
// that actually ran (so the reader can tell "clean" from "not run").
type Report struct {
	Findings []Finding `json:"findings"`
	Ran      []string  `json:"ran"`
}

// OK reports whether the sweep passed: no error-severity finding. Warnings do
// not fail it — the build carries them.
func (r Report) OK() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return false
		}
	}
	return true
}

// Errors returns the count of error-severity findings.
func (r Report) Errors() int { return r.count(SeverityError) }

// Warnings returns the count of warning-severity findings.
func (r Report) Warnings() int { return r.count(SeverityWarn) }

// count tallies findings of one severity.
func (r Report) count(s Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == s {
			n++
		}
	}
	return n
}

// Options selects what the sweep runs against. RepoRoot enables the grep
// checks (public-boundary, diagnostic, sanctuary, links); an empty RepoRoot
// skips them. Ledger enables the schema check; a nil Ledger skips it.
// Denylist is the sanctuary subtree list the router enforces
// (router.SanctuaryDenylist), passed in so validate stays decoupled from the
// router package.
type Options struct {
	RepoRoot string
	Ledger   LedgerSource
	Denylist []string
}

// LedgerSource is the read-only slice of the storage adapter the schema check
// needs. Taking an interface (rather than *storage.Adapter) keeps validate
// dependency-light and lets tests drive the schema check with a fake.
type LedgerSource interface {
	Home() string
	ListProcessedIDs() ([]string, error)
	ReadProcessedErr(id string) error
	ListInsightIDs() ([]string, error)
	ReadInsightErr(id string) error
	ListReflectionIDs() ([]string, error)
	ReadReflectionErr(id string) error
	ListPeopleKeys() ([]string, error)
	ReadPersonErr(key string) error
	LoadConfigErr() error
}

// Run executes every enabled check and returns the merged report. It performs
// no writes and returns an error only for an internal failure (e.g. an
// unreadable repo tree), never for a finding — a breached gate is a finding
// with [SeverityError], surfaced in the report so a caller can print it before
// deciding the exit code.
func Run(opts Options) (Report, error) {
	var rep Report

	if opts.RepoRoot != "" {
		for _, c := range []struct {
			name string
			fn   func(string) ([]Finding, error)
		}{
			{CheckPublicBoundary, CheckPublicBoundaryTree},
			{CheckDiagnostic, CheckDiagnosticLanguage},
			{CheckLinks, CheckDocLinks},
		} {
			found, err := c.fn(opts.RepoRoot)
			if err != nil {
				return Report{}, err
			}
			rep.Ran = append(rep.Ran, c.name)
			rep.Findings = append(rep.Findings, found...)
		}

		found, err := CheckSanctuaryTree(opts.RepoRoot, opts.Denylist)
		if err != nil {
			return Report{}, err
		}
		rep.Ran = append(rep.Ran, CheckSanctuary)
		rep.Findings = append(rep.Findings, found...)
	}

	if opts.Ledger != nil {
		found, err := CheckLedgerSchema(opts.Ledger)
		if err != nil {
			return Report{}, err
		}
		rep.Ran = append(rep.Ran, CheckSchema)
		rep.Findings = append(rep.Findings, found...)
	}

	sortFindings(rep.Findings)
	return rep, nil
}

// sortFindings orders findings deterministically (check, path, line, rule) so
// the human and JSON output — and the tests — are stable across runs.
func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		switch {
		case a.Check != b.Check:
			return a.Check < b.Check
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.Line != b.Line:
			return a.Line < b.Line
		default:
			return a.Rule < b.Rule
		}
	})
}
