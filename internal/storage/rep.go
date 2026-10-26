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

	"github.com/mrz1836/lucid/internal/rep"
)

// Rep-tree names under ~/.lucid/ (reps.md §2, §4). Only this adapter touches
// them; the practice-rep record family is agent-free and reads/writes exclusively
// through these ops (architecture P3). The surface-state projection lives at the
// tree root (reusing the shared surfaceStateFile name), separate from the
// append-only entry stream, so entry history stays pure while `surface` tracks
// what it has shown.
const (
	repDirName    = "reps"
	repFilePrefix = "rep_"
	repFileExt    = ".jsonl"
)

// The rep rotation reuses the shared surface-state projection and rotation
// (surface.go) exactly as focus does: its type, read/write, and one-per-day
// selection are family-agnostic. rep.go keeps only the active-only filter (a
// retired rep is never surfaced).

// repDir returns ~/.lucid/reps/.
func (a *Adapter) repDir() string { return filepath.Join(a.home, repDirName) }

// repSurfaceStatePath returns ~/.lucid/reps/surface_state.json.
func (a *Adapter) repSurfaceStatePath() string {
	return filepath.Join(a.repDir(), surfaceStateFile)
}

// repDayPath returns reps/YYYY/MM/rep_YYYY_MM_DD.jsonl for a logical date
// (YYYY-MM-DD). A malformed date is rejected by [safeDayShard] so it can never
// build a path outside the rep tree (defense-in-depth: the CLI derives every
// logical_date from the civil-day rule).
func (a *Adapter) repDayPath(date string) (string, error) {
	year, month, err := safeDayShard(date)
	if err != nil {
		return "", err
	}
	name := repFilePrefix + strings.ReplaceAll(date, "-", "_") + repFileExt
	return filepath.Join(a.repDir(), year, month, name), nil
}

// ScaffoldReps creates the reps/ tree. It is idempotent — an existing tree (and
// everything under it, including the surface-state projection) is left untouched
// (mirroring [Adapter.ScaffoldFocus]). The append and surface paths also
// self-create their parents, so scaffolding is a convenience the router runs at
// boot, not a precondition for a write.
func (a *Adapter) ScaffoldReps() error {
	if err := os.MkdirAll(a.repDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create reps dir: %w", err)
	}
	return nil
}

// AppendRep assigns the next sequence id under the single-writer discipline and
// appends the entry as one fsync'd JSONL line to its logical-day file (reps.md
// §2). The entry must already carry its logical_date (the CLI derives it,
// backdated by --day); the id is assigned here. An empty source defaults to the
// documented `rep` provenance, and an empty state defaults to active. It returns
// the entry with its id (and any defaults) filled.
func (a *Adapter) AppendRep(r rep.Rep) (rep.Rep, error) {
	if r.LogicalDate == "" {
		return rep.Rep{}, fmt.Errorf("storage: rep is missing logical_date")
	}
	r.Schema = rep.Schema
	if r.Source == "" {
		r.Source = rep.SourceRep
	}
	if r.State == "" {
		r.State = rep.StateActive
	}
	path, err := a.repDayPath(r.LogicalDate)
	if err != nil {
		return rep.Rep{}, err
	}

	seq, err := a.nextRepSeq(path)
	if err != nil {
		return rep.Rep{}, err
	}
	r.ID = rep.ID(r.LogicalDate, seq)

	if err = r.Validate(); err != nil {
		return rep.Rep{}, err
	}
	line, err := r.MarshalLine()
	if err != nil {
		return rep.Rep{}, err
	}
	if err := appendLineFsync(path, line); err != nil {
		return rep.Rep{}, err
	}
	return r, nil
}

// RetireRep retires an active rep by appending a retirement event that
// references it by id (reps.md §5) — the rep is never deleted or rewritten, so
// history is preserved and `list --all` still shows it. The marker lands on the
// logical day named by dayKey with recordedAt as its write time (the router
// resolves both from now). It errors if the id names no rep or one that is
// already retired. It returns the appended retirement event.
func (a *Adapter) RetireRep(id, dayKey, recordedAt string) (rep.Rep, error) {
	pool, err := a.ReadReps()
	if err != nil {
		return rep.Rep{}, err
	}
	var target *rep.Rep
	for i := range pool {
		if pool[i].ID == id {
			t := pool[i]
			target = &t
			break
		}
	}
	if target == nil {
		return rep.Rep{}, fmt.Errorf("storage: no rep %q to retire", id)
	}
	if target.State == rep.StateRetired {
		return rep.Rep{}, fmt.Errorf("storage: rep %q is already retired", id)
	}

	marker := rep.NewRetirement(id, dayKey, recordedAt)
	path, err := a.repDayPath(marker.LogicalDate)
	if err != nil {
		return rep.Rep{}, err
	}
	seq, err := a.nextRepSeq(path)
	if err != nil {
		return rep.Rep{}, err
	}
	marker.ID = rep.ID(marker.LogicalDate, seq)
	if err = marker.Validate(); err != nil {
		return rep.Rep{}, err
	}
	line, err := marker.MarshalLine()
	if err != nil {
		return rep.Rep{}, err
	}
	if err := appendLineFsync(path, line); err != nil {
		return rep.Rep{}, err
	}
	return marker, nil
}

// nextRepSeq returns max-seq+1 over the well-formed lines in the day file
// (reps.md §2: never line count, single-writer). A missing file starts at seq 1;
// malformed lines are ignored, so a truncated line never perturbs id assignment.
// Both reps and retirement markers occupy the day's sequence, so the seq
// derivation counts either.
func (a *Adapter) nextRepSeq(path string) (int, error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return 0, err
	}
	maxSeq := 0
	for _, ln := range lines {
		r, perr := rep.UnmarshalRepLine(ln)
		if perr != nil {
			continue // malformed line: does not contribute a seq
		}
		if s, ok := rep.ParseSeq(r.ID); ok && s > maxSeq {
			maxSeq = s
		}
	}
	return maxSeq + 1, nil
}

// ReadRepDay reads the raw entries for one logical day, skipping malformed lines
// and reporting how many were skipped (error-states "JSONL corruption"). It does
// not fold state — that is [Adapter.ReadReps]'s job, because a retirement event
// for a backdated rep lives in a different day file.
func (a *Adapter) ReadRepDay(date string) (entries []rep.Rep, skipped int, err error) {
	path, err := a.repDayPath(date)
	if err != nil {
		return nil, 0, err
	}
	return a.readRepFile(path)
}

// ReadReps reads every stored rep entry across the tree, sorted by id, with
// state folded and retirement markers removed (reps.md §2, §5) — the rep view
// `list` prints (active by default, retired under `--all`) and `surface` selects
// from (active only). A retired rep is returned with State=retired, not dropped.
func (a *Adapter) ReadReps() ([]rep.Rep, error) {
	all, err := a.readAllReps()
	if err != nil {
		return nil, err
	}
	return rep.FoldState(all), nil
}

// ReadRepByID reads a single stored entry by its id, deriving the logical day
// the id encodes ([rep.Date]) and scanning that day's file. A missing entry — an
// unparseable id, an absent day file, or a day that holds no such id — is
// (zero, false, nil), never an error, so a caller can treat absence as a clean
// "not found". It returns the raw stored entry (unfolded).
func (a *Adapter) ReadRepByID(id string) (rep.Rep, bool, error) {
	date, ok := rep.Date(id)
	if !ok {
		return rep.Rep{}, false, nil
	}
	entries, _, err := a.ReadRepDay(date)
	if err != nil {
		return rep.Rep{}, false, err
	}
	for _, r := range entries {
		if r.ID == id {
			return r, true, nil
		}
	}
	return rep.Rep{}, false, nil
}

// readAllReps walks the rep tree and returns every well-formed entry, sorted by
// id (ids encode the logical date and a monotonic seq, so id order is time
// order). The surface-state projection at the tree root is skipped by the
// prefix/suffix filter — it is not a rep_*.jsonl day file.
func (a *Adapter) readAllReps() ([]rep.Rep, error) {
	var out []rep.Rep
	err := filepath.WalkDir(a.repDir(), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), repFilePrefix) || !strings.HasSuffix(d.Name(), repFileExt) {
			return nil
		}
		entries, _, rerr := a.readRepFile(path)
		if rerr != nil {
			return rerr
		}
		out = append(out, entries...)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	slices.SortFunc(out, func(a, b rep.Rep) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// readRepFile reads and parses one rep file, returning the well-formed entries
// and the count of skipped malformed lines.
func (a *Adapter) readRepFile(path string) (entries []rep.Rep, skipped int, err error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return nil, 0, err
	}
	for _, ln := range lines {
		r, perr := rep.UnmarshalRepLine(ln)
		if perr != nil {
			skipped++
			continue
		}
		entries = append(entries, r)
	}
	return entries, skipped, nil
}

// SurfaceRep returns exactly one active rep for the logical day named by dayKey
// and records that it was shown (reps.md §4). The caller resolves dayKey from now
// ([rep.LogicalDay]); passing it in keeps the rotation deterministic and
// injectable in tests. It is idempotent within a day — a pick already recorded
// for dayKey is returned unchanged and the rotation does not advance. On the
// first call of a new day it selects the least-recently-surfaced active rep
// (unsurfaced reps first, ties broken by rep id ascending), records it as today's
// pick, and advances. Retired reps are filtered out before selection; the
// rotation never reads level. The returned entry carries its solo variant. An
// empty active pool is a clean (zero, false, nil) — "nothing to surface," never
// an error. Only surface_state.json is written; no entry file is touched.
func (a *Adapter) SurfaceRep(dayKey string) (rep.Rep, bool, error) {
	all, err := a.ReadReps()
	if err != nil {
		return rep.Rep{}, false, err
	}
	// The active-only filter is the one axis reps add over the shared rotation,
	// as for focus: a retired rep is kept in history (the audit view) but never
	// surfaced.
	pool := activeReps(all)
	return surfaceOne(a, a.repDir(), "rep", a.repSurfaceStatePath(), dayKey, pool,
		func(r rep.Rep) string { return r.ID })
}

// activeReps returns only the active reps from a folded pool, preserving order
// (state has already been resolved by [rep.FoldState]).
func activeReps(pool []rep.Rep) []rep.Rep {
	out := make([]rep.Rep, 0, len(pool))
	for _, r := range pool {
		if r.State == rep.StateActive {
			out = append(out, r)
		}
	}
	return out
}
