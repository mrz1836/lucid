package storage

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/mrz1836/lucid/internal/contact"
)

// Contact-tree names under ~/.lucid/ (ADR-0014 §2). Only this adapter touches
// them; the contact record family is agent-free and reads/writes exclusively
// through these ops (architecture P3). The tree is the reps/ layout file for
// file: JSONL day shards sharded by the event's logical day. The Monday post's
// delivery receipt sits at the tree root (contact_weekly_receipt.go), outside
// the day shards, so the event stream stays pure.
const (
	contactDirName    = "contacts"
	contactFilePrefix = "contact_"
	contactFileExt    = ".jsonl"
)

// contactIDField salvages the id of a line that no longer decodes — a torn
// tail, a hand edit — so the slot it held is never minted again. The id is the
// first field of every marshaled event, so it survives almost any damage.
var contactIDField = regexp.MustCompile(`"id"\s*:\s*"(` + contact.IDPrefix + `[0-9]{4}_[0-9]{2}_[0-9]{2}_[a-z]+)"`)

// contactDir returns ~/.lucid/contacts/.
func (a *Adapter) contactDir() string { return filepath.Join(a.home, contactDirName) }

// contactDayPath returns contacts/YYYY/MM/contact_YYYY_MM_DD.jsonl for a logical
// date (YYYY-MM-DD). A malformed date is rejected by [safeDayShard] so it can
// never build a path outside the contact tree (defense-in-depth: the router
// derives every logical_date from the civil-day rule).
func (a *Adapter) contactDayPath(date string) (string, error) {
	year, month, err := safeDayShard(date)
	if err != nil {
		return "", err
	}
	name := contactFilePrefix + strings.ReplaceAll(date, "-", "_") + contactFileExt
	return filepath.Join(a.contactDir(), year, month, name), nil
}

// ScaffoldContacts creates the contacts/ tree. It is idempotent — an existing
// tree (and everything under it, including the weekly receipt) is left
// untouched (mirroring [Adapter.ScaffoldReps]). The append path also
// self-creates its parents, so scaffolding is a convenience the router runs
// before a contact verb, not a precondition for a write.
func (a *Adapter) ScaffoldContacts() error {
	if err := os.MkdirAll(a.contactDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create contacts dir: %w", err)
	}
	return nil
}

// AppendContactEvent mints the event's id under the single-writer discipline
// and appends it as one fsync'd JSONL line to its logical-day shard (ADR-0014
// §2). The event must already carry its logical_date (the router derives it,
// backdated by --day for a contact); the id is assigned here, so any id the
// caller set is replaced. The schema is stamped and an empty source defaults
// to the documented `contact` provenance. A malformed event is refused before
// anything is written. It returns the event with its id filled.
func (a *Adapter) AppendContactEvent(e contact.Event) (contact.Event, error) {
	if e.LogicalDate == "" {
		return contact.Event{}, fmt.Errorf("storage: contact event is missing logical_date")
	}
	e.Schema = contact.Schema
	if e.Source == "" {
		e.Source = contact.SourceContact
	}
	path, err := a.contactDayPath(e.LogicalDate)
	if err != nil {
		return contact.Event{}, err
	}
	slot, err := a.nextContactSlot(path)
	if err != nil {
		return contact.Event{}, err
	}
	e.ID = contact.ID(e.LogicalDate, slot)

	if err = e.Validate(); err != nil {
		return contact.Event{}, err
	}
	line, err := e.MarshalLine()
	if err != nil {
		return contact.Event{}, err
	}
	if err := appendLineFsync(path, line); err != nil {
		return contact.Event{}, err
	}
	return e, nil
}

// nextContactSlot returns the first slot past every slot already used in the
// day shard (ADR-0014 §2) — never the line count. A line that still decodes
// contributes its id; a line that no longer decodes contributes the id it
// still names, if any, so a damaged line's slot is never reissued as a second
// receipt. A missing shard starts at slot 0 (`_a`).
func (a *Adapter) nextContactSlot(path string) (int, error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return 0, err
	}
	next := 0
	for _, ln := range lines {
		id := ""
		if e, perr := contact.UnmarshalEventLine(ln); perr == nil {
			id = e.ID
		} else if m := contactIDField.FindSubmatch(ln); m != nil {
			id = string(m[1])
		}
		if n, ok := contactSlotIndex(id); ok && n >= next {
			next = n + 1
		}
	}
	return next, nil
}

// contactSlotIndex returns the zero-based slot an event id carries — the
// inverse of the [engine.SlotLabel] grammar (a→0, z→25, aa→26) — and ok=false
// for anything that is not a well-formed contact id.
func contactSlotIndex(id string) (int, bool) {
	if _, ok := contact.Date(id); !ok {
		return 0, false
	}
	label := id[strings.LastIndexByte(id, '_')+1:]
	n := 0
	for _, r := range label {
		n = n*26 + int(r-'a') + 1
	}
	return n - 1, true
}

// ReadContactDay reads the raw events for one logical day, skipping malformed
// lines and reporting how many were skipped (error-states "JSONL corruption").
// An event that decodes but does not validate is returned as is: the fold
// skips it, and returning it keeps its id visible to every reader.
func (a *Adapter) ReadContactDay(date string) (events []contact.Event, skipped int, err error) {
	path, err := a.contactDayPath(date)
	if err != nil {
		return nil, 0, err
	}
	return a.readContactFile(path)
}

// ReadContactEvents reads every stored contact event across the tree, in
// order: logical day, then slot. It is the raw, unfolded stream — person keys
// are as written, and voids sit beside the events they correct — that the
// router canonicalizes and folds ([contact.Fold]). Malformed lines are
// skipped; a missing tree is the empty stream, not an error.
func (a *Adapter) ReadContactEvents() ([]contact.Event, error) {
	var out []contact.Event
	err := filepath.WalkDir(a.contactDir(), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), contactFilePrefix) || !strings.HasSuffix(d.Name(), contactFileExt) {
			return nil
		}
		events, _, rerr := a.readContactFile(path)
		if rerr != nil {
			return rerr
		}
		out = append(out, events...)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	slices.SortStableFunc(out, compareContactEvents)
	return out, nil
}

// ReadContactEventByID reads a single stored event by its id, deriving the
// logical day the id encodes ([contact.Date]) and scanning that day's shard. A
// missing event — an unparseable id, an absent shard, or a day that holds no
// such id — is (zero, false, nil), never an error, so a caller can treat
// absence as a clean "not found". The event is returned raw (unfolded).
func (a *Adapter) ReadContactEventByID(id string) (contact.Event, bool, error) {
	date, ok := contact.Date(id)
	if !ok {
		return contact.Event{}, false, nil
	}
	events, _, err := a.ReadContactDay(date)
	if err != nil {
		return contact.Event{}, false, err
	}
	for _, e := range events {
		if e.ID == id {
			return e, true, nil
		}
	}
	return contact.Event{}, false, nil
}

// readContactFile reads and parses one contact shard, returning the events
// that decode and the count of skipped malformed lines.
func (a *Adapter) readContactFile(path string) (events []contact.Event, skipped int, err error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return nil, 0, err
	}
	for _, ln := range lines {
		e, perr := contact.UnmarshalEventLine(ln)
		if perr != nil {
			skipped++
			continue
		}
		events = append(events, e)
	}
	return events, skipped, nil
}

// compareContactEvents orders events by logical day, then slot (so `_z` comes
// before `_aa`), then id for anything that is not a well-formed contact id.
func compareContactEvents(x, y contact.Event) int {
	if c := cmp.Compare(x.LogicalDate, y.LogicalDate); c != 0 {
		return c
	}
	sx, okx := contactSlotIndex(x.ID)
	sy, oky := contactSlotIndex(y.ID)
	if okx && oky {
		if c := cmp.Compare(sx, sy); c != 0 {
			return c
		}
	}
	return cmp.Compare(x.ID, y.ID)
}
