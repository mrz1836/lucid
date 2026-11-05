package observations

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// wheel.go is the Wheel of Life record family (wheel.md §2–§6): the monthly
// balance review. A wheel entry is one calendar month, keyed by the month itself
// (wheel_YYYY-MM — deterministic and non-sensitive, so months stay enumerable
// for the trend), carrying an append-only history of `snapshot` events. Each
// snapshot is a whole wheel: your own 1–10 rating for every pillar of the
// snapshot's own pillar set, an optional note and an optional `suggested`
// calibration value per pillar, and the vision-review fields. The month's
// current wheel is the latest snapshot ([WheelEntry.Fold]) — amend by append,
// latest wins. This package owns the schema, the versioned pillar sets, the
// 1–10 scale, the id grammar, and the fold; the
// storage adapter owns the single-writer append/read discipline (architecture
// P3), and no path here touches the filesystem or an LLM (P9).

// WheelSchema is the wheel entry schema version (wheel.md §4 Versioning). New
// needs go in a new optional field or a new event type under a bumped schema;
// readers tolerate an unknown field and a higher version. Schema 2 added the
// per-snapshot `pillar_set` (pillar set 2); this build reads schema 1 and 2,
// and an append raises a schema-1 month to 2 — a schema-1 file that is only
// read is never rewritten.
const WheelSchema = 2

// WheelEventSnapshot is the one event type in schema 1 and 2 (wheel.md §4): a
// whole wheel recorded by one `wheel add`. Every write appends exactly one; no
// event is ever rewritten.
const WheelEventSnapshot = "snapshot"

// The wheel's own rating scale (wheel.md §3): an integer 1–10 per pillar. It is
// NOT the Engine's 1–5 capacity scale and NOT the observation layer's 1–7
// Bristol scale; the three never mix and no conversion between them exists.
const (
	WheelScoreMin = 1
	WheelScoreMax = 10
)

// wheelMonthLayout is the calendar-month key layout (wheel.md §4): YYYY-MM.
const wheelMonthLayout = "2006-01"

// The pillar keys across every pillar set (wheel.md §2). The key is what the
// flags and the JSON use; [WheelPillarsOf] pairs each with its human label in
// its set's canonical order. A key's label is stable across sets.
//
// PillarRelationships is the set-1 key that pillar set 2 retired: it was split
// into PillarRomance and PillarFriends. It stays a known key so set-1 months
// read exactly as recorded and a new write that names it is refused as retired
// rather than as unknown — it is never repurposed for either new spoke.
const (
	PillarHealth        = "health"
	PillarRomance       = "romance"
	PillarFriends       = "friends"
	PillarRelationships = "relationships"
	PillarCareer        = "career"
	PillarFinances      = "finances"
	PillarGrowth        = "growth"
	PillarFun           = "fun"
	PillarEnvironment   = "environment"
	PillarContribution  = "contribution"
)

// CurrentWheelPillarSet is the pillar set every new snapshot is rated in
// (wheel.md §2, §4): pillar set 2, the nine pillars. The storage adapter stamps
// it on every append, so no caller chooses it. A stored snapshot keeps the set
// it was recorded in forever ([WheelSnapshot.EffectivePillarSet]); it is never
// migrated to a newer one.
const CurrentWheelPillarSet = 2

// WheelPillar is one pillar of a pillar set (wheel.md §2): the short Key the
// flags and JSON carry, and the Label human output prints.
type WheelPillar struct {
	Key   string
	Label string
}

// WheelPillarSplit is one pillar-set change entry (wheel.md §2): pillar set Set
// retired the previous set's Retired key and replaced it with Into, in the
// slot Retired held and in canonical order.
type WheelPillarSplit struct {
	Set     int
	Retired string
	Into    []string
}

// wheelPillarSetSpec is one row of the pillar-set table: the set's pillars in
// canonical order, plus the change entries that distinguish it from the set
// before it (none for set 1).
type wheelPillarSetSpec struct {
	pillars []WheelPillar
	splits  []WheelPillarSplit
}

// wheelPillarSetSpecOf is the one pillar-set table (wheel.md §2): set number →
// its ordered pillars and its change entries. Every pillar-set lookup, the
// validation of a snapshot in its own set, the retired-key refusal, and the
// `show` boundary line derive from it. It builds fresh values on every call, so
// no caller can mutate another's view, and it holds no package-level state.
func wheelPillarSetSpecOf(set int) (wheelPillarSetSpec, bool) {
	switch set {
	case 1:
		return wheelPillarSetSpec{pillars: []WheelPillar{
			{Key: PillarHealth, Label: "health"},
			{Key: PillarRelationships, Label: "relationships"},
			{Key: PillarCareer, Label: "career/work"},
			{Key: PillarFinances, Label: "finances"},
			{Key: PillarGrowth, Label: "personal growth"},
			{Key: PillarFun, Label: "fun/recreation"},
			{Key: PillarEnvironment, Label: "environment"},
			{Key: PillarContribution, Label: "contribution"},
		}}, true
	case 2:
		return wheelPillarSetSpec{
			pillars: []WheelPillar{
				{Key: PillarHealth, Label: "health"},
				{Key: PillarRomance, Label: "romance/partner"},
				{Key: PillarFriends, Label: "friends & family"},
				{Key: PillarCareer, Label: "career/work"},
				{Key: PillarFinances, Label: "finances"},
				{Key: PillarGrowth, Label: "personal growth"},
				{Key: PillarFun, Label: "fun/recreation"},
				{Key: PillarEnvironment, Label: "environment"},
				{Key: PillarContribution, Label: "contribution"},
			},
			splits: []WheelPillarSplit{
				{Set: 2, Retired: PillarRelationships, Into: []string{PillarRomance, PillarFriends}},
			},
		}, true
	default:
		return wheelPillarSetSpec{}, false
	}
}

// KnownWheelPillarSet reports whether set is a pillar set this build knows. A
// snapshot recorded in a set this build does not know (a newer build's) is
// refused rather than read in the wrong set.
func KnownWheelPillarSet(set int) bool {
	_, ok := wheelPillarSetSpecOf(set)
	return ok
}

// WheelPillarsOf returns pillar set set's pillars in its canonical order
// (wheel.md §2), or ok=false for a set this build does not know. It returns a
// fresh slice, so no caller can reorder another's view.
func WheelPillarsOf(set int) ([]WheelPillar, bool) {
	spec, ok := wheelPillarSetSpecOf(set)
	if !ok {
		return nil, false
	}
	return spec.pillars, true
}

// WheelPillarKeysOf returns pillar set set's keys in its canonical order, or
// ok=false for a set this build does not know.
func WheelPillarKeysOf(set int) ([]string, bool) {
	pillars, ok := WheelPillarsOf(set)
	if !ok {
		return nil, false
	}
	keys := make([]string, len(pillars))
	for i, p := range pillars {
		keys[i] = p.Key
	}
	return keys, true
}

// WheelPillars returns the current pillar set's pillars in canonical order
// (wheel.md §2) — the order human output, `--json` arrays, and tie-breaks all
// follow. It returns a fresh slice, so no caller can reorder another's view.
func WheelPillars() []WheelPillar {
	pillars, _ := WheelPillarsOf(CurrentWheelPillarSet)
	return pillars
}

// WheelPillarKeys returns the current pillar set's keys in canonical order.
func WheelPillarKeys() []string {
	keys, _ := WheelPillarKeysOf(CurrentWheelPillarSet)
	return keys
}

// WheelPillarLabel returns a pillar key's human label from any pillar set this
// build knows (a key's label is stable across sets), or ok=false for a key that
// no set names.
func WheelPillarLabel(key string) (label string, ok bool) {
	for set := 1; KnownWheelPillarSet(set); set++ {
		pillars, _ := WheelPillarsOf(set)
		for _, p := range pillars {
			if p.Key == key {
				return p.Label, true
			}
		}
	}
	return "", false
}

// IsWheelPillar reports whether key is a pillar of the current pillar set.
func IsWheelPillar(key string) bool {
	return IsWheelPillarIn(CurrentWheelPillarSet, key)
}

// IsWheelPillarIn reports whether key is a pillar of pillar set set — false
// for any key when the set is unknown to this build.
func IsWheelPillarIn(set int, key string) bool {
	keys, ok := WheelPillarKeysOf(set)
	return ok && slices.Contains(keys, key)
}

// WheelPillarRetirement returns the change entry that retired key, relative to
// the current pillar set: ok is true only when key is not a pillar of the
// current set and a set up to the current one split it (wheel.md §2). A key
// that is current, or that no set ever retired, reports ok=false.
func WheelPillarRetirement(key string) (WheelPillarSplit, bool) {
	return wheelPillarRetirementAt(CurrentWheelPillarSet, key)
}

// RetiredWheelPillar returns the keys that replaced a retired pillar key in the
// current pillar set, in canonical order, or ok=false when key is not retired
// (see [WheelPillarRetirement]).
func RetiredWheelPillar(key string) (into []string, ok bool) {
	split, ok := WheelPillarRetirement(key)
	if !ok {
		return nil, false
	}
	return split.Into, true
}

// wheelPillarRetirementAt is [WheelPillarRetirement] relative to pillar set
// current, so the lookup is testable on either side of a set change.
func wheelPillarRetirementAt(current int, key string) (WheelPillarSplit, bool) {
	if !KnownWheelPillarSet(current) || IsWheelPillarIn(current, key) {
		return WheelPillarSplit{}, false
	}
	for set := current; set >= 1; set-- {
		spec, _ := wheelPillarSetSpecOf(set)
		for _, split := range spec.splits {
			if split.Retired == key {
				return split, true
			}
		}
	}
	return WheelPillarSplit{}, false
}

// WheelPillarSetChange returns how pillar set to differs from pillar set from
// (wheel.md §7.2): retired holds the keys of from that to lacks, in from's
// canonical order; added holds the keys of to that from lacks, in to's
// canonical order. Equal sets — or either set unknown to this build — report
// no change.
func WheelPillarSetChange(from, to int) (retired, added []string) {
	fromKeys, okFrom := WheelPillarKeysOf(from)
	toKeys, okTo := WheelPillarKeysOf(to)
	if !okFrom || !okTo || from == to {
		return nil, nil
	}
	for _, key := range fromKeys {
		if !slices.Contains(toKeys, key) {
			retired = append(retired, key)
		}
	}
	for _, key := range toKeys {
		if !slices.Contains(fromKeys, key) {
			added = append(added, key)
		}
	}
	return retired, added
}

// ValidWheelScore reports whether n is on the wheel's own 1–10 scale.
func ValidWheelScore(n int) bool {
	return n >= WheelScoreMin && n <= WheelScoreMax
}

// ParseWheelScore parses a rating as typed (a flag value, or a JSON number's
// raw text) onto the 1–10 scale (wheel.md §3). Only a plain whole number is a
// rating: surrounding spaces are tolerated, but a decimal (6.5, 6.0), a quoted
// JSON string ("6"), a sign, an exponent, and anything outside 1–10 are not —
// ok is false and the caller refuses the write.
func ParseWheelScore(raw string) (score int, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || !ValidWheelScore(n) {
		return 0, false
	}
	return n, true
}

// PillarScore is one pillar in a snapshot (wheel.md §4): your own rating
// (Score, 1–10 — the only number the trend reads), an optional one-line note in
// your words, and an optional Suggested calibration value kept in a SEPARATE
// field (wheel.md §5) — never the score, never a default for a missing score,
// never folded into the trend.
type PillarScore struct {
	Score     int    `json:"score"`
	Note      string `json:"note,omitempty"`
	Suggested *int   `json:"suggested,omitempty"`
}

// WheelSnapshot is one append-only event in a wheel entry's history (wheel.md
// §4): a whole wheel recorded by one write. ID is its receipt id, At the real
// write time, PillarSet the pillar set it was rated in (absent — zero — means
// set 1, every snapshot written before pillar set 2; see
// [WheelSnapshot.EffectivePillarSet]), Pillars every rating of that set;
// VisionReviewed and VisionReflection are the vision-review fields (wheel.md
// §6) — the binary stores the reflection and the yes/no flag, never a link to
// any vision document.
type WheelSnapshot struct {
	ID               string                 `json:"id"`
	At               string                 `json:"at"`
	Type             string                 `json:"type"`
	PillarSet        int                    `json:"pillar_set,omitempty"`
	Pillars          map[string]PillarScore `json:"pillars"`
	VisionReviewed   bool                   `json:"vision_reviewed"`
	VisionReflection string                 `json:"vision_reflection,omitempty"`
}

// WheelEntry is one month's wheel (wheel.md §4): one JSON object, one file per
// month, under registries/wheel/, keyed by [WheelEntryKey]. The month's current
// wheel is DERIVED from History ([WheelEntry.Fold]) and never stored. Field
// order matches the documented schema so a marshaled entry reads like the spec.
type WheelEntry struct {
	Key       string          `json:"key"`
	Kind      string          `json:"kind"`
	Schema    int             `json:"schema"`
	Month     string          `json:"month"`
	History   []WheelSnapshot `json:"history"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// NewWheelEntry builds a fresh wheel record for a month's first write — the
// entry itself, with no snapshots yet; the caller appends the first one.
func NewWheelEntry(month, at string) WheelEntry {
	return WheelEntry{
		Key:       WheelEntryKey(month),
		Kind:      RegistryWheel,
		Schema:    WheelSchema,
		Month:     month,
		History:   []WheelSnapshot{},
		CreatedAt: at,
		UpdatedAt: at,
	}
}

// Fold returns the month's current wheel (wheel.md §4 "Deriving the month"):
// the latest snapshot appended — amend by append, latest wins. An event of a
// type this build does not know is skipped (read what you understand). ok is
// false when the entry holds no snapshot. The fold is pure and rebuildable from
// the events alone; nothing derived is stored.
func (e WheelEntry) Fold() (WheelSnapshot, bool) {
	for i := len(e.History) - 1; i >= 0; i-- {
		if e.History[i].Type == WheelEventSnapshot {
			return e.History[i], true
		}
	}
	return WheelSnapshot{}, false
}

// SnapshotCount returns how many snapshots the month holds — 1 for a month
// recorded once, more when it was amended by append.
func (e WheelEntry) SnapshotCount() int {
	n := 0
	for _, s := range e.History {
		if s.Type == WheelEventSnapshot {
			n++
		}
	}
	return n
}

// Normalized returns a copy whose History is non-nil so the written record
// always carries [] rather than null — a stable on-disk shape.
func (e WheelEntry) Normalized() WheelEntry {
	if e.History == nil {
		e.History = []WheelSnapshot{}
	}
	return e
}

// Validate reports the first structural problem before an entry is written. It
// refuses a schema this build cannot write back faithfully, a wrong kind, a
// malformed month, and a key that is not the month's own key.
func (e WheelEntry) Validate() error {
	if e.Schema < 1 || e.Schema > WheelSchema {
		return fmt.Errorf("observations: unsupported wheel schema %d (want 1–%d)", e.Schema, WheelSchema)
	}
	if e.Kind != RegistryWheel {
		return fmt.Errorf("observations: wheel entry has wrong kind %q", e.Kind)
	}
	if !ValidWheelMonth(e.Month) {
		return fmt.Errorf("observations: wheel entry has malformed month %q (want YYYY-MM)", e.Month)
	}
	if e.Key != WheelEntryKey(e.Month) {
		return fmt.Errorf("observations: wheel entry key %q does not match month %q", e.Key, e.Month)
	}
	return nil
}

// EffectivePillarSet returns the pillar set the snapshot was rated in
// (wheel.md §4): an absent pillar_set reads as set 1, so every snapshot written
// before pillar set 2 reads unchanged with no migration.
func (s WheelSnapshot) EffectivePillarSet() int {
	if s.PillarSet == 0 {
		return 1
	}
	return s.PillarSet
}

// Validate reports the first problem with a snapshot's wheel against the
// snapshot's OWN pillar set (wheel.md §3, §5): a pillar set this build does not
// know, a pillar that set does not name, any of that set's pillars missing — a
// missing rating is refused, never filled from a prior month, a suggestion, or
// a default — a rating off the 1–10 scale, or a suggestion off it.
func (s WheelSnapshot) Validate() error {
	set := s.EffectivePillarSet()
	keys, ok := WheelPillarKeysOf(set)
	if !ok {
		return fmt.Errorf("observations: wheel snapshot pillar set %d is not known to this build", set)
	}
	if unknown := UnknownWheelPillarsIn(set, s.Pillars); len(unknown) > 0 {
		return fmt.Errorf("observations: wheel snapshot names unknown pillar(s) %s", strings.Join(unknown, ", "))
	}
	if missing := MissingWheelPillarsIn(set, s.Pillars); len(missing) > 0 {
		return fmt.Errorf("observations: wheel snapshot is missing rating(s) for %s", strings.Join(missing, ", "))
	}
	for _, key := range keys {
		p := s.Pillars[key]
		if !ValidWheelScore(p.Score) {
			return fmt.Errorf("observations: wheel rating for %s is %d (want %d–%d)", key, p.Score, WheelScoreMin, WheelScoreMax)
		}
		if p.Suggested != nil && !ValidWheelScore(*p.Suggested) {
			return fmt.Errorf("observations: wheel suggestion for %s is %d (want %d–%d)", key, *p.Suggested, WheelScoreMin, WheelScoreMax)
		}
	}
	return nil
}

// MissingWheelPillars returns the current pillar set's keys absent from
// pillars, in canonical order — empty when every pillar is present.
func MissingWheelPillars(pillars map[string]PillarScore) []string {
	return MissingWheelPillarsIn(CurrentWheelPillarSet, pillars)
}

// MissingWheelPillarsIn returns pillar set set's keys absent from pillars, in
// that set's canonical order — empty when every pillar of the set is present,
// and empty for a set this build does not know (the set itself is refused
// first; see [WheelSnapshot.Validate]).
func MissingWheelPillarsIn(set int, pillars map[string]PillarScore) []string {
	keys, _ := WheelPillarKeysOf(set)
	var missing []string
	for _, key := range keys {
		if _, ok := pillars[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}

// UnknownWheelPillars returns the keys in pillars that are not pillars of the
// current pillar set, sorted — empty when every key is a pillar.
func UnknownWheelPillars(pillars map[string]PillarScore) []string {
	return UnknownWheelPillarsIn(CurrentWheelPillarSet, pillars)
}

// UnknownWheelPillarsIn returns the keys in pillars that pillar set set does
// not name, sorted — empty when every key is a pillar of the set. For a set
// this build does not know, every key is unknown.
func UnknownWheelPillarsIn(set int, pillars map[string]PillarScore) []string {
	var unknown []string
	for _, key := range sortedPillarKeys(pillars) {
		if !IsWheelPillarIn(set, key) {
			unknown = append(unknown, key)
		}
	}
	return unknown
}

// WheelScores returns a snapshot's self-ratings as a pillar key → score map —
// the ratings only, never a suggestion (wheel.md §5).
func (s WheelSnapshot) WheelScores() map[string]int {
	out := make(map[string]int, len(s.Pillars))
	for key, p := range s.Pillars {
		out[key] = p.Score
	}
	return out
}

// ValidWheelMonth reports whether month is a well-formed calendar month key,
// exactly YYYY-MM (wheel.md §4).
func ValidWheelMonth(month string) bool {
	t, err := time.Parse(wheelMonthLayout, month)
	return err == nil && t.Format(wheelMonthLayout) == month
}

// WheelMonthOf returns the calendar month of now's logical day (the 04:00
// rollover the rest of the Ledger uses), so a review finished just after
// midnight on the 1st still files under the month it reviewed (wheel.md §7.1).
func WheelMonthOf(now time.Time) string {
	return LogicalBaseDate(now, DefaultRolloverMin).Format(wheelMonthLayout)
}

// WheelEntryKey renders the stable entry id for a month (wheel.md §4 Ids):
// wheel_YYYY-MM, e.g. wheel_2026-09 — one per month, stable forever.
func WheelEntryKey(month string) string {
	return "wheel_" + month
}

// WheelReceiptID renders the receipt id for a month's snapshot (wheel.md §4
// Ids): wheel_YYYY_MM_<seq>, the month in underscores and seq zero-padded to
// three digits (wider values legal). It never collides with the entry key,
// which separates year and month with a hyphen and has no sequence.
func WheelReceiptID(month string, seq int) string {
	return fmt.Sprintf("wheel_%s_%03d", strings.ReplaceAll(month, "-", "_"), seq)
}

// ParseWheelReceiptSeq extracts the numeric sequence from a wheel receipt id,
// parsed numerically so a wider value (wheel_2026_09_1000) is legal. It returns
// ok=false for anything that is not a well-formed wheel receipt — including an
// entry key — so the single-writer seq derivation ignores such an event rather
// than counting it.
func ParseWheelReceiptSeq(id string) (seq int, ok bool) {
	rest, found := strings.CutPrefix(id, "wheel_")
	if !found {
		return 0, false
	}
	parts := strings.Split(rest, "_")
	if len(parts) != 3 || !ValidWheelMonth(parts[0]+"-"+parts[1]) {
		return 0, false
	}
	for _, c := range parts[2] {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, false
	}
	return n, true
}

// NextWheelSeq returns max-seq+1 over the entry's history (wheel.md §4: never a
// count, single-writer). A fresh entry starts at seq 1; an event whose id is not
// a well-formed receipt is ignored, so a hand-edited line never perturbs id
// assignment.
func NextWheelSeq(history []WheelSnapshot) int {
	maxSeq := 0
	for _, s := range history {
		if n, ok := ParseWheelReceiptSeq(s.ID); ok && n > maxSeq {
			maxSeq = n
		}
	}
	return maxSeq + 1
}

// sortedPillarKeys returns a pillar map's keys in sorted order — a determinism
// helper for messages that must iterate the map.
func sortedPillarKeys(m map[string]PillarScore) []string {
	return slices.Sorted(maps.Keys(m))
}
