package router

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/identity"
	"github.com/mrz1836/lucid/internal/storage"
)

// The fixed /person copy (error-states.md §P-1/§P-2/§P-3). None of it is
// agent-authored — /person is a deterministic, no-LLM join.
const (
	personNoMatch    = "No one by that name yet — people appear here as you mention them."
	personDateLayout = "2006-01-02"
)

// PersonCandidate is one row of the P-2 disambiguation list: the record's key,
// its display name, when it was first seen, and whether they are dormant
// (ranked last and labeled).
type PersonCandidate struct {
	PersonKey   string
	DisplayName string
	FirstSeenAt time.Time
	Dormant     bool
}

// PersonResult reports what /person resolved. Matched is false for the empty
// state (§P-1) and the multi-match state (§P-2, with Candidates set). OffLimits
// marks the redacted raw-record-only view (§P-3). Contact is the person's
// check-in summary (ADR-0014 §6), nil when they have no contact events — it is
// set in the off-limits view too, since it is the user's own record. Text is
// the full rendered, byte-stable output the user sees — the same input always
// renders the same bytes (S-22). Dormant marks a matched person who is dormant
// (their view carries the "Dormant since" line), and DormantAlso names the
// dormant people who also hold the queried form when the name resolved to an
// active person — both empty on a Ledger with no dormant people.
type PersonResult struct {
	Query           string
	Matched         bool
	MultipleMatches bool
	Candidates      []PersonCandidate
	OffLimits       bool
	PersonKey       string
	Dormant         bool
	DormantAlso     []PersonRef
	Contact         *PersonContact
	Text            string
}

// PersonRequest carries the one input for a /person turn: the queried name.
type PersonRequest struct {
	Name string
}

// Person executes /person <name-or-key>: a deterministic, no-LLM join over the
// people record, its mention counts, the accepted insights that cite entries
// mentioning them, and its dominance share (agent-contracts.md §"How contracts
// compose"; scope.md §4). It never calls a model and never writes. The query
// resolves to who it means now ([Router.resolvePersonNow]): an exact person_key
// wins (a tombstone key resolves forward to its canonical), then the form's
// active claimant, then the one active display_name / aka[] holder — a dormant
// person never beats an active one — then, when every holder is dormant, the
// first-name rule or the lone dormant holder, labeled. No match returns the
// empty state (§P-1); an ambiguous name lists the candidates, active first and
// dormant ones last and labeled (§P-2); a match named in the off-limits
// registry renders the raw record only, behind the standing header, with
// nothing derived (§P-3). The output is byte-stable across repeated runs on the
// same store (S-22).
func (r *Router) Person(req PersonRequest) (PersonResult, error) {
	query := strings.TrimSpace(req.Name)
	if query == "" {
		return PersonResult{Query: query, Text: personNoMatch}, nil
	}
	subj, err := r.resolvePersonNow(query)
	switch {
	case err == nil:
		return r.renderPerson(query, subj)
	case errors.Is(err, ErrPersonAmbiguousSubject):
		return renderCandidates(query, subj.Candidates), nil
	case errors.Is(err, ErrPersonUnknownSubject):
		return PersonResult{Query: query, Text: personNoMatch}, nil
	}
	return PersonResult{}, err
}

// renderCandidates builds the P-2 disambiguation result: the candidates listed
// by display name and first-seen date — active people first, then dormant
// ones, each labeled — nothing else rendered.
func renderCandidates(query string, matches []PersonMatch) PersonResult {
	cands := make([]PersonCandidate, 0, len(matches))
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		rec := m.Record
		cands = append(cands, PersonCandidate{
			PersonKey: rec.PersonKey, DisplayName: rec.DisplayName, FirstSeenAt: rec.FirstSeenAt, Dormant: m.Dormant,
		})
		label := ""
		if m.Dormant {
			label = ", dormant"
		}
		names = append(names, fmt.Sprintf("%s (first seen %s%s)", rec.DisplayName, rec.FirstSeenAt.Format(personDateLayout), label))
	}
	text := "That matches more than one person — which did you mean: " + strings.Join(names, "; ") + "?"
	return PersonResult{Query: query, MultipleMatches: true, Candidates: cands, Text: text}
}

// renderPerson renders the single-match view. An off-limits person gets the
// §P-3 raw-record-only view (mentions and dates, nothing derived); everyone
// else gets the full deterministic join: the record header, mention counts, the
// accepted insights citing entries that mention them, and a dominance line only
// when their share of entries exceeds person_dominance_threshold. Both views
// add the check-in line after the mention counts when the person has contact
// events (ADR-0014 §6) — it is the user's own record, not an inference — and
// the identity lines right after their first line: "Dormant since …" for a
// dormant person, or the line naming the dormant people who also hold the
// queried form. Neither line renders on a Ledger with no dormant people.
func (r *Router) renderPerson(query string, subj PersonSubject) (PersonResult, error) {
	rec := subj.Record
	identityLines := personIdentityLines(query, subj)
	also := make([]PersonRef, 0, len(subj.DormantAlso))
	for _, m := range subj.DormantAlso {
		also = append(also, PersonRef{PersonKey: m.Record.PersonKey, DisplayName: m.Record.DisplayName})
	}
	if len(also) == 0 {
		also = nil
	}
	offLimits, err := r.store.ReadOffLimitsPersonKeys()
	if err != nil {
		return PersonResult{}, fmt.Errorf("person: read off-limits: %w", err)
	}
	checkIns, err := r.personContact(rec.PersonKey)
	if err != nil {
		return PersonResult{}, err
	}
	// The person's references on read: their entry_refs with every pin and
	// move-ref folded in (data-model.md §"Attribution on read").
	refs, err := r.store.FoldedEntryRefs(rec)
	if err != nil {
		return PersonResult{}, fmt.Errorf("person: fold entry refs for %q: %w", rec.PersonKey, err)
	}
	if toSet(offLimits)[rec.PersonKey] {
		return PersonResult{
			Query: query, Matched: true, OffLimits: true, PersonKey: rec.PersonKey,
			Dormant: subj.Dormant, DormantAlso: also, Contact: checkIns,
			Text: renderOffLimits(rec, refs, checkIns, identityLines),
		}, nil
	}

	insightIDs, err := r.insightsCiting(refs)
	if err != nil {
		return PersonResult{}, err
	}
	dominance, err := r.personDominanceLine(rec)
	if err != nil {
		return PersonResult{}, err
	}

	var b strings.Builder
	b.WriteString(rec.DisplayName + "\n")
	writeLines(&b, identityLines)
	b.WriteString(mentionSummary(rec, refs) + "\n")
	writeCheckInLine(&b, checkIns)
	if len(insightIDs) > 0 {
		b.WriteString("Referenced by accepted insights: " + strings.Join(insightIDs, ", ") + "\n")
	} else {
		b.WriteString("No accepted insights reference them yet.\n")
	}
	if dominance != "" {
		b.WriteString(dominance + "\n")
	}
	linked, err := r.personLinkedMediaLine(rec)
	if err != nil {
		return PersonResult{}, err
	}
	if linked != "" {
		b.WriteString(linked + "\n")
	}
	return PersonResult{
		Query: query, Matched: true, PersonKey: rec.PersonKey,
		Dormant: subj.Dormant, DormantAlso: also, Contact: checkIns,
		Text: strings.TrimRight(b.String(), "\n"),
	}, nil
}

// personIdentityLines returns the identity lines a person view adds right
// after its first line (usage/commands.md §person): "Dormant since
// YYYY-MM-DD." — or "Dormant since YYYY-MM-DD — <reason>" — for a dormant
// person, and, when the query resolved to an active person, one line naming
// every dormant person who also holds the queried form. Both are empty for
// everyone on a Ledger with no dormant people, so such a view renders exactly
// as before (S-22).
func personIdentityLines(query string, subj PersonSubject) []string {
	var lines []string
	if subj.Dormant {
		lines = append(lines, dormantSinceLine(subj.Status))
	}
	if len(subj.DormantAlso) > 0 {
		lines = append(lines, dormantAlsoLine(query, subj.DormantAlso))
	}
	return lines
}

// dormantSinceLine renders a dormant person's status line from their folded
// status event: the day it was recorded, and the one-line reason when one was
// given.
func dormantSinceLine(fact identity.StatusFact) string {
	if reason := strings.TrimSpace(fact.Reason); reason != "" {
		return "Dormant since " + fact.Date + " — " + reason
	}
	return "Dormant since " + fact.Date + "."
}

// dormantAlsoLine names the dormant people who also hold the queried form:
// `"Sam" also names a dormant person: "Sam" (person_s-oak).`, or with several,
// `… also names dormant people: "Sam" (person_s-oak); "Sam" (person_s-oak-2).`
func dormantAlsoLine(query string, also []PersonMatch) string {
	names := make([]string, 0, len(also))
	for _, m := range also {
		names = append(names, fmt.Sprintf("%q (%s)", m.Record.DisplayName, m.Record.PersonKey))
	}
	noun := "a dormant person"
	if len(also) > 1 {
		noun = "dormant people"
	}
	return fmt.Sprintf("%q also names %s: %s.", query, noun, strings.Join(names, "; "))
}

// writeLines writes each line followed by a newline.
func writeLines(b *strings.Builder, lines []string) {
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
}

// writeCheckInLine adds the person's check-in line to a view, or nothing when
// they have no contact events — so their view renders byte-identically to
// before (S-22).
func writeCheckInLine(b *strings.Builder, checkIns *PersonContact) {
	if checkIns != nil {
		b.WriteString(checkIns.Line + "\n")
	}
}

// personLinkedMediaLine renders the "Media linked to them" line: the media
// associated with this person by an explicit link (SC-9), listed by stored id in
// the read seam's byte-stable order. The wording says "linked" so it reads
// distinctly from a same-day coincidence join, and the line renders only when at
// least one link exists — so a person with no linked media renders
// byte-identically to before (S-22). The off-limits view returns before this is
// ever reached, so a redacted person never surfaces derived media.
func (r *Router) personLinkedMediaLine(rec storage.PersonRecord) (string, error) {
	media, err := r.SubjectMedia("person", rec.PersonKey)
	if err != nil {
		return "", fmt.Errorf("person: linked media for %q: %w", rec.PersonKey, err)
	}
	if len(media) == 0 {
		return "", nil
	}
	ids := make([]string, len(media))
	for i, m := range media {
		ids[i] = m.Media.ID
	}
	return "Media linked to them: " + strings.Join(ids, ", "), nil
}

// renderOffLimits builds the §P-3 raw-record-only view: the standing header,
// the identity lines (the user's own curation, not an inference), the mention
// summary, the check-in line when there are contact events (the user's own
// record), and the raw entry ids (refs, the folded references) — mentions and
// dates, nothing derived (no insights, no dominance).
func renderOffLimits(rec storage.PersonRecord, refs []string, checkIns *PersonContact, identityLines []string) string {
	var b strings.Builder
	b.WriteString(rec.DisplayName + " is off-limits to inference — what follows is your raw record only: " +
		"mentions and dates, nothing derived.\n")
	writeLines(&b, identityLines)
	b.WriteString(mentionSummary(rec, refs) + "\n")
	writeCheckInLine(&b, checkIns)
	if len(refs) > 0 {
		b.WriteString("Entries: " + strings.Join(refs, ", ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// mentionSummary renders the deterministic mention-count-over-time line: how
// many entries mention the person (refs, their folded references) and the
// record's own first/last seen dates, which the fold never re-derives.
func mentionSummary(rec storage.PersonRecord, refs []string) string {
	return fmt.Sprintf("Mentioned in %d entr%s (first seen %s, last seen %s).",
		len(refs), plural(len(refs)),
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
// cites a raw entry that mentions this person — one of entryRefs, their folded
// references — the deterministic join scope.md §4 specifies. Ids are sorted so
// the output is byte-stable.
func (r *Router) insightsCiting(entryRefs []string) ([]string, error) {
	refs := toSet(entryRefs)
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
	slices.Sort(citing)
	return citing, nil
}

// personRedirectIndex reads the people tree once and returns, for every person
// key, the canonical key it resolves to (canon), plus the current display name
// of each canonical record (names). The /reflect gate panel renders a counted
// person by their canonical display name from it; the counts themselves come
// from the storage mention fold, which canonicalizes the historical
// processed/*.json keys (an artifact keeps the pre-merge person_key) and folds
// in every pin and move-ref.
func (r *Router) personRedirectIndex() (canon, names map[string]string, err error) {
	keys, err := r.store.ListPeopleKeys()
	if err != nil {
		return nil, nil, fmt.Errorf("person: list people: %w", err)
	}
	canon = make(map[string]string, len(keys))
	names = make(map[string]string, len(keys))
	for _, key := range keys {
		rec, found, rerr := r.store.ReadPerson(key)
		if rerr != nil {
			return nil, nil, fmt.Errorf("person: read %q: %w", key, rerr)
		}
		if !found {
			continue
		}
		if rec.IsTombstone() {
			canon[key] = rec.RedirectTo
			continue
		}
		canon[key] = key
		names[key] = rec.DisplayName
	}
	return canon, names, nil
}

// personDominanceLine returns the one dominance line for this person, or "" when
// their share of processed entries does not exceed person_dominance_threshold.
// Share is the fraction of processed artifacts that mention them on read —
// each artifact's mentions canonicalized through redirects (historical
// artifacts keep the pre-merge key) with every pin and move-ref folded in
// (deterministic, router-side). The line is hypothesis-framed and appears only
// in /person and /reflect gate — never on /status or a daily surface (S-22).
func (r *Router) personDominanceLine(rec storage.PersonRecord) (string, error) {
	ids, err := r.store.ListProcessedIDs()
	if err != nil {
		return "", fmt.Errorf("person: list processed: %w", err)
	}
	total := len(ids)
	if total == 0 {
		return "", nil
	}
	fold, err := r.store.MentionFold()
	if err != nil {
		return "", fmt.Errorf("person: fold mentions: %w", err)
	}
	mentions := 0
	for _, id := range ids {
		art, err := r.store.ReadProcessed(id)
		if err != nil {
			return "", fmt.Errorf("person: read processed %q: %w", id, err)
		}
		if slices.Contains(fold.MentionKeys(art), rec.PersonKey) {
			mentions++
		}
	}
	share := float64(mentions) / float64(total)
	if share <= r.cfg.PersonDominanceThreshold {
		return "", nil
	}
	pct := int(share*100 + 0.5)
	return fmt.Sprintf("%s appears in %d%% of entries — worth a look, or expected?", rec.DisplayName, pct), nil
}
