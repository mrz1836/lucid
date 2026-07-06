package router

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/storage"
)

// The fixed /person copy (error-states.md §P-1/§P-2/§P-3). None of it is
// agent-authored — /person is a deterministic, no-LLM join.
const (
	personNoMatch    = "No one by that name yet — people appear here as you mention them."
	personDateLayout = "2006-01-02"
)

// PersonCandidate is one row of the P-2 disambiguation list: the record's key,
// its display name, and when it was first seen.
type PersonCandidate struct {
	PersonKey   string
	DisplayName string
	FirstSeenAt time.Time
}

// PersonResult reports what /person resolved. Matched is false for the empty
// state (§P-1) and the multi-match state (§P-2, with Candidates set). OffLimits
// marks the redacted raw-record-only view (§P-3). Text is the full rendered,
// byte-stable output the user sees — the same input always renders the same
// bytes (S-22).
type PersonResult struct {
	Query           string
	Matched         bool
	MultipleMatches bool
	Candidates      []PersonCandidate
	OffLimits       bool
	PersonKey       string
	Text            string
}

// PersonRequest carries the one input for a /person turn: the queried name.
type PersonRequest struct {
	Name string
}

// Person executes /person <name>: a deterministic, no-LLM join over the people
// record, its mention counts, the accepted insights that cite entries
// mentioning them, and its dominance share (agent-contracts.md §"How contracts
// compose"; scope.md §4). It never calls a model and never writes. No match
// returns the empty state (§P-1); several matches list the candidates (§P-2); a
// match named in the off-limits registry renders the raw record only, behind
// the standing header, with nothing derived (§P-3). The output is byte-stable
// across repeated runs on the same store (S-22).
func (r *Router) Person(req PersonRequest) (PersonResult, error) {
	query := strings.TrimSpace(req.Name)
	matches, err := r.matchPeople(query)
	if err != nil {
		return PersonResult{}, err
	}

	switch len(matches) {
	case 0:
		return PersonResult{Query: query, Text: personNoMatch}, nil
	case 1:
		return r.renderPerson(query, matches[0])
	default:
		return r.renderCandidates(query, matches), nil
	}
}

// matchPeople returns every people record whose display_name or an aka variant
// equals the query (case-insensitive), sorted by person_key so the candidate
// order — and any output built from it — is deterministic.
func (r *Router) matchPeople(query string) ([]storage.PersonRecord, error) {
	keys, err := r.store.ListPeopleKeys()
	if err != nil {
		return nil, fmt.Errorf("person: list people: %w", err)
	}
	var matches []storage.PersonRecord
	for _, key := range keys {
		rec, found, err := r.store.ReadPerson(key)
		if err != nil {
			return nil, fmt.Errorf("person: read %q: %w", key, err)
		}
		if found && recordMatchesName(rec, query) {
			matches = append(matches, rec)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].PersonKey < matches[j].PersonKey })
	return matches, nil
}

// recordMatchesName reports whether the queried name equals the record's
// display_name or any of its aka variants, case-insensitively.
func recordMatchesName(rec storage.PersonRecord, query string) bool {
	if strings.EqualFold(strings.TrimSpace(rec.DisplayName), query) {
		return true
	}
	for _, aka := range rec.Aka {
		if strings.EqualFold(strings.TrimSpace(aka), query) {
			return true
		}
	}
	return false
}

// renderCandidates builds the P-2 disambiguation result: the candidates listed
// by display name and first-seen date, nothing else rendered.
func (r *Router) renderCandidates(query string, matches []storage.PersonRecord) PersonResult {
	cands := make([]PersonCandidate, 0, len(matches))
	names := make([]string, 0, len(matches))
	for _, rec := range matches {
		cands = append(cands, PersonCandidate{PersonKey: rec.PersonKey, DisplayName: rec.DisplayName, FirstSeenAt: rec.FirstSeenAt})
		names = append(names, fmt.Sprintf("%s (first seen %s)", rec.DisplayName, rec.FirstSeenAt.Format(personDateLayout)))
	}
	text := "That matches more than one person — which did you mean: " + strings.Join(names, "; ") + "?"
	return PersonResult{Query: query, MultipleMatches: true, Candidates: cands, Text: text}
}

// renderPerson renders the single-match view. An off-limits person gets the
// §P-3 raw-record-only view (mentions and dates, nothing derived); everyone
// else gets the full deterministic join: the record header, mention counts, the
// accepted insights citing entries that mention them, and a dominance line only
// when their share of entries exceeds person_dominance_threshold.
func (r *Router) renderPerson(query string, rec storage.PersonRecord) (PersonResult, error) {
	offLimits, err := r.store.ReadOffLimitsPersonKeys()
	if err != nil {
		return PersonResult{}, fmt.Errorf("person: read off-limits: %w", err)
	}
	if toSet(offLimits)[rec.PersonKey] {
		return PersonResult{
			Query: query, Matched: true, OffLimits: true, PersonKey: rec.PersonKey, Text: renderOffLimits(rec),
		}, nil
	}

	insightIDs, err := r.insightsCiting(rec)
	if err != nil {
		return PersonResult{}, err
	}
	dominance, err := r.personDominanceLine(rec)
	if err != nil {
		return PersonResult{}, err
	}

	var b strings.Builder
	b.WriteString(rec.DisplayName + "\n")
	b.WriteString(mentionSummary(rec) + "\n")
	if len(insightIDs) > 0 {
		b.WriteString("Referenced by accepted insights: " + strings.Join(insightIDs, ", ") + "\n")
	} else {
		b.WriteString("No accepted insights reference them yet.\n")
	}
	if dominance != "" {
		b.WriteString(dominance + "\n")
	}
	return PersonResult{Query: query, Matched: true, PersonKey: rec.PersonKey, Text: strings.TrimRight(b.String(), "\n")}, nil
}

// renderOffLimits builds the §P-3 raw-record-only view: the standing header,
// the mention summary, and the raw entry ids — mentions and dates, nothing
// derived (no insights, no dominance).
func renderOffLimits(rec storage.PersonRecord) string {
	var b strings.Builder
	b.WriteString(rec.DisplayName + " is off-limits to inference — what follows is your raw record only: " +
		"mentions and dates, nothing derived.\n")
	b.WriteString(mentionSummary(rec) + "\n")
	if len(rec.EntryRefs) > 0 {
		b.WriteString("Entries: " + strings.Join(rec.EntryRefs, ", ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// mentionSummary renders the deterministic mention-count-over-time line: how
// many entries mention the person and the first/last seen dates.
func mentionSummary(rec storage.PersonRecord) string {
	return fmt.Sprintf("Mentioned in %d entr%s (first seen %s, last seen %s).",
		len(rec.EntryRefs), plural(len(rec.EntryRefs)),
		rec.FirstSeenAt.Format(personDateLayout), rec.LastSeenAt.Format(personDateLayout))
}

// plural returns the "y"/"ies" suffix so the mention line reads naturally for
// one entry versus many, without changing byte-stability for a fixed count.
func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// insightsCiting returns the ids of every accepted insight whose provenance
// cites a raw entry that mentions this person — the deterministic join scope.md
// §4 specifies. Ids are sorted so the output is byte-stable.
func (r *Router) insightsCiting(rec storage.PersonRecord) ([]string, error) {
	refs := toSet(rec.EntryRefs)
	ids, err := r.store.ListInsightIDs()
	if err != nil {
		return nil, fmt.Errorf("person: list insights: %w", err)
	}
	var citing []string
	for _, id := range ids {
		ins, err := r.store.ReadInsight(id)
		if err != nil {
			return nil, fmt.Errorf("person: read insight %q: %w", id, err)
		}
		if ins.Status != storage.InsightStatusAccepted {
			continue
		}
		for _, raw := range ins.Provenance.RawEntryIDs {
			if refs[raw] {
				citing = append(citing, id)
				break
			}
		}
	}
	sort.Strings(citing)
	return citing, nil
}

// personDominanceLine returns the one dominance line for this person, or "" when
// their share of processed entries does not exceed person_dominance_threshold.
// Share is the fraction of processed artifacts that mention them (deterministic,
// router-side). The line is hypothesis-framed and appears only in /person and
// /reflect gate — never on /status or a daily surface (S-22).
func (r *Router) personDominanceLine(rec storage.PersonRecord) (string, error) {
	ids, err := r.store.ListProcessedIDs()
	if err != nil {
		return "", fmt.Errorf("person: list processed: %w", err)
	}
	total := len(ids)
	if total == 0 {
		return "", nil
	}
	mentions := 0
	for _, id := range ids {
		art, err := r.store.ReadProcessed(id)
		if err != nil {
			return "", fmt.Errorf("person: read processed %q: %w", id, err)
		}
		for _, p := range art.People {
			if p.PersonKey == rec.PersonKey {
				mentions++
				break
			}
		}
	}
	share := float64(mentions) / float64(total)
	if share <= r.cfg.PersonDominanceThreshold {
		return "", nil
	}
	pct := int(share*100 + 0.5)
	return fmt.Sprintf("%s appears in %d%% of entries — worth a look, or expected?", rec.DisplayName, pct), nil
}
