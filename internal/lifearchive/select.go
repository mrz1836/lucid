// Package lifearchive is the deterministic core of the memory-excavation
// surface (mvp/life-archive.md): the cluster-selection engine that picks the
// next injury or story cluster to excavate and the generic prompt templates it
// emits for that cluster. Like the Engine and observation modules it is
// entirely agent-free — pure functions over values, no disk, no model, no
// session state (architecture P9; life-archive.md §5 "holds no session state").
// The router feeds it a projection-only bundle; the personal review
// conversation that acts on the selection lives outside this repo.
package lifearchive

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// Track names (mvp/life-archive.md §1: two tracks kept separate so a session
// opens exactly one and never blurs them).
const (
	TrackInjury = "injury"
	TrackStory  = "story"
)

// injuryGapKeys are the injury convention Fields keys (mvp/life-archive.md §2)
// in canonical order. A missing key is a "gap" the excavation asks about, and
// this order fixes the deterministic prompt order for the injury track. `note`
// and the derived `onset_precision` are not gaps — only the eight testimony
// fields plus the backdate-aware `onset` are.
var injuryGapKeys = []string{ //nolint:gochecknoglobals // the fixed injury convention vocabulary (life-archive.md §2)
	"onset",
	"timeline",
	"body_area",
	"cause",
	"severity",
	"lasting_effects",
	"current_limitations",
	"treatments",
	"uncertainty",
}

// storyGapKeys are the generic story dimensions (mvp/life-archive.md §3) in
// canonical order. Unlike an injury's registry Fields, a story's dimensions are
// per-memory, so an era cluster always offers the full set — every new story in
// the chapter can be fleshed on any dimension.
var storyGapKeys = []string{ //nolint:gochecknoglobals // the fixed story dimension vocabulary (life-archive.md §3)
	"date",
	"people",
	"place",
	"tone",
	"why_it_matters",
	"follow_up",
}

// SelectInput is the cluster-selection contract's input (mvp/life-archive.md
// §6): the enabled injury and era registries, the memory events, and the
// current time. Track is an optional caller preference ("injury" | "story");
// when empty the auto rule applies. The slices are read verbatim — SelectCluster
// never mutates them.
type SelectInput struct {
	Injuries []observations.Registry
	Eras     []observations.Registry
	Memories []observations.Event
	Now      time.Time
	Track    string
}

// Cluster is the next thing to excavate (mvp/life-archive.md §6): its track, the
// registry key + display name it names, a plain-language reason it was chosen,
// and the convention gaps to ask about. It carries no score, target, or streak
// (§0 — the archive is inventory, never obligation).
type Cluster struct {
	Track       string
	Key         string
	DisplayName string
	Reason      string
	Gaps        []string
}

// SelectCluster picks the next cluster to excavate. It is a pure function with a
// stable tiebreak (mvp/life-archive.md §6): on the injury track it prefers the
// thinnest injury (most missing convention Fields, tiebroken by key); on the
// story track it prefers the era with the fewest linked memories (tiebroken by
// oldest start, then key). With an explicit Track it selects within that track
// only; with no Track it applies the auto rule — the injury track leads while
// any injury still has gaps, then the story track. The binary holds no session
// state, so cadence and alternation are the personal harness's job (§5); auto is
// a deterministic priority, not a rotation. It returns (Cluster, false) when the
// chosen track has nothing to excavate — an empty or fully-excavated store
// degrades to an honest empty result, with no model to spend over it.
func SelectCluster(in SelectInput) (Cluster, bool) {
	switch in.Track {
	case TrackInjury:
		return injuryCandidate(in.Injuries)
	case TrackStory:
		return storyCandidate(in.Eras, in.Memories)
	}
	if c, ok := injuryCandidate(in.Injuries); ok {
		return c, true
	}
	return storyCandidate(in.Eras, in.Memories)
}

// injuryCandidate returns the thinnest injury with at least one missing
// convention field (mvp/life-archive.md §6). Injuries are scanned in key order
// so the "most gaps" pick is deterministic — among equally-thin records the
// lowest key wins. An injury with no gaps is skipped (nothing to ask). All
// statuses are eligible: excavating a resolved injury's history is exactly the
// point ("dig up the past"); a fully-filled resolved injury simply has no gaps.
func injuryCandidate(injuries []observations.Registry) (Cluster, bool) {
	sorted := slices.Clone(injuries)
	slices.SortFunc(sorted, func(a, b observations.Registry) int { return cmp.Compare(a.Key, b.Key) })

	var best Cluster
	bestGaps := 0
	found := false
	for _, inj := range sorted {
		gaps := injuryGaps(inj.Fields)
		if len(gaps) == 0 {
			continue
		}
		if !found || len(gaps) > bestGaps {
			found = true
			bestGaps = len(gaps)
			best = Cluster{
				Track:       TrackInjury,
				Key:         inj.Key,
				DisplayName: inj.DisplayName,
				Reason:      injuryReason(len(gaps)),
				Gaps:        gaps,
			}
		}
	}
	return best, found
}

// storyCandidate returns the least-excavated era — fewest linked memories,
// tiebroken by oldest start date then key (mvp/life-archive.md §6). The story
// track always offers a chapter as long as one era exists: there is always more
// story to capture, so an era with some memories is still a valid pick. With no
// era, the story track has nothing to browse by and there is no candidate.
func storyCandidate(eras []observations.Registry, memories []observations.Event) (Cluster, bool) {
	if len(eras) == 0 {
		return Cluster{}, false
	}
	linked := map[string]int{}
	for _, ev := range memories {
		if key := memoryEraKey(ev); key != "" {
			linked[key]++
		}
	}
	sorted := slices.Clone(eras)
	slices.SortFunc(sorted, func(a, b observations.Registry) int {
		if d := cmp.Compare(linked[a.Key], linked[b.Key]); d != 0 {
			return d
		}
		if d := cmp.Compare(startSortKey(a), startSortKey(b)); d != 0 {
			return d
		}
		return cmp.Compare(a.Key, b.Key)
	})
	era := sorted[0]
	return Cluster{
		Track:       TrackStory,
		Key:         era.Key,
		DisplayName: era.DisplayName,
		Reason:      storyReason(linked[era.Key]),
		Gaps:        slices.Clone(storyGapKeys),
	}, true
}

// injuryGaps returns the missing convention keys of an injury's Fields, in
// canonical order. A key is present when it holds a non-empty string (the write
// path stores these as strings, registrywrite.go putField); any other present
// value also counts as filled, so a hand-edited record degrades honestly.
func injuryGaps(fields map[string]any) []string {
	var gaps []string
	for _, key := range injuryGapKeys {
		if !hasField(fields, key) {
			gaps = append(gaps, key)
		}
	}
	return gaps
}

// hasField reports whether a Fields key holds a meaningful value: a non-empty
// string, or any non-string present value.
func hasField(fields map[string]any, key string) bool {
	if fields == nil {
		return false
	}
	v, present := fields[key]
	if !present {
		return false
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) != ""
	}
	return true
}

// memoryEraKey returns the era key a memory event is filed under (refs.era,
// written verbatim by resolveMemoryRefs), or "" when the story sits in no era.
func memoryEraKey(ev observations.Event) string {
	if ev.Refs == nil {
		return ""
	}
	if s, ok := ev.Refs["era"].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// startSortKey returns an era's start date for the "oldest un-excavated era"
// tiebreak. An era with no recorded start sorts last (a high sentinel), so a
// dated chapter is preferred as the anchorable "oldest known".
func startSortKey(era observations.Registry) string {
	if s, ok := era.Fields["start"].(string); ok {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	return "￿"
}

// injuryReason renders the plain-language reason an injury was chosen — a
// description of what is missing, never a score or target (§0).
func injuryReason(gaps int) string {
	return fmt.Sprintf("%s still unrecorded — the thinnest injury record.", countable(gaps, "convention field", "convention fields"))
}

// storyReason renders the plain-language reason an era was chosen from how many
// stories it already holds. It reads as inventory, not a quota — "room for more"
// is an invitation, never an obligation (§0).
func storyReason(linked int) string {
	if linked == 0 {
		return "no stories captured in this chapter yet."
	}
	return fmt.Sprintf("%s captured so far — room for more.", countable(linked, "story", "stories"))
}

// countable renders "1 <singular>" / "N <plural>" for a small, human count.
func countable(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}
