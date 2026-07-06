package router

import (
	"context"
	"fmt"
	"time"

	"github.com/mrz1836/lucid/internal/agents/structuring"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// StructureRequest carries the inputs for one downstream Structuring pass
// over a captured raw entry. Structuring runs over every raw entry — /log and
// /closeout journal lines included — as a downstream pass, not at capture
// time (agent-contracts.md §"How contracts compose"); the router reads the
// raw entry and hands only its body to the agent behind the provider
// boundary.
type StructureRequest struct {
	RawID    string
	Now      time.Time
	Provider provider.Provider
}

// StructureResult reports what a Structuring pass produced. Wrote is false on
// the two honest degrade paths that leave the entry captured-but-unprocessed
// (update_person failed — error-states.md §S-5; write_processed failed —
// §St-3); in both the raw entry is untouched and the pass can be replayed.
type StructureResult struct {
	RawID       string
	ProcessedID string
	People      []storage.ProcessedPerson
	Wrote       bool
	Degraded    bool
	Ack         string
}

// Structure runs the deterministic compose for one raw entry
// (agent-contracts.md §"How contracts compose"): read the raw entry, run
// Structuring.extract over its body, run the People routine for each mention
// (which resolves/creates the slug and back-fills person_key so no artifact
// carries a null key), then write the processed artifact. It is idempotent —
// re-running it overwrites the artifact (differing only in produced_at),
// re-derives the same keys, and leaves the raw entry untouched
// (acceptance-criteria.md test cases 4.1–4.3).
//
// The raw entry is already captured, so a People-routine or write failure
// degrades honestly rather than erroring: the entry stays captured and the
// pass is replayable (error-states.md §S-5/§St-3). Only a failure to read the
// raw entry — an infrastructure fault, not a degrade — returns an error.
func (r *Router) Structure(ctx context.Context, req StructureRequest) (StructureResult, error) {
	doc, err := r.store.ReadRaw(req.RawID)
	if err != nil {
		return StructureResult{}, fmt.Errorf("structuring: read raw %q: %w", req.RawID, err)
	}

	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	occurredAt := entryOccurredAt(doc.Fields, now)

	ext := structuring.Extract(ctx, structuring.Input{
		RawID:        req.RawID,
		Body:         doc.EntryText(),
		AgentVersion: r.cfg.AgentVersions.Structuring,
	}, req.Provider)

	people, ok := r.resolvePeople(ext.People, req.RawID, occurredAt)
	if !ok {
		// update_person failed for a mention (e.g. wordlist missing) — abort
		// the write and degrade; the raw entry is captured, replay later (§S-5).
		return StructureResult{RawID: req.RawID, Degraded: true, Ack: ackPeopleTrouble(req.RawID)}, nil
	}

	art := storage.ProcessedArtifact{
		ID:           req.RawID,
		EntryID:      req.RawID,
		ProducedAt:   now,
		AgentVersion: ext.AgentVersion,
		Emotions:     toStorageItems(ext.Emotions),
		Themes:       toStorageItems(ext.Themes),
		People:       people,
		Notes:        optionalNotes(ext.Notes),
	}
	if err := r.store.WriteProcessed(art); err != nil {
		// Structuring succeeded but the artifact could not be written; the raw
		// entry is captured-but-unprocessed and the next pass recovers (§St-3).
		return StructureResult{RawID: req.RawID, Degraded: true, Ack: ackProcessedTrouble(req.RawID)}, nil //nolint:nilerr // St-3: a write_processed failure degrades honestly (raw stays captured), it is not a hard error
	}

	return StructureResult{
		RawID:       req.RawID,
		ProcessedID: req.RawID,
		People:      people,
		Wrote:       true,
		Degraded:    ext.Degraded,
	}, nil
}

// resolvePeople runs the storage People routine for each extracted mention,
// back-filling the resolved person_key and the authoritative first_mention
// onto a processed people[] entry. It deduplicates by resolved key so a name
// mentioned twice in one entry yields one people[] entry. It reports ok=false
// on the first update_person failure so the caller degrades without writing
// (error-states.md §S-5).
func (r *Router) resolvePeople(mentions []structuring.Person, rawID string, at time.Time) ([]storage.ProcessedPerson, bool) {
	people := make([]storage.ProcessedPerson, 0, len(mentions))
	seen := map[string]bool{}
	for _, m := range mentions {
		res, err := r.store.UpdatePerson(storage.PersonMention{
			DisplayName: m.DisplayName,
			RawEntryID:  rawID,
			At:          at,
		})
		if err != nil {
			return nil, false
		}
		if seen[res.PersonKey] {
			continue
		}
		seen[res.PersonKey] = true
		people = append(people, storage.ProcessedPerson{
			DisplayName:  m.DisplayName,
			PersonKey:    res.PersonKey,
			FirstMention: res.FirstMention,
		})
	}
	return people, true
}

// toStorageItems maps agent emotion/theme items to their storage shape.
func toStorageItems(items []structuring.Item) []storage.ProcessedItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]storage.ProcessedItem, 0, len(items))
	for _, it := range items {
		out = append(out, storage.ProcessedItem{Name: it.Name, Rationale: it.Rationale})
	}
	return out
}

// optionalNotes maps the agent's notes string ("" for none) to the nullable
// pointer the artifact stores (nil renders as JSON null).
func optionalNotes(notes string) *string {
	if notes == "" {
		return nil
	}
	return &notes
}

// entryOccurredAt reads the raw entry's occurred_at, which yaml may have
// decoded as a time.Time or left as an RFC3339 string, falling back when
// absent or unparseable. It lets the People routine window on the entry's
// occurred_at without the router assuming the YAML decoder's representation.
func entryOccurredAt(fields map[string]any, fallback time.Time) time.Time {
	switch v := fields["occurred_at"].(type) {
	case time.Time:
		return v
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	return fallback
}

// The downstream Structuring degrade acks (error-states.md §S-5/§St-3). They
// keep the raw entry the honest subject: capture held, the rest deferred.
func ackPeopleTrouble(rawID string) string {
	return fmt.Sprintf("Saved as `%s` — I had trouble organizing the people in this one; I'll catch up next time.", rawID)
}

func ackProcessedTrouble(rawID string) string {
	return fmt.Sprintf("Saved as `%s` — I'll catch up on the rest later.", rawID)
}
