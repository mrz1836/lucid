package safety

import "regexp"

// The phrase blocklist, compiled and categorized (product-principles.md §6;
// scripts/phrase_blocklist.regex is the documented source of truth). The
// categories drive the decision: an external-action verb or a clinical label
// is a hard block; a soft overclaim ("you always …", "clearly …") is a
// rewrite; coaching ("you should …") is an advice block. The union of the
// four categories is the whole blocklist — every line in the .regex file has
// a counterpart here.
//
// Patterns are compiled with the (?i) case-insensitive flag and \b word
// boundaries, matching the .regex file's grep -niE semantics. They are RE2
// (Go regexp), so they carry no backreferences.

// externalActionPatterns match an autonomous external-action verb. The MVP
// has no external-send path, so the only valid decision is a block with
// reason_code external_action_attempt — never a rewrite (agent-contracts.md
// §4; error-states.md §Sf-2).
var externalActionPatterns = []*regexp.Regexp{ //nolint:gochecknoglobals // fixed, read-only compiled blocklist (product-principles.md §6)
	regexp.MustCompile(`(?i)\b(send|dm|email|post|schedule|call|notify|webhook|publish|tweet)\b`),
	regexp.MustCompile(`(?i)\b(auto[- ]?send|send automatically)\b`),
}

// coachingPatterns match advice / recommendation phrasing. In the MVP no
// agent advises; a hit blocks with reason_code agent_self_attempt
// (error-states.md §Sf-8).
var coachingPatterns = []*regexp.Regexp{ //nolint:gochecknoglobals // fixed, read-only compiled blocklist (product-principles.md §6)
	regexp.MustCompile(`(?i)\byou should\b`),
	regexp.MustCompile(`(?i)\byou ought to\b`),
	regexp.MustCompile(`(?i)\bwhat you need to do is\b`),
}

// diagnosticLabelPatterns match clinical / attachment-label vocabulary — a
// diagnosis presented as fact. These cannot be softened; a hit is a hard
// block with reason_code diagnostic_language (error-states.md §Sf-3).
var diagnosticLabelPatterns = []*regexp.Regexp{ //nolint:gochecknoglobals // fixed, read-only compiled blocklist (product-principles.md §6)
	regexp.MustCompile(`(?i)\byou ('?re|have) (an? )?(anxious|avoidant|secure|disorganized)\b`),
	regexp.MustCompile(`(?i)\b(anxious|avoidant|secure|disorganized) (attach\w*|tendenc\w*|style|type|behavior)\b`),
	regexp.MustCompile(`(?i)\b(i (diagnos\w*|am diagnosing)|you'?re suffering from)\b`),
	regexp.MustCompile(`(?i)\b(attachment style|trauma response|narcissist|borderline)\b`),
}

// overclaimPatterns match soft certainty / flattening / performance —
// wording Safety can rewrite into hypothesis framing while preserving intent
// (agent-contracts.md §4; error-states.md §Sf-1). A hit is a rewrite with
// reason_code phrase_blocklist.
var overclaimPatterns = []*regexp.Regexp{ //nolint:gochecknoglobals // fixed, read-only compiled blocklist (product-principles.md §6)
	regexp.MustCompile(`(?i)\byou (always|never)\b`),
	regexp.MustCompile(`(?i)\b(clearly|obviously)\b`),
	regexp.MustCompile(`(?i)(!{2,}|\bOMG\b|\bYay!|\byasss?\b)`),
}

// matchesAny reports whether s matches any pattern in the set.
func matchesAny(patterns []*regexp.Regexp, s string) bool {
	for _, re := range patterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// MatchesBlocklist reports whether s hits any phrase-blocklist pattern, across
// all four categories. It is the deterministic union check used to confirm an
// agent prompt or a rewritten message carries no forbidden phrase.
func MatchesBlocklist(s string) bool {
	return matchesAny(externalActionPatterns, s) ||
		matchesAny(coachingPatterns, s) ||
		matchesAny(diagnosticLabelPatterns, s) ||
		matchesAny(overclaimPatterns, s)
}
