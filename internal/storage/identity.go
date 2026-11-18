package storage

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/identity"
)

// Identity-log names under ~/.lucid/ (data-model.md §"Identity log"; ADR-0016).
// Only this adapter touches the file: every status, claim, release, hold, pin,
// and move-ref is one append-only JSONL event in a single file, and the current
// identity facts are a fold of that log. Nothing here ever rewrites people/ or
// processed/. A Ledger without the file reads as everyone active, and no read
// ever creates it — only the first write that needs it does.
const (
	identityDirName  = "identity"
	identityFileName = "identity.jsonl"
	identityDateForm = "2006-01-02"
)

// identityIDField salvages the id of a line that no longer decodes — a torn
// tail, a hand edit — so the slot it held is never minted again. The id is the
// second field of every marshaled event, so it survives almost any damage.
var identityIDField = regexp.MustCompile(`"id"\s*:\s*"(` + identity.IDPrefix + `[0-9]{4}_[0-9]{2}_[0-9]{2}_[a-z]+)"`)

// identityDir returns ~/.lucid/identity/.
func (a *Adapter) identityDir() string { return filepath.Join(a.home, identityDirName) }

// identityPath returns the single append-only log ~/.lucid/identity/identity.jsonl.
func (a *Adapter) identityPath() string { return filepath.Join(a.identityDir(), identityFileName) }

// AppendIdentityEvent mints the event's id under the single-writer discipline
// and appends it as one fsync'd JSONL line to the identity log, creating
// identity/identity.jsonl on the first write. It is [Adapter.AppendIdentityEvents]
// for one event, and returns the event with its schema and id filled.
func (a *Adapter) AppendIdentityEvent(e identity.Event) (identity.Event, error) {
	out, err := a.AppendIdentityEvents([]identity.Event{e})
	if err != nil {
		return identity.Event{}, err
	}
	return out[0], nil
}

// AppendIdentityEvents appends a batch of identity events as one write — the
// shape a single curation verb needs (a forced claim's release then claim, a
// write's pins). Each event must carry its `at` (RFC3339, host-local offset);
// the schema is stamped and the id is assigned here — the local date of `at`
// plus the first unused per-day slot in the file, never the line count — so
// any id the caller set is replaced. Every event is validated before anything
// is written: one malformed event refuses the whole batch and the log is left
// byte-identical. It returns the events with their schema and ids filled, in
// the order given. An empty batch writes nothing and creates nothing.
func (a *Adapter) AppendIdentityEvents(events []identity.Event) ([]identity.Event, error) {
	if len(events) == 0 {
		return nil, nil
	}
	path := a.identityPath()
	out, err := a.stampIdentityEvents(events)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for i, e := range out {
		line, merr := e.MarshalLine()
		if merr != nil {
			return nil, merr
		}
		if i > 0 {
			buf.WriteByte('\n')
		}
		buf.Write(line)
	}
	if err := appendLineFsync(path, buf.Bytes()); err != nil {
		return nil, err
	}
	return out, nil
}

// stampIdentityEvents fills each event's schema and assigns the id
// [Adapter.AppendIdentityEvents] would give it — the local date of its `at`
// plus the first unused per-day slot in the log — and validates it, writing
// nothing. A curation write uses it to fold its own events in memory before
// appending them, so the pins it records are decided against the state the
// write produces, and the whole batch lands as one append.
func (a *Adapter) stampIdentityEvents(events []identity.Event) ([]identity.Event, error) {
	next, err := a.identitySlots(a.identityPath())
	if err != nil {
		return nil, err
	}
	out := make([]identity.Event, len(events))
	for i, e := range events {
		at, perr := time.Parse(time.RFC3339, e.At)
		if perr != nil {
			return nil, fmt.Errorf("storage: identity event %s needs an RFC3339 at, got %q", e.Kind, e.At)
		}
		date := at.Format(identityDateForm)
		e.Schema = identity.Schema
		e.ID = identity.ID(date, next[date])
		next[date]++
		if verr := e.Validate(); verr != nil {
			return nil, verr
		}
		out[i] = e
	}
	return out, nil
}

// identitySlots returns, per local date, the first slot past every slot
// already used in the log — never the line count. A line that still decodes
// contributes its id; a line that no longer decodes contributes the id it
// still names, if any, so a damaged line's slot is never reissued as a second
// receipt. A missing log starts every date at slot 0 (`_a`).
func (a *Adapter) identitySlots(path string) (map[string]int, error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return nil, err
	}
	next := map[string]int{}
	for _, ln := range lines {
		id := ""
		if e, perr := identity.UnmarshalEventLine(ln); perr == nil {
			id = e.ID
		}
		if _, ok := identity.Date(id); !ok {
			if m := identityIDField.FindSubmatch(ln); m != nil {
				id = string(m[1])
			}
		}
		date, ok := identity.Date(id)
		if !ok {
			continue
		}
		if n, _ := identity.Slot(id); n >= next[date] {
			next[date] = n + 1
		}
	}
	return next, nil
}

// ReadIdentityEvents reads every well-formed identity event in append order.
// A missing log is the empty stream — never an error, and the read creates
// nothing. A malformed line (one that does not decode, carries no id, or fails
// validation) is skipped so the fold never sees a partial event; lucid validate
// reports it through [Adapter.IdentityLogErr].
func (a *Adapter) ReadIdentityEvents() ([]identity.Event, error) {
	lines, err := a.identityLines()
	if err != nil {
		return nil, err
	}
	var out []identity.Event
	for _, ln := range lines {
		e, perr := unmarshalIdentityLine(ln.raw)
		if perr != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// IdentityState folds the identity log into the current identity facts,
// canonicalizing claims, pins, move-refs, and hold candidates through the
// people tree's merge redirects (status stays keyed on the exact key). A
// Ledger without the log reads as everyone active with no claims, holds, pins,
// or move-refs — and then the people tree is not even read. The read never
// writes.
func (a *Adapter) IdentityState() (identity.State, error) {
	events, err := a.ReadIdentityEvents()
	if err != nil {
		return identity.State{}, err
	}
	if len(events) == 0 {
		return identity.Fold(nil, nil), nil
	}
	redirects, err := a.personRedirects()
	if err != nil {
		return identity.State{}, err
	}
	return identity.Fold(events, func(k string) string {
		if to, ok := redirects[k]; ok {
			return to
		}
		return k
	}), nil
}

// IdentityLogErr reports the identity log's malformed lines — each one's
// locator and why it is skipped — or nil when every line is well-formed (and
// when there is no log). It is read-only.
func (a *Adapter) IdentityLogErr() error {
	lines, err := a.identityLines()
	if err != nil {
		return err
	}
	var bad []string
	for _, ln := range lines {
		if _, perr := unmarshalIdentityLine(ln.raw); perr != nil {
			bad = append(bad, ln.locator+": "+perr.Error())
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("storage: identity log has %d malformed line(s), skipped on read: %s",
		len(bad), strings.Join(bad, "; "))
}

// ListIdentityEventIDs returns a locator for every line in the identity log,
// in append order — the event id when the line names one, else a positional
// line-<n> locator so a malformed line is still nameable in a validate
// finding. A missing log yields no ids and no error.
func (a *Adapter) ListIdentityEventIDs() ([]string, error) {
	lines, err := a.identityLines()
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(lines))
	for i, ln := range lines {
		ids[i] = ln.locator
	}
	return ids, nil
}

// ReadIdentityEventErr re-parses the single line a locator names and returns
// only its parse/validation error (nil when it is well-formed). An unknown
// locator is treated as clean. It is read-only.
func (a *Adapter) ReadIdentityEventErr(id string) error {
	lines, err := a.identityLines()
	if err != nil {
		return err
	}
	for _, ln := range lines {
		if ln.locator == id {
			_, perr := unmarshalIdentityLine(ln.raw)
			return perr
		}
	}
	return nil
}

// personRedirects maps every tombstone's key to the record it forwards to —
// the single-hop redirect graph [Adapter.ResolvePersonRedirect] reads one key
// at a time. A missing people/ tree is the empty map.
func (a *Adapter) personRedirects() (map[string]string, error) {
	keys, err := a.ListPeopleKeys()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, k := range keys {
		rec, found, rerr := a.ReadPerson(k)
		if rerr != nil {
			return nil, rerr
		}
		if found && rec.IsTombstone() {
			out[k] = rec.RedirectTo
		}
	}
	return out, nil
}

// identityLine pairs a raw identity-log line with the locator a validate
// finding addresses it by.
type identityLine struct {
	locator string
	raw     []byte
}

// identityLines reads the log's raw lines and assigns each a stable locator:
// the id the line names when it decodes to one, else line-<n>. A missing log
// yields no lines and no error, and is never created.
func (a *Adapter) identityLines() ([]identityLine, error) {
	lines, err := readJSONLLines(a.identityPath())
	if err != nil {
		return nil, err
	}
	out := make([]identityLine, 0, len(lines))
	for i, ln := range lines {
		loc := fmt.Sprintf("line-%d", i+1)
		if e, perr := identity.UnmarshalEventLine(ln); perr == nil && e.ID != "" {
			loc = e.ID
		}
		out = append(out, identityLine{locator: loc, raw: ln})
	}
	return out, nil
}

// unmarshalIdentityLine decodes and validates one identity-log line. It is the
// single definition of "well-formed" every reader shares, so the fold, the
// listing, and validate never disagree about which lines count. A stored event
// must carry its id (its receipt).
func unmarshalIdentityLine(raw []byte) (identity.Event, error) {
	e, err := identity.UnmarshalEventLine(raw)
	if err != nil {
		return identity.Event{}, err
	}
	if e.ID == "" {
		return identity.Event{}, errors.New("storage: identity event has no id")
	}
	if err := e.Validate(); err != nil {
		return identity.Event{}, err
	}
	return e, nil
}
