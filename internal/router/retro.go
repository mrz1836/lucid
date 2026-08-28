package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/retro"
)

// retro.go is the user-facing retro parking-lot path (retro.md §3–§6): the R-NNN
// queue for anything parked to revisit at the weekly Retro or a Gate. `park`
// appends one item and returns the minted R-NNN plus a per-write receipt;
// `list`/`show` fold the stream into current items (a pure read); and
// `resolve`/`defer` append a transition that references an existing item without
// consuming a new R-NNN. Every write is deterministic and agent-free — no LLM in
// any path (architecture P9); the two ids are minted by the storage adapter under
// the single-writer discipline.

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

// ListRetroRequest selects the list view (retro.md §4). The default (both flags
// false) is the Sunday-walk view — open ∪ deferred. Resolved switches to the
// resolved audit trail alone; All widens the default to append the resolved items
// after the open/deferred section. Resolved takes precedence over All, so the
// most specific filter always wins.
type ListRetroRequest struct {
	All      bool
	Resolved bool
}

// ListRetroView is the `lucid retro list --json` payload (retro.md §4): the
// folded items in the selected view and their count. Items is never null so
// automation always reads an array; each item exposes its R-NNN id (so an agent
// can resolve/defer it deterministically) and every folded field.
type ListRetroView struct {
	Count int          `json:"count"`
	Items []retro.Item `json:"items"`
}

// ListRetroResult carries the machine view and the human-first lines the CLI
// shares through emit (ADR-0007).
type ListRetroResult struct {
	View  ListRetroView
	Lines []string
}

// ShowRetroResult is one `lucid retro show <R-NNN>` result: the single folded
// item (all fields) and its human-first lines. The view is the item itself so
// `--json` emits the folded shape directly.
type ShowRetroResult struct {
	View  retro.Item
	Lines []string
}

// TransitionRetroView is the `--json` payload a resolve or defer returns
// (retro.md §5): the affected R-NNN, the transition's own fresh receipt, and the
// item as it folds after the transition — so the ack's two identities are
// machine-readable, not scraped from prose.
type TransitionRetroView struct {
	ID      string     `json:"id"`
	EventID string     `json:"event_id"`
	Item    retro.Item `json:"item"`
}

// TransitionRetroResult reports a resolve/defer's structured view and its
// acknowledgment — "Resolved <R-NNN> (receipt <event_id>)." / "Deferred …" — the
// provenance-over-magic ack that names both the item audited and the write that
// recorded it (retro.md §5).
type TransitionRetroResult struct {
	View TransitionRetroView
	Ack  string
}

// ResolveRetroRequest is one `lucid retro resolve <R-NNN> <resolution>` turn: the
// affected item id, the verbatim record of what we did, and now (injected so the
// resolved-date and receipt are deterministic in tests).
type ResolveRetroRequest struct {
	ID         string
	Resolution string
	Now        time.Time
}

// DeferRetroRequest is one `lucid retro defer <R-NNN> <reason>` turn: the
// affected item id, the verbatim one-line reason it is set aside, and now.
type DeferRetroRequest struct {
	ID     string
	Reason string
	Now    time.Time
}

// ListRetro reads the retro pool, folds it, and returns the selected view
// (retro.md §4). It writes nothing. The storage read already returns items in
// ascending R-NNN order; this only partitions by status, keeping the open ∪
// deferred section (ascending) ahead of the resolved section (ascending) for the
// `--all` audit view.
func (r *Router) ListRetro(req ListRetroRequest) (ListRetroResult, error) {
	items, err := r.store.ReadRetro()
	if err != nil {
		return ListRetroResult{}, fmt.Errorf("could not read the retro items: %w", err)
	}
	view := selectRetroView(items, req)
	if view == nil {
		view = []retro.Item{}
	}
	return ListRetroResult{
		View:  ListRetroView{Count: len(view), Items: view},
		Lines: retroListLines(view, req),
	}, nil
}

// ShowRetro returns a single folded item by its R-NNN id with every field
// (retro.md §4). An empty or unknown id is a clean error and nothing is read as a
// result; the whole stream is folded fresh, so the status/resolution/defer-reason
// reflect the latest transition.
func (r *Router) ShowRetro(id string) (ShowRetroResult, error) {
	if strings.TrimSpace(id) == "" {
		return ShowRetroResult{}, fmt.Errorf("a retro id is required to show; nothing was read")
	}
	item, found, err := r.store.ReadRetroByID(id)
	if err != nil {
		return ShowRetroResult{}, fmt.Errorf("could not read retro item %s: %w", id, err)
	}
	if !found {
		return ShowRetroResult{}, fmt.Errorf("no retro item %s — check the id with `lucid retro list`", id)
	}
	return ShowRetroResult{View: item, Lines: retroShowLines(item)}, nil
}

// ResolveRetro appends one resolve transition that references an existing item
// (retro.md §5). It never rewrites or deletes the original park line — the item
// folds to resolved and leaves the default list, staying readable under
// `list --resolved` / `list --all` as the audit trail. Both the id and a
// non-empty resolution are required; an unknown id is a clean error and nothing
// is appended. The transition mints its own receipt and never advances the R-NNN
// counter. `resolved-date` is the transition's logical day (today); `recorded_at`
// is the real write time.
func (r *Router) ResolveRetro(req ResolveRetroRequest) (TransitionRetroResult, error) {
	if strings.TrimSpace(req.ID) == "" {
		return TransitionRetroResult{}, fmt.Errorf("a retro id is required to resolve; nothing was changed")
	}
	if strings.TrimSpace(req.Resolution) == "" {
		return TransitionRetroResult{}, fmt.Errorf("a resolution is required to resolve %s; nothing was changed", req.ID)
	}
	if err := r.prepareRetro(); err != nil {
		return TransitionRetroResult{}, err
	}
	now := whenOr(req.Now)
	day := retro.LogicalDay(now)
	ev := retro.Retro{
		Schema:       retro.Schema,
		EventType:    retro.EventResolve,
		ID:           req.ID,
		Status:       retro.StatusResolved,
		Source:       retro.SourceRetro,
		Resolution:   req.Resolution,
		ResolvedDate: day,
		RecordedAt:   now.Format(time.RFC3339),
		LogicalDate:  day,
	}
	return r.transitionRetro(ev, "Resolved")
}

// DeferRetro appends one defer transition that references an existing item
// (retro.md §5) — the item folds to the first-class `deferred` status with its
// reason and stays visible in the default list, so the Sunday walk never loses
// it. Both the id and a non-empty reason are required; an unknown id is a clean
// error and nothing is appended. Like resolve it mints its own receipt and never
// advances the R-NNN counter.
func (r *Router) DeferRetro(req DeferRetroRequest) (TransitionRetroResult, error) {
	if strings.TrimSpace(req.ID) == "" {
		return TransitionRetroResult{}, fmt.Errorf("a retro id is required to defer; nothing was changed")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return TransitionRetroResult{}, fmt.Errorf("a reason is required to defer %s; nothing was changed", req.ID)
	}
	if err := r.prepareRetro(); err != nil {
		return TransitionRetroResult{}, err
	}
	now := whenOr(req.Now)
	ev := retro.Retro{
		Schema:      retro.Schema,
		EventType:   retro.EventDefer,
		ID:          req.ID,
		Status:      retro.StatusDeferred,
		Source:      retro.SourceRetro,
		DeferReason: req.Reason,
		RecordedAt:  now.Format(time.RFC3339),
		LogicalDate: retro.LogicalDay(now),
	}
	return r.transitionRetro(ev, "Deferred")
}

// transitionRetro appends one prepared resolve/defer event, then reads the item
// back so the result carries its post-transition folded state. verb is the
// capitalized ack lead ("Resolved"/"Deferred"). It centralizes the shared
// append→read-back→ack tail so the two transitions differ only in the event they
// hand it.
func (r *Router) transitionRetro(ev retro.Retro, verb string) (TransitionRetroResult, error) {
	stored, err := r.store.AppendRetroTransition(ev)
	if err != nil {
		return TransitionRetroResult{}, retroTransitionError(ev.ID, err)
	}
	item, _, err := r.store.ReadRetroByID(stored.ID)
	if err != nil {
		return TransitionRetroResult{}, fmt.Errorf("could not read retro item %s after the update: %w", stored.ID, err)
	}
	return TransitionRetroResult{
		View: TransitionRetroView{ID: stored.ID, EventID: stored.EventID, Item: item},
		Ack:  fmt.Sprintf("%s %s (receipt %s).", verb, stored.ID, stored.EventID),
	}, nil
}

// retroTransitionError turns the storage never-conjure guard into clean,
// user-facing prose (a resolve/defer of an id no park ever minted), leaving any
// other fault as a wrapped runtime error.
func retroTransitionError(id string, err error) error {
	if strings.Contains(err.Error(), "to transition") {
		return fmt.Errorf("no retro item %s to update — check the id with `lucid retro list`", id)
	}
	return fmt.Errorf("could not update retro item %s; nothing was changed: %w", id, err)
}

// ImportRetroRequest is the one-time migration payload (retro.md §6): the folded
// items to reproduce — each with its explicit R-NNN, original parked-date,
// verbatim source and item text, final status, and (where they apply) a
// defer-reason or a resolution + resolved-date — plus now (injected so the
// generated events' recorded_at is deterministic in tests). Items use the folded
// [retro.Item] shape, so a file produced by `retro list --all --json` round-trips
// straight back through import.
type ImportRetroRequest struct {
	Items []retro.Item
	Now   time.Time
}

// ImportRetroView is the `lucid retro import --json` payload: how many parked
// items were reproduced and their ids in supplied order — the record of exactly
// what the single migration pass landed.
type ImportRetroView struct {
	Imported int      `json:"imported"`
	IDs      []string `json:"ids"`
}

// ImportRetroResult reports the completed import's structured view and its
// acknowledgment.
type ImportRetroResult struct {
	View ImportRetroView
	Ack  string
}

// ImportRetro reproduces a pre-existing queue in the Ledger exactly (retro.md
// §6) — the hidden, one-time cutover the everyday `park` cannot do (it mints a
// fresh id and files under today). For each item it appends a `park` carrying the
// SUPPLIED R-NNN, the backdated parked-date, and the verbatim source + item text,
// then — if the item's final status is resolved or deferred — the matching
// transition (a resolve with the original resolution + resolved-date, or a defer
// with the original reason) so the folded status and reason reproduce the source
// exactly. Every generated event gets its own fresh receipt; only the supplied
// park ids seed the R-NNN minter, so the next everyday `park` continues at
// imported-max+1 regardless of how many transition events were written (§1).
//
// It validates every item BEFORE writing anything, so a malformed item aborts the
// whole import rather than leaving a partial write behind. It is deterministic and
// agent-free — no LLM in the path (architecture P9). Import is NOT idempotent
// (re-running duplicates ids); a run that fails mid-write is recovered by
// restoring the pre-migration `lucid backup` snapshot and replaying, never by
// re-running on top of a partial store (retro.md §6).
func (r *Router) ImportRetro(req ImportRetroRequest) (ImportRetroResult, error) {
	if len(req.Items) == 0 {
		return ImportRetroResult{}, fmt.Errorf("no retro items to import; nothing was written")
	}
	for i, it := range req.Items {
		if err := validateImportItem(it); err != nil {
			return ImportRetroResult{}, fmt.Errorf("retro import item %d (%q): %w", i+1, it.ID, err)
		}
	}
	if err := r.prepareRetro(); err != nil {
		return ImportRetroResult{}, err
	}
	now := whenOr(req.Now)
	ids := make([]string, 0, len(req.Items))
	for _, it := range req.Items {
		if err := r.importOne(now, it); err != nil {
			return ImportRetroResult{}, fmt.Errorf("could not import retro item %s; the store may be partially migrated — restore the pre-migration backup before replaying: %w", it.ID, err)
		}
		ids = append(ids, it.ID)
	}
	return ImportRetroResult{
		View: ImportRetroView{Imported: len(ids), IDs: ids},
		Ack:  fmt.Sprintf("Imported %d parked item(s): %s.", len(ids), strings.Join(ids, ", ")),
	}, nil
}

// importOne appends one item's park (supplied id, backdated parked-date, verbatim
// source and text) and, when its final status calls for it, the matching resolve
// or defer transition — reproducing the folded status and reason from the source.
// A resolve is attributed to its resolved-date (the fold reads resolved-date from
// the resolve event's logical_date); a defer, which carries no date of its own in
// the folded view, is attributed to the parked-date. An empty source defaults to
// the `migration` provenance so an imported item is always distinguishable from a
// hand-typed park.
func (r *Router) importOne(now time.Time, it retro.Item) error {
	source := it.Source
	if source == "" {
		source = retro.SourceMigration
	}
	if _, err := r.store.AppendRetroEvent(retro.Retro{
		Schema:      retro.Schema,
		EventType:   retro.EventPark,
		ID:          it.ID, // supplied → the store keeps it, never auto-mints
		Item:        it.Item,
		Status:      retro.StatusOpen,
		Source:      source,
		RecordedAt:  now.Format(time.RFC3339),
		LogicalDate: it.ParkedDate,
	}); err != nil {
		return err
	}
	switch it.Status {
	case retro.StatusResolved:
		_, err := r.store.AppendRetroEvent(retro.Retro{
			Schema:       retro.Schema,
			EventType:    retro.EventResolve,
			ID:           it.ID,
			Status:       retro.StatusResolved,
			Source:       retro.SourceRetro,
			Resolution:   it.Resolution,
			ResolvedDate: it.ResolvedDate,
			RecordedAt:   now.Format(time.RFC3339),
			LogicalDate:  it.ResolvedDate,
		})
		return err
	case retro.StatusDeferred:
		_, err := r.store.AppendRetroEvent(retro.Retro{
			Schema:      retro.Schema,
			EventType:   retro.EventDefer,
			ID:          it.ID,
			Status:      retro.StatusDeferred,
			Source:      retro.SourceRetro,
			DeferReason: it.DeferReason,
			RecordedAt:  now.Format(time.RFC3339),
			LogicalDate: it.ParkedDate,
		})
		return err
	}
	return nil
}

// validateImportItem rejects an item the import cannot reproduce faithfully before
// any write, so a bad row aborts the whole pass. It guards the fields the writer
// cannot invent: a well-formed R-NNN, non-empty item text, a valid parked-date,
// and — by final status — the transition fields (a resolved item needs a
// resolved-date and a resolution; a deferred item needs a reason). An empty status
// is treated as open (the default), matching the folded shape.
func validateImportItem(it retro.Item) error {
	if _, ok := retro.ParseSeq(it.ID); !ok {
		return fmt.Errorf("id must be a well-formed R-NNN")
	}
	if strings.TrimSpace(it.Item) == "" {
		return fmt.Errorf("item text is required")
	}
	if !validRetroDate(it.ParkedDate) {
		return fmt.Errorf("parked_date must be YYYY-MM-DD, got %q", it.ParkedDate)
	}
	switch it.Status {
	case "", retro.StatusOpen:
		return nil
	case retro.StatusResolved:
		if !validRetroDate(it.ResolvedDate) {
			return fmt.Errorf("a resolved item needs a resolved_date (YYYY-MM-DD), got %q", it.ResolvedDate)
		}
		if strings.TrimSpace(it.Resolution) == "" {
			return fmt.Errorf("a resolved item needs a resolution")
		}
		return nil
	case retro.StatusDeferred:
		if strings.TrimSpace(it.DeferReason) == "" {
			return fmt.Errorf("a deferred item needs a defer_reason")
		}
		return nil
	default:
		return fmt.Errorf("status must be %q, %q, or %q, got %q", retro.StatusOpen, retro.StatusResolved, retro.StatusDeferred, it.Status)
	}
}

// validRetroDate reports whether s is a civil YYYY-MM-DD date, the logical-date
// form every retro event carries.
func validRetroDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// selectRetroView partitions the ascending-R-NNN pool into the requested view
// (retro.md §4): Resolved → the resolved items alone; All → open ∪ deferred then
// resolved; default → open ∪ deferred. Resolved wins over All. Each partition
// preserves the ascending order the storage read established.
func selectRetroView(items []retro.Item, req ListRetroRequest) []retro.Item {
	if req.Resolved {
		return retroWithStatus(items, retro.StatusResolved)
	}
	openDeferred := make([]retro.Item, 0, len(items))
	for _, it := range items {
		if it.Status == retro.StatusOpen || it.Status == retro.StatusDeferred {
			openDeferred = append(openDeferred, it)
		}
	}
	if !req.All {
		return openDeferred
	}
	return append(openDeferred, retroWithStatus(items, retro.StatusResolved)...)
}

// retroWithStatus returns the items with the given status, preserving order.
func retroWithStatus(items []retro.Item, status string) []retro.Item {
	out := make([]retro.Item, 0, len(items))
	for _, it := range items {
		if it.Status == status {
			out = append(out, it)
		}
	}
	return out
}

// retroListLines renders the human-first list: a count header then one compact
// line per item, `<id>  <status>  <summary>`, with the resolution or defer-reason
// appended so the Sunday walk reads the whole queue at a glance. A multi-line item
// body is summarized to its first non-empty line so a verbatim multi-paragraph
// park never breaks the layout. An empty view prints a hint keyed to the filter,
// so the read is never a bare blank.
func retroListLines(items []retro.Item, req ListRetroRequest) []string {
	if len(items) == 0 {
		if req.Resolved {
			return []string{"No resolved items yet."}
		}
		return []string{"No parked items yet — park one with `lucid retro park <item>`."}
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, fmt.Sprintf("%d parked item(s):", len(items)))
	for _, it := range items {
		line := fmt.Sprintf("  %s  %-8s  %s", it.ID, it.Status, retroSummary(it.Item))
		switch it.Status {
		case retro.StatusResolved:
			line += fmt.Sprintf("  (resolved %s: %s)", it.ResolvedDate, retroSummary(it.Resolution))
		case retro.StatusDeferred:
			if it.DeferReason != "" {
				line += fmt.Sprintf("  (deferred: %s)", retroSummary(it.DeferReason))
			}
		}
		lines = append(lines, line)
	}
	return lines
}

// retroShowLines renders every folded field of a single item (retro.md §4), the
// verbatim item body as an indented block so a multi-paragraph park is shown in
// full. Resolution/resolved-date appear only for a resolved item, defer-reason
// only for a deferred one, so the record reads as exactly what it is.
func retroShowLines(it retro.Item) []string {
	lines := []string{
		it.ID,
		fmt.Sprintf("  Parked:  %s (source: %s)", it.ParkedDate, it.Source),
		fmt.Sprintf("  Status:  %s", it.Status),
		"  Item:",
	}
	for _, body := range strings.Split(it.Item, "\n") {
		lines = append(lines, "    "+body)
	}
	if it.Status == retro.StatusResolved {
		lines = append(lines, fmt.Sprintf("  Resolved: %s", it.ResolvedDate))
		lines = append(lines, fmt.Sprintf("  Resolution: %s", it.Resolution))
	}
	if it.Status == retro.StatusDeferred && it.DeferReason != "" {
		lines = append(lines, fmt.Sprintf("  Defer reason: %s", it.DeferReason))
	}
	return lines
}

// retroSummary collapses a verbatim body to its first non-empty line, so a
// compact list/inline rendering never spills a multi-paragraph park across the
// layout. The full text stays intact in the store and in `show`.
func retroSummary(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return strings.TrimSpace(s)
}
