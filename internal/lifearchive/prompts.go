package lifearchive

import "fmt"

// prompts.go emits the generic excavation prompt templates for a selected
// cluster (mvp/life-archive.md §5–§6). The templates are keyed by (track, gap)
// and are deliberately generic: personal specifics come from the Cluster data
// (its display name, its gaps), never from hard-coded copy in the repo
// (product-principles.md §9 — synthetic examples only). No model runs here; the
// personal driver turns these into a gentle, one-cluster-at-a-time conversation
// on its own surface.

// injuryPrompts maps an injury convention gap (life-archive.md §2) to a generic
// question. Every key in injuryGapKeys has an entry.
var injuryPrompts = map[string]string{ //nolint:gochecknoglobals // fixed generic prompt copy, keyed by the §2 convention
	"onset":               "When did it start? Even a rough year or season is enough.",
	"timeline":            "Walk me through what happened with it over the years — flares, quiet stretches, anything that changed it.",
	"body_area":           "Where in the body is it, in your own words?",
	"cause":               "How did it happen?",
	"severity":            "How bad has it been — at its worst, and where it sits now?",
	"lasting_effects":     "What did it leave behind that's still with you?",
	"current_limitations": "What does it still stop or shape, day to day?",
	"treatments":          "What have you tried for it — PT, rest, rehab, anything that helped or didn't?",
	"uncertainty":         "What are you not sure about with this one?",
}

// storyPrompts maps a generic story dimension (life-archive.md §3) to a
// question. Every key in storyGapKeys has an entry.
var storyPrompts = map[string]string{ //nolint:gochecknoglobals // fixed generic prompt copy, keyed by the §3 story dimensions
	"date":           "Roughly when was this — a year, a season, how old you were?",
	"people":         "Who was there with you?",
	"place":          "Where did it happen?",
	"tone":           "What did it feel like — the mood of it, in a phrase?",
	"why_it_matters": "Why has this one stuck with you?",
	"follow_up":      "What's the thread to pull next time — what else was going on around then?",
}

// Prompts renders the excavation prompts for a cluster: a data-driven lead-in
// that names the cluster (its display name comes from the data, so this stays
// generic copy), followed by one generic question per gap in the cluster's
// order. An unknown track or a cluster with no gaps yields just the lead-in, so
// the surface always has at least one thing to open with. The result is
// deterministic — the gap order fixes the prompt order.
func Prompts(c Cluster) []string {
	var lead string
	var table map[string]string
	switch c.Track {
	case TrackInjury:
		lead = fmt.Sprintf("Let's fill in what we know about %s.", displayOr(c, "this injury"))
		table = injuryPrompts
	case TrackStory:
		lead = fmt.Sprintf("Let's open the chapter %q — tell me a story from it.", displayOr(c, "this era"))
		table = storyPrompts
	default:
		return nil
	}
	// A future gap key with no template is skipped rather than rendered blank —
	// a forward-compatible guard.
	out := make([]string, 0, 1+len(c.Gaps))
	out = append(out, lead)
	for _, g := range c.Gaps {
		if q, ok := table[g]; ok {
			out = append(out, q)
		}
	}
	return out
}

// displayOr returns the cluster's display name, or a generic fallback when it is
// blank — so the lead-in never renders an empty name.
func displayOr(c Cluster, fallback string) string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return fallback
}
