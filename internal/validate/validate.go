// Package validate implements `lucid validate`: a read-only, deterministic
// sweep that makes the architecture's verification gates (architecture.md §6)
// checkable from one command. None of its checks mutates anything:
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
//   - people-redirects: the people tree's redirect tombstones form a
//     single-hop, acyclic graph.
//   - people-identity: the identity log agrees with the people tree — no claim
//     on a missing person or a tombstone, no second claim on a claimed form, no
//     dormant status on a tombstone. It runs only when the log exists.
//   - anchors: anchors.json and the anchor event store hold well-formed
//     records whose joins are whole — every event targets a recorded anchor,
//     every void target is legal, and no two-file write is half-written
//     (engine-module.md §"Anchor event store").
//   - links: relative Markdown links across the doc set resolve (a warning-
//     level hygiene check, never a gate).
//
// The grep-style checks (public-boundary, diagnostic, sanctuary, links) run
// over a repo root; the Ledger checks (schema, people-redirects, anchors) run
// over a Ledger home through the storage adapter (people-identity only when the
// identity log exists). Either half may be absent — `lucid validate` runs whatever
// is present and says what it skipped.
package validate

import (
	"cmp"
	"slices"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/identity"
)

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
	CheckPublicBoundary  = "public-boundary"
	CheckDiagnostic      = "diagnostic-language"
	CheckSanctuary       = "sanctuary"
	CheckSchema          = "schema"
	CheckLinks           = "links"
	CheckPeopleRedirects = "people-redirects"
	CheckPeopleIdentity  = "people-identity"
	CheckAnchors         = "anchors"
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
// skips them. Ledger enables the Ledger checks (schema, people-redirects,
// anchors); a nil Ledger skips them.
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
	// ListLinkEventIDs enumerates the link-ledger event locators in append
	// order (the parsed id, or a positional line-<n> for a malformed line), and
	// ReadLinkEventErr re-parses the one line a locator names and returns only
	// its error. Together they let the schema sweep walk the single
	// links/links.jsonl log the way it walks the per-file record families.
	ListLinkEventIDs() ([]string, error)
	ReadLinkEventErr(id string) error
	// ListSecretRefIDs enumerates the secret-reference event locators in
	// append order, and ReadSecretRefErr validates the one line a locator
	// names. The schema sweep sees catalog structure only, never secret
	// material.
	ListSecretRefIDs() ([]string, error)
	ReadSecretRefErr(id string) error
	// ListIdentityEventIDs enumerates the identity-log event locators in
	// append order (the parsed id, or a positional line-<n> for a malformed
	// line), and ReadIdentityEventErr re-parses the one line a locator names
	// and returns only its error, so the schema sweep reports a malformed
	// identity line. A missing log yields no locators.
	ListIdentityEventIDs() ([]string, error)
	ReadIdentityEventErr(id string) error
	// IdentityEvents returns the identity log's well-formed events in append
	// order; a missing log is an error wrapping fs.ErrNotExist (the
	// people-identity check then does not run). It backs [CheckPeopleIdentity].
	IdentityEvents() ([]identity.Event, error)
	// PersonRedirect returns the redirect_to of a person record — the empty
	// string for a canonical record or one that cannot be read (the schema
	// check reports an unreadable record; the redirect check does not
	// double-report it). It backs [CheckPeopleRedirects].
	PersonRedirect(key string) (string, error)
	LoadConfigErr() error
	// ReadAnchorsErr returns anchors.json's decoded log with its read error,
	// and ReadAnchorEventsErr the anchor event store's. Unlike the per-record
	// Read*Err ops they return the content as well as the error, because the
	// anchors check reads both files whole to join them; a read or parse
	// failure is still only a finding. A missing anchors.json is an error
	// wrapping fs.ErrNotExist (the check reads it as nothing recorded); a
	// missing event store is the empty log, as storage reads it. Both back
	// [CheckAnchors].
	ReadAnchorsErr() (engine.AnchorLog, error)
	ReadAnchorEventsErr() (engine.AnchorEventLog, error)
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
		if err := runLedgerChecks(&rep, opts.Ledger); err != nil {
			return Report{}, err
		}
	}

	sortFindings(rep.Findings)
	return rep, nil
}

// runLedgerChecks runs the Ledger half of the sweep — schema, people-redirects,
// people-identity (only when the identity log exists, so a Ledger that never
// wrote an identity fact validates exactly as it always has), and anchors —
// appending each check that ran and its findings to rep.
func runLedgerChecks(rep *Report, src LedgerSource) error {
	found, err := CheckLedgerSchema(src)
	if err != nil {
		return err
	}
	rep.Ran = append(rep.Ran, CheckSchema)
	rep.Findings = append(rep.Findings, found...)

	redirects, err := CheckPeopleRedirectGraph(src)
	if err != nil {
		return err
	}
	rep.Ran = append(rep.Ran, CheckPeopleRedirects)
	rep.Findings = append(rep.Findings, redirects...)

	identityFindings, ran, err := CheckPeopleIdentityLog(src)
	if err != nil {
		return err
	}
	if ran {
		rep.Ran = append(rep.Ran, CheckPeopleIdentity)
		rep.Findings = append(rep.Findings, identityFindings...)
	}

	rep.Ran = append(rep.Ran, CheckAnchors)
	rep.Findings = append(rep.Findings, CheckAnchorFiles(src)...)
	return nil
}

// sortFindings orders findings deterministically (check, path, line, rule) so
// the human and JSON output — and the tests — are stable across runs.
func sortFindings(fs []Finding) {
	slices.SortStableFunc(fs, func(a, b Finding) int {
		return cmp.Or(
			cmp.Compare(a.Check, b.Check),
			cmp.Compare(a.Path, b.Path),
			cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Rule, b.Rule),
		)
	})
}
