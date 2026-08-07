package validate

import "fmt"

// The redirect sub-rules CheckPeopleRedirects reports, each a stable string a
// script (or a test) can branch on. They mirror the four ways a people tree's
// tombstone graph can be malformed (data-model.md §"Merge & redirect"): a
// tombstone that points nowhere, one that points at another tombstone (a chain,
// which the single-hop invariant forbids), one that points at itself, and a
// longer loop.
const (
	ruleRedirectTargetMissing = "redirect-target-missing"
	ruleRedirectChain         = "redirect-chain"
	ruleRedirectSelf          = "redirect-self"
	ruleRedirectCycle         = "redirect-cycle"
)

// CheckPeopleRedirectGraph verifies the people tree's redirect-tombstone graph
// is well-formed, read-only (the [CheckPeopleRedirects] gate). The merge/alias
// verbs maintain a single-hop, acyclic redirect model — a tombstone points
// straight at a canonical record and never at another tombstone (data-model.md
// §"Merge & redirect") — and this check is the architecture gate that keeps a
// hand-corrupted or half-written store from resolving a bare mention to the
// wrong person (or looping). Each malformed tombstone is one error-severity
// finding; the store is never rewritten. An unreadable record is left to the
// schema check, not double-reported here.
func CheckPeopleRedirectGraph(src LedgerSource) ([]Finding, error) {
	keys, err := src.ListPeopleKeys()
	if err != nil {
		return nil, err
	}

	exists := make(map[string]bool, len(keys))
	for _, k := range keys {
		exists[k] = true
	}
	redirect := make(map[string]string, len(keys))
	for _, k := range keys {
		to, rerr := src.PersonRedirect(k)
		if rerr != nil {
			// An unreadable record is the schema check's finding; skip it here so
			// one corrupt file is not reported twice.
			continue
		}
		if to != "" {
			redirect[k] = to
		}
	}

	var findings []Finding
	for _, k := range keys { // ListPeopleKeys is sorted → deterministic order
		to, ok := redirect[k]
		if !ok {
			continue
		}
		switch {
		case to == k:
			findings = append(findings, redirectFinding(k,
				ruleRedirectSelf, fmt.Sprintf("person %q redirects to itself", k)))
		case !exists[to]:
			findings = append(findings, redirectFinding(k,
				ruleRedirectTargetMissing, fmt.Sprintf("person %q redirects to %q, which has no record", k, to)))
		case redirectCycle(k, redirect):
			findings = append(findings, redirectFinding(k,
				ruleRedirectCycle, fmt.Sprintf("person %q is part of a redirect cycle", k)))
		case redirect[to] != "":
			findings = append(findings, redirectFinding(k,
				ruleRedirectChain, fmt.Sprintf("person %q redirects to %q, which is itself a tombstone (chains are not allowed)", k, to)))
		}
	}
	return findings, nil
}

// redirectCycle reports whether following redirects from start ever revisits a
// key — a loop the single-hop invariant forbids. A self-redirect is handled by
// its own rule before this runs, so a cycle here means a two-or-more-hop loop.
func redirectCycle(start string, redirect map[string]string) bool {
	visited := map[string]bool{start: true}
	cur := start
	for {
		to, ok := redirect[cur]
		if !ok {
			return false // reached a canonical record — no cycle
		}
		if visited[to] {
			return true
		}
		visited[to] = true
		cur = to
	}
}

// redirectFinding builds one error-severity redirect finding anchored at the
// offending record's path.
func redirectFinding(key, rule, msg string) Finding {
	return Finding{
		Check:    CheckPeopleRedirects,
		Severity: SeverityError,
		Path:     "people/" + key,
		Rule:     rule,
		Message:  msg,
	}
}
