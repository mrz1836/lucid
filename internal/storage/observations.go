package storage

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/data"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// Observation-tree names under ~/.lucid/ (observations-module.md §"Storage
// additions"). Only this adapter touches them; the observation module is
// agent-free and reads/writes exclusively through these ops.
const (
	observationsDirName = "observations"
	registriesDirName   = "registries"
	projectionsDirName  = "projections"
	obsConfigFile       = "config.json"
	obsFilePrefix       = "obs_"
	obsFileExt          = ".jsonl"
	rangeIndexFile      = "obs_range_index.jsonl"
	keySaltBytes        = 16
)

// registryDirNames returns the four registry subtrees created at scaffold
// (observations.md §8).
func registryDirNames() []string { return []string{"injuries", "threads", "places", "eras"} }

// DayView is the `/day` join across trees (observations.md §7): the folded
// engine day record, the observation events for the logical day (plus range
// events spanning it), the raw entry ids recorded that day, and the media
// attachments attributed to that logical day. A user-invoked projection may
// join across trees — that is the user reading their own ledger, not an agent
// reading it (the sanctuary boundary constrains agent slices, not `/day`).
type DayView struct {
	Date        string
	EngineDay   *engine.DayRecord
	Obs         observations.DayView
	RawEntryIDs []string
	Media       []MediaRecord
}

// rangeIndexEntry is one line of the rebuildable range index under
// projections/ (observations.md §7: range events "found via a rebuildable
// range index"). It lets `/day` surface a range event that began on an
// earlier day without scanning the whole tree.
type rangeIndexEntry struct {
	ID    string `json:"id"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// observationsDir returns ~/.lucid/observations/.
func (a *Adapter) observationsDir() string { return filepath.Join(a.home, observationsDirName) }

// registriesDir returns ~/.lucid/registries/.
func (a *Adapter) registriesDir() string { return filepath.Join(a.home, registriesDirName) }

// projectionsDir returns ~/.lucid/projections/.
func (a *Adapter) projectionsDir() string { return filepath.Join(a.home, projectionsDirName) }

// obsConfigPath returns ~/.lucid/observations/config.json.
func (a *Adapter) obsConfigPath() string { return filepath.Join(a.observationsDir(), obsConfigFile) }

// ScaffoldObservations creates the observations/, registries/, and
// projections/ trees and writes observations/config.json with a freshly
// generated per-instance key_salt if missing (observations-module.md
// §"Storage additions"). It is idempotent: an existing config is never
// overwritten, so a hand-edited kinds_enabled or key_salt survives a
// re-scaffold.
func (a *Adapter) ScaffoldObservations() error {
	if err := os.MkdirAll(a.observationsDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create observations dir: %w", err)
	}
	for _, d := range registryDirNames() {
		if err := os.MkdirAll(filepath.Join(a.registriesDir(), d), dirPerm); err != nil {
			return fmt.Errorf("storage: create registry dir %q: %w", d, err)
		}
	}
	if err := os.MkdirAll(a.projectionsDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create projections dir: %w", err)
	}
	return a.ensureObservationsConfig()
}

// ensureObservationsConfig writes observations/config.json from the documented
// default plus a generated key_salt, only when the file does not exist.
func (a *Adapter) ensureObservationsConfig() error {
	if _, err := os.Stat(a.obsConfigPath()); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: stat observations config: %w", err)
	}
	salt, err := generateKeySalt()
	if err != nil {
		return err
	}
	cfg := observations.DefaultConfig()
	cfg.KeySalt = salt
	b, err := cfg.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.obsConfigPath(), b, filePerm); err != nil {
		return fmt.Errorf("storage: write observations config: %w", err)
	}
	return nil
}

// ReadObservationsConfig reads observations/config.json.
func (a *Adapter) ReadObservationsConfig() (observations.Config, error) {
	b, err := os.ReadFile(a.obsConfigPath())
	if err != nil {
		return observations.Config{}, fmt.Errorf("storage: read observations config: %w", err)
	}
	return observations.UnmarshalConfig(b)
}

// SaveObservationsConfig writes observations/config.json, replacing any
// existing file. It is the sanctioned path for changing enabled kinds or
// clinical-context lines (the config is hand-editable; this keeps the exact
// on-disk shape).
func (a *Adapter) SaveObservationsConfig(cfg observations.Config) error {
	b, err := cfg.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.obsConfigPath(), b, filePerm); err != nil {
		return fmt.Errorf("storage: write observations config: %w", err)
	}
	return nil
}

// AppendObservation assigns the next sequence id under the single-writer
// discipline, appends the event as one fsync'd JSONL line to its logical-day
// file, and — for a multi-day range event — records it in the rebuildable
// range index (observations-module.md §"Storage additions"; observations.md
// §2 ids). The event must already carry its logical_date (the router derives
// it); the id is assigned here. It returns the event with its id filled.
func (a *Adapter) AppendObservation(ev observations.Event) (observations.Event, error) {
	if ev.LogicalDate == "" {
		return observations.Event{}, fmt.Errorf("storage: observation is missing logical_date")
	}
	ev.Schema = observations.Schema
	path := a.obsDayPath(ev.LogicalDate)

	seq, err := a.nextObsSeq(path)
	if err != nil {
		return observations.Event{}, err
	}
	ev.ID = observations.EventID(ev.LogicalDate, seq)

	if err = ev.Validate(); err != nil {
		return observations.Event{}, err
	}
	line, err := ev.MarshalLine()
	if err != nil {
		return observations.Event{}, err
	}
	if err := appendLineFsync(path, line); err != nil {
		return observations.Event{}, err
	}
	if err := a.indexRangeEvent(ev); err != nil {
		return observations.Event{}, err
	}
	return ev, nil
}

// nextObsSeq returns max-seq+1 over the well-formed lines in the day file
// (observations.md §2: never line count, single-writer). A missing file
// starts at seq 1; malformed lines are ignored, so a truncated line never
// perturbs id assignment.
func (a *Adapter) nextObsSeq(path string) (int, error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return 0, err
	}
	maxSeq := 0
	for _, ln := range lines {
		ev, perr := observations.UnmarshalEventLine(ln)
		if perr != nil {
			continue // malformed line: does not contribute a seq
		}
		if s, ok := observations.ParseSeq(ev.ID); ok && s > maxSeq {
			maxSeq = s
		}
	}
	return maxSeq + 1, nil
}

// ReadObservationsDay reads the events for one logical day, skipping malformed
// lines and reporting how many were skipped (observations-module.md §Error
// states: "Reader skips bad lines, reports count").
func (a *Adapter) ReadObservationsDay(date string) (events []observations.Event, skipped int, err error) {
	return a.readObsFile(a.obsDayPath(date))
}

// ReadObservationsRange reads the events whose logical_date falls in
// [start, end] (inclusive), across the day files in range, sorted by id.
func (a *Adapter) ReadObservationsRange(start, end string) ([]observations.Event, error) {
	loc := time.UTC
	s, err := observations.ParseDate(start, loc)
	if err != nil {
		return nil, fmt.Errorf("storage: bad range start %q: %w", start, err)
	}
	e, err := observations.ParseDate(end, loc)
	if err != nil {
		return nil, fmt.Errorf("storage: bad range end %q: %w", end, err)
	}
	var out []observations.Event
	for d := s; !d.After(e); d = d.AddDate(0, 0, 1) {
		evs, _, rerr := a.ReadObservationsDay(observations.DateString(d))
		if rerr != nil {
			return nil, rerr
		}
		out = append(out, evs...)
	}
	return observations.SortEventsByID(out), nil
}

// RecentObservations reads the observation events whose logical day falls in
// the bounded, contract-named recent window [now-windowDays, now] (inclusive),
// sorted by id — the slice the daily companion enriches its message from. The
// window is anchored on now's civil date in now's own location, so "recent"
// tracks the operator's day boundary; windowDays is the look-back the caller
// names (the companion passes a fixed constant, the same order as
// config.recent_window). A non-positive window reads only today, never an
// inverted range. It wraps [Adapter.ReadObservationsRange], so it inherits the
// day-file iteration and the malformed-line skip discipline and reads nothing
// outside the named span — it does not filter by kind, leaving the
// render-relevant selection to the composer.
func (a *Adapter) RecentObservations(now time.Time, windowDays int) ([]observations.Event, error) {
	if windowDays < 0 {
		windowDays = 0
	}
	end := observations.DateOf(now)
	start := end.AddDate(0, 0, -windowDays)
	return a.ReadObservationsRange(observations.DateString(start), observations.DateString(end))
}

// ReadObservationsKind reads every event of one kind across the whole tree,
// sorted by id — the series read exports build on (Phase 12). It walks the
// observation files and filters, skipping malformed lines.
func (a *Adapter) ReadObservationsKind(kind observations.Kind) ([]observations.Event, error) {
	var out []observations.Event
	err := filepath.WalkDir(a.observationsDir(), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), obsFilePrefix) || !strings.HasSuffix(d.Name(), obsFileExt) {
			return nil
		}
		evs, _, rerr := a.readObsFile(path)
		if rerr != nil {
			return rerr
		}
		for _, ev := range evs {
			if ev.Kind == kind {
				out = append(out, ev)
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return observations.SortEventsByID(out), nil
}

// ResolveRegistryKey returns the salted, collision-suffixed key for a referent
// name under the instance's key_salt (observations-module.md §"Registry
// keys"). The owner check reads the registry dir so a second name that hashes
// to a taken key gets a -2 suffix.
func (a *Adapter) ResolveRegistryKey(kind, name string) (string, error) {
	cfg, err := a.ReadObservationsConfig()
	if err != nil {
		return "", err
	}
	owner := func(candidate string) (string, bool) {
		rec, found, rerr := a.ReadRegistry(kind, candidate)
		if rerr != nil || !found {
			return "", false
		}
		return observations.NormalizedName(rec.DisplayName), true
	}
	return observations.ResolveRegistryKey(kind, name, cfg.KeySalt, data.Wordlist(), owner)
}

// ReadRegistry reads one registry record, returning (record, found, error). A
// missing record is not an error.
func (a *Adapter) ReadRegistry(kind, key string) (observations.Registry, bool, error) {
	path, err := a.registryPath(kind, key)
	if err != nil {
		return observations.Registry{}, false, err
	}
	return readJSONOptional[observations.Registry](path, fmt.Sprintf("registry %q", key))
}

// ReadRegistryKind reads every record of one registry kind, sorted by key.
func (a *Adapter) ReadRegistryKind(kind string) ([]observations.Registry, error) {
	dir, ok := observations.RegistryDir(kind)
	if !ok {
		return nil, fmt.Errorf("storage: unknown registry kind %q", kind)
	}
	entries, err := os.ReadDir(filepath.Join(a.registriesDir(), dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read registry dir %q: %w", dir, err)
	}
	var out []observations.Registry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".json")
		rec, found, rerr := a.ReadRegistry(kind, key)
		if rerr != nil {
			return nil, rerr
		}
		if found {
			out = append(out, rec)
		}
	}
	slices.SortFunc(out, func(a, b observations.Registry) int { return cmp.Compare(a.Key, b.Key) })
	return out, nil
}

// UpdateRegistry creates or merges a registry record (observations.md §1
// merge/history): a fresh referent is created active with one history entry;
// an existing one has the patch applied, appending a status_history entry. It
// returns the resulting record.
func (a *Adapter) UpdateRegistry(kind, key string, patch observations.RegistryPatch) (observations.Registry, error) {
	path, err := a.registryPath(kind, key)
	if err != nil {
		return observations.Registry{}, err
	}
	existing, found, err := a.ReadRegistry(kind, key)
	if err != nil {
		return observations.Registry{}, err
	}
	var rec observations.Registry
	if found {
		rec = existing.Apply(patch)
	} else {
		rec = observations.NewRegistry(kind, key, patch.DisplayName, patch.At)
		if patch.Status != "" {
			rec.Status = patch.Status
			rec.StatusHistory[0].Status = patch.Status
		}
		for k, v := range patch.Fields {
			if v != nil {
				rec.Fields[k] = v
			}
		}
	}
	if err = ensureDir(filepath.Dir(path), "registry"); err != nil {
		return observations.Registry{}, err
	}
	content, err := marshalJSON(rec.Normalized())
	if err != nil {
		return observations.Registry{}, err
	}
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return observations.Registry{}, fmt.Errorf("storage: write registry %q: %w", key, err)
	}
	return rec, nil
}

// ReadDayView assembles the `/day` join for a logical date (observations.md
// §7): the folded engine day record if present, the day's observation events
// plus any range event spanning the day (from the range index), the raw entry
// ids recorded that day, and the media attachments attributed to the day
// (data-model.md §"Media attachments"). loc interprets the civil dates. It is
// a pure read — nothing is written.
func (a *Adapter) ReadDayView(date string, loc *time.Location) (DayView, error) {
	if loc == nil {
		loc = time.UTC
	}
	view := DayView{Date: date}

	rec, found, err := a.ReadEngineDayFolded(engine.DayID(mustDate(date, loc)))
	if err == nil && found {
		view.EngineDay = &rec
	}

	dayEvents, _, err := a.ReadObservationsDay(date)
	if err != nil {
		return DayView{}, err
	}
	rangeCandidates, err := a.rangeCandidatesFor(date)
	if err != nil {
		return DayView{}, err
	}
	view.Obs = observations.AssembleDayView(date, dayEvents, rangeCandidates, loc)

	view.RawEntryIDs, err = a.rawIDsForDate(date)
	if err != nil {
		return DayView{}, err
	}
	if rec.RawEntryID != "" && !slices.Contains(view.RawEntryIDs, rec.RawEntryID) {
		view.RawEntryIDs = append(view.RawEntryIDs, rec.RawEntryID)
		slices.Sort(view.RawEntryIDs)
	}

	view.Media, err = a.ReadMediaForDay(date)
	if err != nil {
		return DayView{}, err
	}
	return view, nil
}

// indexRangeEvent appends a range-index line when an event spans more than one
// logical day, so `/day` can surface it on the days after its start.
func (a *Adapter) indexRangeEvent(ev observations.Event) error {
	if ev.OccurredAtPrecision != observations.PrecisionRange || ev.OccurredAtEnd == nil {
		return nil
	}
	end, err := time.Parse(time.RFC3339, *ev.OccurredAtEnd)
	if err != nil {
		return nil //nolint:nilerr // an unparseable end date is skipped from the index, not a write failure
	}
	endDate := observations.DateString(observations.DateOf(end))
	if endDate == ev.LogicalDate {
		return nil // single-day range: already lives in its own day file
	}
	entry, err := json.Marshal(rangeIndexEntry{ID: ev.ID, Start: ev.LogicalDate, End: endDate})
	if err != nil {
		return fmt.Errorf("storage: marshal range index entry: %w", err)
	}
	return appendLineFsync(filepath.Join(a.projectionsDir(), rangeIndexFile), entry)
}

// rangeCandidatesFor loads the range events whose span covers date but which
// started earlier, from the range index.
func (a *Adapter) rangeCandidatesFor(date string) ([]observations.Event, error) {
	lines, err := readJSONLLines(filepath.Join(a.projectionsDir(), rangeIndexFile))
	if err != nil {
		return nil, err
	}
	var out []observations.Event
	seen := map[string]bool{}
	for _, ln := range lines {
		var idx rangeIndexEntry
		if json.Unmarshal(ln, &idx) != nil {
			continue
		}
		// Spanning means started strictly before the day and ends on/after it.
		if idx.Start >= date || date > idx.End || seen[idx.ID] {
			continue
		}
		seen[idx.ID] = true
		ev, ok, lerr := a.readObsEventByID(idx.Start, idx.ID)
		if lerr != nil {
			return nil, lerr
		}
		if ok {
			out = append(out, ev)
		}
	}
	return out, nil
}

// readObsEventByID loads a single event from its start-day file.
func (a *Adapter) readObsEventByID(startDate, id string) (observations.Event, bool, error) {
	evs, _, err := a.ReadObservationsDay(startDate)
	if err != nil {
		return observations.Event{}, false, err
	}
	for _, ev := range evs {
		if ev.ID == id {
			return ev, true, nil
		}
	}
	return observations.Event{}, false, nil
}

// readObsFile reads and parses one observation file, returning the well-formed
// events and the count of skipped malformed lines.
func (a *Adapter) readObsFile(path string) (events []observations.Event, skipped int, err error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return nil, 0, err
	}
	for _, ln := range lines {
		ev, perr := observations.UnmarshalEventLine(ln)
		if perr != nil {
			skipped++
			continue
		}
		events = append(events, ev)
	}
	return events, skipped, nil
}

// rawIDsForDate returns the ids of raw entries recorded on a civil date,
// sorted — the "entry list" half of the day view (data-model.md raw ids
// encode the recorded date; raw_YYYY_MM_DD_*).
func (a *Adapter) rawIDsForDate(date string) ([]string, error) {
	d, err := observations.ParseDate(date, time.UTC)
	if err != nil {
		return nil, fmt.Errorf("storage: bad day date %q: %w", date, err)
	}
	shard := filepath.Join(a.home, rawDirName, fmt.Sprintf("%04d", d.Year()), fmt.Sprintf("%02d", int(d.Month())))
	entries, rerr := os.ReadDir(shard)
	if errors.Is(rerr, fs.ErrNotExist) {
		return nil, nil
	}
	if rerr != nil {
		return nil, fmt.Errorf("storage: read raw shard %q: %w", shard, rerr)
	}
	prefix := fmt.Sprintf("raw_%04d_%02d_%02d_", d.Year(), int(d.Month()), d.Day())
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), rawExt) {
			continue
		}
		id := strings.TrimSuffix(e.Name(), rawExt)
		if strings.HasPrefix(id, prefix) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out, nil
}

// obsDayPath returns observations/YYYY/MM/obs_YYYY_MM_DD.jsonl for a logical
// date (YYYY-MM-DD).
func (a *Adapter) obsDayPath(date string) string {
	parts := strings.Split(date, "-")
	year, month := "0000", "00"
	if len(parts) == 3 {
		year, month = parts[0], parts[1]
	}
	name := obsFilePrefix + strings.ReplaceAll(date, "-", "_") + obsFileExt
	return filepath.Join(a.observationsDir(), year, month, name)
}

// registryPath returns registries/<dir>/<key>.json for a registry kind+key.
func (a *Adapter) registryPath(kind, key string) (string, error) {
	dir, ok := observations.RegistryDir(kind)
	if !ok {
		return "", fmt.Errorf("storage: unknown registry kind %q", kind)
	}
	return filepath.Join(a.registriesDir(), dir, key+".json"), nil
}

// generateKeySalt returns a hex-encoded random per-instance secret used to
// salt registry key derivation (observations-module.md §"Registry keys").
func generateKeySalt() (string, error) {
	b := make([]byte, keySaltBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("storage: generate key_salt: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// appendLineFsync appends content plus a newline to a file, creating it (and
// its parent shard) if needed, and fsyncs — the JSONL whole-line append
// discipline (observations-module.md §"Storage additions"). A crash never
// leaves a partial line.
func appendLineFsync(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("storage: prepare dir for %q: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm) //nolint:gosec // adapter-internal path under the Ledger home
	if err != nil {
		return fmt.Errorf("storage: open %q for append: %w", path, err)
	}
	line := append(slices.Clone(content), '\n')
	if _, werr := f.Write(line); werr != nil {
		_ = f.Close()
		return fmt.Errorf("storage: append to %q: %w", path, werr)
	}
	if serr := f.Sync(); serr != nil {
		_ = f.Close()
		return fmt.Errorf("storage: fsync %q: %w", path, serr)
	}
	return f.Close()
}

// readJSONLLines reads a JSONL file and returns its non-empty lines. A missing
// file yields no lines and no error; blank lines are dropped.
func readJSONLLines(path string) (lines [][]byte, err error) {
	b, err := os.ReadFile(path) //nolint:gosec // adapter-internal path under the Ledger home
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read %q: %w", path, err)
	}
	for _, raw := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		lines = append(lines, []byte(raw))
	}
	return lines, nil
}

// mustDate parses a YYYY-MM-DD date in loc, falling back to the zero civil
// day on a malformed value (the caller only uses it to derive an engine day
// id, and a missing record is not an error).
func mustDate(date string, loc *time.Location) time.Time {
	if d, err := observations.ParseDate(date, loc); err == nil {
		return d
	}
	return time.Time{}
}
