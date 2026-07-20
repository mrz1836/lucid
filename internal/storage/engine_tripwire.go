package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrz1836/lucid/internal/engine"
)

// tripwireFile is the scheduler's bookkeeping file under engine/. It records
// what the tripwire has already done (the last heartbeat month, the last run
// date) — state the day records cannot carry because the dead-man fires on
// their absence. It is deliberately not part of the derived status.json
// projection.
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
func (a *Adapter) AppendStormEvents(events ...engine.StormEvent) error {
	if len(events) == 0 {
		return nil
	}
	h, err := a.ReadStormState()
	if err != nil {
		return err
	}
	h.History = append(h.History, events...)
	content, err := marshalJSON(h)
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.stormPath(), content, filePerm); err != nil {
		return fmt.Errorf("storage: write storm.json: %w", err)
	}
	return nil
}

// TripwireState is engine/tripwire.json: the scheduler's small durable memory
// of what it has already posted. LastHeartbeatMonth (YYYY-MM) drives the
// "first run of each calendar month" heartbeat; LastRunDate (YYYY-MM-DD) lets
// the run stay idempotent within a morning.
type TripwireState struct {
	LastHeartbeatMonth string `json:"last_heartbeat_month"`
	LastRunDate        string `json:"last_run_date"`
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
