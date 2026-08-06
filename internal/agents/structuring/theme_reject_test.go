package structuring_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mrz1836/lucid/internal/agents/structuring"
	"github.com/mrz1836/lucid/internal/provider"
)

// TestExtract_ThemeMissingFieldsRejected covers the theme-validation rule: a
// theme missing its name or its rationale makes the whole extraction unusable,
// so a reply carrying one on both attempts degrades rather than persisting a
// half-named theme.
func TestExtract_ThemeMissingFieldsRejected(t *testing.T) {
	cases := map[string]string{
		"theme missing rationale": `{"emotions":[],"themes":[{"name":"voice-not-heard","rationale":"  "}],"people":[],"notes":"x"}`,
		"theme missing name":      `{"emotions":[],"themes":[{"name":"","rationale":"pushed back then dropped it"}],"people":[],"notes":"x"}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			p := &provider.Fake{Script: []provider.Exchange{reply(payload), reply(payload)}}
			res := structuring.Extract(context.Background(), input("real content"), p)
			assert.True(t, res.Degraded, "%s should be rejected and degrade", name)
			assert.Equal(t, structuring.NotesStructuringFailed, res.Notes)
		})
	}
}
