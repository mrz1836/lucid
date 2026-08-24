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

	"github.com/mrz1836/lucid/internal/focus"
)

// Focus-tree names under ~/.lucid/ (focus.md §2, §4). Only this adapter touches
// them; the focus record family is agent-free and reads/writes exclusively
// through these ops (architecture P3). The surface-state projection lives at the
// tree root (reusing the shared surfaceStateFile name), separate from the
// append-only entry stream, so entry history stays pure while `surface` tracks
// what it has shown.
const (
	focusDirName    = "focus"
	focusFilePrefix = "focus_"
	focusFileExt    = ".jsonl"
)

// focusSurfaceState is the verb-owned rotation projection (focus.md §4): the
// per-id last-surfaced logical-date and the current day's pick. It is rebuildable
// and disposable — the entry JSONL is the source of truth, and deleting it only
// resets rotation memory. It records only which focus item was shown when, never
// anything that grades the user (focus.md §0).
type focusSurfaceState struct {
	Schema       int               `json:"schema"`
	LastSurfaced map[string]string `json:"last_surfaced"`
	Today        *focusTodayPick   `json:"today"`
}

// focusTodayPick is the current logical day's recorded pick — the day key and the
// chosen focus id — that makes `surface` idempotent within a day.
type focusTodayPick struct {
	Day string `json:"day"`
	ID  string `json:"id"`
}

// focusDir returns ~/.lucid/focus/.
func (a *Adapter) focusDir() string { return filepath.Join(a.home, focusDirName) }

// focusSurfaceStatePath returns ~/.lucid/focus/surface_state.json.
func (a *Adapter) focusSurfaceStatePath() string {
	return filepath.Join(a.focusDir(), surfaceStateFile)
}

// focusDayPath returns focus/YYYY/MM/focus_YYYY_MM_DD.jsonl for a logical date
// (YYYY-MM-DD).
func (a *Adapter) focusDayPath(date string) string {
	parts := strings.Split(date, "-")
	year, month := "0000", "00"
	if len(parts) == 3 {
		year, month = parts[0], parts[1]
	}
	name := focusFilePrefix + strings.ReplaceAll(date, "-", "_") + focusFileExt
	return filepath.Join(a.focusDir(), year, month, name)
}

// ScaffoldFocus creates the focus/ tree. It is idempotent — an existing tree (and
// everything under it, including the surface-state projection) is left untouched
// (mirroring [Adapter.ScaffoldReframes]). The append and surface paths also
// self-create their parents, so scaffolding is a convenience the router runs at
// boot, not a precondition for a write.
func (a *Adapter) ScaffoldFocus() error {
	if err := os.MkdirAll(a.focusDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create focus dir: %w", err)
	}
	return nil
}

// AppendFocus assigns the next sequence id under the single-writer discipline and
// appends the entry as one fsync'd JSONL line to its logical-day file (focus.md
// §2). The entry must already carry its logical_date (the CLI derives it,
// backdated by --day); the id is assigned here. An empty source defaults to the
// documented `focus` provenance, and an empty state defaults to active. It
// returns the entry with its id (and any defaults) filled.
func (a *Adapter) AppendFocus(f focus.Focus) (focus.Focus, error) {
	if f.LogicalDate == "" {
		return focus.Focus{}, fmt.Errorf("storage: focus is missing logical_date")
	}
	f.Schema = focus.Schema
	if f.Source == "" {
		f.Source = focus.SourceFocus
	}
	if f.State == "" {
		f.State = focus.StateActive
	}
	path := a.focusDayPath(f.LogicalDate)

	seq, err := a.nextFocusSeq(path)
	if err != nil {
		return focus.Focus{}, err
	}
	f.ID = focus.FocusID(f.LogicalDate, seq)

	if err = f.Validate(); err != nil {
		return focus.Focus{}, err
	}
	line, err := f.MarshalLine()
	if err != nil {
		return focus.Focus{}, err
	}
	if err := appendLineFsync(path, line); err != nil {
		return focus.Focus{}, err
	}
	return f, nil
}

// RetireFocus retires an active focus item by appending a retirement event that
// references it by id (focus.md §2) — the item is never deleted or rewritten, so
// history is preserved and `list --all` still shows it. The marker lands on the
// logical day named by dayKey with recordedAt as its write time (the router
// resolves both from now). It errors if the id names no focus item or one that is
// already retired. It returns the appended retirement event.
func (a *Adapter) RetireFocus(id, dayKey, recordedAt string) (focus.Focus, error) {
	pool, err := a.ReadFocus()
	if err != nil {
		return focus.Focus{}, err
	}
	var target *focus.Focus
	for i := range pool {
		if pool[i].ID == id {
			t := pool[i]
			target = &t
			break
		}
	}
	if target == nil {
		return focus.Focus{}, fmt.Errorf("storage: no focus item %q to retire", id)
	}
	if target.State == focus.StateRetired {
		return focus.Focus{}, fmt.Errorf("storage: focus item %q is already retired", id)
	}

	marker := focus.NewRetirement(id, dayKey, recordedAt)
	path := a.focusDayPath(marker.LogicalDate)
	seq, err := a.nextFocusSeq(path)
	if err != nil {
		return focus.Focus{}, err
	}
	marker.ID = focus.FocusID(marker.LogicalDate, seq)
	if err = marker.Validate(); err != nil {
		return focus.Focus{}, err
	}
	line, err := marker.MarshalLine()
	if err != nil {
		return focus.Focus{}, err
	}
	if err := appendLineFsync(path, line); err != nil {
		return focus.Focus{}, err
	}
	return marker, nil
}

// nextFocusSeq returns max-seq+1 over the well-formed lines in the day file
// (focus.md §2: never line count, single-writer). A missing file starts at seq 1;
// malformed lines are ignored, so a truncated line never perturbs id assignment.
// Both focus items and retirement markers occupy the day's sequence, so the seq
// derivation counts either.
func (a *Adapter) nextFocusSeq(path string) (int, error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return 0, err
	}
	maxSeq := 0
	for _, ln := range lines {
		f, perr := focus.UnmarshalFocusLine(ln)
		if perr != nil {
			continue // malformed line: does not contribute a seq
		}
		if s, ok := focus.ParseSeq(f.ID); ok && s > maxSeq {
			maxSeq = s
		}
	}
	return maxSeq + 1, nil
}

// ReadFocusDay reads the raw entries for one logical day, skipping malformed
// lines and reporting how many were skipped (error-states "JSONL corruption").
// It does not fold state — that is [Adapter.ReadFocus]'s job, because a retirement
// event for a backdated item lives in a different day file.
func (a *Adapter) ReadFocusDay(date string) (entries []focus.Focus, skipped int, err error) {
	return a.readFocusFile(a.focusDayPath(date))
}

// ReadFocus reads every stored focus entry across the tree, sorted by id, with
// state folded and retirement markers removed (focus.md §2, §5) — the item view
// `list` prints (active by default, retired under `--all`) and `surface` selects
// from (active only). A retired item is returned with State=retired, not dropped.
func (a *Adapter) ReadFocus() ([]focus.Focus, error) {
	all, err := a.readAllFocus()
	if err != nil {
		return nil, err
	}
	return focus.FoldState(all), nil
}

// ReadFocusByID reads a single stored entry by its id, deriving the logical day
// the id encodes ([focus.FocusDate]) and scanning that day's file. A missing
// entry — an unparseable id, an absent day file, or a day that holds no such id —
// is (zero, false, nil), never an error, so a caller can treat absence as a clean
// "not found". It returns the raw stored entry (unfolded), the direct lookup
// `surface` uses to resolve a picked id to its content.
func (a *Adapter) ReadFocusByID(id string) (focus.Focus, bool, error) {
	date, ok := focus.FocusDate(id)
	if !ok {
		return focus.Focus{}, false, nil
	}
	entries, _, err := a.ReadFocusDay(date)
	if err != nil {
		return focus.Focus{}, false, err
	}
	for _, f := range entries {
		if f.ID == id {
			return f, true, nil
		}
	}
	return focus.Focus{}, false, nil
}

// readAllFocus walks the focus tree and returns every well-formed entry, sorted
// by id (ids encode the logical date and a monotonic seq, so id order is time
// order). The surface-state projection at the tree root is skipped by the
// prefix/suffix filter — it is not a focus_*.jsonl day file.
func (a *Adapter) readAllFocus() ([]focus.Focus, error) {
	var out []focus.Focus
	err := filepath.WalkDir(a.focusDir(), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), focusFilePrefix) || !strings.HasSuffix(d.Name(), focusFileExt) {
			return nil
		}
		entries, _, rerr := a.readFocusFile(path)
		if rerr != nil {
			return rerr
		}
		out = append(out, entries...)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	slices.SortFunc(out, func(a, b focus.Focus) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// readFocusFile reads and parses one focus file, returning the well-formed
// entries and the count of skipped malformed lines.
func (a *Adapter) readFocusFile(path string) (entries []focus.Focus, skipped int, err error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return nil, 0, err
	}
	for _, ln := range lines {
		f, perr := focus.UnmarshalFocusLine(ln)
		if perr != nil {
			skipped++
			continue
		}
		entries = append(entries, f)
	}
	return entries, skipped, nil
}

// SurfaceFocus returns exactly one active focus item for the logical day named by
// dayKey and records that it was shown (focus.md §4). The caller resolves dayKey
// from now ([focus.LogicalDay]); passing it in keeps the rotation deterministic
// and injectable in tests. It is idempotent within a day — a pick already
// recorded for dayKey is returned unchanged and the rotation does not advance. On
// the first call of a new day it selects the least-recently-surfaced active entry
// (unsurfaced entries first, ties broken by focus id ascending), records it as
// today's pick, and advances. Retired items are filtered out before selection. An
// empty active pool is a clean (zero, false, nil) — "nothing to surface," never an
// error.
func (a *Adapter) SurfaceFocus(dayKey string) (focus.Focus, bool, error) {
	all, err := a.ReadFocus()
	if err != nil {
		return focus.Focus{}, false, err
	}
	pool := activeFocus(all)
	if len(pool) == 0 {
		return focus.Focus{}, false, nil
	}
	st, err := a.readFocusSurfaceState()
	if err != nil {
		return focus.Focus{}, false, err
	}

	// Within-day idempotence: return the recorded pick unchanged, no advance.
	if st.Today != nil && st.Today.Day == dayKey {
		if f, ok := findFocusByID(pool, st.Today.ID); ok {
			return f, true, nil
		}
		// The recorded pick was retired since it was chosen: fall through and
		// re-pick from the current active pool so the day still surfaces a live item.
	}

	pick := leastRecentlySurfacedFocus(pool, st.LastSurfaced)
	if st.LastSurfaced == nil {
		st.LastSurfaced = map[string]string{}
	}
	st.Schema = surfaceStateSchema
	st.LastSurfaced[pick.ID] = dayKey
	st.Today = &focusTodayPick{Day: dayKey, ID: pick.ID}
	if err := a.writeFocusSurfaceState(st); err != nil {
		return focus.Focus{}, false, err
	}
	return pick, true, nil
}

// activeFocus returns only the active items from a folded pool, preserving order
// (state has already been resolved by [focus.FoldState]).
func activeFocus(pool []focus.Focus) []focus.Focus {
	out := make([]focus.Focus, 0, len(pool))
	for _, f := range pool {
		if f.State == focus.StateActive {
			out = append(out, f)
		}
	}
	return out
}

// leastRecentlySurfacedFocus returns the entry surfaced least recently. pool is
// already sorted by id; a stable sort keyed by the last-surfaced date leaves
// equal-date entries (including the cold-start case where every date is the empty
// string, which sorts before any real YYYY-MM-DD) in id-ascending order, so the
// pick is fully deterministic (focus.md §4).
func leastRecentlySurfacedFocus(pool []focus.Focus, last map[string]string) focus.Focus {
	ordered := slices.Clone(pool)
	slices.SortStableFunc(ordered, func(x, y focus.Focus) int {
		return cmp.Compare(last[x.ID], last[y.ID])
	})
	return ordered[0]
}

// findFocusByID returns the pool entry with the given id.
func findFocusByID(pool []focus.Focus, id string) (focus.Focus, bool) {
	for _, f := range pool {
		if f.ID == id {
			return f, true
		}
	}
	return focus.Focus{}, false
}

// readFocusSurfaceState reads the rotation projection, treating both a missing and
// a corrupt file as a fresh (cold-start) state rather than an error — it is
// rebuildable and disposable (focus.md §4), so a torn write only resets rotation
// memory and the next surface re-cold-starts.
func (a *Adapter) readFocusSurfaceState() (focusSurfaceState, error) {
	return readJSONResilient[focusSurfaceState](a.focusSurfaceStatePath(), "focus surface state")
}

// writeFocusSurfaceState persists the rotation projection, creating the focus tree
// if needed. It is the only writer of the focus surface_state.json (architecture
// P3).
func (a *Adapter) writeFocusSurfaceState(st focusSurfaceState) error {
	if err := ensureDir(a.focusDir(), "focus"); err != nil {
		return err
	}
	b, err := marshalJSON(st)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.focusSurfaceStatePath(), b, filePerm); err != nil {
		return fmt.Errorf("storage: write focus surface state: %w", err)
	}
	return nil
}
