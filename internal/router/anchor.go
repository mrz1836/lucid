package router

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// ErrAnchorRejected marks a deterministic validation rejection of `anchor add`
// — an empty label or an unparseable YYYY-MM-DD date. It wraps the fixed,
// model-free reason so the CLI can print the copy on stderr and map it to a
// non-zero exit; a script never reads a rejected record as a success. The
// rejection happens before any disk write, so a bad input never mutates the
// Ledger.
var ErrAnchorRejected = errors.New("anchor rejected")

// The state rejections the sunset and rename verbs raise. They are distinct
// from [ErrAnchorRejected], which gates `anchor add`'s *input* (a blank label,
// an unreadable date): these three are about the state of the store — which
// anchor a bare label means, and whether that anchor can still be acted on.
// Two concerns, two types, so neither verb inherits the other's copy.
var (
	// ErrAnchorUnknownLabel — no anchor holds the label.
	ErrAnchorUnknownLabel = errors.New("anchor label not recorded")
	// ErrAnchorAlreadySunset — the label's anchor is already retired.
	ErrAnchorAlreadySunset = errors.New("anchor already sunset")
	// ErrAnchorLabelTaken — an active anchor already holds the target label.
	ErrAnchorLabelTaken = errors.New("anchor label taken")
)

// AnchorRejectedError is a deterministic sunset/rename rejection: a fixed,
// model-free Reason that is already a complete user-facing sentence, and
// nothing appended. Cause classifies it, so a caller matches the *class* with
// errors.Is while the CLI prints Reason verbatim — the wording can change
// without breaking the contract. It mirrors [DayRejectedError], the strict
// tier's other refusal type.
//
// The Reason never repeats the sentinel's text: a user must read one clean
// sentence, not `anchor label not recorded: no anchor named …`.
type AnchorRejectedError struct {
	Reason string
	Cause  error
}

// Error returns the fixed reason, a complete user-facing sentence.
func (e *AnchorRejectedError) Error() string { return e.Reason }

// Unwrap exposes the classifying sentinel to errors.Is.
func (e *AnchorRejectedError) Unwrap() error { return e.Cause }

// rejectAnchor builds an [AnchorRejectedError] from a sentinel and a formatted
// reason.
func rejectAnchor(cause error, format string, args ...any) error {
	return &AnchorRejectedError{Reason: fmt.Sprintf(format, args...), Cause: cause}
}

// anchorStateRejection explains why a bare label named no *active* anchor: it
// was already retired, or it was never recorded at all. Both the sunset and
// the rename verb resolve a label the same way, so they reject it the same way
// — the copy lives here once rather than being re-drafted per verb.
//
// The unrecorded case names the labels that do exist, because there is no
// `anchor list` verb: the remedy has to travel with the error. An empty store
// says so instead of trailing off after a list with nothing in it.
func anchorStateRejection(latest []engine.Anchor, label string) error {
	if prior, ok := engine.LatestSunsetFor(latest, label); ok {
		return rejectAnchor(ErrAnchorAlreadySunset,
			"%q was already sunset on %s — add it again to start a new milestone under that name; nothing was saved.",
			label, engine.SunsetDate(prior),
		)
	}
	recorded := engine.AnchorLabels(latest)
	if len(recorded) == 0 {
		return rejectAnchor(ErrAnchorUnknownLabel, "no anchor named %q — no anchors are recorded yet", label)
	}
	return rejectAnchor(ErrAnchorUnknownLabel,
		"no anchor named %q — recorded anchors: %s", label, strings.Join(recorded, ", "),
	)
}

// AnchorAddRequest is one `lucid anchor add` intent: record a labeled civil-date
// milestone the days-since metric counts from (engine-module.md §anchors.json).
// The free-text annotation is optional. Now is the record time — the wall clock
// when zero — and stamps RecordedAt, which resolves latest-wins per identity at
// read.
type AnchorAddRequest struct {
	Label string
	Date  string
	Note  string
	Now   time.Time
}

// AnchorAddResult reports the recorded anchor and the inventory-only ack.
// Recording a milestone is never scored, so the ack carries no evaluative
// language (engine §0) — just the label and the date that were written.
type AnchorAddResult struct {
	Anchor engine.Anchor
	Ack    string
}

// AnchorAdd executes `lucid anchor add <label> <date> [note]` (engine-module.md
// §Commands): validate the label/date deterministically, then append one
// milestone to the append-only anchors store. The store is never rewritten in
// place, so a correction (a mistyped date) and a reset (the count restarts) are
// the same operation — a new dated append; [engine.LatestAnchors] folds the
// history to the effective anchor per identity at read. No model is reachable
// from this write path (Sanctuary): validation is pure and the record is
// verbatim.
//
// Which identity the append lands on is the whole of the identity model, and
// it is decided here. Recording a label an *active* anchor already holds
// targets that anchor — the append carries its id, so the correction/reset
// grammar above is unchanged. Recording a label no active anchor holds mints a
// new id: a first use, or a name reused after a retirement, which is a new
// milestone rather than a resumed one, and the ack says so.
func (r *Router) AnchorAdd(req AnchorAddRequest) (AnchorAddResult, error) {
	now := whenOr(req.Now)

	// Validate before touching disk so a bad input is rejected without a
	// partial write. The reason is fixed copy (no clock, no model).
	if err := engine.ValidateAnchor(req.Label, req.Date); err != nil {
		return AnchorAddResult{}, anchorRejection(req.Label, req.Date)
	}

	// Scaffold before reading: a never-scaffolded tree has no anchors.json.
	if err := r.prepareEngine(); err != nil {
		return AnchorAddResult{}, err
	}
	log, err := r.store.ReadAnchors()
	if err != nil {
		return AnchorAddResult{}, err
	}
	latest := engine.LatestAnchors(log)

	anchor := engine.Anchor{
		Label:      req.Label,
		Date:       req.Date,
		Note:       req.Note,
		RecordedAt: now.Format(time.RFC3339),
	}

	ack := anchorAck(anchor)
	if target, ok := engine.FindActiveAnchor(latest, req.Label); ok {
		// Copied verbatim, empty included: a correction to a record written
		// before ids existed stays id-less so it folds under that record's
		// synthetic identity. Minting an id here would leave the original
		// folding as its own still-active anchor and fork the surface.
		anchor.ID = target.ID
	} else {
		// The whole history feeds the mint, not the fold, so a slot held by a
		// retired anchor is never reissued.
		anchor.ID = engine.NextAnchorID(log, now)
		if prior, retired := engine.LatestSunsetFor(latest, req.Label); retired {
			ack = anchorReaddAck(anchor, engine.SunsetDate(prior))
		}
	}

	if err := r.store.AppendAnchor(anchor); err != nil {
		return AnchorAddResult{}, fmt.Errorf("could not record the anchor; nothing was saved: %w", err)
	}

	return AnchorAddResult{Anchor: anchor, Ack: ack}, nil
}

// anchorRejection composes the fixed rejection copy, distinguishing the two
// failure reasons [engine.ValidateAnchor] gates on so the message is actionable.
// It always wraps [ErrAnchorRejected] so the CLI can classify it.
func anchorRejection(label, date string) error {
	if strings.TrimSpace(label) == "" {
		return fmt.Errorf("%w: give a non-empty label, e.g. `anchor add sobriety 2026-01-01`", ErrAnchorRejected)
	}
	return fmt.Errorf("%w: date %q must be a YYYY-MM-DD date, e.g. 2026-01-01", ErrAnchorRejected, date)
}

// anchorAck builds the inventory ack for a recorded anchor: the label and the
// date that were written, nothing evaluative.
func anchorAck(a engine.Anchor) string {
	return fmt.Sprintf("Anchor recorded: %s — %s.", a.Label, a.Date)
}

// anchorReaddAck builds the ack for a label whose only prior holder was
// retired. It names the earlier retirement so the reuse is never a surprise,
// and it says *added* rather than resumed: the store minted a new identity
// here, so a new milestone is starting under a name that was freed — the two
// runs stay distinct in the ledger and on both projected surfaces.
func anchorReaddAck(a engine.Anchor, sunsetOn string) string {
	return fmt.Sprintf("Anchor added: %s — %s (a previous %s was sunset %s).", a.Label, a.Date, a.Label, sunsetOn)
}

// AnchorSunsetRequest is one `lucid anchor sunset` intent: retire the milestone
// a label names. The reason is optional free text. Now is the record time — the
// wall clock when zero — and stamps the retirement.
type AnchorSunsetRequest struct {
	Label  string
	Reason string
	Now    time.Time
}

// AnchorSunsetResult reports the appended sunset record and the inventory-only
// ack. Retiring a milestone is never scored, so the ack carries no evaluative
// language (engine §0) — just what was retired and what became of it.
type AnchorSunsetResult struct {
	Anchor engine.Anchor
	Ack    string
}

// AnchorSunset executes `lucid anchor sunset <label> [reason]` (engine-module.md
// §Commands): retire a milestone from the counting surfaces by *appending*,
// never by editing. The appended record mirrors the anchor it retires and adds
// the sunset state, so the milestone stays in the append-only history and in
// `metrics --json` → anchors_sunset[] while dropping out of the metrics prose,
// anchors[], and the witness aging-anchor check.
//
// A bare label is unambiguous because at most one active anchor can hold one.
// A label no active anchor holds is rejected before any append — already
// retired, or never recorded — and the store is untouched (error-states.md
// §An-1, §An-2). The engine tree is still scaffolded, as it is for any engine
// verb; nothing is appended to the history.
func (r *Router) AnchorSunset(req AnchorSunsetRequest) (AnchorSunsetResult, error) {
	now := whenOr(req.Now)

	if err := r.prepareEngine(); err != nil {
		return AnchorSunsetResult{}, err
	}
	log, err := r.store.ReadAnchors()
	if err != nil {
		return AnchorSunsetResult{}, err
	}
	latest := engine.LatestAnchors(log)

	target, ok := engine.FindActiveAnchor(latest, req.Label)
	if !ok {
		return AnchorSunsetResult{}, anchorStateRejection(latest, req.Label)
	}

	record := engine.Anchor{
		// Copied verbatim, empty included. Retiring an anchor written before
		// ids existed means appending an id-less record, so it folds under the
		// same synthetic identity and actually retires it; stamping a fresh id
		// here would retire nothing and leave the original active.
		ID:    target.ID,
		Label: target.Label,
		// The milestone's own date, never today: the field means the same
		// thing in every record of this store, which is what keeps a sunset
		// readable without knowing which kind of record it is.
		Date:       target.Date,
		Note:       req.Reason,
		RecordedAt: now.Format(time.RFC3339),
		State:      engine.AnchorStateSunset,
	}
	if err := r.store.AppendAnchor(record); err != nil {
		return AnchorSunsetResult{}, fmt.Errorf("could not sunset the anchor; nothing was saved: %w", err)
	}

	return AnchorSunsetResult{Anchor: record, Ack: anchorSunsetAck(record)}, nil
}

// anchorSunsetAck builds the inventory ack for a retired anchor: what was
// retired, what it counted from, and the two facts a user needs next — it stops
// counting, and the record is kept.
func anchorSunsetAck(a engine.Anchor) string {
	return fmt.Sprintf(
		"Anchor sunset: %s — %s. It no longer counts on the metrics surface; the record is kept.", a.Label, a.Date,
	)
}

// AnchorRenameRequest is one `lucid anchor rename` intent: change the display
// name of the milestone a label names. There is no reason field — nothing is
// being retired, so there is nothing to explain. Now is the record time — the
// wall clock when zero — and stamps the append.
type AnchorRenameRequest struct {
	Label    string
	NewLabel string
	Now      time.Time
}

// AnchorRenameResult reports the surviving renamed record and the
// inventory-only ack. Retired carries the retirement stub that adopting a
// pre-id anchor writes alongside it, and is nil on every other path: that one
// case is the only anchor write in the store that produces two records.
type AnchorRenameResult struct {
	Anchor  engine.Anchor
	Retired *engine.Anchor
	Ack     string
}

// AnchorRename executes `lucid anchor rename <label> <new-label>`
// (engine-module.md §Commands): give an active milestone a different display
// name. The label is a display name, not the identity, so on an anchor that
// carries an id this is one more append under that same id: its date, its
// annotation, and therefore its running day count all carry forward
// untouched. A rename costs the user nothing.
//
// Renaming an anchor recorded *before* ids existed adopts it into the identity
// model instead, because such a record folds under an identity derived from its
// label and the label is exactly what changes here. See renameAdoption for why
// that path writes two records and why they must land together.
//
// Every rejection is decided from the folded store before any append, and the
// store is untouched when one fires (error-states.md §An-1, §An-2, §An-3): a
// blank new name, renaming a label to itself, a label no active anchor holds
// (never recorded, or already retired), or a new name an *active* anchor
// already holds. A name held only by a retired anchor is free, exactly as it is
// for `anchor add`.
func (r *Router) AnchorRename(req AnchorRenameRequest) (AnchorRenameResult, error) {
	now := whenOr(req.Now)

	// The two pure guards run before any read, so a malformed request never
	// reaches the store.
	newLabel := strings.TrimSpace(req.NewLabel)
	if err := validateRename(req.Label, newLabel); err != nil {
		return AnchorRenameResult{}, err
	}

	// Scaffold before reading: a never-scaffolded tree has no anchors.json.
	if err := r.prepareEngine(); err != nil {
		return AnchorRenameResult{}, err
	}
	log, err := r.store.ReadAnchors()
	if err != nil {
		return AnchorRenameResult{}, err
	}
	latest := engine.LatestAnchors(log)

	target, err := resolveRenameTarget(latest, req.Label, newLabel)
	if err != nil {
		return AnchorRenameResult{}, err
	}

	// The id is copied verbatim, so the milestone keeps its identity and only
	// its display name moves. Date and note travel with it.
	renamed := engine.Anchor{
		ID:         target.ID,
		Label:      newLabel,
		Date:       target.Date,
		Note:       target.Note,
		RecordedAt: now.Format(time.RFC3339),
	}
	if target.ID == "" {
		return r.renameAdoption(log, target, renamed, now)
	}

	if err := r.store.AppendAnchor(renamed); err != nil {
		return AnchorRenameResult{}, renameWriteError(err)
	}
	return AnchorRenameResult{Anchor: renamed, Ack: anchorRenameAck(target.Label, renamed)}, nil
}

// validateRename gates the two things a rename can get wrong without consulting
// the store: a blank new name, and renaming a label to the name it already has.
// A blank name reuses the copy `anchor add` raises, so both verbs refuse an
// empty label identically.
func validateRename(label, newLabel string) error {
	if newLabel == "" {
		return anchorRejection(newLabel, "")
	}
	if strings.TrimSpace(label) == newLabel {
		return rejectAnchor(ErrAnchorLabelTaken, "%q is already its name; nothing was saved.", newLabel)
	}
	return nil
}

// resolveRenameTarget resolves the anchor a bare label names and confirms the
// new name is free, returning the fixed rejection copy when either fails.
//
// Only an *active* holder blocks the new name. A name held solely by a retired
// anchor is available — the retirement freed it — which is the same rule
// `anchor add` follows when a sunset label is recorded again.
func resolveRenameTarget(latest []engine.Anchor, label, newLabel string) (engine.Anchor, error) {
	target, ok := engine.FindActiveAnchor(latest, label)
	if !ok {
		return engine.Anchor{}, anchorStateRejection(latest, label)
	}
	if _, taken := engine.FindActiveAnchor(latest, newLabel); taken {
		return engine.Anchor{}, rejectAnchor(ErrAnchorLabelTaken,
			"an active anchor already uses %q — sunset or rename it first; nothing was saved.", newLabel,
		)
	}
	return target, nil
}

// renameAdoption renames an anchor recorded before ids existed by adopting it
// into the identity model.
//
// Such a record folds under an identity synthesized from its label, so renaming
// it in place would move it to a *different* synthetic identity and leave the
// original folding as its own still-active anchor — the same milestone under
// two live names. Adoption is the honest resolution: retire the synthetic
// identity with an id-less stub under the old name, and mint a real id for a
// record that carries the date and note forward under the new one. The stub is
// deliberately note-free; the original note travels on the surviving record.
//
// This is the only path in the anchors store that writes two records, and they
// must land together — a half-applied pair leaves both names active at once,
// the exact fork this identity model exists to prevent. AppendAnchors is one
// read and one write for precisely this reason; it must not be split into two
// appends. Every other anchor verb writes exactly one record, and that
// asymmetry is meant to stay visible rather than be generalized away.
func (r *Router) renameAdoption(
	log engine.AnchorLog, target, renamed engine.Anchor, now time.Time,
) (AnchorRenameResult, error) {
	stub := engine.Anchor{
		Label:      target.Label,
		Date:       target.Date,
		RecordedAt: renamed.RecordedAt,
		State:      engine.AnchorStateSunset,
	}
	renamed.ID = engine.NextAnchorID(log, now)

	// Stub first, adopted second, so the history reads in the order the two
	// facts happened: the old name retired, the milestone continued.
	if err := r.store.AppendAnchors(stub, renamed); err != nil {
		return AnchorRenameResult{}, renameWriteError(err)
	}
	return AnchorRenameResult{
		Anchor: renamed, Retired: &stub, Ack: anchorAdoptedRenameAck(target.Label, renamed),
	}, nil
}

// renameWriteError wraps a failed rename append with the nothing-was-saved
// clause, so a caller that could not write reads the same guarantee the
// rejection paths give.
func renameWriteError(err error) error {
	return fmt.Errorf("could not rename the anchor; nothing was saved: %w", err)
}

// anchorRenameAck builds the inventory ack for a renamed anchor: the name it
// had, the name it has, and the date it still counts from.
func anchorRenameAck(from string, a engine.Anchor) string {
	return fmt.Sprintf("Anchor renamed: %s → %s — %s.", from, a.Label, a.Date)
}

// anchorAdoptedRenameAck builds the ack for a rename that adopted a pre-id
// anchor. It names the retired stub, because that record is visible on the
// retired surface and a user who found it there without being told would read
// it as a bug rather than as the documented cost of the rename.
func anchorAdoptedRenameAck(from string, a engine.Anchor) string {
	return anchorRenameAck(from, a) + " The old name is retired and appears in anchors_sunset."
}
