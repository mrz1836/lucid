package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentContext_CarriesOnlyItsSlice(t *testing.T) {
	type intakeInput struct {
		Thread []string
	}
	slice := intakeInput{Thread: []string{"q1", "a1"}}
	ctx := NewAgentContext("checkin.intake", slice)

	assert.Equal(t, "checkin.intake", ctx.Intent())
	assert.Equal(t, slice, ctx.Slice())
}

// TestPathAllowedForAgent asserts the sanctuary boundary fails closed:
// engine/, observations/, and registries/ (at root or nested) are
// denied; Mirror trees and lookalikes ("engineering/") are allowed.
func TestPathAllowedForAgent(t *testing.T) {
	denied := []string{
		"engine/",
		"engine",
		"engine/chain.json",
		"engine/days/2026/07/day_2026_07_02.json",
		"observations/2026/07/obs_2026_07_02.jsonl",
		"registries/injuries/injury_x.json",
		"./engine/status.json",
		"observations",
	}
	for _, p := range denied {
		assert.Falsef(t, PathAllowedForAgent(p), "expected %q denied", p)
	}

	allowed := []string{
		"raw/2026/05/raw_x.md",
		"processed/raw_x.json",
		"insights/i_x.md",
		"people/person_a-river.json",
		"sessions/session_x.json",
		"reflections/reflection_2026_w18.md",
		"engineering/notes.md", // lookalike, not a sanctuary prefix
		"lucid.json",
	}
	for _, p := range allowed {
		assert.Truef(t, PathAllowedForAgent(p), "expected %q allowed", p)
	}
}

// TestSanctuaryDenylist_NamesTheThreeTrees guards that the denylist
// covers exactly the three sanctuary trees the plan requires.
func TestSanctuaryDenylist_NamesTheThreeTrees(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{"engine/", "observations/", "registries/"},
		SanctuaryDenylist())
}
