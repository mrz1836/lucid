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

	"github.com/mrz1836/lucid/internal/reframes"
)

// Reframe-tree names under ~/.lucid/ (reframes.md §2, §4). Only this adapter
// touches them; the reframe record family is agent-free and reads/writes
// exclusively through these ops (architecture P3). The surface-state projection
// lives at the tree root, separate from the append-only entry stream, so entry
// history stays pure while `surface` tracks what it has shown.
const (
	reframesDirName   = "reframes"
	reframeFilePrefix = "reframe_"
	reframeFileExt    = ".jsonl"
	surfaceStateFile  = "surface_state.json"
	// surfaceStateSchema versions the rebuildable projection independently of
	// the entry schema; it is disposable, so a bump only changes how a stale
	// file is read, never entry history.
	surfaceStateSchema = 1
)

// reframeSurfaceState is the verb-owned rotation projection (reframes.md §4):
// the per-id last-surfaced logical-date and the current day's pick. It is
// rebuildable and disposable — the entry JSONL is the source of truth, and
// deleting it only resets rotation memory. It records only which reframe was
// shown when, never anything that grades the user (reframes.md §0).
type reframeSurfaceState struct {
	Schema       int               `json:"schema"`
	LastSurfaced map[string]string `json:"last_surfaced"`
	Today        *reframeTodayPick `json:"today"`
}

// reframeTodayPick is the current logical day's recorded pick — the day key and
// the chosen reframe id — that makes `surface` idempotent within a day.
type reframeTodayPick struct {
	Day string `json:"day"`
	ID  string `json:"id"`
}

// reframesDir returns ~/.lucid/reframes/.
func (a *Adapter) reframesDir() string { return filepath.Join(a.home, reframesDirName) }

// surfaceStatePath returns ~/.lucid/reframes/surface_state.json.
func (a *Adapter) surfaceStatePath() string {
	return filepath.Join(a.reframesDir(), surfaceStateFile)
}

// reframeDayPath returns reframes/YYYY/MM/reframe_YYYY_MM_DD.jsonl for a logical
// date (YYYY-MM-DD). A malformed date is rejected by [safeDayShard] so it can
// never build a path outside the reframes tree (defense-in-depth: the CLI
// derives every logical_date from the civil-day rule).
func (a *Adapter) reframeDayPath(date string) (string, error) {
	year, month, err := safeDayShard(date)
	if err != nil {
		return "", err
	}
	name := reframeFilePrefix + strings.ReplaceAll(date, "-", "_") + reframeFileExt
	return filepath.Join(a.reframesDir(), year, month, name), nil
}

// ScaffoldReframes creates the reframes/ tree. It is idempotent — an existing
// tree (and everything under it, including the surface-state projection) is
// left untouched (mirroring [Adapter.ScaffoldObservations] / [Adapter.ScaffoldEngine]).
// The append and surface paths also self-create their parents, so scaffolding
// is a convenience the router runs at boot, not a precondition for a write.
func (a *Adapter) ScaffoldReframes() error {
	if err := os.MkdirAll(a.reframesDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create reframes dir: %w", err)
	}
	return nil
}

// AppendReframe assigns the next sequence id under the single-writer discipline
// and appends the entry as one fsync'd JSONL line to its logical-day file
// (reframes.md §2). The entry must already carry its logical_date (the CLI
// derives it, backdated by --day); the id is assigned here. An empty source
// defaults to the documented `reframe` provenance (reframes.md §7). It returns
// the entry with its id (and any defaults) filled.
func (a *Adapter) AppendReframe(rf reframes.Reframe) (reframes.Reframe, error) {
	if rf.LogicalDate == "" {
		return reframes.Reframe{}, fmt.Errorf("storage: reframe is missing logical_date")
	}
	rf.Schema = reframes.Schema
	if rf.Source == "" {
		rf.Source = reframes.SourceReframe
	}
	path, err := a.reframeDayPath(rf.LogicalDate)
	if err != nil {
		return reframes.Reframe{}, err
	}

	seq, err := a.nextReframeSeq(path)
	if err != nil {
		return reframes.Reframe{}, err
	}
	rf.ID = reframes.ReframeID(rf.LogicalDate, seq)

	if err = rf.Validate(); err != nil {
		return reframes.Reframe{}, err
	}
	line, err := rf.MarshalLine()
	if err != nil {
		return reframes.Reframe{}, err
	}
	if err := appendLineFsync(path, line); err != nil {
		return reframes.Reframe{}, err
	}
	return rf, nil
}

// nextReframeSeq returns max-seq+1 over the well-formed lines in the day file
// (reframes.md §2: never line count, single-writer). A missing file starts at
// seq 1; malformed lines are ignored, so a truncated line never perturbs id
// assignment.
func (a *Adapter) nextReframeSeq(path string) (int, error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return 0, err
	}
	maxSeq := 0
	for _, ln := range lines {
		rf, perr := reframes.UnmarshalReframeLine(ln)
		if perr != nil {
			continue // malformed line: does not contribute a seq
		}
		if s, ok := reframes.ParseSeq(rf.ID); ok && s > maxSeq {
			maxSeq = s
		}
	}
	return maxSeq + 1, nil
}

// ReadReframesDay reads the raw entries for one logical day, skipping malformed
// lines and reporting how many were skipped (error-states "JSONL corruption").
// It does not fold corrections — that is [Adapter.ReadReframes]'s job, because a
// correction of a backdated entry lives in a different day file.
func (a *Adapter) ReadReframesDay(date string) (entries []reframes.Reframe, skipped int, err error) {
	path, err := a.reframeDayPath(date)
	if err != nil {
		return nil, 0, err
	}
	return a.readReframeFile(path)
}

// ReadReframes reads every stored reframe across the tree, sorted by id, with
// corrections folded and superseded entries dropped (reframes.md §2, §5) — the
// live pool `list` prints and `surface` selects from.
func (a *Adapter) ReadReframes() ([]reframes.Reframe, error) {
	all, err := a.readAllReframes()
	if err != nil {
		return nil, err
	}
	return reframes.FoldCorrections(all), nil
}

// ReadReframeByID reads a single reframe by its id, deriving the logical day the
// id encodes ([reframes.ReframeDate]) and scanning that day's file. A missing
// reframe — an unparseable id, an absent day file, or a day that holds no such
// id — is (zero, false, nil), never an error, so a caller can treat absence as a
// clean "not found". It returns the raw stored entry (unfolded), the direct
// lookup `surface` uses to resolve a picked id to its content.
func (a *Adapter) ReadReframeByID(id string) (reframes.Reframe, bool, error) {
	date, ok := reframes.ReframeDate(id)
	if !ok {
		return reframes.Reframe{}, false, nil
	}
	entries, _, err := a.ReadReframesDay(date)
	if err != nil {
		return reframes.Reframe{}, false, err
	}
	for _, rf := range entries {
		if rf.ID == id {
			return rf, true, nil
		}
	}
	return reframes.Reframe{}, false, nil
}

// readAllReframes walks the reframe tree and returns every well-formed entry,
// sorted by id (ids encode the logical date and a monotonic seq, so id order is
// time order). The surface-state projection at the tree root is skipped by the
// prefix/suffix filter — it is not a reframe_*.jsonl day file.
func (a *Adapter) readAllReframes() ([]reframes.Reframe, error) {
	var out []reframes.Reframe
	err := filepath.WalkDir(a.reframesDir(), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), reframeFilePrefix) || !strings.HasSuffix(d.Name(), reframeFileExt) {
			return nil
		}
		entries, _, rerr := a.readReframeFile(path)
		if rerr != nil {
			return rerr
		}
		out = append(out, entries...)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	slices.SortFunc(out, func(a, b reframes.Reframe) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// readReframeFile reads and parses one reframe file, returning the well-formed
// entries and the count of skipped malformed lines.
func (a *Adapter) readReframeFile(path string) (entries []reframes.Reframe, skipped int, err error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return nil, 0, err
	}
	for _, ln := range lines {
		rf, perr := reframes.UnmarshalReframeLine(ln)
		if perr != nil {
			skipped++
			continue
		}
		entries = append(entries, rf)
	}
	return entries, skipped, nil
}

// SurfaceReframe returns exactly one reframe for the logical day named by dayKey
// and records that it was shown (reframes.md §4). The caller resolves dayKey
// from now ([reframes.LogicalDay]); passing it in keeps the rotation
// deterministic and injectable in tests. It is idempotent within a day — a pick
// already recorded for dayKey is returned unchanged and the rotation does not
// advance. On the first call of a new day it selects the least-recently-surfaced
// non-superseded entry (unsurfaced entries first, ties broken by reframe id
// ascending), records it as today's pick, and advances. An empty pool is a clean
// (zero, false, nil) — "nothing to surface," never an error.
func (a *Adapter) SurfaceReframe(dayKey string) (reframes.Reframe, bool, error) {
	pool, err := a.ReadReframes()
	if err != nil {
		return reframes.Reframe{}, false, err
	}
	if len(pool) == 0 {
		return reframes.Reframe{}, false, nil
	}
	st, err := a.readSurfaceState()
	if err != nil {
		return reframes.Reframe{}, false, err
	}

	// Within-day idempotence: return the recorded pick unchanged, no advance.
	if st.Today != nil && st.Today.Day == dayKey {
		if rf, ok := findReframeByID(pool, st.Today.ID); ok {
			return rf, true, nil
		}
		// The recorded pick was superseded since it was chosen: fall through and
		// re-pick from the current pool so the day still surfaces a live entry.
	}

	pick := leastRecentlySurfaced(pool, st.LastSurfaced)
	if st.LastSurfaced == nil {
		st.LastSurfaced = map[string]string{}
	}
	st.Schema = surfaceStateSchema
	st.LastSurfaced[pick.ID] = dayKey
	st.Today = &reframeTodayPick{Day: dayKey, ID: pick.ID}
	if err := a.writeSurfaceState(st); err != nil {
		return reframes.Reframe{}, false, err
	}
	return pick, true, nil
}

// leastRecentlySurfaced returns the entry surfaced least recently. pool is
// already sorted by id; a stable sort keyed by the last-surfaced date leaves
// equal-date entries (including the cold-start case where every date is the
// empty string, which sorts before any real YYYY-MM-DD) in id-ascending order,
// so the pick is fully deterministic (reframes.md §4).
func leastRecentlySurfaced(pool []reframes.Reframe, last map[string]string) reframes.Reframe {
	ordered := slices.Clone(pool)
	slices.SortStableFunc(ordered, func(x, y reframes.Reframe) int {
		return cmp.Compare(last[x.ID], last[y.ID])
	})
	return ordered[0]
}

// findReframeByID returns the pool entry with the given id.
func findReframeByID(pool []reframes.Reframe, id string) (reframes.Reframe, bool) {
	for _, rf := range pool {
		if rf.ID == id {
			return rf, true
		}
	}
	return reframes.Reframe{}, false
}

// readSurfaceState reads the rotation projection, treating both a missing and a
// corrupt file as a fresh (cold-start) state rather than an error — it is
// rebuildable and disposable (reframes.md §4), so a torn write only resets
// rotation memory and the next surface re-cold-starts.
func (a *Adapter) readSurfaceState() (reframeSurfaceState, error) {
	return readJSONResilient[reframeSurfaceState](a.surfaceStatePath(), "reframe surface state")
}

// writeSurfaceState persists the rotation projection, creating the reframes tree
// if needed. It is the only writer of surface_state.json (architecture P3).
func (a *Adapter) writeSurfaceState(st reframeSurfaceState) error {
	if err := ensureDir(a.reframesDir(), "reframes"); err != nil {
		return err
	}
	b, err := marshalJSON(st)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.surfaceStatePath(), b, filePerm); err != nil {
		return fmt.Errorf("storage: write reframe surface state: %w", err)
	}
	return nil
}
