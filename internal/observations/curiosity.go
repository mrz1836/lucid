package observations

import "time"

// Curiosity is the MVP's small contextual-question surface (observations.md §6,
// observations-module.md §Curiosity). It is deterministic — a template table,
// not a model — budgeted (at most curiosity_budget_per_day per logical day),
// and backed off: a template just asked is suppressed for CuriosityBackoffDays,
// and after CuriosityRetireIgnores asks it is retired until its underlying
// condition changes. The ask-state is ephemeral (never the Ledger); only the
// non-answer is never recorded. All logic here is pure over values so the
// selection is testable without disk or a model.

// Curiosity template ids (observations.md §6: "no sticky location? pain with no
// site?"). The table is closed and ships in the repo.
const (
	CuriosityMissingLocation = "missing-location"
	CuriosityPainNoSite      = "pain-no-site"
)

// Curiosity backoff defaults (observations.md §6, §10: "backoff 7 days, retire
// at 3 ignores").
const (
	CuriosityBackoffDays   = 7
	CuriosityRetireIgnores = 3
)

// curiosityTemplate is one deterministic ask. Templates are evaluated in a
// fixed order so selection is stable.
type curiosityTemplate struct {
	id   string
	text string
}

// curiosityTable is the closed, ordered template set. Text is a neutral
// question — inventory, never obligation: no streak, score, or judgment.
var curiosityTable = []curiosityTemplate{ //nolint:gochecknoglobals // a fixed, read-only table
	{id: CuriosityMissingLocation, text: "Where are you these days? Set it with `/obs where <place>`."},
	{id: CuriosityPainNoSite, text: "Which site was that? Add it next time, e.g. `/pain 6 knee`."},
}

// CuriosityState is the ephemeral per-instance ask-state. It lives under
// projections/ (rebuildable — losing it only resets backoff), never the Ledger.
type CuriosityState struct {
	Day        string            `json:"day"`
	AskedToday int               `json:"asked_today"`
	Suppressed map[string]string `json:"suppressed"` // template id → suppressed-through date
	Ignores    map[string]int    `json:"ignores"`    // template id → cumulative ask count
}

// CuriosityContext is what curiosity inspects for the logical day: whether a
// sticky location is on file, and whether the current capture was a pain event
// with no site (the two MVP triggers).
type CuriosityContext struct {
	Day             string
	HasLocation     bool
	PainWithoutSite bool
}

// conditionHolds reports whether a template's triggering condition is currently
// true — the signal both for eligibility and for the "condition changed" reset.
func (c CuriosityContext) conditionHolds(id string) bool {
	switch id {
	case CuriosityMissingLocation:
		return !c.HasLocation
	case CuriosityPainNoSite:
		return c.PainWithoutSite
	default:
		return false
	}
}

// ChooseCuriosity selects at most one micro-question for the current capture,
// honoring the per-day budget, the 7-day backoff, and the 3-ignore retirement,
// and returns the updated ephemeral state. asked is false when nothing fires
// (budget spent, no condition, or every eligible template suppressed/retired).
// A template whose condition no longer holds has its ignore count and backoff
// cleared — retirement lasts only until the condition changes.
func ChooseCuriosity(state CuriosityState, ctx CuriosityContext, budget int) (ask string, newState CuriosityState, asked bool) {
	st := normalizeCuriosity(state)
	if st.Day != ctx.Day {
		st.Day = ctx.Day
		st.AskedToday = 0
	}

	// A cleared condition re-arms its template (observations.md §6: retired
	// "until its underlying condition changes").
	for _, tmpl := range curiosityTable {
		if !ctx.conditionHolds(tmpl.id) {
			delete(st.Ignores, tmpl.id)
			delete(st.Suppressed, tmpl.id)
		}
	}

	if budget <= 0 || st.AskedToday >= budget {
		return "", st, false
	}

	for _, tmpl := range curiosityTable {
		if !ctx.conditionHolds(tmpl.id) {
			continue
		}
		if st.Ignores[tmpl.id] >= CuriosityRetireIgnores {
			continue // retired until the condition changes
		}
		if through, ok := st.Suppressed[tmpl.id]; ok && ctx.Day <= through {
			continue // inside the backoff window
		}
		st.AskedToday++
		st.Ignores[tmpl.id]++
		st.Suppressed[tmpl.id] = curiositySuppressThrough(ctx.Day)
		return tmpl.text, st, true
	}
	return "", st, false
}

// curiositySuppressThrough returns the last day an asked template stays
// suppressed (day + CuriosityBackoffDays), or "" when the day is unparseable
// (a missing suppression window simply never suppresses — fail open, since
// curiosity is skippable by design).
func curiositySuppressThrough(day string) string {
	d, err := ParseDate(day, time.UTC)
	if err != nil {
		return ""
	}
	return DateString(d.AddDate(0, 0, CuriosityBackoffDays))
}

// normalizeCuriosity guarantees the maps are non-nil so the state marshals and
// mutates cleanly.
func normalizeCuriosity(s CuriosityState) CuriosityState {
	if s.Suppressed == nil {
		s.Suppressed = map[string]string{}
	}
	if s.Ignores == nil {
		s.Ignores = map[string]int{}
	}
	return s
}
