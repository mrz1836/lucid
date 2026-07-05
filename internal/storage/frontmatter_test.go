package storage

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validRaw = `---
id: raw_2026_05_06_07_15
recorded_at: 2026-05-06T07:15:02-04:00
occurred_at: 2026-05-06T07:15:02-04:00
occurred_at_precision: exact
source: discord
session_id: session_2026_05_06_07_15
command: /log
agent_versions:
  intake: null
bootstrap: false
---

# Entry

Quiet day. Read for an hour. Felt fine.
`

func TestSplitFrontmatter_OK(t *testing.T) {
	front, body, err := SplitFrontmatter([]byte(validRaw))
	require.NoError(t, err)
	assert.Contains(t, string(front), "id: raw_2026_05_06_07_15")
	assert.NotContains(t, string(front), "---")
	assert.Contains(t, string(body), "# Entry")
	assert.Contains(t, string(body), "Felt fine.")
}

func TestSplitFrontmatter_Errors(t *testing.T) {
	_, _, err := SplitFrontmatter([]byte("no fence here\njust text"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not start with")

	_, _, err = SplitFrontmatter([]byte("---\nid: x\nnever closed\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated")

	_, _, err = SplitFrontmatter(nil)
	assert.Error(t, err)
}

// TestSplitFrontmatter_EmptyBody covers a document whose closing fence
// is the final line (no trailing body).
func TestSplitFrontmatter_EmptyBody(t *testing.T) {
	front, body, err := SplitFrontmatter([]byte("---\nid: x\n---"))
	require.NoError(t, err)
	assert.Equal(t, "id: x", strings.TrimSpace(string(front)))
	assert.Empty(t, body)
}

func TestSplitFrontmatter_HandlesCRLF(t *testing.T) {
	_, _, err := SplitFrontmatter([]byte("---\r\nid: x\r\n---\r\nbody\r\n"))
	require.NoError(t, err)
}

func TestParseFrontmatter_OK(t *testing.T) {
	fields, body, err := ParseFrontmatter([]byte(validRaw))
	require.NoError(t, err)
	assert.Equal(t, "raw_2026_05_06_07_15", fields["id"])
	assert.Equal(t, "/log", fields["command"])
	assert.Equal(t, false, fields["bootstrap"])
	assert.Contains(t, fields, "agent_versions")
	assert.Contains(t, string(body), "# Entry")
}

func TestParseFrontmatter_BadYAML(t *testing.T) {
	_, _, err := ParseFrontmatter([]byte("---\nid: [unclosed\n---\nbody\n"))
	assert.Error(t, err)
}

func TestValidateRequiredKeys(t *testing.T) {
	fields := map[string]any{"a": 1, "b": nil}
	require.NoError(t, ValidateRequiredKeys(fields, []string{"a", "b"}))

	err := ValidateRequiredKeys(fields, []string{"a", "c"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"c"`)
}

// TestValidateRawFrontmatter_OK confirms a well-formed /log entry
// passes — including agent_versions present with a null child.
func TestValidateRawFrontmatter_OK(t *testing.T) {
	require.NoError(t, ValidateRawFrontmatter([]byte(validRaw)))
}

func TestValidateRawFrontmatter_MissingKey(t *testing.T) {
	missing := strings.Replace(validRaw, "bootstrap: false\n", "", 1)
	err := ValidateRawFrontmatter([]byte(missing))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bootstrap")
}

func TestValidateRawFrontmatter_NoFence(t *testing.T) {
	assert.Error(t, ValidateRawFrontmatter([]byte("plain text, no frontmatter")))
}

func TestValidateJSONRequiredKeys(t *testing.T) {
	obj := []byte(`{"id":"x","entry_id":"x","people":[]}`)
	require.NoError(t, ValidateJSONRequiredKeys(obj, []string{"id", "entry_id", "people"}))

	err := ValidateJSONRequiredKeys(obj, []string{"id", "produced_at"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "produced_at")

	assert.Error(t, ValidateJSONRequiredKeys([]byte("{bad json"), []string{"id"}))
}
