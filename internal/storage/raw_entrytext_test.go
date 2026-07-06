package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRawDocument_EntryText covers the heading-stripping helper Structuring
// reads through: an empty capture (heading only) yields "", a normal entry
// yields the prose without the "# Entry" heading, and a body missing the
// expected heading is returned as-is (defensive).
func TestRawDocument_EntryText(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"heading only (empty capture)": {"# Entry", ""},
		"heading with content":         {"# Entry\n\nQuiet day. Read for an hour.", "Quiet day. Read for an hour."},
		"heading with padded content":  {"# Entry\n\n  Dinner with M.  ", "Dinner with M."},
		"no heading (defensive)":       {"just prose, no heading", "just prose, no heading"},
		"empty body":                   {"", ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, RawDocument{Body: tc.body}.EntryText())
		})
	}
}
