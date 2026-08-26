package router

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// storagePrefix is the package prefix the storage adapter stamps on its errors.
// A gratitude-merge storage error is the one gratitude return whose message is
// already a complete user-facing sentence (a self-merge, a missing or tombstoned
// source/target — each ending "nothing was changed"), so surfacing it cleanly is
// a matter of stripping this prefix, matching the self/person path's enginePrefix
// stripping (self.go). The add/import paths wrap their opaque I/O failures with
// intent instead.
const storagePrefix = "storage: "

// surfaceMergeError turns a gratitude-merge storage error into clean user-facing
// prose by stripping the storage: prefix, so cli/output.go prints the sentence
// the storage op composed rather than leaking the package name.
func surfaceMergeError(err error) error {
	return errors.New(strings.TrimPrefix(err.Error(), storagePrefix))
}

// gratitude.go is the user-facing gratitude-tally path (gratitude.md §3, §6):
// the accumulating nightly-gratitude count. `add` tallies one occurrence —
// creating an entry or bumping the canonical-key match — and returns the
// appended event's receipt id; `list` folds each live entry's Count/First/Last
// and prints the tally sorted by count then recency. Every write is
// deterministic and agent-free (architecture P9): the canonical-key match is a
// normalized string derivation, and by-meaning matching is the deferred R-011.

// AddGratitudeRequest is one `lucid gratitude add` turn (gratitude.md §3): the
// verbatim phrase, the strict-tier --day value, an optional Into target, and now
// (injected so backdating and receipt ids are deterministic in tests). When Into
// is set, the phrase bumps that specific stable entry regardless of wording (the
// interim by-meaning path, gratitude.md §4) instead of resolving a canonical key.
type AddGratitudeRequest struct {
	Thing  string
	DayArg string
	Into   string
	Now    time.Time
}

// ImportGratitudeRequest is one `lucid gratitude import` (or `add --count …`)
// turn (gratitude.md §5): the one-time seed/migration path. It writes a single
// entry carrying one `seed` event with the explicit Count/First/Last and
// fabricates no per-occurrence dates — distinct from the nightly `add`.
type ImportGratitudeRequest struct {
	Thing string
	Count int
	First string
	Last  string
	Now   time.Time
}

// GratitudeMergeRequest is one `lucid gratitude merge <src> <dst>` turn
// (gratitude.md §4): fold an accidental duplicate's whole tally into the
// canonical entry and rewrite the source as a redirect tombstone.
type GratitudeMergeRequest struct {
	Source string
	Target string
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

	// Resolve which entry this occurrence lands on (the `--into` target verbatim
	// or the v1 canonical-key seam) and whether the write refreshes the display.
	key, makePrimary, err := r.resolveGratitudeAddKey(req.Into, thing)
	if err != nil {
		return GratitudeWriteResult{}, err
	}

	ev := observations.GratitudeEvent{
		Type:   observations.GratitudeEventOccurrence,
		Date:   logicalDate,
		Source: observations.GratitudeSourceGratitude,
	}
	entry, appended, err := r.store.AppendGratitudeEvent(key, thing, logicalDate, makePrimary, ev, now)
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

// resolveGratitudeAddKey picks the entry key an `add` lands on and whether the
// write refreshes the display wording. With `--into` the stable id is targeted
// verbatim (the interim by-meaning path, gratitude.md §4): it must already name a
// live entry — a bump never creates one, and tonight's differing wording only
// joins aka[] (makePrimary=false). Without it, the v1 canonical-key seam
// resolves-or-creates by the normalized phrase (makePrimary=true; the eventual
// automatic by-meaning match is R-011).
func (r *Router) resolveGratitudeAddKey(into, thing string) (key string, makePrimary bool, err error) {
	into = strings.TrimSpace(into)
	if into == "" {
		key, err = r.store.ResolveGratitudeKey(thing)
		if err != nil {
			return "", false, fmt.Errorf("could not resolve the gratitude key; nothing was saved: %w", err)
		}
		return key, true, nil
	}
	existing, found, rerr := r.store.ReadGratitude(into)
	if rerr != nil {
		return "", false, fmt.Errorf("could not read the gratitude entry; nothing was saved: %w", rerr)
	}
	if !found || existing.IsTombstone() {
		return "", false, fmt.Errorf(
			"no live gratitude entry %q to bump; run `lucid gratitude list` for the id; nothing was saved", into,
		)
	}
	return into, false, nil
}

// GratitudeList reads the live tally, folds each entry's Count/First/Last, and
// returns it sorted by count then recency (gratitude.md §6). It writes nothing.
// Tombstones (merge redirects) are omitted from the active tally.
func (r *Router) GratitudeList() (GratitudeListResult, error) {
	all, err := r.store.ReadGratitudeAll()
	if err != nil {
		return GratitudeListResult{}, fmt.Errorf("could not read the gratitude tally: %w", err)
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

// ImportGratitude seeds one gratitude entry from a pre-counted tally row
// (gratitude.md §5): the one-time migration path, distinct from nightly `add`.
// It resolves the canonical key from the phrase and appends a single `seed`
// event carrying the explicit Count/First/Last — fabricating no per-occurrence
// dates, since the raw per-night truth already lives in the separate `#gratitude`
// logs. It is deliberately NOT idempotent: a second import appends a second seed
// and double-counts (a retry restores the pre-migration backup first). An empty
// phrase, a non-positive count, a malformed date, or first-after-last is a clean
// error and nothing is written.
func (r *Router) ImportGratitude(req ImportGratitudeRequest) (GratitudeWriteResult, error) {
	now := whenOr(req.Now)
	thing := strings.TrimSpace(req.Thing)
	if thing == "" {
		return GratitudeWriteResult{}, fmt.Errorf("gratitude import needs something you're grateful for; nothing was saved")
	}
	if req.Count < 1 {
		return GratitudeWriteResult{}, fmt.Errorf("gratitude import needs --count of at least 1; nothing was saved")
	}
	first, last, err := validateSeedSpan(req.First, req.Last)
	if err != nil {
		return GratitudeWriteResult{}, err
	}
	if err = r.prepareGratitude(); err != nil {
		return GratitudeWriteResult{}, err
	}

	key, err := r.store.ResolveGratitudeKey(thing)
	if err != nil {
		return GratitudeWriteResult{}, fmt.Errorf("could not resolve the gratitude key; nothing was saved: %w", err)
	}

	ev := observations.GratitudeEvent{
		Type:   observations.GratitudeEventSeed,
		Source: observations.GratitudeSourceMigration,
		Count:  req.Count,
		First:  first,
		Last:   last,
	}
	// The seed's receipt encodes its last date — the representative day of the
	// pre-counted span — so the id is meaningful without inventing occurrences.
	entry, appended, err := r.store.AppendGratitudeEvent(key, thing, last, true, ev, now)
	if err != nil {
		return GratitudeWriteResult{}, fmt.Errorf("could not import the gratitude; nothing was saved: %w", err)
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
		Ack:     gratitudeImportAck(entry.DisplayName, tally.Count, first, last, appended.ID),
	}, nil
}

// MergeGratitude folds an accidental duplicate into a canonical entry
// (gratitude.md §4): the source's whole count and first/last span move into the
// target via a `merge` event, the target absorbs the source's wordings, and the
// source becomes a redirect tombstone omitted from the active tally but kept for
// audit. A self-merge or a missing/tombstoned source or target is a clean error
// that changes nothing. It returns the merged target and the merge event's
// receipt, like every other mutation.
func (r *Router) MergeGratitude(req GratitudeMergeRequest) (GratitudeWriteResult, error) {
	now := whenOr(req.Now)
	entry, appended, err := r.store.MergeGratitude(req.Source, req.Target, now)
	if err != nil {
		return GratitudeWriteResult{}, surfaceMergeError(err)
	}
	tally := entry.Tally()
	return GratitudeWriteResult{
		Entry:   entry,
		Receipt: appended.ID,
		Key:     entry.Key,
		Thing:   entry.DisplayName,
		Count:   tally.Count,
		First:   tally.First,
		Last:    tally.Last,
		Created: false,
		Ack:     gratitudeMergeAck(appended.SourceKey, entry.DisplayName, entry.Key, tally.Count, appended.ID),
	}, nil
}

// validateSeedSpan checks a seed/import span: both dates must be civil
// YYYY-MM-DD and first must not fall after last. It returns the trimmed dates so
// the seed event stores them exactly as documented.
func validateSeedSpan(first, last string) (string, string, error) {
	first = strings.TrimSpace(first)
	last = strings.TrimSpace(last)
	fd, err := observations.ParseDate(first, time.UTC)
	if err != nil {
		return "", "", fmt.Errorf("gratitude import --first %q is not a civil YYYY-MM-DD date; nothing was saved", first)
	}
	ld, err := observations.ParseDate(last, time.UTC)
	if err != nil {
		return "", "", fmt.Errorf("gratitude import --last %q is not a civil YYYY-MM-DD date; nothing was saved", last)
	}
	if fd.After(ld) {
		return "", "", fmt.Errorf("gratitude import --first %q is after --last %q; nothing was saved", first, last)
	}
	return first, last, nil
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

// gratitudeImportAck builds the ack emitted after a one-time seed lands: the
// thing, its seeded running count, the migrated first/last span, and the receipt
// id — provenance over magic, distinct from the nightly `add` ack so a migration
// seed reads as what it is (gratitude.md §5).
func gratitudeImportAck(thing string, count int, first, last, receipt string) string {
	return fmt.Sprintf("Seeded %q (×%d, first %s · last %s) as `%s`.", thing, count, first, last, receipt)
}

// gratitudeMergeAck builds the ack emitted after a merge folds a duplicate: the
// source key that was absorbed, the canonical thing + id it folded into, the
// resulting running count, and the merge event's receipt id (gratitude.md §4).
func gratitudeMergeAck(sourceKey, thing, targetKey string, count int, receipt string) string {
	return fmt.Sprintf("Merged `%s` into %q (`%s`, now ×%d) as `%s`.", sourceKey, thing, targetKey, count, receipt)
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
