package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// tripwireFile is the scheduler's bookkeeping file under engine/. It records
// what the tripwire has already done (the last run date) — state the day
// records cannot carry because the dead-man fires on their absence. It is
// deliberately not part of the derived status.json projection.
const tripwireFile = "tripwire.json" //nolint:gosec // G101 false positive: a filename, not a credential

// witnessPath / tripwirePath resolve the two engine-tree files the tripwire
// phase reads and writes (chain/profile/storm paths live in engine.go).
func (a *Adapter) witnessPath() string  { return filepath.Join(a.engineDir(), witnessFile) }
func (a *Adapter) tripwirePath() string { return filepath.Join(a.engineDir(), tripwireFile) }

// ReadWitnessContract reads witness.json (engine-module.md §witness.json). The
// fresh scaffold stub round-trips to a zero-identity, unconfirmed contract, so
// a never-provisioned witness reads as "not confirmed, not lapsed".
func (a *Adapter) ReadWitnessContract() (engine.WitnessContract, error) {
	return readJSON[engine.WitnessContract](a.witnessPath(), "witness.json")
}

// WriteWitnessContract writes witness.json. It is used by the witness
// setup/confirmation flow (host-side, Phase 16); the tripwire itself only
// reads the contract.
func (a *Adapter) WriteWitnessContract(w engine.WitnessContract) error {
	content, err := marshalJSON(w)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.witnessPath(), content, filePerm); err != nil {
		return fmt.Errorf("storage: write witness.json: %w", err)
	}
	return nil
}

// AppendStormEvent appends one event to storm.json's history (engine-module.md
// §storm.json: append-only history — declarations, confirmations, entries,
// renewals, expiries, and ends are recorded, never overwritten).
func (a *Adapter) AppendStormEvent(ev engine.StormEvent) error {
	return a.AppendStormEvents(ev)
}

// AppendStormEvents appends events to storm.json's history in order — the form
// a single tripwire run's storm bookkeeping (lapse/expire/enter) persists.
//
// Every append also mints an episode id for any run this write opens and stamps
// the storm.json schema version. Identity lives in the sibling episodes[] index,
// never in history[] (engine.PlanStormEpisodes is idempotent and derives runs
// from the folded history), so this leaves the derived status.json untouched:
// StormStanding reads history's last element and ignores episodes[] entirely.
// Because the planner matches runs already indexed, the first append after an
// upgrade also self-heals — it mints ids for any runs that predate the index,
// and every later append then adds nothing new. That is why this and the
// explicit backfill command coexist: this keeps the index current as storms
// happen, the command completes it without requiring a new event to occur.
func (a *Adapter) AppendStormEvents(events ...engine.StormEvent) error {
	if len(events) == 0 {
		return nil
	}
	h, err := a.ReadStormState()
	if err != nil {
		return err
	}
	h.History = append(h.History, events...)
	h.Version = engine.StormVersion
	h.Episodes = append(h.Episodes, engine.PlanStormEpisodes(h, mintDayFor(events[len(events)-1]))...)
	content, err := marshalJSON(h)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.stormPath(), content, filePerm); err != nil {
		return fmt.Errorf("storage: write storm.json: %w", err)
	}
	return nil
}

// WriteStormEpisodes appends episode records to storm.json's episodes[] index
// and stamps the schema version, touching history[] not at all. It is the
// storage seam the `storm backfill-episodes` command writes through, so identity
// is added to primary testimony the same append-only way a storm event is: no
// history record is edited, reordered, or removed, and the derived status.json
// is unchanged because StormStanding never reads episodes[]. Passing no episodes
// is a no-op — nothing is read, nothing is written.
func (a *Adapter) WriteStormEpisodes(episodes ...engine.StormEpisode) error {
	if len(episodes) == 0 {
		return nil
	}
	h, err := a.ReadStormState()
	if err != nil {
		return err
	}
	h.Version = engine.StormVersion
	h.Episodes = append(h.Episodes, episodes...)
	content, err := marshalJSON(h)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.stormPath(), content, filePerm); err != nil {
		return fmt.Errorf("storage: write storm.json: %w", err)
	}
	return nil
}

// mintDayFor resolves the reference instant a same-write episode mint uses, from
// the just-appended event's RFC3339 `at`. It matters only at the margins:
// PlanStormEpisodes dates each event-opened run by that run's own opener_at, so
// this fixes the bare-window cutoff (a window is an episode once its start is on
// or before this instant) and the ultimate fallback. An unparseable `at` yields
// the zero instant, which mints no not-yet-open bare window rather than
// fabricating an id — the run's own opener still dates every event-opened
// episode correctly.
func mintDayFor(ev engine.StormEvent) time.Time {
	if at, err := time.Parse(time.RFC3339, ev.At); err == nil {
		return at
	}
	return time.Time{}
}

// TripwireState is engine/tripwire.json: the scheduler's small durable memory
// of what it has already posted. LastRunDate (YYYY-MM-DD) lets the run stay
// idempotent within a morning. A legacy last_heartbeat_month key from an
// earlier build is simply ignored on read (the monthly heartbeat is retired —
// the weekly witness report absorbs the all-clear).
type TripwireState struct {
	LastRunDate string `json:"last_run_date"`
}

// ReadTripwireState reads engine/tripwire.json. A missing file is not an
// error — a fresh Ledger has simply never run the tripwire — so it yields the
// zero state (which makes the next run the first of its month).
func (a *Adapter) ReadTripwireState() (TripwireState, error) {
	s, _, err := readJSONOptional[TripwireState](a.tripwirePath(), "tripwire.json")
	return s, err
}

// WriteTripwireState writes engine/tripwire.json, creating the engine tree if
// a bare run reaches it before a scaffold.
func (a *Adapter) WriteTripwireState(s TripwireState) error {
	content, err := marshalJSON(s)
	if err != nil {
		return err
	}
	if err := ensureDir(a.engineDir(), "engine"); err != nil {
		return err
	}
	if err := os.WriteFile(a.tripwirePath(), content, filePerm); err != nil {
		return fmt.Errorf("storage: write tripwire.json: %w", err)
	}
	return nil
}
