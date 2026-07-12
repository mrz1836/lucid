package storage

import (
	"fmt"
	"regexp"
	"strings"
)

// sourceTokenRE is the grammar for a harness source token: a non-empty
// lowercase token that starts alphanumeric and then allows the separators
// the observation source vocabulary already uses — `.`, `-`, `_`, and the
// `:` of the `enricher:<name>` form (observations.md §2). It is deliberately
// permissive: there is no allowlist, so a new harness needs only a passed
// token, not a code change. Membership stays the caller's concern; this is
// purely about a well-formed, normalizable token.
var sourceTokenRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]*$`)

// NormalizeSource trims, lowercases, and validates a harness source token,
// returning the normalized token or a clear error. It is the single grammar
// for a source/harness token across the raw/log path and the observation
// provenance path — neither invents a parallel rule (per the data-model and
// observations docs). Empty-after-trim or a charset violation is rejected
// honestly with a descriptive error; a malformed token is never silently
// coerced to a default. The function is pure and idempotent: feeding its
// own output back in returns that output unchanged.
func NormalizeSource(raw string) (string, error) {
	token := strings.ToLower(strings.TrimSpace(raw))
	if token == "" {
		return "", fmt.Errorf("storage: invalid source token %q: must not be empty", raw)
	}
	if !sourceTokenRE.MatchString(token) {
		return "", fmt.Errorf("storage: invalid source token %q: must start alphanumeric and contain only [a-z0-9._:-]", raw)
	}
	return token, nil
}
