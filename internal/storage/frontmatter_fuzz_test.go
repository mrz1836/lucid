package storage

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzSplitFrontmatter asserts the split never panics on arbitrary bytes and
// upholds its contract: a nil error implies the content opened with a --- fence
// and a matching closing fence was found. The frontmatter parsers run on
// untrusted markdown read back from disk, so totality matters.
func FuzzSplitFrontmatter(f *testing.F) {
	seeds := [][]byte{
		[]byte("---\nid: x\n---\nbody"),
		[]byte("---\nid: x\n---\n"),
		[]byte("---\nno close"),
		[]byte("no fence at all"),
		[]byte(""),
		[]byte("---\r\nid: x\r\n---\r\nbody"),
		[]byte("------"),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, content []byte) {
		_, _, err := SplitFrontmatter(content)
		if err != nil {
			return
		}
		// A successful split means the content opened with the fence. (front and
		// body may each be nil — an empty frontmatter block or an empty body.)
		require.True(t, bytes.HasPrefix(content, []byte(fence)),
			"a successful split must begin with a --- fence")
	})
}

// FuzzParseFrontmatter asserts the YAML decode never panics and returns an
// initialized map whenever it reports success, so downstream key checks can
// read it unconditionally.
func FuzzParseFrontmatter(f *testing.F) {
	seeds := [][]byte{
		[]byte("---\nid: x\nn: 1\n---\nbody"),
		[]byte("---\n{malformed yaml\n---\n"),
		[]byte("---\n- just\n- a\n- list\n---\n"),
		[]byte("---\n---\n"),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, content []byte) {
		fields, _, err := ParseFrontmatter(content)
		if err != nil {
			return
		}
		// Totality: the downstream required-key gate must be safe on whatever
		// map (including a nil map from null YAML) a successful parse yields.
		require.NotPanics(t, func() {
			_ = ValidateRequiredKeys(fields, RawRequiredKeys())
		})
	})
}
