package storage

import (
	"cmp"
	"fmt"
	"os"
	"slices"
)

// surface.go is the shared one-per-day rotation the focus and reframe record
// families both run (focus.md §4 / reframes.md §4). Their surface-state
// projections are byte-identical JSON and their selection logic was, until this,
// two near-identical copies — the divergence risk the maintainability pass calls
// out (a fix to one rotation path silently missing the other). The rotation now
// lives here once, parameterized over the entry type and the pool the caller
// hands it; each family keeps only what genuinely differs: which fold builds the
// pool (FoldState vs FoldCorrections) and, for focus, the active-only filter.

const (
	// surfaceStateFile is the rebuildable rotation projection at each family's
	// tree root. Both families reuse the same file name under their own tree.
	surfaceStateFile = "surface_state.json"
	// surfaceStateSchema versions the rebuildable projection independently of the
	// entry schema; it is disposable, so a bump only changes how a stale file is
	// read, never entry history.
	surfaceStateSchema = 1
)

// surfaceState is the verb-owned rotation projection shared by the focus and
// reframe families: the per-id last-surfaced logical-date and the current day's
// pick. It is rebuildable and disposable — the entry JSONL is the source of
// truth, and deleting it only resets rotation memory. It records only which item
// was shown when, never anything that grades the user (focus.md §0 / reframes.md
// §0). Both families use byte-identical JSON, so one type serves both.
type surfaceState struct {
	Schema       int               `json:"schema"`
	LastSurfaced map[string]string `json:"last_surfaced"`
	Today        *todayPick        `json:"today"`
}

// todayPick is the current logical day's recorded pick — the day key and the
// chosen entry id — that makes `surface` idempotent within a day.
type todayPick struct {
	Day string `json:"day"`
	ID  string `json:"id"`
}

// surfaceOne selects exactly one entry for the logical day named by dayKey from
// pool — already folded (and, for focus, filtered to active) by the caller — and
// records the pick in the rotation projection at statePath (created under dir,
// labeled by label for its write error). It is idempotent within a day: a pick
// already recorded for dayKey is returned unchanged and the rotation does not
// advance; if that recorded pick has left the pool since (retired/superseded), it
// re-picks from the current pool so the day still surfaces a live entry. On the
// first call of a new day it selects the least-recently-surfaced entry
// (unsurfaced first, ties broken by id ascending) and advances. An empty pool is
// a clean (zero, false, nil) — "nothing to surface," never an error. id extracts
// an entry's stable id.
func surfaceOne[T any](
	a *Adapter, dir, label, statePath, dayKey string, pool []T, id func(T) string,
) (T, bool, error) {
	var zero T
	if len(pool) == 0 {
		return zero, false, nil
	}
	st, err := a.readSurfaceState(statePath)
	if err != nil {
		return zero, false, err
	}

	// Within-day idempotence: return the recorded pick unchanged, no advance.
	if st.Today != nil && st.Today.Day == dayKey {
		if e, ok := findEntryByID(pool, st.Today.ID, id); ok {
			return e, true, nil
		}
		// The recorded pick left the pool since it was chosen: fall through and
		// re-pick from the current pool so the day still surfaces a live entry.
	}

	pick := leastRecentlySurfaced(pool, st.LastSurfaced, id)
	if st.LastSurfaced == nil {
		st.LastSurfaced = map[string]string{}
	}
	st.Schema = surfaceStateSchema
	st.LastSurfaced[id(pick)] = dayKey
	st.Today = &todayPick{Day: dayKey, ID: id(pick)}
	if werr := a.writeSurfaceState(dir, label, statePath, st); werr != nil {
		return zero, false, werr
	}
	return pick, true, nil
}

// leastRecentlySurfaced returns the entry surfaced least recently. pool is
// already sorted by id; a stable sort keyed by the last-surfaced date leaves
// equal-date entries (including the cold-start case where every date is the empty
// string, which sorts before any real YYYY-MM-DD) in id-ascending order, so the
// pick is fully deterministic (focus.md §4 / reframes.md §4).
func leastRecentlySurfaced[T any](pool []T, last map[string]string, id func(T) string) T {
	ordered := slices.Clone(pool)
	slices.SortStableFunc(ordered, func(x, y T) int {
		return cmp.Compare(last[id(x)], last[id(y)])
	})
	return ordered[0]
}

// findEntryByID returns the pool entry with the given id.
func findEntryByID[T any](pool []T, wantID string, id func(T) string) (T, bool) {
	for _, e := range pool {
		if id(e) == wantID {
			return e, true
		}
	}
	var zero T
	return zero, false
}

// readSurfaceState reads a rotation projection, treating both a missing and a
// corrupt file as a fresh (cold-start) state rather than an error — it is
// rebuildable and disposable, so a torn write only resets rotation memory and the
// next surface re-cold-starts.
func (a *Adapter) readSurfaceState(path string) (surfaceState, error) {
	return readJSONResilient[surfaceState](path, "surface state")
}

// writeSurfaceState persists a rotation projection, creating its tree if needed.
// It is the only writer of a family's surface_state.json (architecture P3). label
// names the family for the create/write errors ("focus" / "reframe"). The write
// is a plain os.WriteFile, not atomic: the projection is disposable, so a torn
// write is a clean cold-start on the next read, never a lost record.
func (a *Adapter) writeSurfaceState(dir, label, path string, st surfaceState) error {
	if err := ensureDir(dir, label); err != nil {
		return err
	}
	b, err := marshalJSON(st)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, filePerm); err != nil {
		return fmt.Errorf("storage: write %s surface state: %w", label, err)
	}
	return nil
}
