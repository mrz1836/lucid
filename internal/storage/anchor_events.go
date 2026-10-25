package storage

import (
	"fmt"
	"path/filepath"

	"github.com/mrz1836/lucid/internal/engine"
)

// anchorEventsFile is the anchor event store under ~/.lucid/engine/
// (engine-module.md §"Anchor event store (`anchor_events.json`)"). It sits
// beside anchors.json rather than inside it: an older binary re-marshals
// anchors.json on every anchor write and drops whatever it does not know, so
// anything it must not erase — every kind, every logged fact, every pair
// intent — lives in a file it never opens. Only this adapter touches it.
const anchorEventsFile = "anchor_events.json"

func (a *Adapter) anchorEventsPath() string { return filepath.Join(a.engineDir(), anchorEventsFile) }

// ReadAnchorEvents reads the anchor event store. The log is returned unfolded;
// joining it to anchors.json is a read-time fold ([engine.FoldAnchorEvents]).
//
// A missing file is the empty log, not an error — the tolerance
// [Adapter.ReadSelfFacts] has. The store is never scaffolded: the first event
// write creates it, so a Ledger that has never recorded a kind has no store and
// must read exactly as it always has. The zero log carries Version 0, and that
// is correct: the version is a signal, not a gate — nothing checks it on read —
// and every append stamps the current one.
//
// A record that decodes but is not well formed (an unknown type, a malformed
// day, a missing per-type field) is returned as it is: the fold skips it and
// `lucid validate` reports it, and returning it is what keeps the next append
// from silently erasing it. A body that does not decode is a loud error naming
// the file, never an empty log — a corrupt store must surface rather than read
// as empty and invite an append that buries it.
func (a *Adapter) ReadAnchorEvents() (engine.AnchorEventLog, error) {
	log, _, err := readJSONOptional[engine.AnchorEventLog](a.anchorEventsPath(), anchorEventsFile)
	return log, err
}

// AppendAnchorEvents records one or more anchor events in a single write: read
// the store, append every event, write once. Either every event lands or none
// does. Passing no events is a no-op: nothing is read, nothing is written.
//
// The store is append-only: no event or pair intent is ever rewritten or
// removed, and the only field this touches outside the appended events is the
// envelope's schema version, stamped on every write as [Adapter.AppendAnchors]
// stamps anchors.json's.
//
// Every event is checked before anything is written, so a refused batch leaves
// the store byte-unchanged:
//
//   - it must be well formed ([engine.ValidateAnchorEvent]) — the fold skips a
//     malformed record, so writing one would record a fact that never counts;
//   - its id is a receipt, so an id already held by a stored event, or by an
//     earlier event in the same batch, is refused;
//   - an id a pair intent has reserved is accepted only for that intent's exact
//     planned event, because a pair's event half is recognized by equality and
//     any other record under the id would leave the pair unfinishable.
func (a *Adapter) AppendAnchorEvents(events ...engine.AnchorEvent) error {
	if len(events) == 0 {
		return nil
	}
	log, err := a.ReadAnchorEvents()
	if err != nil {
		return err
	}
	stored := make(map[string]bool, len(log.Events)+len(events))
	for _, e := range log.Events {
		stored[e.ID] = true
	}
	reserved := make(map[string]engine.AnchorEvent, len(log.PairIntents))
	for _, p := range log.PairIntents {
		reserved[p.Event.ID] = p.Event
	}
	for _, e := range events {
		if err := engine.ValidateAnchorEvent(e); err != nil {
			return fmt.Errorf("storage: refuse anchor event: %w", err)
		}
		if stored[e.ID] {
			return fmt.Errorf("storage: anchor event id %s is already recorded; an event id is never reused", e.ID)
		}
		if planned, ok := reserved[e.ID]; ok && planned != e {
			return fmt.Errorf("storage: anchor event id %s is reserved by a pair intent for a different event", e.ID)
		}
		stored[e.ID] = true
	}
	log.Events = append(log.Events, events...)
	return a.writeAnchorEvents(log)
}

// AppendAnchorEvent records one anchor event. It is
// [Adapter.AppendAnchorEvents] with a single event, so every event write — one
// or many — takes the same path.
func (a *Adapter) AppendAnchorEvent(event engine.AnchorEvent) error {
	return a.AppendAnchorEvents(event)
}

// AppendAnchorPairIntent records the transaction metadata a two-file verb
// writes before either of its domain halves (engine-module.md §"Two-file verbs
// and the write order"): it reserves the intent's event id and holds the exact
// planned event, the exact planned v3 record and their order. The events are
// left exactly as they are — an intent is metadata, never an applied fact.
//
// Intents are append-only and never overwritten or removed. Replaying an
// intent already in the store, exactly, is a no-op that writes nothing, so a
// retry of an interrupted verb cannot duplicate its reservation. Anything else
// under an id that is already taken — a different intent reserving the same
// id, or an id a stored event already holds — is refused, as is an intent that
// is not well formed ([engine.ValidateAnchorPairIntent]). A refused intent
// leaves the store byte-unchanged.
func (a *Adapter) AppendAnchorPairIntent(intent engine.AnchorPairIntent) error {
	if err := engine.ValidateAnchorPairIntent(intent); err != nil {
		return fmt.Errorf("storage: refuse pair intent: %w", err)
	}
	log, err := a.ReadAnchorEvents()
	if err != nil {
		return err
	}
	id := intent.Event.ID
	for _, p := range log.PairIntents {
		if p.Event.ID != id {
			continue
		}
		if p == intent {
			return nil // an exact replay: the reservation already stands
		}
		return fmt.Errorf("storage: anchor event id %s is already reserved by a different pair intent", id)
	}
	for _, e := range log.Events {
		if e.ID == id {
			return fmt.Errorf("storage: anchor event id %s is already recorded; a pair intent reserves an unused id", id)
		}
	}
	log.PairIntents = append(log.PairIntents, intent)
	return a.writeAnchorEvents(log)
}

// writeAnchorEvents stamps the schema version and writes the whole store once.
// The events array is written as [] rather than null even when only an intent
// has been recorded, so the envelope always reads {"version": 1, "events":
// [...]}; pair_intents stays absent until the first intent.
//
// The write is atomic: the store is the only copy of every kind and every
// logged fact, and a torn write would truncate history the fold cannot
// rebuild.
func (a *Adapter) writeAnchorEvents(log engine.AnchorEventLog) error {
	log.Version = engine.AnchorEventsVersion
	if log.Events == nil {
		log.Events = []engine.AnchorEvent{}
	}
	content, err := marshalJSON(log)
	if err != nil {
		return err
	}
	return writeFileAtomic(a.anchorEventsPath(), content, anchorEventsFile)
}
