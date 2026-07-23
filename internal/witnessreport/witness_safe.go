package witnessreport

import "regexp"

// This file owns the witness-safe output scan — the second layer of the report's
// two-layer privacy guarantee. The primary firewall is structural: this package
// wires no observations, journal, or raw-entry reader (Deps carries only the
// metrics/day-record seams), so private detail is unreachable by the model by
// construction. The scan below is defense-in-depth: it fails the composed prose
// closed if any private-detail marker slips through anyway, so a friend-facing
// surface never shows flagged text — it degrades to the deterministic
// metrics-only report instead.
//
// The list is deliberately NOT internal/agents/safety.MatchesBlocklist. That
// blocklist's whole external-action category (send / dm / email / post / notify)
// would false-trip a legitimate friend-ask like "post me a reminder" or "DM me
// midweek", and its overclaim category ("clearly", "!!") is about the teeth's
// tone, not privacy. What a witness report must never carry is a different set:
// a raw journal citation, or an elevated medical / private-relationship detail
// that belongs in the private user channel and not in front of the witness audience.

// journalCitationPatterns match the raw-entry / journal citation shapes the
// private reflection deep-dive emits — entry/observation ids, [[wikilinks]], and
// the on-disk raw/processed/~/.lucid paths. A witness report summarizes; it
// never cites or quotes the private journal, so any of these in the composed
// prose is a hard trip.
var journalCitationPatterns = []*regexp.Regexp{ //nolint:gochecknoglobals // a fixed, read-only compiled private-detail blocklist
	regexp.MustCompile(`(?i)\b(entry|obs|observation|journal|reflection|note)[_-]?\d`),
	regexp.MustCompile(`\[\[.+?\]\]`),
	regexp.MustCompile(`(?i)(^|[\s(])(raw|processed|insights|sessions)/`),
	regexp.MustCompile(`(?i)~?/?\.lucid\b`),
}

// medicalDetailPatterns match elevated clinical detail — a diagnosis, a
// prescription, a dosage, a lab or vital reading. A witness-safe summary may say
// "a rough health week" but never the underlying medical specifics, so any of
// these trips the scan.
var medicalDetailPatterns = []*regexp.Regexp{ //nolint:gochecknoglobals // a fixed, read-only compiled private-detail blocklist
	regexp.MustCompile(`(?i)\b(diagnos\w+|prescrib\w+|prescription|dosage|milligrams?)\b`),
	regexp.MustCompile(`(?i)\b\d+\s?(mg|mcg|ml)\b`),
	regexp.MustCompile(`(?i)\b(blood pressure|heart rate|lab (result|value)s?|cholesterol|a1c|bloodwork)\b`),
}

// relationshipDetailPatterns match named private-relationship detail — a partner
// or therapist referenced in the possessive, or a quoted private conversation.
// These belong in the private channel, never in front of the witness audience.
// The set is kept narrow (the possessive "my <role>") so a generic friend-ask
// like "ask me about my week" never trips it.
var relationshipDetailPatterns = []*regexp.Regexp{ //nolint:gochecknoglobals // a fixed, read-only compiled private-detail blocklist
	regexp.MustCompile(`(?i)\bmy (wife|husband|partner|girlfriend|boyfriend|ex|therapist|psychiatrist)\b`),
	regexp.MustCompile(`(?i)\b(couples|marriage) (counsel|therap)\w*`),
}

// witnessSafe reports whether every piece of composed, friend-facing prose is
// safe to show the witness channel — i.e. none carries a private-detail marker.
// It is the fail-closed gate the composer runs the model output through before
// accepting it: a false result discards the prose in favor of the deterministic
// metrics-only report. It is a pure function of its inputs (no IO, no model), so
// the guarantee is unit-testable against planted-term fixtures.
func witnessSafe(prose ...string) bool {
	for _, s := range prose {
		if s == "" {
			continue
		}
		if matchesAnyPrivate(journalCitationPatterns, s) ||
			matchesAnyPrivate(medicalDetailPatterns, s) ||
			matchesAnyPrivate(relationshipDetailPatterns, s) {
			return false
		}
	}
	return true
}

// matchesAnyPrivate reports whether s matches any pattern in the set.
func matchesAnyPrivate(patterns []*regexp.Regexp, s string) bool {
	for _, re := range patterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
