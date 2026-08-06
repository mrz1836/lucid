package cli

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmit covers the shared read-command tail directly: --json writes the
// machine payload (and skips the prose lines), while the default prints each
// line to stdout.
func TestEmit(t *testing.T) {
	newCmd := func(asJSON bool) (*cobra.Command, *bytes.Buffer) {
		cmd := &cobra.Command{}
		cmd.Flags().Bool(jsonFlag, asJSON, "")
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		return cmd, &buf
	}

	t.Run("json writes the payload and skips lines", func(t *testing.T) {
		cmd, buf := newCmd(true)
		require.NoError(t, emit(cmd, map[string]int{"n": 7}, []string{"ignored"}))
		assert.JSONEq(t, `{"n":7}`, buf.String())
		assert.NotContains(t, buf.String(), "ignored")
	})
	t.Run("prose prints each line", func(t *testing.T) {
		cmd, buf := newCmd(false)
		require.NoError(t, emit(cmd, map[string]int{"n": 7}, []string{"line one", "line two"}))
		assert.Equal(t, "line one\nline two\n", buf.String())
	})
}

// TestWriteJSON_IndentsWithTrailingNewline proves the shared JSON writer emits
// two-space-indented output terminated by a newline.
func TestWriteJSON_IndentsWithTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeJSON(&buf, map[string]any{"n": 7}))
	assert.Equal(t, "{\n  \"n\": 7\n}\n", buf.String())
}

// TestWriteJSON_EncodeErrorSurfaces proves an unmarshalable value (a channel has
// no JSON representation) surfaces the wrapped encode error rather than writing
// half a document.
func TestWriteJSON_EncodeErrorSurfaces(t *testing.T) {
	err := writeJSON(&bytes.Buffer{}, map[string]any{"bad": make(chan int)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encode json")
}
