package router

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// ErrSelfRejected marks a deterministic validation rejection of a self-fact
// write — a key outside the namespace taxonomy, a malformed key, an empty or
// oversized value. It wraps the fixed, model-free reason so the CLI can print
// the copy on stderr and map it to a non-zero exit; a script never reads a
// rejected record as a success. The rejection happens before any disk write, so
// a bad input never mutates the Ledger.
var ErrSelfRejected = errors.New("self fact rejected")

// The state rejections the retire and move verbs raise. They are distinct from
// [ErrSelfRejected], which gates a verb's *input* (a bad namespace, an empty
// value): these three are about the state of the store — which fact a bare key
// means, and whether that fact can still be acted on. Two concerns, two types,
// so neither verb inherits the other's copy.
var (
	// ErrSelfUnknownKey — no active fact holds the key.
	ErrSelfUnknownKey = errors.New("self fact key not recorded")
	// ErrSelfAlreadyRetired — the key's fact is already retired.
	ErrSelfAlreadyRetired = errors.New("self fact already retired")
	// ErrSelfKeyTaken — an active fact already holds the target key.
	ErrSelfKeyTaken = errors.New("self fact key taken")
)

// SelfRejectedError is a deterministic self-fact rejection: a fixed, model-free
// Reason that is already a complete user-facing sentence, and nothing appended.
// Cause classifies it, so a caller matches the *class* with errors.Is while the
// CLI prints Reason verbatim — the wording can change without breaking the
// contract. It mirrors [AnchorRejectedError], the anchors store's refusal type.
//
// The Reason never repeats the sentinel's text: a user must read one clean
// sentence, not `self fact key not recorded: no fact recorded under …`. Every
// self-fact rejection is built this way, input ones included, so the store has
// exactly one refusal shape rather than one per concern.
type SelfRejectedError struct {
	Reason string
	Cause  error
}

// Error returns the fixed reason, a complete user-facing sentence.
func (e *SelfRejectedError) Error() string { return e.Reason }

// Unwrap exposes the classifying sentinel to errors.Is.
func (e *SelfRejectedError) Unwrap() error { return e.Cause }

// rejectSelf builds a [SelfRejectedError] from a sentinel and a formatted
// reason.
func rejectSelf(cause error, format string, args ...any) error {
	return &SelfRejectedError{Reason: fmt.Sprintf(format, args...), Cause: cause}
}

// enginePrefix is the package prefix [engine.ValidateSelfFact] stamps on its
// errors. It is stripped when that error becomes user-facing copy: the reason a
// user reads names the fact, not the package that refused it.
const enginePrefix = "engine: "

// selfInputRejection turns a pure validation failure into the fixed rejection
// the CLI prints.
//
// It reuses the engine's wording rather than re-drafting it, deliberately: the
// taxonomy — the accepted namespaces and the misc. landing spot — is stated in
// exactly one place, so the copy a user reads on a rejected `set` and the copy
// they read on a rejected `move` can never drift apart, and neither can drift
// from what the validator actually accepts.
func selfInputRejection(err error) error {
	return rejectSelf(ErrSelfRejected, "%s; nothing was saved.", strings.TrimPrefix(err.Error(), enginePrefix))
}

// selfStateRejection explains why a bare key named no *active* fact: it was
// already retired, or it was never recorded at all. Both the retire and the
// move verb resolve a key the same way, so they reject it the same way — the
// copy lives here once rather than being re-drafted per verb.
//
// The unrecorded case names the keys that do exist, because there is no
// `self list` verb: the remedy has to travel with the error. An empty store
// says so instead of trailing off after a list with nothing in it.
func selfStateRejection(latest []engine.SelfFact, key string) error {
	if prior, ok := engine.LatestRetiredSelfFactFor(latest, key); ok {
		return rejectSelf(ErrSelfAlreadyRetired,
			"%q was already retired on %s — set it again to start a new fact under that key; nothing was saved.",
			key, engine.RetiredDate(prior),
		)
	}
	recorded := engine.SelfFactKeys(latest)
	if len(recorded) == 0 {
		return rejectSelf(ErrSelfUnknownKey, "no fact recorded under %q — no facts are recorded yet", key)
	}
	return rejectSelf(ErrSelfUnknownKey,
		"no fact recorded under %q — recorded keys: %s", key, strings.Join(recorded, ", "),
	)
}

// SelfSetRequest is one `lucid self set` intent: record a durable, atemporal
// attribute of the subject (engine-module.md §self.json). Since is an optional
// origin — the only write surface in the CLI where a date is not required at
// all — and Note is an optional annotation. Now is the record time — the wall
// clock when zero — and stamps RecordedAt, which the append-only history keeps
// in full.
type SelfSetRequest struct {
	Key   string
	Value string
	Since string
	Note  string
	Now   time.Time
}

// SelfSetResult reports the recorded fact and the inventory-only ack. Recording
// an attribute is never scored, so the ack carries no evaluative language
// (engine §0) — just the key and the value that were written.
type SelfSetResult struct {
	Fact engine.SelfFact
	Ack  string
}

// SelfSet executes `lucid self set <key> <value…>` (engine-module.md
// §Commands): validate the key and value deterministically, then append one
// fact to the append-only self store. The store is never rewritten in place, so
// a correction (a mistyped value) and an update (the value genuinely changed)
// are the same operation — a new append under the same identity;
// [engine.LatestSelfFacts] folds the history to the effective profile at read.
// No model is reachable from this write path (Sanctuary): validation is pure
// and the record is verbatim.
//
// Which identity the append lands on is the whole of the identity model, and it
// is decided here. Recording a key an *active* fact already holds targets that
// fact — the append carries its id, so the fact keeps one history across every
// correction. Recording a key no active fact holds mints a new id: a first use,
// or a key reused after a retirement, which is a new fact rather than a resumed
// one, and the ack says so.
func (r *Router) SelfSet(req SelfSetRequest) (SelfSetResult, error) {
	now := whenOr(req.Now)

	// Validate before touching disk so a bad input is rejected without a
	// partial write. The reason is fixed copy (no clock, no model).
	if err := engine.ValidateSelfFact(req.Key, req.Value); err != nil {
		return SelfSetResult{}, selfInputRejection(err)
	}

	// Scaffold before reading: a never-scaffolded tree has no self.json.
	if err := r.prepareEngine(); err != nil {
		return SelfSetResult{}, err
	}
	log, err := r.store.ReadSelfFacts()
	if err != nil {
		return SelfSetResult{}, err
	}
	latest := engine.LatestSelfFacts(log)

	fact := engine.SelfFact{
		Key:        req.Key,
		Value:      req.Value,
		Since:      req.Since,
		Note:       req.Note,
		RecordedAt: now.Format(time.RFC3339),
	}

	ack := selfAck(fact)
	if target, ok := engine.FindActiveSelfFact(latest, req.Key); ok {
		fact.ID = target.ID
	} else {
		// The whole history feeds the mint, not the fold, so a slot held by a
		// retired fact is never reissued.
		fact.ID = engine.NextSelfFactID(log, now)
		if prior, retired := engine.LatestRetiredSelfFactFor(latest, req.Key); retired {
			ack = selfReaddAck(fact, engine.RetiredDate(prior))
		}
	}

	if err := r.store.AppendSelfFact(fact); err != nil {
		return SelfSetResult{}, fmt.Errorf("could not record the fact; nothing was saved: %w", err)
	}

	return SelfSetResult{Fact: fact, Ack: ack}, nil
}

// selfAck builds the inventory ack for a recorded fact: the key and the value
// that were written, nothing evaluative.
func selfAck(f engine.SelfFact) string {
	return fmt.Sprintf("Fact recorded: %s — %s.", f.Key, f.Value)
}

// selfReaddAck builds the ack for a key whose only prior holder was retired. It
// names the earlier retirement so the reuse is never a surprise: the store
// minted a new identity here, so a new fact is starting under a key that was
// freed, and the two stay distinct in the history.
func selfReaddAck(f engine.SelfFact, retiredOn string) string {
	return fmt.Sprintf("Fact recorded: %s — %s (a previous %s was retired %s).", f.Key, f.Value, f.Key, retiredOn)
}

// SelfRetireRequest is one `lucid self retire` intent: retire the fact a key
// names, because the attribute stopped being true. The reason is optional free
// text. Now is the record time — the wall clock when zero — and stamps the
// retirement.
type SelfRetireRequest struct {
	Key    string
	Reason string
	Now    time.Time
}

// SelfRetireResult reports the appended retirement record and the
// inventory-only ack. Retiring an attribute is never scored, so the ack carries
// no evaluative language (engine §0) — just what was retired and what became
// of it.
type SelfRetireResult struct {
	Fact engine.SelfFact
	Ack  string
}

// SelfRetire executes `lucid self retire <key> [reason…]` (engine-module.md
// §Commands): drop a fact from the profile by *appending*, never by editing.
// The appended record mirrors the fact it retires and adds the retired state,
// so the attribute stays in the append-only history and in `self --history`
// while dropping out of the rendered profile, the JSON profile, and the /ask
// grounding slice.
//
// A bare key is unambiguous because at most one active fact can hold one. A key
// no active fact holds is rejected before any append — already retired, or
// never recorded — and the store is untouched. The engine tree is still
// scaffolded, as it is for any engine write verb; nothing is appended.
func (r *Router) SelfRetire(req SelfRetireRequest) (SelfRetireResult, error) {
	now := whenOr(req.Now)

	if err := r.prepareEngine(); err != nil {
		return SelfRetireResult{}, err
	}
	log, err := r.store.ReadSelfFacts()
	if err != nil {
		return SelfRetireResult{}, err
	}
	latest := engine.LatestSelfFacts(log)

	target, ok := engine.FindActiveSelfFact(latest, req.Key)
	if !ok {
		return SelfRetireResult{}, selfStateRejection(latest, req.Key)
	}

	record := engine.SelfFact{
		ID:    target.ID,
		Key:   target.Key,
		Value: target.Value,
		// The fact's own origin, never today: the field means the same thing in
		// every record of this store, which is what keeps a retirement readable
		// without knowing which kind of record it is.
		Since:      target.Since,
		Note:       req.Reason,
		RecordedAt: now.Format(time.RFC3339),
		State:      engine.SelfStateRetired,
	}
	if err := r.store.AppendSelfFact(record); err != nil {
		return SelfRetireResult{}, fmt.Errorf("could not retire the fact; nothing was saved: %w", err)
	}

	return SelfRetireResult{Fact: record, Ack: selfRetireAck(record)}, nil
}

// selfRetireAck builds the inventory ack for a retired fact: what was retired,
// what it said, and the two facts a user needs next — it leaves the profile,
// and the record is kept.
func selfRetireAck(f engine.SelfFact) string {
	return fmt.Sprintf(
		"Fact retired: %s — %s. It no longer appears on the profile; the record is kept.", f.Key, f.Value,
	)
}

// SelfMoveRequest is one `lucid self move` intent: re-categorize a fact under a
// different key. There is no reason field — nothing is being retired, so there
// is nothing to explain. Now is the record time — the wall clock when zero —
// and stamps the append.
type SelfMoveRequest struct {
	Key    string
	NewKey string
	Now    time.Time
}

// SelfMoveResult reports the moved record and the inventory-only ack. There is
// no second record to report: unlike [AnchorRenameResult] there is no Retired
// field, because a move here is always exactly one append.
type SelfMoveResult struct {
	Fact engine.SelfFact
	Ack  string
}

// SelfMove executes `lucid self move <key> <new-key>` (engine-module.md
// §Commands): file an existing fact under a different key. The key is a mutable
// address, not the identity, so this is one more append under the same id: the
// value, the origin and the annotation all carry forward, and the fact's whole
// history — including every record written under the previous key — stays
// reachable by its id. Re-categorizing costs the user nothing, which is what
// makes the taxonomy repairable: no key-naming decision is a one-way door.
//
// Unlike [Router.AnchorRename] there is no adoption path and no second record.
// Anchor rename writes a pair only when it renames an anchor recorded before
// ids existed, because such a record folds under an identity derived from its
// label. This store is version 1 from birth and every record carries an id, so
// that case cannot arise here. Do not port the adoption path back by analogy —
// this verb appends exactly once, and a test asserts the log grows by one.
//
// Every rejection is decided from the folded store before any append, and the
// store is untouched when one fires: an invalid or blank new key, moving a key
// to itself, a key no active fact holds (never recorded, or already retired),
// or a new key an *active* fact already holds. A key held only by a retired
// fact is free, exactly as it is for `self set`.
func (r *Router) SelfMove(req SelfMoveRequest) (SelfMoveResult, error) {
	now := whenOr(req.Now)

	// The two pure guards run before any read, so a malformed request never
	// reaches the store.
	newKey := strings.TrimSpace(req.NewKey)
	if err := validateSelfMove(req.Key, newKey); err != nil {
		return SelfMoveResult{}, err
	}

	if err := r.prepareEngine(); err != nil {
		return SelfMoveResult{}, err
	}
	log, err := r.store.ReadSelfFacts()
	if err != nil {
		return SelfMoveResult{}, err
	}
	latest := engine.LatestSelfFacts(log)

	target, err := resolveSelfMoveTarget(latest, req.Key, newKey)
	if err != nil {
		return SelfMoveResult{}, err
	}

	// The id is copied verbatim, so the fact keeps its identity and only its
	// address moves. Value, origin and annotation travel with it.
	moved := engine.SelfFact{
		ID:         target.ID,
		Key:        newKey,
		Value:      target.Value,
		Since:      target.Since,
		Note:       target.Note,
		RecordedAt: now.Format(time.RFC3339),
	}
	if err := r.store.AppendSelfFact(moved); err != nil {
		return SelfMoveResult{}, fmt.Errorf("could not move the fact; nothing was saved: %w", err)
	}

	return SelfMoveResult{Fact: moved, Ack: selfMoveAck(target.Key, moved)}, nil
}

// validateSelfMove gates the two things a move can get wrong without consulting
// the store: a new key that is blank or outside the taxonomy, and moving a key
// to the address it already has. An unusable new key reuses the copy `self set`
// raises, so both verbs refuse an illegal key identically and the taxonomy has
// one wording rather than two.
func validateSelfMove(key, newKey string) error {
	if err := engine.ValidateSelfKey(newKey); err != nil {
		return selfInputRejection(err)
	}
	if strings.TrimSpace(key) == newKey {
		return rejectSelf(ErrSelfKeyTaken, "%q is already its key; nothing was saved.", newKey)
	}
	return nil
}

// resolveSelfMoveTarget resolves the fact a bare key names and confirms the new
// key is free, returning the fixed rejection copy when either fails.
//
// Only an *active* holder blocks the new key. A key held solely by a retired
// fact is available — the retirement freed it — which is the same rule
// `self set` follows when a retired key is recorded again.
func resolveSelfMoveTarget(latest []engine.SelfFact, key, newKey string) (engine.SelfFact, error) {
	target, ok := engine.FindActiveSelfFact(latest, key)
	if !ok {
		return engine.SelfFact{}, selfStateRejection(latest, key)
	}
	if _, taken := engine.FindActiveSelfFact(latest, newKey); taken {
		return engine.SelfFact{}, rejectSelf(ErrSelfKeyTaken,
			"an active fact already uses %q — retire or move it first; nothing was saved.", newKey,
		)
	}
	return target, nil
}

// selfMoveAck builds the inventory ack for a moved fact: the key it had, the
// key it has, and the value that carried forward untouched.
func selfMoveAck(from string, f engine.SelfFact) string {
	return fmt.Sprintf("Fact moved: %s → %s — %s.", from, f.Key, f.Value)
}

// The selection rule [Router.SelfProfile] applied, reported in
// [SelfProfileResult.Matched] so a caller can render a single fact differently
// from a group without re-deriving which rule fired.
const (
	// SelfMatchAll — an empty query selected the whole profile.
	SelfMatchAll = "all"
	// SelfMatchKey — the query was an active key and selected that one fact.
	SelfMatchKey = "key"
	// SelfMatchPrefix — the query selected a namespace or key prefix.
	SelfMatchPrefix = "prefix"
)

// SelfProfileRequest is one `lucid self [<key-or-prefix>]` intent. An empty
// Query reads the whole profile. History additionally returns the append log
// for whatever the query selected.
type SelfProfileRequest struct {
	Query   string
	History bool
}

// SelfProfileResult reports the folded profile the query selected. Facts holds
// only the facts that are still true, in namespace-priority order — that is the
// rendered profile. History is the append log behind the same selection,
// retirements and superseded values included, and is empty unless the request
// asked for it. Matched names the selection rule that fired.
type SelfProfileResult struct {
	Facts   []engine.SelfFact
	History []engine.SelfFact
	Matched string
}

// SelfProfile reads the self-facts profile (engine-module.md §self.json): fold
// the append-only history to one record per identity, select by the query, and
// return the active facts plus — on request — the append log behind them.
//
// It is read-only and deliberately does not scaffold. Every write verb on this
// store calls prepareEngine first because a never-scaffolded tree has no
// self.json; this one relies on [storage.Adapter.ReadSelfFacts] tolerating an
// absent file instead, and returns an empty profile. That is not a convenience:
// a read that scaffolds is a read that writes, and /ask grounds on this same
// tolerance while being contractually read-only. Do not add prepareEngine here.
func (r *Router) SelfProfile(req SelfProfileRequest) (SelfProfileResult, error) {
	log, err := r.store.ReadSelfFacts()
	if err != nil {
		return SelfProfileResult{}, err
	}
	latest := engine.LatestSelfFacts(log)

	selected, matched := selectSelfFacts(latest, req.Query)
	res := SelfProfileResult{Facts: engine.ActiveSelfFacts(selected), Matched: matched}
	if req.History {
		res.History = selfHistoryFor(log, selected)
	}
	return res, nil
}

// selectSelfFacts applies the read surface's selection rule to a folded set and
// reports which rule fired. The rule is deterministic and ordered:
//
//	empty query          → the whole profile
//	an *active* key      → that one fact
//	anything else        → a prefix filter
//
// An active key wins over the prefix reading so `self body.height` is the one
// fact rather than that fact plus everything nested beneath it. The prefix form
// matches the key itself or a dot-delimited descendant, never a bare string
// prefix: `body` selects `body.height` and must not select `bodyweight`, the
// same fail-closed instinct [PathAllowedForAgent] applies to path segments.
//
// Selection runs over the folded set, retired facts included, because it also
// scopes the history surface — a retirement is exactly the record a user asks
// --history for. The caller filters to the active facts for the profile.
func selectSelfFacts(latest []engine.SelfFact, query string) (selected []engine.SelfFact, matched string) {
	q := strings.TrimSpace(query)
	if q == "" {
		return latest, SelfMatchAll
	}
	if fact, ok := engine.FindActiveSelfFact(latest, q); ok {
		return []engine.SelfFact{fact}, SelfMatchKey
	}
	out := make([]engine.SelfFact, 0, len(latest))
	for _, f := range latest {
		if f.Key == q || strings.HasPrefix(f.Key, q+".") {
			out = append(out, f)
		}
	}
	return out, SelfMatchPrefix
}

// selfHistoryFor returns every record behind the selected facts, in append
// order. It is [engine.SelfFactHistory] generalized to a set of identities, and
// it resolves by id for the same reason: a moved fact's records were written
// under its previous key, and those records are still its history. Filtering
// the log by key instead would silently drop exactly what a move preserves.
//
// One pass over the log rather than one per id, so records belonging to
// different facts stay interleaved in the order they were actually written.
func selfHistoryFor(log engine.SelfLog, selected []engine.SelfFact) []engine.SelfFact {
	ids := make(map[string]bool, len(selected))
	for _, f := range selected {
		ids[f.ID] = true
	}
	out := make([]engine.SelfFact, 0, len(log.History))
	for _, f := range log.History {
		if ids[f.ID] {
			out = append(out, f)
		}
	}
	return out
}
