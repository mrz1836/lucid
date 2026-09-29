package router

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// gratexpress.go is the gratitude layer's outward expression (gratitude.md §8):
// linking an entry to a person in the people registry, keeping a private note
// that you told them, and the gentle "you might tell …" lines `list` offers.
// Lucid never sends anything — it prompts and records; the person does the
// telling. Expression is tally-neutral (an `expressed` event moves no count), a
// person subject is resolved and validated before anything is written, and a
// person marked off-limits may be linked (it is the user's own private record)
// but is never named in a reminder, and an entry linked to one never reaches the
// tier-3 judge (gratitude.md §7.5). Every step here is deterministic and
// model-free (architecture P9); the storage adapter is the only writer (P3).

// gratitudeReminderCap is the fixed, quiet ceiling on the reminder lines `list`
// offers (gratitude.md §8). It is a ceiling, never a count: nothing says how many
// others there might be.
const gratitudeReminderCap = 3

// ThankGratitudeRequest is one `lucid gratitude thank <id> --person <subject>`
// turn (gratitude.md §8): the stable entry id, the person subject (a name or a
// person_key, resolved like every person write verb's), the strict-tier --day
// value for "I told them yesterday", and now (injected so the logical date and
// receipt are deterministic in tests).
type ThankGratitudeRequest struct {
	ID     string
	Person string
	DayArg string
	Now    time.Time
}

// GratitudeThankResult reports a recorded thank-you: the entry, the appended
// `expressed` event's receipt, the stable id and display wording, the tally
// count (unchanged — expression is tally-neutral), the canonical person key and
// display name, and the logical date recorded.
type GratitudeThankResult struct {
	Entry      observations.GratitudeEntry
	Receipt    string
	Key        string
	Thing      string
	Count      int
	PersonKey  string
	PersonName string
	Date       string
	Ack        string
}

// gratitudePerson is a person subject resolved for gratitude: the canonical
// person key, their display name, and whether they are off-limits.
type gratitudePerson struct {
	Key         string
	DisplayName string
	OffLimits   bool
}

// ThankGratitude records that you told a person you were grateful (gratitude.md
// §8). It validates everything before anything is written — the id must name a
// live entry (an unknown id or a merge tombstone is a clean error, like
// `--into`), the person subject must resolve to exactly one live person
// ([Router.resolvePersonSubject]: no match or several is a clean error naming
// the remedy or the candidate keys), and `--day` runs the shared strict tier —
// then links the canonical person key onto the entry and appends one
// tally-neutral `expressed` event, returning its receipt. An off-limits person
// may be thanked: the note is the user's own record; reminders simply never
// name them. Count, First, and Last do not move.
func (r *Router) ThankGratitude(req ThankGratitudeRequest) (GratitudeThankResult, error) {
	now := whenOr(req.Now)
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return GratitudeThankResult{}, fmt.Errorf(
			"gratitude thank needs an entry id; run `lucid gratitude list` for it; nothing was saved",
		)
	}
	if strings.TrimSpace(req.Person) == "" {
		return GratitudeThankResult{}, fmt.Errorf(
			"gratitude thank needs --person <subject> — whom you told; nothing was saved",
		)
	}
	if err := r.prepareGratitude(); err != nil {
		return GratitudeThankResult{}, err
	}

	existing, found, err := r.store.ReadGratitude(id)
	if err != nil {
		return GratitudeThankResult{}, fmt.Errorf("could not read the gratitude entry; nothing was saved: %w", err)
	}
	if !found {
		return GratitudeThankResult{}, fmt.Errorf(
			"no live gratitude entry %q to thank for; run `lucid gratitude list` for the id; nothing was saved", id,
		)
	}
	if existing.IsTombstone() {
		return GratitudeThankResult{}, fmt.Errorf(
			"gratitude entry %q was merged into %q; thank for that entry instead; nothing was saved", id, existing.RedirectTo,
		)
	}
	person, err := r.resolveGratitudePerson(req.Person)
	if err != nil {
		return GratitudeThankResult{}, err
	}
	when, err := resolveCaptureWhen(req.DayArg, now)
	if err != nil {
		return GratitudeThankResult{}, err
	}

	entry, ev, err := r.store.AppendGratitudeExpressed(id, person.Key, when.LogicalDate, now)
	if err != nil {
		return GratitudeThankResult{}, fmt.Errorf("could not record the thank-you; nothing was saved: %w", err)
	}
	tally := entry.Tally()
	return GratitudeThankResult{
		Entry:      entry,
		Receipt:    ev.ID,
		Key:        entry.Key,
		Thing:      entry.DisplayName,
		Count:      tally.Count,
		PersonKey:  person.Key,
		PersonName: person.DisplayName,
		Date:       ev.Date,
		Ack:        gratitudeThankAck(person.DisplayName, entry.DisplayName, ev.Date, ev.ID, tally.Count),
	}, nil
}

// resolveGratitudePerson resolves a `--person` subject for `add --person` or
// `thank` exactly as the person write verbs resolve one
// ([Router.resolvePersonSubject]): an exact person_key wins (a redirect followed
// to its canonical record), otherwise the name is matched against live
// display_name / aka[]; no match or several is a clean [PersonRejectedError]
// ending "nothing was saved". It also reports whether the person is off-limits,
// which an add uses to withhold the tier-3 judge — reading the registry fails
// closed, as an error before anything is written.
func (r *Router) resolveGratitudePerson(raw string) (gratitudePerson, error) {
	rec, err := r.resolvePersonSubject(raw)
	if err != nil {
		return gratitudePerson{}, err
	}
	off, err := r.store.ReadOffLimitsPersonKeys()
	if err != nil {
		return gratitudePerson{}, fmt.Errorf("could not read the off-limits registry; nothing was saved: %w", err)
	}
	return gratitudePerson{Key: rec.PersonKey, DisplayName: rec.DisplayName, OffLimits: toSet(off)[rec.PersonKey]}, nil
}

// gratitudePeople resolves the person keys gratitude entries are linked to, at
// read time (gratitude.md §8): each stored key is followed forward through its
// person redirect, so a later `person merge` never strands a link, and is
// checked against the off-limits registry on both the stored and the canonical
// key, so a merge never un-redacts one. The registry is read on the first
// resolve — an unlinked tally never touches it — and resolutions are cached for
// one read.
type gratitudePeople struct {
	r         *Router
	loaded    bool
	offLimits map[string]bool
	loadErr   error
	cache     map[string]gratitudePerson
}

// newGratitudePeople returns a resolver for one read.
func (r *Router) newGratitudePeople() *gratitudePeople {
	return &gratitudePeople{r: r, cache: map[string]gratitudePerson{}}
}

// resolve follows one stored person key to its canonical person: the canonical
// key, the record's display name (the key itself when no record is on file),
// and whether either key is off-limits. An unreadable off-limits registry is an
// error on every resolve, so no caller can mistake it for "nobody off-limits".
func (p *gratitudePeople) resolve(stored string) (gratitudePerson, error) {
	if ref, ok := p.cache[stored]; ok {
		return ref, nil
	}
	if !p.loaded {
		off, err := p.r.store.ReadOffLimitsPersonKeys()
		p.loaded, p.offLimits, p.loadErr = true, toSet(off), err
	}
	if p.loadErr != nil {
		return gratitudePerson{}, p.loadErr
	}
	canon, err := p.r.store.ResolvePersonRedirect(stored)
	if err != nil {
		return gratitudePerson{}, err
	}
	ref := gratitudePerson{Key: canon, DisplayName: canon, OffLimits: p.offLimits[stored] || p.offLimits[canon]}
	rec, found, err := p.r.store.ReadPerson(canon)
	if err != nil {
		return gratitudePerson{}, err
	}
	if found && strings.TrimSpace(rec.DisplayName) != "" {
		ref.DisplayName = rec.DisplayName
	}
	p.cache[stored] = ref
	return ref, nil
}

// linked lists an entry's people for `list` (gratitude.md §6): one row per
// canonical person, in link order, each with the last date you told them about
// this gratitude. Two stored keys that now resolve to the same person (a later
// `person merge`) collapse into one row carrying the later date. It is always an
// array, never nil.
func (p *gratitudePeople) linked(e observations.GratitudeEntry) ([]GratitudeListPerson, error) {
	out := make([]GratitudeListPerson, 0, len(e.People))
	if len(e.People) == 0 {
		return out, nil
	}
	expressed := e.LastExpressed()
	index := make(map[string]int, len(e.People))
	for _, stored := range e.People {
		ref, err := p.resolve(stored)
		if err != nil {
			return nil, err
		}
		last := expressed[stored]
		if i, seen := index[ref.Key]; seen {
			out[i].LastExpressed = max(out[i].LastExpressed, last)
			out[i].OffLimits = out[i].OffLimits || ref.OffLimits
			continue
		}
		index[ref.Key] = len(out)
		out = append(out, GratitudeListPerson{
			PersonKey: ref.Key, DisplayName: ref.DisplayName, OffLimits: ref.OffLimits, LastExpressed: last,
		})
	}
	return out, nil
}

// withholds reports whether an entry must be kept from the tier-3 judge: it is
// linked to an off-limits person, or a link cannot be resolved (fail closed).
func (p *gratitudePeople) withholds(e observations.GratitudeEntry) bool {
	for _, stored := range e.People {
		ref, err := p.resolve(stored)
		if err != nil || ref.OffLimits {
			return true
		}
	}
	return false
}

// judgeableGratitude is the live set less every entry linked to an off-limits
// person — the entries whose wordings may reach a tier-3 judge (gratitude.md
// §7.5, §8). Off-limits means off-limits to inference: tiers 1–2 still match
// such an entry deterministically, but a model never sees it. It fails closed:
// when the off-limits registry cannot be read, every linked entry is withheld,
// and so is any entry whose link cannot be resolved. An unlinked tally never
// reads the people registry at all.
func (r *Router) judgeableGratitude(live []observations.GratitudeEntry) []observations.GratitudeEntry {
	people := r.newGratitudePeople()
	out := make([]observations.GratitudeEntry, 0, len(live))
	for _, e := range live {
		if !people.withholds(e) {
			out = append(out, e)
		}
	}
	return out
}

// gratitudeReminders picks the gentle "you might tell …" lines (gratitude.md
// §8): a linked, not-off-limits person is offered for an entry when you have
// never recorded telling them, or when the entry has been tallied again since
// you last did. Entries are taken most recently tallied first (then by count,
// then by stable id), people in link order, and at most [gratitudeReminderCap]
// lines are returned — a fixed ceiling, with nothing recording how many others
// there might be. It is always an array, never nil.
func gratitudeReminders(entries []GratitudeListEntry) []GratitudeReminder {
	byRecency := slices.Clone(entries)
	slices.SortStableFunc(byRecency, func(x, y GratitudeListEntry) int {
		if c := cmp.Compare(y.Last, x.Last); c != 0 {
			return c
		}
		if c := cmp.Compare(y.Count, x.Count); c != 0 {
			return c
		}
		return cmp.Compare(x.ID, y.ID)
	})
	out := make([]GratitudeReminder, 0, gratitudeReminderCap)
	for _, e := range byRecency {
		for _, p := range e.People {
			if p.OffLimits || (p.LastExpressed != "" && e.Last <= p.LastExpressed) {
				continue
			}
			out = append(out, GratitudeReminder{
				PersonKey: p.PersonKey, DisplayName: p.DisplayName, EntryID: e.ID, Thing: e.Thing,
			})
			if len(out) == gratitudeReminderCap {
				return out
			}
		}
	}
	return out
}

// gratitudeReminderLines renders the reminders below the tally (gratitude.md
// §8): a blank separator, a plain "You might tell:" header, and one line per
// reminder — the same quiet wording every time, however long a line has been
// offered. No reminders renders nothing.
func gratitudeReminderLines(reminders []GratitudeReminder) []string {
	if len(reminders) == 0 {
		return nil
	}
	lines := make([]string, 0, len(reminders)+2)
	lines = append(lines, "", "You might tell:")
	for _, rm := range reminders {
		lines = append(lines, fmt.Sprintf("  %s — %q (%s)", rm.DisplayName, rm.Thing, rm.EntryID))
	}
	return lines
}

// gratitudePeopleSuffix renders a tally row's linked people (gratitude.md §6):
// each named, with when you last told them and an off-limits flag where they
// apply. An unlinked row gets nothing, so a v1 row renders exactly as before.
func gratitudePeopleSuffix(people []GratitudeListPerson) string {
	if len(people) == 0 {
		return ""
	}
	parts := make([]string, 0, len(people))
	for _, p := range people {
		part := p.DisplayName
		if p.LastExpressed != "" {
			part += fmt.Sprintf(" (told %s)", p.LastExpressed)
		}
		if p.OffLimits {
			part += " (off-limits)"
		}
		parts = append(parts, part)
	}
	return "  · with " + strings.Join(parts, ", ")
}

// gratitudeThankAck builds the ack after a thank-you is recorded: whom you told,
// about which gratitude, on which day, the receipt — and the unchanged tally, so
// it is legible that telling someone never moves the count (gratitude.md §8).
func gratitudeThankAck(person, thing, date, receipt string, count int) string {
	return fmt.Sprintf("Noted that you told %s about %q (%s) as `%s` — the tally stays ×%d.", person, thing, date, receipt, count)
}

// gratitudeLinkAck is the sentence an `add --person` appends to its ack: the
// person the entry is now linked to (gratitude.md §3, §8).
func gratitudeLinkAck(person string) string {
	return fmt.Sprintf(" Linked to %s.", person)
}
