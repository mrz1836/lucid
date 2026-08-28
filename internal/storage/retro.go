package storage

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mrz1836/lucid/internal/retro"
)

// Retro-tree names under ~/.lucid/ (retro.md §2). Only this adapter touches
// them; the retro record family is agent-free and reads/writes exclusively
// through these ops (architecture P3). Unlike focus there is no surface-state
// projection — a parking lot is walked whole, not rotated one-per-day.
const (
	retroDirName    = "retro"
	retroFilePrefix = "retro_"
	retroFileExt    = ".jsonl"
)

// retroDir returns ~/.lucid/retro/.
func (a *Adapter) retroDir() string { return filepath.Join(a.home, retroDirName) }

// retroDayPath returns retro/YYYY/MM/retro_YYYY_MM_DD.jsonl for a logical date
// (YYYY-MM-DD). A malformed date is rejected by [safeDayShard] so it can never
// build a path outside the retro tree (defense-in-depth: the CLI derives every
// logical_date from the civil-day rule).
func (a *Adapter) retroDayPath(date string) (string, error) {
	year, month, err := safeDayShard(date)
	if err != nil {
		return "", err
	}
	name := retroFilePrefix + strings.ReplaceAll(date, "-", "_") + retroFileExt
	return filepath.Join(a.retroDir(), year, month, name), nil
}

// ScaffoldRetro creates the retro/ tree. It is idempotent — an existing tree is
// left untouched (mirroring [Adapter.ScaffoldFocus]). The append path
// self-creates its parents, so scaffolding is a convenience the router runs at
// boot, not a precondition for a write.
func (a *Adapter) ScaffoldRetro() error {
	if err := os.MkdirAll(a.retroDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create retro dir: %w", err)
	}
	return nil
}

// AppendRetroEvent appends one lifecycle event as an fsync'd JSONL line to its
// logical-day file under the single-writer discipline (retro.md §2). It mints the
// two ids kept distinct (§1):
//
//   - the parked-item id (R-NNN) is minted global-monotonic across the whole
//     store, but ONLY for a park with no supplied id. A park that carries an id
//     (the one-time import) keeps it, and a resolve/defer names an existing item
//     and mints nothing — so a transition never advances the R-NNN counter;
//   - the per-write receipt (event_id) is minted per logical-day for EVERY
//     appended event, so two writes touching the same R-NNN return two different
//     receipts.
//
// The event must already carry its logical_date (the router derives it, backdated
// by --day on a park). An empty source defaults to the documented `retro`
// provenance, and an empty status defaults by event type. It returns the event
// with both ids (and any defaults) filled.
func (a *Adapter) AppendRetroEvent(ev retro.Retro) (retro.Retro, error) {
	if ev.LogicalDate == "" {
		return retro.Retro{}, fmt.Errorf("storage: retro event is missing logical_date")
	}
	ev.Schema = retro.Schema
	if ev.Source == "" {
		ev.Source = retro.SourceRetro
	}
	if ev.Status == "" {
		ev.Status = defaultRetroStatus(ev.EventType)
	}
	path, err := a.retroDayPath(ev.LogicalDate)
	if err != nil {
		return retro.Retro{}, err
	}

	// Mint the parked-item id only for a park with no supplied id; the import
	// path supplies its own explicit ids, and transitions reference an existing
	// item (so neither advances the global counter).
	if ev.EventType == retro.EventPark && ev.ID == "" {
		seq, serr := a.nextRetroSeq()
		if serr != nil {
			return retro.Retro{}, serr
		}
		ev.ID = retro.ID(seq)
	}

	// Every appended event consumes a fresh per-day receipt.
	eseq, err := a.nextRetroEventSeq(path)
	if err != nil {
		return retro.Retro{}, err
	}
	ev.EventID = retro.EventID(ev.LogicalDate, eseq)

	if err = ev.Validate(); err != nil {
		return retro.Retro{}, err
	}
	line, err := ev.MarshalLine()
	if err != nil {
		return retro.Retro{}, err
	}
	if err := appendLineFsync(path, line); err != nil {
		return retro.Retro{}, err
	}
	return ev, nil
}

// AppendRetroTransition appends a resolve or defer event that references an
// existing parked item (retro.md §5). It guards the never-conjure rule: a
// transition may only name an item a park already minted, so an id that folds to
// nothing is a clean error and nothing is written. When the target exists it
// delegates to [Adapter.AppendRetroEvent], which mints the fresh per-write
// receipt and — because the event is not a park — never advances the R-NNN
// counter. It returns the appended event (with its receipt filled).
func (a *Adapter) AppendRetroTransition(ev retro.Retro) (retro.Retro, error) {
	if _, found, err := a.ReadRetroByID(ev.ID); err != nil {
		return retro.Retro{}, err
	} else if !found {
		return retro.Retro{}, fmt.Errorf("storage: no retro item %q to transition", ev.ID)
	}
	return a.AppendRetroEvent(ev)
}

// defaultRetroStatus maps an event type to the status it sets when the caller
// left it empty: park → open, resolve → resolved, defer → deferred. The router
// sets it explicitly; this is the storage-side safety net.
func defaultRetroStatus(eventType string) string {
	switch eventType {
	case retro.EventResolve:
		return retro.StatusResolved
	case retro.EventDefer:
		return retro.StatusDeferred
	default:
		return retro.StatusOpen
	}
}

// nextRetroSeq returns max-R-NNN+1 over every well-formed park event in the whole
// retro tree (retro.md §2: global-monotonic, single-writer, never line count). An
// empty tree starts at seq 1. Only park events feed the max; resolve/defer events
// reference an existing id and are ignored, so a transition never opens a gap in
// the numbering. After a bulk import that supplies explicit ids, the next park
// continues at imported-max+1 with no separate seed step.
func (a *Adapter) nextRetroSeq() (int, error) {
	all, err := a.readAllRetro()
	if err != nil {
		return 0, err
	}
	maxSeq := 0
	for _, ev := range all {
		if ev.EventType != retro.EventPark {
			continue
		}
		if s, ok := retro.ParseSeq(ev.ID); ok && s > maxSeq {
			maxSeq = s
		}
	}
	return maxSeq + 1, nil
}

// nextRetroEventSeq returns max-event-seq+1 over the well-formed lines in one
// logical-day file (retro.md §2: never line count, single-writer). A missing file
// starts at seq 1; malformed lines are ignored, so a truncated line never
// perturbs receipt assignment. Every event kind occupies the day's receipt
// sequence, so the derivation counts park, resolve, and defer alike.
func (a *Adapter) nextRetroEventSeq(path string) (int, error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return 0, err
	}
	maxSeq := 0
	for _, ln := range lines {
		ev, perr := retro.UnmarshalLine(ln)
		if perr != nil {
			continue // malformed line: does not contribute a seq
		}
		if s, ok := retro.ParseEventSeq(ev.EventID); ok && s > maxSeq {
			maxSeq = s
		}
	}
	return maxSeq + 1, nil
}

// ReadRetro reads every stored retro event across the tree, folds the stream into
// current items, and returns them sorted by ascending R-NNN (retro.md §2, §4) —
// the item view `list` filters and `show` selects from. A resolved item is
// returned with status resolved, not dropped; a deferred item with status
// deferred and its reason. It writes nothing.
func (a *Adapter) ReadRetro() ([]retro.Item, error) {
	all, err := a.readAllRetro()
	if err != nil {
		return nil, err
	}
	items := retro.FoldState(all)
	slices.SortFunc(items, func(x, y retro.Item) int {
		xs, _ := retro.ParseSeq(x.ID)
		ys, _ := retro.ParseSeq(y.ID)
		return cmp.Compare(xs, ys)
	})
	return items, nil
}

// ReadRetroByID folds the whole stream and returns the single item with the given
// R-NNN id, or found=false when no park event ever minted it. A missing item is a
// clean (zero, false, nil), never an error, so a caller can treat absence as
// "not found" — the lookup `show` and the transition guards use.
func (a *Adapter) ReadRetroByID(id string) (retro.Item, bool, error) {
	items, err := a.ReadRetro()
	if err != nil {
		return retro.Item{}, false, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, true, nil
		}
	}
	return retro.Item{}, false, nil
}

// readAllRetro walks the retro tree and returns every well-formed event, sorted
// by receipt id (receipts encode the logical date and a monotonic seq, so receipt
// order is time order — the order the fold applies transitions in).
func (a *Adapter) readAllRetro() ([]retro.Retro, error) {
	var out []retro.Retro
	err := filepath.WalkDir(a.retroDir(), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), retroFilePrefix) || !strings.HasSuffix(d.Name(), retroFileExt) {
			return nil
		}
		events, _, rerr := a.readRetroFile(path)
		if rerr != nil {
			return rerr
		}
		out = append(out, events...)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	slices.SortFunc(out, func(x, y retro.Retro) int { return cmp.Compare(x.EventID, y.EventID) })
	return out, nil
}

// readRetroFile reads and parses one retro day file, returning the well-formed
// events and the count of skipped malformed lines.
func (a *Adapter) readRetroFile(path string) (events []retro.Retro, skipped int, err error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return nil, 0, err
	}
	for _, ln := range lines {
		ev, perr := retro.UnmarshalLine(ln)
		if perr != nil {
			skipped++
			continue
		}
		events = append(events, ev)
	}
	return events, skipped, nil
}
