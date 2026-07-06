package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// Engine-tree names under ~/.lucid/engine/ (engine-module.md §"Storage
// additions"). Only this adapter touches them; the Engine module reads and
// writes exclusively here — never raw/, processed/, insights/, or people/.
const (
	engineDirName    = "engine"
	engineDaysDir    = "days"
	chainFile        = "chain.json"
	witnessFile      = "witness.json"
	stormFile        = "storm.json"
	profileFile      = "profile.json"
	engineStatusFile = "status.json"
	engineDayExt     = ".json"
	engineDayPrefix  = "day_"
)

// witnessStub and stormStub are the Phase-8 placeholder contents for the
// two Engine files the tripwire phase (Phase 10) owns. They are written so
// the engine/ tree is complete from scaffold; their real readers/writers
// arrive with the tripwire. l2_enabled stays false until a witness
// confirmation is recorded (engine-module.md §witness.json).
const (
	witnessStub = "{\n  \"confirmed_at\": null,\n  \"l2_enabled\": false,\n  \"status_history\": []\n}\n"
	stormStub   = "{\n  \"clauses\": [],\n  \"windows\": [],\n  \"duration_days\": 14,\n  \"history\": []\n}\n"
)

// engineDir returns the ~/.lucid/engine/ root.
func (a *Adapter) engineDir() string { return filepath.Join(a.home, engineDirName) }

func (a *Adapter) chainPath() string   { return filepath.Join(a.engineDir(), chainFile) }
func (a *Adapter) profilePath() string { return filepath.Join(a.engineDir(), profileFile) }
func (a *Adapter) stormPath() string   { return filepath.Join(a.engineDir(), stormFile) }
func (a *Adapter) statusPath() string  { return filepath.Join(a.engineDir(), engineStatusFile) }

// ScaffoldEngine creates the engine/ tree and writes the default
// chain.json, witness.json, storm.json, and profile.json if missing
// (engine-module.md §"Storage additions"). It is idempotent: existing
// files are never overwritten, so a hand-edited chain.json survives a
// re-scaffold. status.json is not written here — it is derived, produced
// by the first RebuildEngineStatus.
func (a *Adapter) ScaffoldEngine() error {
	daysDir := filepath.Join(a.engineDir(), engineDaysDir)
	if err := os.MkdirAll(daysDir, dirPerm); err != nil {
		return fmt.Errorf("storage: create engine days dir: %w", err)
	}

	chainBytes, err := marshalJSON(engine.DefaultChain())
	if err != nil {
		return err
	}
	files := []struct {
		path    string
		content []byte
	}{
		{a.chainPath(), chainBytes},
		{filepath.Join(a.engineDir(), witnessFile), []byte(witnessStub)},
		{filepath.Join(a.engineDir(), stormFile), []byte(stormStub)},
		{a.profilePath(), mustMarshalProfile(engine.DefaultProfileState())},
	}
	for _, f := range files {
		if _, err := writeExcl(f.path, f.content); err != nil {
			return err
		}
	}
	return nil
}

// WriteEngineDay writes a new day record under
// engine/days/YYYY/MM/day_YYYY_MM_DD.json. days/ is append-only per day-id
// (engine-module.md §"Storage additions"): an existing record is never
// overwritten here — corrections go through [Adapter.AppendEngineCorrection].
func (a *Adapter) WriteEngineDay(rec engine.DayRecord) error {
	path, err := a.engineDayPath(rec.DayID)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("storage: prepare engine day dir: %w", err)
	}
	content, err := marshalJSON(rec)
	if err != nil {
		return err
	}
	wrote, err := writeExcl(path, content)
	if err != nil {
		return err
	}
	if !wrote {
		return fmt.Errorf("storage: engine day %q already exists (use a correction)", rec.DayID)
	}
	return nil
}

// ReadEngineDay reads one day record by id, unfolded (corrections still
// pending). It returns (record, found, error): a missing record is not an
// error — the caller decides whether that means "create fresh".
func (a *Adapter) ReadEngineDay(dayID string) (rec engine.DayRecord, found bool, err error) {
	path, err := a.engineDayPath(dayID)
	if err != nil {
		return engine.DayRecord{}, false, err
	}
	b, err := os.ReadFile(path) //nolint:gosec // adapter-internal path from a validated day id
	if errors.Is(err, fs.ErrNotExist) {
		return engine.DayRecord{}, false, nil
	}
	if err != nil {
		return engine.DayRecord{}, false, fmt.Errorf("storage: read engine day %q: %w", dayID, err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return engine.DayRecord{}, false, fmt.Errorf("storage: parse engine day %q: %w", dayID, err)
	}
	return rec, true, nil
}

// ReadEngineDayFolded reads one day record and applies its corrections,
// returning the effective record (engine-module.md §corrections[] fold).
func (a *Adapter) ReadEngineDayFolded(dayID string) (rec engine.DayRecord, found bool, err error) {
	raw, found, err := a.ReadEngineDay(dayID)
	if err != nil || !found {
		return engine.DayRecord{}, found, err
	}
	return raw.Folded(), true, nil
}

// AppendEngineCorrection appends a correction to an existing day record
// and rewrites the file, leaving every original field intact — the only
// sanctioned mutation of a day record (engine-module.md §corrections[]). A
// correction naming an immutable field is rejected before any write
// (engine-module.md §Error states).
func (a *Adapter) AppendEngineCorrection(dayID string, corr engine.Correction) error {
	if bad := engine.ImmutableCorrectionFields(corr); len(bad) > 0 {
		return fmt.Errorf("storage: correction names immutable field(s) %s", strings.Join(bad, ", "))
	}
	rec, found, err := a.ReadEngineDay(dayID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("storage: cannot correct missing engine day %q", dayID)
	}
	rec.Corrections = append(rec.Corrections, corr)
	path, err := a.engineDayPath(dayID)
	if err != nil {
		return err
	}
	content, err := marshalJSON(rec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return fmt.Errorf("storage: rewrite engine day %q: %w", dayID, err)
	}
	return nil
}

// ReadEngineDays reads every day record under engine/days/, folds each,
// and returns them sorted by logical_date. A fresh (empty) tree yields an
// empty slice, not an error.
func (a *Adapter) ReadEngineDays() ([]engine.DayRecord, error) {
	daysDir := filepath.Join(a.engineDir(), engineDaysDir)
	var records []engine.DayRecord
	err := filepath.WalkDir(daysDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), engineDayPrefix) || !strings.HasSuffix(d.Name(), engineDayExt) {
			return nil
		}
		b, readErr := os.ReadFile(path) //nolint:gosec // adapter-internal path under the engine tree
		if readErr != nil {
			return fmt.Errorf("storage: read engine day %q: %w", path, readErr)
		}
		var rec engine.DayRecord
		if err := json.Unmarshal(b, &rec); err != nil {
			return fmt.Errorf("storage: parse engine day %q: %w", path, err)
		}
		records = append(records, rec.Folded())
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].LogicalDate < records[j].LogicalDate })
	return records, nil
}

// ReadChainConfig reads chain.json.
func (a *Adapter) ReadChainConfig() (engine.ChainConfig, error) {
	b, err := os.ReadFile(a.chainPath())
	if err != nil {
		return engine.ChainConfig{}, fmt.Errorf("storage: read chain.json: %w", err)
	}
	var c engine.ChainConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return engine.ChainConfig{}, fmt.Errorf("storage: parse chain.json: %w", err)
	}
	return c, nil
}

// WriteChainConfig writes chain.json. It is used to stamp chain_start on
// the first completed close-out; the file is otherwise hand-edited only.
func (a *Adapter) WriteChainConfig(c engine.ChainConfig) error {
	content, err := marshalJSON(c)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.chainPath(), content, filePerm); err != nil {
		return fmt.Errorf("storage: write chain.json: %w", err)
	}
	return nil
}

// ReadProfileState reads profile.json.
func (a *Adapter) ReadProfileState() (engine.ProfileState, error) {
	b, err := os.ReadFile(a.profilePath())
	if err != nil {
		return engine.ProfileState{}, fmt.Errorf("storage: read profile.json: %w", err)
	}
	var s engine.ProfileState
	if err := json.Unmarshal(b, &s); err != nil {
		return engine.ProfileState{}, fmt.Errorf("storage: parse profile.json: %w", err)
	}
	return s, nil
}

// ReadStormState reads storm.json (engine-module.md §storm.json). The
// derived status reads it to decide whether a storm stands and through what
// date; the tripwire phase (Phase 10) owns appending its history events.
func (a *Adapter) ReadStormState() (engine.StormHistory, error) {
	b, err := os.ReadFile(a.stormPath())
	if err != nil {
		return engine.StormHistory{}, fmt.Errorf("storage: read storm.json: %w", err)
	}
	var s engine.StormHistory
	if err := json.Unmarshal(b, &s); err != nil {
		return engine.StormHistory{}, fmt.Errorf("storage: parse storm.json: %w", err)
	}
	return s, nil
}

// AppendProfileEvent records a profile switch: it appends sw to the
// history and updates the active profile (engine-module.md §profile.json,
// append-only history).
func (a *Adapter) AppendProfileEvent(sw engine.ProfileSwitch) error {
	state, err := a.ReadProfileState()
	if err != nil {
		return err
	}
	next := state.WithSwitch(sw)
	if err := os.WriteFile(a.profilePath(), mustMarshalProfile(next), filePerm); err != nil {
		return fmt.Errorf("storage: write profile.json: %w", err)
	}
	return nil
}

// RebuildEngineStatus recomputes engine/status.json from the folded day
// records plus chain, profile, storm, and witness state (engine-module.md
// §status.json, derived and rebuildable). It stamps chain_start exactly once —
// on the first completed close-out — and never changes it thereafter.
//
// escalation_state and stake_owed are tripwire-owned (engine-module.md §The
// tripwire), so a plain rebuild — the close-out / mode / status path —
// preserves whatever the last tripwire run set rather than clearing the
// ladder; the tripwire changes them through [Adapter.SetEngineEscalation]. A
// missing or corrupt status.json preserves nothing (the ladder reads as clear
// from scratch), which keeps the delete-and-rebuild determinism intact for the
// day-derived fields. loc is the location logical dates are interpreted in.
func (a *Adapter) RebuildEngineStatus(loc *time.Location) (engine.Status, error) {
	prior, err := a.ReadEngineStatus()
	if err != nil {
		prior = engine.Status{} // missing/corrupt ⇒ clear ladder, rebuild from days/
	}
	escalation := prior.EscalationState
	if escalation == "" {
		escalation = engine.EscalationNone
	}
	return a.buildAndWriteStatus(loc, escalation, prior.StakeOwed)
}

// SetEngineEscalation rebuilds status.json with an explicit escalation_state
// and stake_owed — the tripwire's persistence path (engine-module.md §The
// tripwire "sets escalation_state"). Every other status field is recomputed
// from the day records, so a backfill that landed before the run still folds
// the streak and budget correctly on the same write.
func (a *Adapter) SetEngineEscalation(loc *time.Location, escalation string, stakeOwed bool) (engine.Status, error) {
	if escalation == "" {
		escalation = engine.EscalationNone
	}
	return a.buildAndWriteStatus(loc, escalation, stakeOwed)
}

// buildAndWriteStatus is the shared fold: it stamps chain_start once, derives
// witness_lapsed from witness.json, folds the day records with the given
// tripwire-owned escalation/stake, and writes status.json. Splitting it out
// keeps the plain-rebuild and the tripwire-set paths byte-identical apart from
// the two fields the tripwire owns.
func (a *Adapter) buildAndWriteStatus(loc *time.Location, escalation string, stakeOwed bool) (engine.Status, error) {
	chain, err := a.ReadChainConfig()
	if err != nil {
		return engine.Status{}, err
	}
	records, err := a.ReadEngineDays()
	if err != nil {
		return engine.Status{}, err
	}

	if chain.ChainStart == nil {
		if start := engine.EarliestCompletedDate(records, loc); start != "" {
			chain.ChainStart = &start
			if err = a.WriteChainConfig(chain); err != nil {
				return engine.Status{}, err
			}
		}
	}

	profile, err := a.ReadProfileState()
	if err != nil {
		return engine.Status{}, err
	}

	storm, err := a.ReadStormState()
	if err != nil {
		return engine.Status{}, err
	}

	// A missing witness.json is not fatal here — an unprovisioned witness is
	// simply "not lapsed"; the tripwire's own reads own the L2 gating.
	witness, _ := a.ReadWitnessContract()

	status := engine.BuildStatus(engine.StatusInput{
		Records:       records,
		Chain:         chain,
		Storm:         storm,
		ChainStart:    chain.ChainStart,
		Profile:       profile.Active,
		Escalation:    escalation,
		StakeOwed:     stakeOwed,
		WitnessLapsed: witness.IsLapsed(),
		Loc:           loc,
	})
	content, err := marshalJSON(status)
	if err != nil {
		return engine.Status{}, err
	}
	if err := os.WriteFile(a.statusPath(), content, filePerm); err != nil {
		return engine.Status{}, fmt.Errorf("storage: write status.json: %w", err)
	}
	return status, nil
}

// ReadEngineStatus reads engine/status.json (the derived projection).
func (a *Adapter) ReadEngineStatus() (engine.Status, error) {
	b, err := os.ReadFile(a.statusPath())
	if err != nil {
		return engine.Status{}, fmt.Errorf("storage: read status.json: %w", err)
	}
	var s engine.Status
	if err := json.Unmarshal(b, &s); err != nil {
		return engine.Status{}, fmt.Errorf("storage: parse status.json: %w", err)
	}
	return s, nil
}

// engineDayPath resolves the on-disk path for a day id
// (engine/days/YYYY/MM/day_YYYY_MM_DD.json), deriving the shard from the
// id itself.
func (a *Adapter) engineDayPath(dayID string) (string, error) {
	year, month, err := engineDayShard(dayID)
	if err != nil {
		return "", err
	}
	return filepath.Join(a.engineDir(), engineDaysDir, year, month, dayID+engineDayExt), nil
}

// engineDayShard extracts the YYYY and MM shard from a day id of the form
// day_YYYY_MM_DD.
func engineDayShard(dayID string) (year, month string, err error) {
	parts := strings.Split(dayID, "_")
	if len(parts) != 4 || parts[0] != "day" {
		return "", "", fmt.Errorf("storage: malformed day id %q", dayID)
	}
	return parts[1], parts[2], nil
}

// marshalJSON renders v as indented JSON with a trailing newline — the
// stable on-disk form (deterministic: map keys sort, struct field order is
// fixed), so status.json rebuilds byte-for-byte.
func marshalJSON(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("storage: marshal json: %w", err)
	}
	return append(b, '\n'), nil
}

// mustMarshalProfile renders profile.json; the ProfileState shape cannot
// fail to marshal, so an error here is a programming bug.
func mustMarshalProfile(s engine.ProfileState) []byte {
	b, err := marshalJSON(s)
	if err != nil {
		panic(fmt.Sprintf("storage: marshal profile.json: %v", err))
	}
	return b
}
