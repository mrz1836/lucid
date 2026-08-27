package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/retro"
)

// retro.go is the user-facing retro parking-lot path (retro.md §3–§6): the R-NNN
// queue for anything parked to revisit at the weekly Retro or a Gate. `park`
// appends one item and returns the minted R-NNN plus a per-write receipt; the
// read (`list`/`show`) and transition (`resolve`/`defer`) verbs land in later
// build stages. Every write is deterministic and agent-free — no LLM in any path
// (architecture P9); the two ids are minted by the storage adapter under the
// single-writer discipline.

// ParkRetroRequest is one `lucid retro park` turn (retro.md §3): the verbatim
// item text, an optional --source, obs-parity #tags, the strict-tier --day value,
// and now (injected so backdating and receipt ids are deterministic in tests). ID
// is set ONLY by the one-time import path to supply an explicit R-NNN; a normal
// park leaves it empty so the store auto-mints the next global id.
type ParkRetroRequest struct {
	Item   string
	Source string
	Tags   []string
	DayArg string
	ID     string
	Now    time.Time
}

// ParkRetroView is the `lucid retro park --json` payload (retro.md §3): the
// minted parked-item id, the per-write receipt, and the folded item — so the
// echo contract (§0) is machine-readable, not scraped from prose.
type ParkRetroView struct {
	ID      string     `json:"id"`
	EventID string     `json:"event_id"`
	Item    retro.Item `json:"item"`
}

// ParkRetroResult reports the appended park's structured view and its
// acknowledgment — "Parked as <R-NNN> (receipt <event_id>)", the
// provenance-over-magic ack that makes the echo contract structural: the id in
// the ack is the id that landed.
type ParkRetroResult struct {
	View ParkRetroView
	Ack  string
}

// ParkRetro appends one immutable parked item to the Ledger (retro.md §2, §3). It
// is deterministic and agent-free — no LLM in the path (architecture P9). An
// empty item writes nothing (a clean usage error), and a backdating `--day` runs
// on the strict tier: an unreadable token or a future day is a [DayRejectedError]
// and nothing is written (error-states.md §St-1), exactly as the observation
// micro-log's `--day` behaves. `recorded_at` is always the real write time; only
// `logical_date` (the parked date) follows `--day`. The item text and --source
// are stored verbatim; they are never synthesized. The returned view carries the
// minted R-NNN and the event receipt, the two identities the echo contract rests
// on (§1).
func (r *Router) ParkRetro(req ParkRetroRequest) (ParkRetroResult, error) {
	if strings.TrimSpace(req.Item) == "" {
		return ParkRetroResult{}, fmt.Errorf("a retro item needs some text; nothing was parked")
	}
	if err := r.prepareRetro(); err != nil {
		return ParkRetroResult{}, err
	}

	// Strict-tier `--day`: reuse the one shared capture grammar so the accepted
	// forms, the 04:00 rollover, and the future ceiling match every other dated
	// capture. A refusal is a DayRejectedError the CLI prints; nothing lands.
	when, err := resolveCaptureWhen(req.DayArg, whenOr(req.Now))
	if err != nil {
		return ParkRetroResult{}, err
	}

	ev := retro.Retro{
		Schema:      retro.Schema,
		EventType:   retro.EventPark,
		ID:          req.ID, // empty → store mints; supplied → import keeps it
		Item:        req.Item,
		Status:      retro.StatusOpen,
		Source:      req.Source, // empty → store defaults to SourceRetro
		RecordedAt:  whenOr(req.Now).Format(time.RFC3339),
		LogicalDate: when.LogicalDate,
		Tags:        req.Tags,
	}
	stored, err := r.store.AppendRetroEvent(ev)
	if err != nil {
		return ParkRetroResult{}, fmt.Errorf("could not park the item; nothing was parked: %w", err)
	}
	return ParkRetroResult{
		View: ParkRetroView{
			ID:      stored.ID,
			EventID: stored.EventID,
			Item:    foldedParkItem(stored),
		},
		Ack: fmt.Sprintf("Parked as %s (receipt %s).", stored.ID, stored.EventID),
	}, nil
}

// prepareRetro scaffolds the retro tree idempotently, wrapping any failure with
// the shared message the retro verbs report (mirrors [Router.prepareFocus]).
func (r *Router) prepareRetro() error {
	if err := r.store.ScaffoldRetro(); err != nil {
		return fmt.Errorf("could not prepare the retro tree: %w", err)
	}
	return nil
}

// foldedParkItem builds the folded item view a just-appended park represents: a
// freshly parked item is always open with no resolution or defer-reason yet.
func foldedParkItem(ev retro.Retro) retro.Item {
	return retro.Item{
		ID:         ev.ID,
		ParkedDate: ev.LogicalDate,
		Source:     ev.Source,
		Item:       ev.Item,
		Status:     retro.StatusOpen,
	}
}
