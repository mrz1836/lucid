package intake

import "strings"

// bundleAuthorshipFloor is the minimum fraction of a bundle's tokens that
// must be user-authored (or invisible connective tissue) for the bundle to
// be accepted (agent-contracts.md §1; product-principles.md §6 "Bundling
// rules"). A bundle below this line has been editorialized — Intake
// invented words the user did not say — and is rejected, retried stricter,
// then downgraded (error-states.md §I-6 → §I-2).
const bundleAuthorshipFloor = 0.90

// connectiveTokens is the tiny allowlist of joining words Intake may add
// without them counting against authorship: paragraph glue and the "Q"/"A"
// question/answer prefixes shown in the acceptable example
// (product-principles.md §6). The list is intentionally short — its job is
// to let invisible connective tissue through, not to launder invented
// vocabulary.
var connectiveTokens = map[string]bool{ //nolint:gochecknoglobals // a fixed, read-only connective allowlist
	"q":         true,
	"a":         true,
	"and":       true,
	"then":      true,
	"after":     true,
	"afterward": true,
}

// BundleAuthorship returns the fraction of the bundle's tokens that are
// user-authored: present in the opening or any answer, in a question Intake
// asked (framing the user consented to by answering), or in the small
// connective allowlist. A bundle that merely reflows the user's words
// scores ~1.0; one that rewrites them in Intake's own vocabulary drops
// below the floor. An empty bundle scores 0 (nothing user-authored to
// speak of). The check is deterministic — no LLM decides authorship.
func BundleAuthorship(bundle, opening string, questions, answers []string) float64 {
	authored := map[string]bool{}
	addTokens(authored, opening)
	for _, a := range answers {
		addTokens(authored, a)
	}
	// Questions are Intake's framing, not the user's words, but the user
	// answered them, so their tokens are permitted connective context.
	framing := map[string]bool{}
	for _, q := range questions {
		addTokens(framing, q)
	}

	toks := tokenize(bundle)
	if len(toks) == 0 {
		return 0
	}
	var ok int
	for _, t := range toks {
		if authored[t] || framing[t] || connectiveTokens[t] {
			ok++
		}
	}
	return float64(ok) / float64(len(toks))
}

// bundleIsUserAuthored reports whether a bundle clears the ≥90%
// user-authored floor (agent-contracts.md §1 validation rules).
func bundleIsUserAuthored(bundle, opening string, questions, answers []string) bool {
	return BundleAuthorship(bundle, opening, questions, answers) >= bundleAuthorshipFloor
}

// addTokens folds every token of s into set.
func addTokens(set map[string]bool, s string) {
	for _, t := range tokenize(s) {
		set[t] = true
	}
}

// tokenize lowercases s and splits it into word tokens, dropping anything
// that is not a letter or digit (punctuation, quotes, dashes, and the
// ellipsis used as connective tissue all fall away). It is the single,
// deterministic tokenizer the authorship check relies on so the linter's
// notion of a "token" is stable and testable.
func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !isWordRune(r)
	})
	return fields
}

// isWordRune reports whether r is a token-forming character: a lowercase
// ASCII letter, a digit, or any non-ASCII rune (accented letters). It is
// called only on already-lowercased input from [tokenize], so it does not
// special-case uppercase.
func isWordRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r > 127:
		// Keep non-ASCII letters (accented characters) as word content.
		return true
	default:
		return false
	}
}
