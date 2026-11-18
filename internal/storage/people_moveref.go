package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/identity"
)

// move-ref and the read fold (data-model.md §"The fold — current identity
// facts are a read" §"Attribution on read"; §"Status, claims and move-ref").
// A move-ref, like every identity write, appends to identity/identity.jsonl
// only; people/ and processed/ keep their bytes. Every read that attributes a
// raw entry to a person instead folds the log's pins and move-refs over the
// processed artifact through one [MentionFold], so a correction shows up the
// same way on every surface.

// The §P-12 move-ref refusals. Each is decided before anything is written.
var (
	// ErrMoveRefUnknownRaw — the raw id names no captured entry.
	ErrMoveRefUnknownRaw = errors.New("storage: move-ref names no raw entry")
	// ErrMoveRefSamePerson — --from and --to are the same canonical person.
	ErrMoveRefSamePerson = errors.New("storage: move-ref from and to are the same person")
	// ErrMoveRefNoMention — the raw entry, read through the fold, has no
	// mention under --from.
	ErrMoveRefNoMention = errors.New("storage: move-ref finds no mention under from")
	// ErrMoveRefNoHeld — without --from, the raw entry has no pending held
	// mention to settle.
	ErrMoveRefNoHeld = errors.New("storage: move-ref finds no held mention")
)

// MoveRefHeldError is a move-ref without --from on a raw entry with more than
// one pending held mention (§P-12): which one was meant is never guessed.
// Forms are the held forms as the holds recorded them, sorted by normalized
// form.
type MoveRefHeldError struct {
	RawID string
	Forms []string
}

// Error names the raw entry and its held forms.
func (e *MoveRefHeldError) Error() string {
	return fmt.Sprintf("storage: %s has more than one held mention: %s", e.RawID, strings.Join(e.Forms, ", "))
}

// RawExists reports whether a raw entry with the given id is captured. An id
// that cannot name a raw entry — malformed, or carrying a path separator —
// names none; it is never an error. The read never writes.
func (a *Adapter) RawExists(id string) (bool, error) {
	if strings.ContainsAny(id, `/\`) {
		return false, nil
	}
	year, month, err := rawShardFromID(id)
	if err != nil || !allDigits(year) || !allDigits(month) {
		return false, nil //nolint:nilerr // a malformed id simply names no entry
	}
	_, err = os.Stat(filepath.Join(a.home, rawDirName, year, month, id+rawExt))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage: stat raw %q: %w", id, err)
	}
	return true, nil
}

// MoveRef records one append-only correction for a misfiled mention (`lucid
// person move-ref`). With from set it appends a move_ref event that moves the
// raw entry's mention off from and onto to on read; it is refused when from
// and to are the same person, or when the raw entry — read through the fold —
// has no mention under from. With from empty it pins the raw entry's one
// pending held mention to to (a pin with cause move_ref), refused when the raw
// entry has no held mention or more than one ([MoveRefHeldError]). An unknown
// raw id is refused first. from and to must be live canonical people. Nothing
// in people/ or processed/ is ever written, and every refusal is decided before
// anything is appended. The undo is the reverse move-ref.
func (a *Adapter) MoveRef(rawID, from, to string, at time.Time) (IdentityWrite, error) {
	exists, err := a.RawExists(rawID)
	if err != nil {
		return IdentityWrite{}, err
	}
	if !exists {
		return IdentityWrite{}, fmt.Errorf("%w: %q", ErrMoveRefUnknownRaw, rawID)
	}
	ix, _, st, err := a.identitySnapshot()
	if err != nil {
		return IdentityWrite{}, err
	}
	if _, err = canonicalSubject(ix, to); err != nil {
		return IdentityWrite{}, err
	}
	stamp := formatAt(at)

	if from == "" {
		h, herr := soleHold(st, rawID)
		if herr != nil {
			return IdentityWrite{}, herr
		}
		ev := identity.NewPin(stamp, identity.SourceCLI, rawID, h.Form, to, identity.CauseMoveRef)
		out, aerr := a.AppendIdentityEvents([]identity.Event{ev})
		if aerr != nil {
			return IdentityWrite{}, aerr
		}
		return IdentityWrite{
			Receipt: out[0].ID,
			Events:  out,
			Pinned:  []PinnedMention{{RawID: rawID, Form: h.Form, PersonKey: to}},
		}, nil
	}

	if _, err = canonicalSubject(ix, from); err != nil {
		return IdentityWrite{}, err
	}
	if from == to {
		return IdentityWrite{}, ErrMoveRefSamePerson
	}
	art, found, err := a.readPriorProcessed(rawID)
	if err != nil {
		return IdentityWrite{}, err
	}
	if !found || !slices.Contains(newMentionFold(st, ix.canonKey).MentionKeys(art), from) {
		return IdentityWrite{}, ErrMoveRefNoMention
	}
	ev := identity.NewMoveRef(stamp, identity.SourceCLI, rawID, from, to)
	out, err := a.AppendIdentityEvents([]identity.Event{ev})
	if err != nil {
		return IdentityWrite{}, err
	}
	return IdentityWrite{Receipt: out[0].ID, Events: out}, nil
}

// soleHold returns the raw entry's one pending held mention, refusing none
// ([ErrMoveRefNoHeld]) or several ([MoveRefHeldError]).
func soleHold(st identity.State, rawID string) (identity.Hold, error) {
	var holds []identity.Hold
	for _, h := range st.PendingHolds() {
		if h.RawID == rawID {
			holds = append(holds, h)
		}
	}
	switch len(holds) {
	case 0:
		return identity.Hold{}, ErrMoveRefNoHeld
	case 1:
		return holds[0], nil
	}
	forms := make([]string, len(holds))
	for i, h := range holds {
		forms[i] = h.Form
	}
	return identity.Hold{}, &MoveRefHeldError{RawID: rawID, Forms: forms}
}

// FoldedMention is one mention of a raw entry as every read attributes it.
type FoldedMention struct {
	// DisplayName is the form as the processed artifact recorded it.
	DisplayName string
	// StoredKey is what the log or artifact recorded for the mention before
	// any move-ref: the people[] person_key, or — for a pinned held_people[]
	// entry — the person it is pinned to. A read that redacts checks it as
	// well as PersonKey, so it fails closed.
	StoredKey string
	// PersonKey is who the mention is attributed to on read: the stored key
	// canonicalized through redirects (a pin for the mention outranks it),
	// with the raw entry's move-refs applied in order.
	PersonKey string
}

// MentionFold is the identity log folded for the reads that attribute a raw
// entry to a person (data-model.md §"Attribution on read"): the `/person`
// mention count, entry list, insight join and dominance line, the `/reflect
// gate` dominance panel, the agent-bound Reflection slice, and reconcile's
// mention counts. A raw entry's mentions on read are its people[] keys
// canonicalized (a pin for a mention outranks the stored key), each pinned
// held_people[] entry's person, with the raw's move-refs applied in order. A
// person's references on read are their stored entry_refs re-derived from that
// fold for every raw entry a pin or move-ref touches. Build one with
// [Adapter.MentionFold]; it is a read and never writes.
type MentionFold struct {
	st    identity.State
	canon func(string) string
	// touched maps each raw entry a pin or move-ref touches, whose processed
	// artifact is readable, to its folded mention keys.
	touched map[string][]string
}

// MentionFold reads the identity log, the people tree's redirects, and the
// processed artifact of every raw entry a pin or move-ref touches, and folds
// them for attribution. A Ledger without the log folds to the processed
// artifacts and entry_refs exactly as stored, canonicalized through
// redirects. The read never writes.
func (a *Adapter) MentionFold() (MentionFold, error) {
	events, err := a.ReadIdentityEvents()
	if err != nil {
		return MentionFold{}, err
	}
	return a.mentionFold(events)
}

// mentionFold builds a [MentionFold] from already-read identity events.
func (a *Adapter) mentionFold(events []identity.Event) (MentionFold, error) {
	redirects, err := a.personRedirects()
	if err != nil {
		return MentionFold{}, err
	}
	canon := func(k string) string {
		if to, ok := redirects[k]; ok {
			return to
		}
		return k
	}
	f := newMentionFold(identity.Fold(events, canon), canon)
	for _, raw := range f.st.TouchedRawIDs() {
		art, found, rerr := a.readPriorProcessed(raw)
		if rerr != nil {
			return MentionFold{}, rerr
		}
		if found {
			f.touched[raw] = f.MentionKeys(art)
		}
	}
	return f, nil
}

// newMentionFold builds a fold over a folded identity state, with no touched
// raw entries loaded — enough for [MentionFold.Mentions] and
// [MentionFold.MentionKeys].
func newMentionFold(st identity.State, canon func(string) string) MentionFold {
	return MentionFold{st: st, canon: canon, touched: map[string][]string{}}
}

// Canon canonicalizes a person_key through the people tree's merge redirects.
func (f MentionFold) Canon(key string) string {
	if f.canon == nil || key == "" {
		return key
	}
	return f.canon(key)
}

// Mentions returns every mention of the processed artifact as reads attribute
// it: its people[] entries in order, then each held_people[] entry a pin
// settled. A held mention no pin settles is attributed to no one and left out.
func (f MentionFold) Mentions(art ProcessedArtifact) []FoldedMention {
	moves := f.st.MovesFor(art.ID)
	out := make([]FoldedMention, 0, len(art.People)+len(art.HeldPeople))
	for _, p := range art.People {
		key := f.Canon(p.PersonKey)
		if pin, ok := f.st.Pin(art.ID, p.DisplayName); ok {
			key = pin.PersonKey
		}
		out = append(out, FoldedMention{DisplayName: p.DisplayName, StoredKey: p.PersonKey, PersonKey: applyMoves(key, moves)})
	}
	for _, h := range art.HeldPeople {
		pin, ok := f.st.Pin(art.ID, h.DisplayName)
		if !ok {
			continue
		}
		out = append(out, FoldedMention{DisplayName: h.DisplayName, StoredKey: pin.PersonKey, PersonKey: applyMoves(pin.PersonKey, moves)})
	}
	return out
}

// MentionKeys returns the canonical person_keys the processed artifact's
// mentions are attributed to on read, sorted and de-duplicated.
func (f MentionFold) MentionKeys(art ProcessedArtifact) []string {
	var keys []string
	for _, m := range f.Mentions(art) {
		if m.PersonKey != "" {
			keys = append(keys, m.PersonKey)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// EntryRefs returns the canonical record's references on read: its stored
// entry_refs, re-derived from the fold for every raw entry a pin or move-ref
// touches — added when the folded entry mentions them, dropped when it no
// longer does. A touched raw entry whose processed artifact is missing keeps
// the stored reference as is. With nothing touched the stored entry_refs come
// back unchanged, so an untouched Ledger reads exactly as before.
func (f MentionFold) EntryRefs(rec PersonRecord) []string {
	refs := slices.Clone(rec.EntryRefs)
	if len(f.touched) == 0 || rec.IsTombstone() {
		return refs
	}
	set := map[string]bool{}
	for _, r := range refs {
		set[r] = true
	}
	changed := false
	for raw, keys := range f.touched {
		has := slices.Contains(keys, rec.PersonKey)
		if has != set[raw] {
			changed = true
			if has {
				set[raw] = true
			} else {
				delete(set, raw)
			}
		}
	}
	if !changed {
		return refs
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	slices.Sort(out)
	return out
}

// FoldedMentionKeys returns the canonical person_keys a processed artifact's
// mentions are attributed to on read (see [MentionFold.MentionKeys]). A loop
// over many artifacts should build one [Adapter.MentionFold] instead.
func (a *Adapter) FoldedMentionKeys(art ProcessedArtifact) ([]string, error) {
	f, err := a.MentionFold()
	if err != nil {
		return nil, err
	}
	return f.MentionKeys(art), nil
}

// FoldedEntryRefs returns the canonical record's references on read (see
// [MentionFold.EntryRefs]). A Ledger without the identity log returns the
// stored entry_refs without reading anything else.
func (a *Adapter) FoldedEntryRefs(rec PersonRecord) ([]string, error) {
	events, err := a.ReadIdentityEvents()
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return slices.Clone(rec.EntryRefs), nil
	}
	f, err := a.mentionFold(events)
	if err != nil {
		return nil, err
	}
	return f.EntryRefs(rec), nil
}

// applyMoves applies a raw entry's move-refs, in order, to one mention's key:
// each moves it off From and onto To.
func applyMoves(key string, moves []identity.Move) string {
	for _, m := range moves {
		if key == m.From {
			key = m.To
		}
	}
	return key
}

// allDigits reports whether s is a non-empty run of ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
