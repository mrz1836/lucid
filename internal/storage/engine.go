package storage

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	anchorsFile      = "anchors.json"
	selfFile         = "self.json"
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
func (a *Adapter) anchorsPath() string { return filepath.Join(a.engineDir(), anchorsFile) }
func (a *Adapter) selfPath() string    { return filepath.Join(a.engineDir(), selfFile) }
func (a *Adapter) statusPath() string  { return filepath.Join(a.engineDir(), engineStatusFile) }

// ScaffoldEngine creates the engine/ tree and writes the default
// chain.json, witness.json, storm.json, profile.json, and empty anchors.json
// and self.json files if missing (engine-module.md §"Storage additions"). It is
// idempotent: existing files are never overwritten, so a hand-edited
// chain.json survives a re-scaffold. status.json is not written here — it is
// derived, produced by the first RebuildEngineStatus.
func (a *Adapter) ScaffoldEngine() error {
	daysDir := filepath.Join(a.engineDir(), engineDaysDir)
	if err := os.MkdirAll(daysDir, dirPerm); err != nil {
		return fmt.Errorf("storage: create engine days dir: %w", err)
	}

	chainBytes, err := marshalJSON(engine.DefaultChain())
	if err != nil {
		return err
	}
	anchorsBytes, err := marshalJSON(engine.AnchorLog{Version: engine.AnchorVersion, History: []engine.Anchor{}})
	if err != nil {
		return err
	}
	// The history is an empty slice rather than nil so the scaffolded file
	// reads "history": [] like its anchors.json sibling — a fresh store says
	// "nothing recorded yet", not "null".
	selfBytes, err := marshalJSON(engine.SelfLog{Version: engine.SelfVersion, History: []engine.SelfFact{}})
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
		{a.anchorsPath(), anchorsBytes},
		{a.selfPath(), selfBytes},
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
func (a *Adapter) ReadEngineDay(dayID string) (engine.DayRecord, bool, error) {
	path, err := a.engineDayPath(dayID)
	if err != nil {
		return engine.DayRecord{}, false, err
	}
	return readJSONOptional[engine.DayRecord](path, fmt.Sprintf("engine day %q", dayID))
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

// FillEngineDayMode fills `mode` and `mode_declared_at` on an existing day
// record that never declared one — the single guarded write that touches a day
// record's base after creation (data-model.md §"One guarded exception",
// engine-module.md §"`/mode --day` gap-fill").
//
// The gap is read from `mode_declared_at`, not from `mode`. A close-out builds
// its record with the chain's default mode already in place, so a day that was
// closed out but never declared — the ordinary shape a gap-fill exists for —
// carries `mode: green` and an empty `mode_declared_at`. That default is a
// placeholder the record needed in order to exist, not testimony about the day;
// the declaration timestamp is the only field that says a mode was ever chosen.
//
// The never-overwrite guard lives here rather than in the caller, so no future
// call site can reach a declared mode by taking a different route: a record
// whose mode was declared is refused, and the bell's "no retroactive amendment"
// (engine §2) therefore holds against every path. Nothing else on the base is
// touched, and `mode` stays off the foldable-field whitelist, so the correction
// path cannot reach it either.
func (a *Adapter) FillEngineDayMode(dayID string, mode engine.Mode, declaredAt string) error {
	rec, found, err := a.ReadEngineDay(dayID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("storage: cannot fill mode on missing engine day %q", dayID)
	}
	if rec.ModeDeclaredAt != "" {
		return fmt.Errorf("storage: engine day %q already declares a mode (a gap-fill never overwrites)", dayID)
	}
	rec.Mode = mode
	rec.ModeDeclaredAt = declaredAt

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
	slices.SortFunc(records, func(a, b engine.DayRecord) int { return cmp.Compare(a.LogicalDate, b.LogicalDate) })
	return records, nil
}

// ReadChainConfig reads chain.json.
func (a *Adapter) ReadChainConfig() (engine.ChainConfig, error) {
	return readJSON[engine.ChainConfig](a.chainPath(), "chain.json")
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
	return readJSON[engine.ProfileState](a.profilePath(), "profile.json")
}

// ReadStormState reads storm.json (engine-module.md §storm.json). The
// derived status reads it to decide whether a storm stands and through what
// date; the tripwire phase (Phase 10) owns appending its history events.
func (a *Adapter) ReadStormState() (engine.StormHistory, error) {
	return readJSON[engine.StormHistory](a.stormPath(), "storm.json")
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

// ReadAnchors reads anchors.json (engine-module.md §anchors.json). A fresh
// tree yields the scaffolded empty log; the full append-only history is
// returned unfolded — the effective set is a read-time fold, one record per
// identity ([engine.LatestAnchors]).
func (a *Adapter) ReadAnchors() (engine.AnchorLog, error) {
	return readJSON[engine.AnchorLog](a.anchorsPath(), "anchors.json")
}

// AppendAnchors records one or more milestones in a single write: read the
// log, append every record, write once (engine-module.md §anchors.json,
// append-only; the latest record per identity wins at read).
//
// One read, one marshal, one write is the point. Either every record lands or
// none does, which is what lets a rename retire one identity and mint another
// in the same operation without forking the surface — a half-applied pair
// would leave both the old and the new label active at once. Callers that need
// two records written together must pass them here rather than calling twice.
// Passing no records is a no-op: nothing is read, nothing is written.
//
// No record is ever rewritten in place and none is ever removed; the only
// field this touches outside the history is the envelope's schema version,
// stamped on every append so the number in a file is always true of that file.
// A correction or a reset is therefore a new dated append, never an edit —
// mirroring [Adapter.AppendProfileEvent].
func (a *Adapter) AppendAnchors(anchors ...engine.Anchor) error {
	if len(anchors) == 0 {
		return nil
	}
	log, err := a.ReadAnchors()
	if err != nil {
		return err
	}
	log.Version = engine.AnchorVersion
	log.History = append(log.History, anchors...)
	content, err := marshalJSON(log)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.anchorsPath(), content, filePerm); err != nil {
		return fmt.Errorf("storage: write anchors.json: %w", err)
	}
	return nil
}

// AppendAnchor records one milestone. It is [Adapter.AppendAnchors] with a
// single record, so every anchor write — one or many — takes the same path.
func (a *Adapter) AppendAnchor(anchor engine.Anchor) error {
	return a.AppendAnchors(anchor)
}

// ReadSelfFacts reads self.json (engine-module.md §self.json). The full
// append-only history is returned unfolded — the effective profile is a
// read-time fold, one record per identity ([engine.LatestSelfFacts]).
//
// A missing file is not an error here, which is the one place this diverges
// from [Adapter.ReadAnchors], and the divergence is deliberate. ReadAnchors
// treats the record as required because every anchor verb scaffolds first;
// this store's most important reader does not. Router.Ask is contractually
// read-only — it never scaffolds — so on a Ledger scaffolded before this store
// existed, a required-file read would make /ask fail outright rather than
// simply ground on no facts. An absent file therefore reads as the empty log,
// and the read stays a read.
//
// The zero log it returns carries Version 0 rather than SelfVersion, and that
// is correct: nothing on any read path gates on the version, and every append
// stamps the current one. A read that repaired the envelope would be a read
// that writes, which is exactly what the tolerance exists to avoid.
//
// A malformed body is still a parse error. A corrupt self-profile must surface
// rather than silently reading as empty and inviting an append that buries it.
func (a *Adapter) ReadSelfFacts() (engine.SelfLog, error) {
	log, _, err := readJSONOptional[engine.SelfLog](a.selfPath(), "self.json")
	return log, err
}

// AppendSelfFacts records one or more self facts in a single write: read the
// log, append every record, write once (engine-module.md §self.json,
// append-only; the latest record per identity wins at read).
//
// One read, one marshal, one write, mirroring [Adapter.AppendAnchors]: either
// every record lands or none does. This store's verbs each need only a single
// record — a move is one append, because no record here predates ids — but the
// contract is kept so a caller that ever does need two written together has a
// path that cannot half-apply. Passing no records is a no-op: nothing is read,
// nothing is written.
//
// The write goes through [writeFileAtomic] rather than os.WriteFile, the second
// deliberate divergence from the anchors path. This file accumulates for years
// and holds the only copy of the subject's semantic memory, so a write
// interrupted partway must leave the previous file intact rather than truncate
// it.
//
// No record is ever rewritten in place and none is ever removed; the only field
// this touches outside the history is the envelope's schema version, stamped on
// every append so the number in a file is always true of that file. A
// correction, a move and a retirement are therefore each a new append, never an
// edit.
func (a *Adapter) AppendSelfFacts(facts ...engine.SelfFact) error {
	if len(facts) == 0 {
		return nil
	}
	log, err := a.ReadSelfFacts()
	if err != nil {
		return err
	}
	log.Version = engine.SelfVersion
	log.History = append(log.History, facts...)
	content, err := marshalJSON(log)
	if err != nil {
		return err
	}
	return writeFileAtomic(a.selfPath(), content, "self.json")
}

// AppendSelfFact records one self fact. It is [Adapter.AppendSelfFacts] with a
// single record, so every self-fact write — one or many — takes the same path.
func (a *Adapter) AppendSelfFact(fact engine.SelfFact) error {
	return a.AppendSelfFacts(fact)
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
func (a *Adapter) SetEngineEscalation(loc *time.Location, escalation engine.EscalationState, stakeOwed bool) (engine.Status, error) {
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
func (a *Adapter) buildAndWriteStatus(loc *time.Location, escalation engine.EscalationState, stakeOwed bool) (engine.Status, error) {
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
	return readJSON[engine.Status](a.statusPath(), "status.json")
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
