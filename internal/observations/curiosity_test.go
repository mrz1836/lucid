package observations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// missingLocationCtx is a day with no sticky location — the missing-location
// trigger.
func missingLocationCtx(day string) CuriosityContext {
	return CuriosityContext{Day: day, HasLocation: false}
}

func TestChooseCuriosity_MissingLocationFiresWithinBudget(t *testing.T) {
	ask, st, asked := ChooseCuriosity(CuriosityState{}, missingLocationCtx("2026-07-02"), 1)
	require.True(t, asked)
	assert.Contains(t, ask, "/obs where")
	assert.Equal(t, 1, st.AskedToday)

	// A second capture the same day is over budget — silence.
	ask2, _, asked2 := ChooseCuriosity(st, missingLocationCtx("2026-07-02"), 1)
	assert.False(t, asked2)
	assert.Empty(t, ask2)
}

func TestChooseCuriosity_BudgetZeroNeverAsks(t *testing.T) {
	_, _, asked := ChooseCuriosity(CuriosityState{}, missingLocationCtx("2026-07-02"), 0)
	assert.False(t, asked)
}

func TestChooseCuriosity_BackoffSuppressesForSevenDays(t *testing.T) {
	_, st, asked := ChooseCuriosity(CuriosityState{}, missingLocationCtx("2026-07-02"), 1)
	require.True(t, asked)

	// Next day, still inside the 7-day backoff → no re-ask even with budget.
	_, st, asked = ChooseCuriosity(st, missingLocationCtx("2026-07-03"), 1)
	assert.False(t, asked, "suppressed inside the backoff window")

	// The last suppressed day (day+7) is still suppressed.
	_, st, asked = ChooseCuriosity(st, missingLocationCtx("2026-07-09"), 1)
	assert.False(t, asked)

	// One day past the window → eligible again.
	_, _, asked = ChooseCuriosity(st, missingLocationCtx("2026-07-10"), 1)
	assert.True(t, asked, "past the backoff window it re-asks")
}

func TestChooseCuriosity_RetiresAfterThreeIgnores(t *testing.T) {
	st := CuriosityState{}
	// Ask on three well-spaced days (each past the previous backoff window).
	for _, day := range []string{"2026-07-02", "2026-07-10", "2026-07-18"} {
		var asked bool
		_, st, asked = ChooseCuriosity(st, missingLocationCtx(day), 1)
		require.Truef(t, asked, "expected an ask on %s", day)
	}
	// The fourth eligible day is past all backoff but retired at 3 ignores.
	_, st, asked := ChooseCuriosity(st, missingLocationCtx("2026-07-26"), 1)
	assert.False(t, asked, "retired after three ignores")

	// The condition clearing (a location now on file) re-arms the template.
	_, st, _ = ChooseCuriosity(st, CuriosityContext{Day: "2026-07-27", HasLocation: true}, 1)
	_, _, revived := ChooseCuriosity(st, missingLocationCtx("2026-07-28"), 1)
	assert.True(t, revived, "a changed condition revives a retired template")
}

func TestChooseCuriosity_PainNoSiteFiresWhenLocationKnown(t *testing.T) {
	ctx := CuriosityContext{Day: "2026-07-02", HasLocation: true, PainWithoutSite: true}
	ask, _, asked := ChooseCuriosity(CuriosityState{}, ctx, 1)
	require.True(t, asked)
	assert.Contains(t, ask, "site")
}

func TestChooseCuriosity_NoConditionNoAsk(t *testing.T) {
	ctx := CuriosityContext{Day: "2026-07-02", HasLocation: true, PainWithoutSite: false}
	_, _, asked := ChooseCuriosity(CuriosityState{}, ctx, 1)
	assert.False(t, asked)
}

func TestChooseCuriosity_NewDayResetsBudget(t *testing.T) {
	// Spend the budget on the missing-location ask, then the next day — with a
	// location now on file (so that template is out of the running) — a fresh
	// pain-no-site trigger fires, proving the per-day budget reopened.
	_, st, asked := ChooseCuriosity(CuriosityState{}, missingLocationCtx("2026-07-02"), 1)
	require.True(t, asked)
	assert.Equal(t, 1, st.AskedToday)

	next := CuriosityContext{Day: "2026-07-03", HasLocation: true, PainWithoutSite: true}
	_, st, asked = ChooseCuriosity(st, next, 1)
	assert.True(t, asked, "a new day re-opens the budget")
	assert.Equal(t, 1, st.AskedToday, "the budget count reset for the new day")
}
