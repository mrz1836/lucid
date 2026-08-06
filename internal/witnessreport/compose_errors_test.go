package witnessreport

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
)

// TestCompose_BuildReportErrorIsLoud: a failing numbers reader aborts the compose
// before any prompt is read or model is built — the deterministic scaffold is the
// report's spine, so its failure is a loud error, never a half-built send.
func TestCompose_BuildReportErrorIsLoud(t *testing.T) {
	c := New(Deps{
		Provider: config.ProviderConfig{Backend: "fake"},
		Numbers:  fakeNumbers{err: errors.New("metrics unreadable")},
		Records:  fakeRecords{},
		Build:    func(config.ProviderConfig) (provider.Provider, error) { return &provider.Fake{}, nil },
	})

	_, err := c.Compose(context.Background(), reportNow())
	require.Error(t, err, "a failing numbers reader is loud, not a silent empty report")
}

// TestCompose_BuildProviderErrorIsLoud: a backend that will not build is a loud
// configuration error after the prompt files are read — the compose never
// proceeds to an empty send.
func TestCompose_BuildProviderErrorIsLoud(t *testing.T) {
	dir := t.TempDir()
	numbers, records := fixtureReaders(t, "strong-week.json")
	c := New(Deps{
		SystemPrompt: writeFile(t, dir, "system.md", "voice"),
		Template:     writeFile(t, dir, "template.md", "tmpl"),
		Provider:     config.ProviderConfig{Backend: "fake"},
		Numbers:      numbers,
		Records:      records,
		Build:        func(config.ProviderConfig) (provider.Provider, error) { return nil, errors.New("bad backend") },
	})

	_, err := c.Compose(context.Background(), reportNow())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build provider")
}

// TestCompose_ProviderErrorIsLoud: a non-timeout, non-unavailable provider error
// is a loud failure (not the graceful fallback the outage errors take) — the
// caller never delivers a silently-empty report on an unexpected backend fault.
func TestCompose_ProviderErrorIsLoud(t *testing.T) {
	c, _ := composerDeps(t, "miss-heavy-week.json", "", provider.Exchange{Err: errors.New("400 bad request")})

	_, err := c.Compose(context.Background(), reportNow())
	require.Error(t, err, "an unexpected provider error is loud, not a fallback")
	assert.Contains(t, err.Error(), "compose narrative")
}

// TestReportDigest_NoDecidedDayReadsBuilding: a report whose 30-day window has no
// decided day yet renders the "building" line rather than a misleading 0%
// adherence — the cold-start grounding the model reads.
func TestReportDigest_NoDecidedDayReadsBuilding(t *testing.T) {
	got := reportDigest(Report{ISOWeek: "2026-W29", Streak: 3, LongestStreak: 5})
	assert.Contains(t, got, "30-day adherence: building — no decided day yet")
	assert.NotContains(t, got, "% (", "a building window shows no adherence percentage")
}
