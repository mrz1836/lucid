package router

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// gratitude.go is the user-facing gratitude-tally path (gratitude.md §3, §6):
// the accumulating nightly-gratitude count. `add` tallies one occurrence —
// creating an entry or bumping the canonical-key match — and returns the
// appended event's receipt id; `list` folds each live entry's Count/First/Last
// and prints the tally sorted by count then recency. Every write is
// deterministic and agent-free (architecture P9): the canonical-key match is a
// normalized string derivation, and by-meaning matching is the deferred R-011.

// AddGratitudeRequest is one `lucid gratitude add` turn (gratitude.md §3): the
// verbatim phrase, the strict-tier --day value, and now (injected so backdating
// and receipt ids are deterministic in tests).
type AddGratitudeRequest struct {
	Thing  string
	DayArg string
	Now    time.Time
}

// GratitudeWriteResult reports the appended occurrence and the resulting tally.
// Receipt is the newly appended event's unique receipt id (gratitude.md §2 Ids)
// — distinct from Key, the stable entry id that `list` shows and `--into`/`merge`
// target. Created is true when this call minted the entry (its history is the one
// occurrence just appended).
type GratitudeWriteResult struct {
	Entry   observations.GratitudeEntry
	Receipt string
	Key     string
	Thing   string
	Count   int
	First   string
	Last    string
	Created bool
	Ack     string
}

// GratitudeListView is the `lucid gratitude list --json` payload: the live tally
// (tombstones omitted) and its count, sorted by count then recency. Entries is
// never null so automation always reads an array.
type GratitudeListView struct {
	Count   int                  `json:"count"`
	Entries []GratitudeListEntry `json:"entries"`
}

// GratitudeListEntry is one folded tally row: the stable entry id, the display
// phrase and its alternate wordings, and the derived count + first/last span.
type GratitudeListEntry struct {
	ID    string   `json:"id"`
	Thing string   `json:"thing"`
	Aka   []string `json:"aka"`
	Count int      `json:"count"`
	First string   `json:"first"`
	Last  string   `json:"last"`
}

// GratitudeListResult carries the machine view and the human-first lines the CLI
// shares through emit (ADR-0007).
type GratitudeListResult struct {
	View  GratitudeListView
	Lines []string
}

// AddGratitude tallies one occurrence of a thing a person is grateful for
// (gratitude.md §3). It resolves the canonical entry key from the normalized
// phrase (the v1 match seam — by-meaning matching is R-011), appends one
// occurrence event, and returns that event's receipt id alongside the resulting
// tally. It is deterministic and agent-free. An empty phrase writes nothing (a
// clean usage error), and a strict-tier `--day` runs the shared capture grammar:
// an unreadable token or a future day is a [DayRejectedError] and nothing is
// written. The receipt encodes the occurrence's logical date; the event's `at`
// is always the real write time.
func (r *Router) AddGratitude(req AddGratitudeRequest) (GratitudeWriteResult, error) {
	now := whenOr(req.Now)
	thing := strings.TrimSpace(req.Thing)
	if thing == "" {
		return GratitudeWriteResult{}, fmt.Errorf("gratitude needs something you're grateful for; nothing was saved")
	}
	if err := r.prepareGratitude(); err != nil {
		return GratitudeWriteResult{}, err
	}

	// Strict-tier `--day`: reuse the one shared capture grammar so the accepted
	// forms, the 04:00 rollover, and the future ceiling match every other dated
	// capture. A refusal is a DayRejectedError the CLI prints; nothing lands.
	when, err := resolveCaptureWhen(req.DayArg, now)
	if err != nil {
		return GratitudeWriteResult{}, err
	}
	logicalDate := when.LogicalDate

	// v1 canonical-key match seam: the normalized phrase resolves-or-creates by
	// its salted key. Differing wording lands a new entry — the interim by-meaning
	// path (`--into`/`merge`) and the eventual automatic match are R-011.
	key, err := r.store.ResolveGratitudeKey(thing)
	if err != nil {
		return GratitudeWriteResult{}, fmt.Errorf("could not resolve the gratitude key; nothing was saved: %w", err)
	}

	ev := observations.GratitudeEvent{
		Type:   observations.GratitudeEventOccurrence,
		Date:   logicalDate,
		Source: observations.GratitudeSourceGratitude,
	}
	entry, appended, err := r.store.AppendGratitudeEvent(key, thing, logicalDate, true, ev, now)
	if err != nil {
		return GratitudeWriteResult{}, fmt.Errorf("could not add the gratitude; nothing was saved: %w", err)
	}

	tally := entry.Tally()
	created := len(entry.History) == 1
	return GratitudeWriteResult{
		Entry:   entry,
		Receipt: appended.ID,
		Key:     entry.Key,
		Thing:   entry.DisplayName,
		Count:   tally.Count,
		First:   tally.First,
		Last:    tally.Last,
		Created: created,
		Ack:     gratitudeAddAck(entry.DisplayName, tally.Count, appended.ID, created),
	}, nil
}

// GratitudeList reads the live tally, folds each entry's Count/First/Last, and
// returns it sorted by count then recency (gratitude.md §6). It writes nothing.
// Tombstones (merge redirects) are omitted from the active tally.
func (r *Router) GratitudeList() (GratitudeListResult, error) {
	all, err := r.store.ReadGratitudeAll()
	if err != nil {
		return GratitudeListResult{}, err
	}
	entries := make([]GratitudeListEntry, 0, len(all))
	for _, e := range all {
		if e.IsTombstone() {
			continue // a merged-away duplicate is omitted from the active tally
		}
		t := e.Tally()
		aka := e.Aka
		if aka == nil {
			aka = []string{}
		}
		entries = append(entries, GratitudeListEntry{
			ID:    e.Key,
			Thing: e.DisplayName,
			Aka:   aka,
			Count: t.Count,
			First: t.First,
			Last:  t.Last,
		})
	}
	// Sort by count desc, then most-recent last desc, then key asc — the things
	// returned to most, most recently, at the top, deterministic on ties.
	slices.SortFunc(entries, func(x, y GratitudeListEntry) int {
		if x.Count != y.Count {
			return cmp.Compare(y.Count, x.Count)
		}
		if x.Last != y.Last {
			return cmp.Compare(y.Last, x.Last)
		}
		return cmp.Compare(x.ID, y.ID)
	})
	return GratitudeListResult{
		View:  GratitudeListView{Count: len(entries), Entries: entries},
		Lines: gratitudeListLines(entries),
	}, nil
}

// prepareGratitude scaffolds the gratitude tree idempotently, wrapping any
// failure with the shared message the gratitude verbs report.
func (r *Router) prepareGratitude() error {
	if err := r.store.ScaffoldGratitude(); err != nil {
		return fmt.Errorf("could not prepare the gratitude tree: %w", err)
	}
	return nil
}

// gratitudeAddAck builds the inventory ack emitted after a tally lands: the
// thing, its running count, and the receipt id — provenance over magic, no
// score, no evaluation (gratitude.md §0). A first mention reads "Started tally
// for", a bump reads "Tallied", so what changed is legible.
func gratitudeAddAck(thing string, count int, receipt string, created bool) string {
	verb := "Tallied"
	if created {
		verb = "Started tally for"
	}
	return fmt.Sprintf("%s %q (×%d) as `%s`.", verb, thing, count, receipt)
}

// gratitudeListLines renders the human-first tally: a count header then one line
// per entry, `<id>  ×<count>  <thing>  (span)`. An empty tally prints the add
// hint, so the read is never a bare blank.
func gratitudeListLines(entries []GratitudeListEntry) []string {
	if len(entries) == 0 {
		return []string{"No gratitude tallied yet — add one with `lucid gratitude add \"<thing>\"`."}
	}
	noun := "entries"
	if len(entries) == 1 {
		noun = "entry"
	}
	lines := make([]string, 0, len(entries)+1)
	lines = append(lines, fmt.Sprintf("%d gratitude %s:", len(entries), noun))
	for _, e := range entries {
		lines = append(lines, fmt.Sprintf("  %s  ×%d  %s%s", e.ID, e.Count, e.Thing, gratitudeSpan(e.First, e.Last)))
	}
	return lines
}

// gratitudeSpan renders the first/last span suffix for a tally row: nothing when
// unknown, a single date when first == last (or only last is known), else the
// full first · last range.
func gratitudeSpan(first, last string) string {
	switch {
	case first == "" && last == "":
		return ""
	case first == "" || first == last:
		return fmt.Sprintf("  (last %s)", last)
	default:
		return fmt.Sprintf("  (first %s · last %s)", first, last)
	}
}
