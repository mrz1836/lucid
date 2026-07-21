package workout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExampleProgramValidates guards that the synthetic program shipped for
// tests and docs is itself well-formed — a broken fixture would silently weaken
// every recommender test that leans on it.
func TestExampleProgramValidates(t *testing.T) {
	t.Parallel()
	require.NoError(t, ExampleProgram().Validate())
}

// TestProgramRoundTrip marshals the synthetic program and reads it back,
// asserting the schema survives a JSON round-trip byte-for-value: the loader and
// the writer agree, so a program authored by hand and one produced in-process
// validate identically.
func TestProgramRoundTrip(t *testing.T) {
	t.Parallel()

	original := ExampleProgram()
	b, err := json.Marshal(original)
	require.NoError(t, err)

	var got Program
	require.NoError(t, json.Unmarshal(b, &got))
	require.NoError(t, got.Validate())

	assert.Equal(t, original, got)
	// Spot-check the fields the recommender leans on survived the trip.
	assert.Equal(t, "example_foundation", got.ProgramID)
	assert.Len(t, got.Rotation, 7)
	assert.Equal(t, 48, got.RecoveryHours["legs"])
	require.NotNil(t, mustCard(t, got, "legs").Easier)
	assert.Equal(t, LoadLight, mustCard(t, got, "legs").Easier.Load)
}

// TestLoadProgramReadsOpaquePath writes the synthetic program to a temp file and
// loads it directly, proving the opaque-path loader reads exactly the file it is
// given.
func TestLoadProgramReadsOpaquePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "program.json")
	b, err := json.Marshal(ExampleProgram())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o600))

	got, err := LoadProgram(path)
	require.NoError(t, err)
	assert.Equal(t, ExampleProgram(), got)
}

// TestLoadProgramDegradesOnBadInput covers the loader's failure surface: an
// empty path, a missing file, a directory (the never-dir-walk guarantee — a dir
// is not a file, so the read fails rather than the loader walking it), malformed
// JSON, and a structurally invalid program all return a loud error and a zero
// Program for the surface to degrade on (workout-module.md W-1), never a partial
// program silently.
func TestLoadProgramDegradesOnBadInput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	badJSON := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(badJSON, []byte("{not json"), 0o600))

	invalid := filepath.Join(dir, "invalid.json")
	require.NoError(t, os.WriteFile(invalid, []byte(`{"version":1,"cards":[]}`), 0o600))

	cases := []struct {
		name string
		path string
	}{
		{"empty path", ""},
		{"missing file", filepath.Join(dir, "nope.json")},
		{"directory not file", dir},
		{"malformed json", badJSON},
		{"invalid program", invalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := LoadProgram(tc.path)
			require.Error(t, err)
			assert.Equal(t, Program{}, got)
		})
	}
}

// TestProgramValidateRejects walks the structural rules Validate enforces, so a
// hand-authored program that would break the recommender is caught at load, not
// at recommend time.
func TestProgramValidateRejects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(*Program)
	}{
		{"unsupported version", func(p *Program) { p.Version = 2 }},
		{"missing program id", func(p *Program) { p.ProgramID = "" }},
		{"no cards", func(p *Program) { p.Cards = nil }},
		{"card without id", func(p *Program) { p.Cards[0].ID = "" }},
		{"duplicate card id", func(p *Program) { p.Cards[1].ID = p.Cards[0].ID }},
		{"invalid card load", func(p *Program) { p.Cards[0].Load = "brutal" }},
		{"invalid easier load", func(p *Program) { p.Cards[0].Easier.Load = "brutal" }},
		{"bad rotation weekday", func(p *Program) { p.Rotation[0].Weekday = "someday" }},
		{"rotation unknown card", func(p *Program) { p.Rotation[0].Card = "ghost" }},
		{"bad calendar date", func(p *Program) {
			p.Calendar = []CalendarEntry{{Date: "2026-13-40", Card: "legs"}}
		}},
		{"calendar unknown card", func(p *Program) {
			p.Calendar = []CalendarEntry{{Date: "2026-07-20", Card: "ghost"}}
		}},
		{"bad start date", func(p *Program) { p.StartDate = "not-a-date" }},
		{"pain threshold out of range", func(p *Program) { p.PainFlagThreshold = 42 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := ExampleProgram()
			tc.mutate(&p)
			assert.Error(t, p.Validate())
		})
	}
}

// TestProgramValidateAcceptsCalendarOverride confirms a well-formed dated
// calendar override passes validation — the exception path the recommender's
// rotation pick reads.
func TestProgramValidateAcceptsCalendarOverride(t *testing.T) {
	t.Parallel()
	p := ExampleProgram()
	p.Calendar = []CalendarEntry{{Date: "2026-07-20", Card: "recovery"}}
	require.NoError(t, p.Validate())
}

// mustCard fetches a card by id or fails the test — a small helper so a fixture
// drift (a renamed card) surfaces as a clear failure rather than a nil deref.
func mustCard(t *testing.T, p Program, id string) Card {
	t.Helper()
	c, ok := p.card(id)
	require.Truef(t, ok, "card %q must exist in the example program", id)
	return c
}
