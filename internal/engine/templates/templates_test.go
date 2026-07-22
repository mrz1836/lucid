package templates

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/engine"
)

func endsWithSignOff(s string) bool { return strings.HasSuffix(s, SignOff) }

// TestBell names the chain and carries no sign-off.
func TestBell(t *testing.T) {
	got := Bell("Journal. Dock. Read.")
	assert.Contains(t, got, "Journal. Dock. Read.")
	assert.False(t, endsWithSignOff(got), "the bell does not sting")
}

// TestL1AndStormVariant name the floor and end with the pinned sign-off.
func TestL1AndStormVariant(t *testing.T) {
	l1 := L1("one line")
	assert.Contains(t, l1, "one line")
	assert.Contains(t, l1, "tonight is a must")
	assert.True(t, endsWithSignOff(l1))

	storm := L1Storm("one line")
	assert.Contains(t, storm, "storm standing, nothing is owed")
	assert.Contains(t, storm, "the floor: one line.")
	assert.True(t, endsWithSignOff(storm))
}

// TestL2 carries streak and declared mode only — never journal or capacity —
// and ends with the sign-off.
func TestL2(t *testing.T) {
	got := L2(7, engine.ModeYellow)
	assert.Contains(t, got, "Streak: 7")
	assert.Contains(t, got, "Declared mode: yellow")
	assert.Contains(t, got, "Storm: none")
	assert.True(t, endsWithSignOff(got))
}

// TestL2Storm is the verbatim storm variant with the confirmation date.
func TestL2Storm(t *testing.T) {
	got := L2Storm("2026-07-14")
	assert.Contains(t, got, "storm standing (confirmed 2026-07-14)")
	assert.Contains(t, got, "The stake is stayed; the ask-once still applies.")
	assert.True(t, endsWithSignOff(got))
}

// TestL2Blocked notifies the user without leaking to a nonexistent witness.
func TestL2Blocked(t *testing.T) {
	assert.Contains(t, L2Blocked(false), "L2 is disarmed (witness not confirmed)")
	assert.Contains(t, L2Blocked(true), "storm standing")
}

// TestL2Unreachable is the verbatim "you owe the message" fallback.
func TestL2Unreachable(t *testing.T) {
	assert.Equal(t, "L2 fired but couldn't reach J. — you owe the message.", L2Unreachable("J."))
	assert.Contains(t, L2Unreachable(""), "your witness")
}

// TestStormLapse is the verbatim lapse note (the witness name substitutes into
// the `talk to <witness>.` placeholder).
func TestStormLapse(t *testing.T) {
	assert.Equal(t,
		"storm declaration lapsed — no confirmation within 72h. Declare again, or talk to Jordan.",
		StormLapse("Jordan"))
	assert.Contains(t, StormLapse(""), "talk to your witness.")
}

// TestRender dispatches each decided Send to its fixed template, and the
// sign-off rule holds across the render seam: L1, L2, and both storm variants
// sign off; the bell and the L2-blocked note do not.
func TestRender(t *testing.T) {
	cases := []struct {
		name    string
		send    engine.Send
		wants   string
		signOff bool
	}{
		{"l1", engine.Send{Kind: engine.SendL1, Floor: "one line"}, "tonight is a must", true},
		{"l1 storm", engine.Send{Kind: engine.SendL1, Storm: true, Floor: "one line"}, "nothing is owed", true},
		{"l2", engine.Send{Kind: engine.SendL2, Streak: 3, Mode: engine.ModeGreen}, "Streak: 3", true},
		{"l2 storm", engine.Send{Kind: engine.SendL2, Storm: true, ConfirmedDate: "2026-07-14"}, "confirmed 2026-07-14", true},
		{"l2 blocked", engine.Send{Kind: engine.SendL2Blocked}, "disarmed", false},
		{"storm lapse", engine.Send{Kind: engine.SendStormLapse, WitnessName: "J."}, "lapsed", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Render(c.send)
			assert.Contains(t, got, c.wants)
			assert.Equal(t, c.signOff, endsWithSignOff(got), "sign-off rule for %s", c.name)
		})
	}
	assert.Empty(t, Render(engine.Send{Kind: "unknown"}), "an unknown kind renders nothing, never invented copy")
}

// TestTemplatesImportOnlyStdlibAndEngine is the "no LLM in the send path"
// guard for the template surface: every non-test file here may import only the
// standard library and the pure engine package — no provider/agent/model.
func TestTemplatesImportOnlyStdlibAndEngine(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	allowed := map[string]bool{
		`"fmt"`: true,
		`"github.com/mrz1836/lucid/internal/engine"`: true,
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		require.NoError(t, perr)
		for _, imp := range f.Imports {
			require.Truef(t, allowed[imp.Path.Value], "templates file %s imports %s — no model may be reachable", name, imp.Path.Value)
		}
	}
}
