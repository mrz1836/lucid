package validate

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/mrz1836/lucid/internal/identity"
)

// The identity sub-rules CheckPeopleIdentity reports, each a stable string a
// script (or a test) can branch on. They mirror the ways the identity log can
// disagree with the people tree (data-model.md §"Identity log"; ADR-0016): a
// claim on a person with no record, a claim still recorded on a merged-away
// tombstone, a second claim on a form someone else already claims, and a
// dormant status left on a tombstone.
const (
	ruleIdentityClaimDangling   = "identity-claim-dangling"
	ruleIdentityClaimTombstone  = "identity-claim-tombstone"
	ruleIdentityClaimDuplicate  = "identity-claim-duplicate"
	ruleIdentityStatusTombstone = "identity-status-tombstone"
)

// identityLogPath is the Ledger-relative identity log every finding anchors
// at, suffixed with the offending event's id.
const identityLogPath = "identity/identity.jsonl"

// CheckPeopleIdentityLog verifies the identity log agrees with the people tree,
// read-only (the [CheckPeopleIdentity] gate). It folds the log through the
// tree's merge redirects, as every reader does, and reports one error-severity
// finding per offending event:
//
//   - identity-claim-dangling — an effective claim names a person_key with no
//     record;
//   - identity-claim-tombstone — an effective claim is still recorded on a
//     redirect tombstone (a merge re-points claims, so only a hand edit or an
//     older binary leaves one);
//   - identity-claim-duplicate — a claim was written while another person
//     claimed the same normalized form, so it is inert;
//   - identity-status-tombstone — a tombstone's folded status is dormant
//     (a merge clears a dormant source).
//
// ran reports whether the check applied at all: a Ledger without the log has
// nothing to check, so the check adds neither a finding nor an entry to the
// ran list, and an untouched Ledger validates exactly as before. A malformed
// log line is the schema check's finding, never this one's. The log and the
// tree are never rewritten.
func CheckPeopleIdentityLog(src LedgerSource) (findings []Finding, ran bool, err error) {
	events, err := src.IdentityEvents()
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	keys, err := src.ListPeopleKeys()
	if err != nil {
		return nil, false, err
	}

	exists := make(map[string]bool, len(keys))
	redirect := make(map[string]string, len(keys))
	for _, k := range keys {
		exists[k] = true
		to, rerr := src.PersonRedirect(k)
		if rerr != nil {
			// An unreadable record is the schema check's finding; read it here as
			// a canonical record so one corrupt file is not reported twice.
			continue
		}
		if to != "" {
			redirect[k] = to
		}
	}
	st := identity.Fold(events, func(k string) string {
		if to, ok := redirect[k]; ok {
			return to
		}
		return k
	})
	written := make(map[string]identity.Event, len(events))
	for _, e := range events {
		written[e.ID] = e
	}

	for _, c := range st.Claims() {
		key := written[c.EventID].PersonKey // as recorded, before redirects
		switch {
		case !exists[key]:
			findings = append(findings, identityFinding(c.EventID, ruleIdentityClaimDangling,
				fmt.Sprintf("the claim on %q names person %q, which has no record", c.Form, key)))
		case redirect[key] != "":
			findings = append(findings, identityFinding(c.EventID, ruleIdentityClaimTombstone,
				fmt.Sprintf("the claim on %q names person %q, a redirect tombstone (merged into %q); claim the form for %q instead",
					c.Form, key, redirect[key], redirect[key])))
		}
	}
	for _, e := range st.Duplicates() {
		findings = append(findings, identityFinding(e.ID, ruleIdentityClaimDuplicate,
			fmt.Sprintf("the claim on %q for person %q was written while another person claimed the form, so it is inert",
				e.Form, e.PersonKey)))
	}
	for _, key := range st.StatusKeys() {
		to := redirect[key]
		if to == "" || !st.IsDormant(key) {
			continue
		}
		fact, _ := st.StatusEvent(key)
		findings = append(findings, identityFinding(fact.EventID, ruleIdentityStatusTombstone,
			fmt.Sprintf("person %q is a redirect tombstone (merged into %q) but its status is dormant", key, to)))
	}
	return findings, true, nil
}

// identityFinding builds one error-severity identity finding anchored at the
// offending event in the identity log.
func identityFinding(eventID, rule, msg string) Finding {
	return Finding{
		Check:    CheckPeopleIdentity,
		Severity: SeverityError,
		Path:     identityLogPath + "#" + eventID,
		Rule:     rule,
		Message:  msg,
	}
}
