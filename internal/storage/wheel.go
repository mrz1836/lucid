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
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// wheel.go is the storage adapter for the Wheel of Life record family (wheel.md
// §4): the only code that touches registries/wheel/ (architecture P3). A wheel
// entry is one JSON file per calendar month, keyed by the deterministic month
// key wheel_YYYY-MM (not the salted phrase key the other registries use — a
// month is not sensitive, and it must stay enumerable for the trend), carrying
// an append-only snapshot history. Every write is a whole-file
// read-modify-write under the single-writer discipline — read the month, mint
// the next receipt id from its history, append one snapshot, write it back
// atomically — so the month's current wheel stays a pure latest-wins fold over
// snapshots that are never rewritten.

const wheelDirName = "wheel"

// wheelKeyPrefix is the filename prefix every wheel entry carries: the stable
// entry id is wheel_YYYY-MM, and the file is <key>.json.
const wheelKeyPrefix = "wheel_"

// wheelDir returns ~/.lucid/registries/wheel/.
func (a *Adapter) wheelDir() string {
	return filepath.Join(a.registriesDir(), wheelDirName)
}

// wheelPath returns registries/wheel/wheel_YYYY-MM.json. The month is checked
// against the strict YYYY-MM grammar first and the key is routed through
// [safeRecordPath], so no caller-supplied month can name a path outside the
// wheel tree.
func (a *Adapter) wheelPath(month string) (string, error) {
	if !observations.ValidWheelMonth(month) {
		return "", fmt.Errorf("storage: invalid wheel month %q (want YYYY-MM)", month)
	}
	return safeRecordPath(a.wheelDir(), observations.WheelEntryKey(month), ".json", "wheel month")
}

// ScaffoldWheel ensures the wheel subtree exists. It is idempotent and the
// append path runs it itself, so scaffolding is never a precondition the caller
// must remember.
func (a *Adapter) ScaffoldWheel() error {
	if err := os.MkdirAll(a.wheelDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create wheel dir: %w", err)
	}
	return nil
}

// ReadWheel reads one month's wheel entry, returning (entry, found, error). A
// month with no wheel is not an error.
func (a *Adapter) ReadWheel(month string) (observations.WheelEntry, bool, error) {
	path, err := a.wheelPath(month)
	if err != nil {
		return observations.WheelEntry{}, false, err
	}
	return readJSONOptional[observations.WheelEntry](path, fmt.Sprintf("wheel %q", month))
}

// ReadWheelAll reads every recorded month's wheel entry, sorted oldest month
// first — the enumeration the trend, the delta, and `wheel list` read. Only
// files named like a wheel entry (wheel_YYYY-MM.json) are read; anything else in
// the directory is ignored. A missing tree is (nil, nil).
func (a *Adapter) ReadWheelAll() ([]observations.WheelEntry, error) {
	dirEntries, err := os.ReadDir(a.wheelDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read wheel dir: %w", err)
	}
	var out []observations.WheelEntry
	for _, de := range dirEntries {
		month, ok := wheelMonthOfFile(de)
		if !ok {
			continue
		}
		rec, found, rerr := a.ReadWheel(month)
		if rerr != nil {
			return nil, rerr
		}
		if found {
			out = append(out, rec)
		}
	}
	slices.SortFunc(out, func(x, y observations.WheelEntry) int { return cmp.Compare(x.Month, y.Month) })
	return out, nil
}

// wheelMonthOfFile returns the month a directory entry records, or ok=false for
// anything that is not a wheel entry file (a subdirectory, a staged temp file, a
// stray file).
func wheelMonthOfFile(de fs.DirEntry) (string, bool) {
	if de.IsDir() {
		return "", false
	}
	key, ok := strings.CutSuffix(de.Name(), ".json")
	if !ok {
		return "", false
	}
	month, ok := strings.CutPrefix(key, wheelKeyPrefix)
	if !ok || !observations.ValidWheelMonth(month) {
		return "", false
	}
	return month, true
}

// AppendWheelSnapshot appends snap to month's wheel — creating the month's entry
// when none exists — mints snap's receipt id under the single-writer discipline
// (seq = max over the month's history + 1, never a count), stamps snap.At with
// the real write time, and writes the file back atomically. It returns the
// stored entry and the appended snapshot with its receipt id filled.
//
// It is append-only (wheel.md §4): a second call for the same month does not
// refuse and mutates nothing — it appends another snapshot to the same file,
// which then wins the fold, and every earlier snapshot keeps its receipt. The
// snapshot is validated before anything is written (all eight pillars rated
// 1–10, any suggestion 1–10), so an invalid wheel never reaches disk.
func (a *Adapter) AppendWheelSnapshot(
	month string, snap observations.WheelSnapshot, now time.Time,
) (observations.WheelEntry, observations.WheelSnapshot, error) {
	if !observations.ValidWheelMonth(month) {
		return observations.WheelEntry{}, observations.WheelSnapshot{}, fmt.Errorf(
			"storage: invalid wheel month %q (want YYYY-MM)", month,
		)
	}
	if err := snap.Validate(); err != nil {
		return observations.WheelEntry{}, observations.WheelSnapshot{}, err
	}
	if err := a.ScaffoldWheel(); err != nil {
		return observations.WheelEntry{}, observations.WheelSnapshot{}, err
	}
	nowStr := now.Format(time.RFC3339)

	entry, found, err := a.ReadWheel(month)
	if err != nil {
		return observations.WheelEntry{}, observations.WheelSnapshot{}, err
	}
	if !found {
		entry = observations.NewWheelEntry(month, nowStr)
	}

	snap.ID = observations.WheelReceiptID(month, observations.NextWheelSeq(entry.History))
	snap.At = nowStr
	snap.Type = observations.WheelEventSnapshot
	entry.History = append(slices.Clone(entry.History), snap)
	entry.UpdatedAt = nowStr

	if err = entry.Validate(); err != nil {
		return observations.WheelEntry{}, observations.WheelSnapshot{}, err
	}
	if err = a.writeWheel(entry); err != nil {
		return observations.WheelEntry{}, observations.WheelSnapshot{}, err
	}
	return entry, snap, nil
}

// writeWheel persists one month's wheel entry as indented JSON, creating the
// subtree if needed. It is the only writer of a wheel file (architecture P3).
func (a *Adapter) writeWheel(entry observations.WheelEntry) error {
	path, err := a.wheelPath(entry.Month)
	if err != nil {
		return err
	}
	if err = ensureDir(a.wheelDir(), "wheel"); err != nil {
		return err
	}
	content, err := marshalJSON(entry.Normalized())
	if err != nil {
		return err
	}
	// Atomic: a wheel file holds a month's whole append-only history, so a torn
	// write must leave the previous file intact rather than lose the month.
	return writeFileAtomic(path, content, fmt.Sprintf("wheel %q", entry.Month))
}
