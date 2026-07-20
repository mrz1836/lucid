package observations

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// FuzzParseDate asserts the civil-date parser is robust: for any input it never
// panics, and on success the value is in the requested location and its
// canonical rendering round-trips through the parser. Seeds cover the normal
// case plus the boundary, invalid-calendar, extra-text, and empty edges.
func FuzzParseDate(f *testing.F) {
	for _, s := range []string{
		"2026-07-20", "0001-01-01", "9999-12-31",
		"2026-02-29", "2026-13-45", "2026-07-20T10:00",
		"  2026-07-20  ", "", "garbage",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := ParseDate(s, time.UTC)
		if err != nil {
			return
		}
		require.Equal(t, time.UTC, got.Location())
		reparsed, err := ParseDate(DateString(got), time.UTC)
		require.NoError(t, err, "the canonical rendering must re-parse")
		require.True(t, got.Equal(reparsed), "canonical date must round-trip")
	})
}
