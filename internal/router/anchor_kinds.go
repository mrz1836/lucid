package router

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
)

// The rejection classes the kind and event verbs add (error-states.md
// §"Anchors"). Each is the Cause of an [AnchorRejectedError], so a caller
// matches the class with errors.Is while the CLI prints the fixed reason.
var (
	// ErrAnchorWrongKind — the event belongs to a different kind (An-4).
	ErrAnchorWrongKind = errors.New("anchor event of the wrong kind")
	// ErrAnchorNeedsID — the anchor was recorded before ids existed (An-5).
	ErrAnchorNeedsID = errors.New("anchor recorded before ids")
	// ErrAnchorBeforeStart — the event day falls before the anchor's start (An-12).
	ErrAnchorBeforeStart = errors.New("anchor event before the anchor's start")
	// ErrAnchorEventUnknown — no stored event holds the id a void names (An-8).
	ErrAnchorEventUnknown = errors.New("anchor event not recorded")
	// ErrAnchorEventNotVoidable — the void target is a kind event or a void (An-8).
	ErrAnchorEventNotVoidable = errors.New("anchor event not voidable")
	// ErrAnchorAlreadyVoided — the void target is already voided (An-8).
	ErrAnchorAlreadyVoided = errors.New("anchor event already voided")
	// ErrAnchorUnfinished — the anchor has a half-written pair (An-13).
	ErrAnchorUnfinished = errors.New("anchor has an unfinished write")
	// ErrAnchorConfirmSpan — a confirm --from falls outside the anchor's life (An-11).
	ErrAnchorConfirmSpan = errors.New("anchor confirm span outside the anchor's life")
)

// errPairUnfinished marks a two-file write that failed after its pair intent
// landed: something is on disk, so "nothing was saved" would be false, and the
// remedy is to retry the same command.
var errPairUnfinished = errors.New("the write is unfinished")

// The durable boundaries of a two-file write, in the order they happen: the
// pair intent, then the first and second domain halves. The fault seam
// (Router.pairFault) is consulted before each one.
const (
	pairStepIntent = "intent"
	pairStepFirst  = "first"
	pairStepSecond = "second"
)

// AnchorEventResult reports one kind or event verb's write: the stored event
// (its id is the receipt), the definition record appended beside it when the
// verb wrote both files (a compat re-date or a restoring record; nil
// otherwise), the anchor's current label, and the ack. Resumed is true when the
// call was a matching retry that finished an earlier unfinished write rather
// than starting a new one; the event is then the original, with its original
// receipt.
type AnchorEventResult struct {
	Label   string
	Event   engine.AnchorEvent
	Compat  *engine.Anchor
	Ack     string
	Resumed bool
}

// AnchorKindRequest is one `lucid anchor kind` intent: reclassify the active
// anchor a label names. Allowance and Window are zero when not given. Note is
// the optional reason recorded on the kind event. Now is the record time — the
// wall clock when zero.
type AnchorKindRequest struct {
	Label     string
	Kind      string
	Allowance int
	Window    int
	Note      string
	Now       time.Time
}

// AnchorSlipRequest is one `lucid anchor slip` intent. DayArg is the --day
// value in the shared grammar; empty is the current logical day.
type AnchorSlipRequest struct {
	Label  string
	DayArg string
	Note   string
	Now    time.Time
}

// AnchorUseRequest is one `lucid anchor use` intent. Intent is required and is
// planned or slip; the binary never guesses it.
type AnchorUseRequest struct {
	Label  string
	Intent string
	DayArg string
	Note   string
	Now    time.Time
}

// AnchorVoidRequest is one `lucid anchor void` intent: correct the event
// EventID names. Reason is the optional note recorded on the void.
type AnchorVoidRequest struct {
	EventID string
	Reason  string
	Now     time.Time
}

// AnchorMissRequest is one `lucid anchor miss` intent. DayArg is the --day
// value in the shared grammar; empty is the current logical day.
type AnchorMissRequest struct {
	Label  string
	DayArg string
	Note   string
	Now    time.Time
}

// AnchorConfirmRequest is one `lucid anchor confirm` intent: attest a span of
// days for each named habit, or for every active habit when Labels is empty.
// ThroughArg is --through in the shared grammar, empty for yesterday's logical
// day; FromArg is --from, empty for each habit's default start.
type AnchorConfirmRequest struct {
	Labels     []string
	ThroughArg string
	FromArg    string
	Now        time.Time
}

// AnchorConfirmed is one habit's written confirm. The event's From and Day are
// the span it attests, and its id is the receipt.
type AnchorConfirmed struct {
	Label string
	Event engine.AnchorEvent
}

// AnchorConfirmSkipped is one habit whose span was empty, so nothing was
// written for it: already confirmed through the through-day, or the through-day
// falls before it became a habit. LastConfirmed is its latest confirmed
// through-day, empty for a habit never confirmed; NextFrom is the day its next
// confirm starts by default.
type AnchorConfirmSkipped struct {
	Label         string
	AnchorID      string
	LastConfirmed string
	NextFrom      string
}

// AnchorConfirmResult reports one confirm: the through-day it used, the habits
// it confirmed (in label order), the habits whose span was empty, and the ack —
// one line per habit, naming the exact span or why nothing was written.
type AnchorConfirmResult struct {
	Through          string
	Confirmed        []AnchorConfirmed
	AlreadyConfirmed []AnchorConfirmSkipped
	Ack              string
}

// AnchorKind executes `lucid anchor kind <label> <kind>` (engine-module.md
// §Commands): reclassify an active anchor in place. Its id, label and date are
// unchanged; one dated kind event is appended. The one exception is a
// reclassify to quit whose latest use-day is later than the anchor's date: it
// also appends a compat re-date, written before the kind event, so an older
// binary counts from that use-day too and a recent use can never hide inside a
// long quit run.
func (r *Router) AnchorKind(req AnchorKindRequest) (AnchorEventResult, error) {
	now := whenOr(req.Now)
	if err := validateKindParams(req.Kind, req.Allowance, req.Window); err != nil {
		return AnchorEventResult{}, err
	}
	w, err := r.openAnchorWrite(now)
	if err != nil {
		return AnchorEventResult{}, err
	}
	f, err := w.eventTarget(req.Label)
	if err != nil {
		return AnchorEventResult{}, err
	}
	planned := engine.AnchorEvent{
		AnchorID: f.AnchorID, Type: engine.AnchorEventKind,
		Kind: req.Kind, Allowance: req.Allowance, WindowDays: req.Window,
	}
	if res, done, err := r.resumeEvent(w, f, planned); done || err != nil {
		return res, err
	}

	ev := w.newEvent(f, engine.AnchorEventKind, w.todayString(), req.Note)
	ev.Kind, ev.Allowance, ev.WindowDays = req.Kind, req.Allowance, req.Window
	var compat *engine.Anchor
	if req.Kind == engine.AnchorKindQuit {
		after := w.withEvent(ev).facts[f.AnchorID]
		if eff := quitEffectiveDate(after); eff > f.Anchor.Date {
			compat = compatRecord(f.Anchor, eff, w.stamp)
		}
	}
	if err := r.appendEventWithCompat(ev, compat, engine.AnchorPairCompatFirst); err != nil {
		return AnchorEventResult{}, eventWriteError("the kind", err)
	}
	return eventResult(w.afterWrite(ev, compat, engine.AnchorPairCompatFirst), ev, compat, w.today, false), nil
}

// AnchorSlip executes `lucid anchor slip <label>` (engine-module.md §Commands):
// record a relapse on a quit, kept separate from a date correction. When the
// slip moves the quit's effective date — the later of its start and every
// accepted use-day, this one included — a compat re-date is appended to
// anchors.json *before* the slip event, so neither binary ever counts a run
// longer than the truth. A slip that does not move it (backdated behind a later
// slip, or on a day already counted) writes its event alone.
func (r *Router) AnchorSlip(req AnchorSlipRequest) (AnchorEventResult, error) {
	now := whenOr(req.Now)
	w, err := r.openAnchorWrite(now)
	if err != nil {
		return AnchorEventResult{}, err
	}
	day, err := resolveAnchorDay(req.DayArg, now, w.today)
	if err != nil {
		return AnchorEventResult{}, err
	}
	f, err := w.eventTarget(req.Label)
	if err != nil {
		return AnchorEventResult{}, err
	}
	planned := engine.AnchorEvent{AnchorID: f.AnchorID, Type: engine.AnchorEventSlip, Day: day}
	if res, done, err := r.resumeEvent(w, f, planned); done || err != nil {
		return res, err
	}
	if err := requireAnchorKind(f, engine.AnchorKindQuit, "slip"); err != nil {
		return AnchorEventResult{}, err
	}
	if err := requireOnOrAfterStart(f, day); err != nil {
		return AnchorEventResult{}, err
	}

	ev := w.newEvent(f, engine.AnchorEventSlip, day, req.Note)
	var compat *engine.Anchor
	if eff := quitEffectiveDate(w.withEvent(ev).facts[f.AnchorID]); eff > f.Anchor.Date {
		compat = compatRecord(f.Anchor, eff, w.stamp)
	}
	if err := r.appendEventWithCompat(ev, compat, engine.AnchorPairCompatFirst); err != nil {
		return AnchorEventResult{}, eventWriteError("the slip", err)
	}
	return eventResult(w.afterWrite(ev, compat, engine.AnchorPairCompatFirst), ev, compat, w.today, false), nil
}

// AnchorUse executes `lucid anchor use <label> (--planned | --slip)`
// (engine-module.md §Commands): log one use of a limit's allowance with its
// intent. A use touches the event store only; a planned use is spend, never
// failure, so it resets and zeroes nothing.
func (r *Router) AnchorUse(req AnchorUseRequest) (AnchorEventResult, error) {
	now := whenOr(req.Now)
	if req.Intent != engine.AnchorIntentPlanned && req.Intent != engine.AnchorIntentSlip {
		return AnchorEventResult{}, rejectAnchor(ErrAnchorRejected,
			"say whether this use was planned or a slip: --planned or --slip. Nothing was saved.")
	}
	w, err := r.openAnchorWrite(now)
	if err != nil {
		return AnchorEventResult{}, err
	}
	day, err := resolveAnchorDay(req.DayArg, now, w.today)
	if err != nil {
		return AnchorEventResult{}, err
	}
	f, err := w.eventTarget(req.Label)
	if err != nil {
		return AnchorEventResult{}, err
	}
	planned := engine.AnchorEvent{AnchorID: f.AnchorID, Type: engine.AnchorEventUse, Day: day}
	if res, done, err := r.resumeEvent(w, f, planned); done || err != nil {
		return res, err // a use is never half of a pair, so this only ever rejects
	}
	if err := requireAnchorKind(f, engine.AnchorKindLimit, "use"); err != nil {
		return AnchorEventResult{}, err
	}
	if err := requireOnOrAfterStart(f, day); err != nil {
		return AnchorEventResult{}, err
	}

	ev := w.newEvent(f, engine.AnchorEventUse, day, req.Note)
	ev.Intent = req.Intent
	if err := r.appendEventWithCompat(ev, nil, ""); err != nil {
		return AnchorEventResult{}, eventWriteError("the use", err)
	}
	return eventResult(w.withEvent(ev), ev, nil, w.today, false), nil
}

// AnchorVoid executes `lucid anchor void <event-id>` (engine-module.md
// §Commands): correct a misheard or mistaken slip, use, miss, confirm or
// graduate by appending a void that names it. The voided event drops out of
// all math and both records stay. A kind event is never voidable — the dated
// kind events are the parameter history — so a wrong kind is reclassified.
//
// Voiding a slip that had moved a quit's effective date also appends the
// restoring definition record, after the void, so an older binary reads the
// corrected date too: until the record lands that binary counts a shorter run,
// never a longer one.
func (r *Router) AnchorVoid(req AnchorVoidRequest) (AnchorEventResult, error) {
	now := whenOr(req.Now)
	w, err := r.openAnchorWrite(now)
	if err != nil {
		return AnchorEventResult{}, err
	}
	target, f, err := w.voidTarget(req.EventID)
	if err != nil {
		return AnchorEventResult{}, err
	}
	planned := engine.AnchorEvent{AnchorID: f.AnchorID, Type: engine.AnchorEventVoid, Target: target.ID}
	if res, done, err := r.resumeEvent(w, f, planned); done || err != nil {
		return res, err
	}
	if err := checkVoidable(f, target); err != nil {
		return AnchorEventResult{}, err
	}

	ev := w.newEvent(f, engine.AnchorEventVoid, w.todayString(), req.Reason)
	ev.Target = target.ID
	var compat *engine.Anchor
	if target.Type == engine.AnchorEventSlip {
		if eff := quitEffectiveDate(w.withEvent(ev).facts[f.AnchorID]); eff < f.Anchor.Date {
			compat = compatRecord(f.Anchor, eff, w.stamp)
		}
	}
	if err := r.appendEventWithCompat(ev, compat, engine.AnchorPairEventFirst); err != nil {
		return AnchorEventResult{}, eventWriteError("the void", err)
	}
	return eventResult(w.afterWrite(ev, compat, engine.AnchorPairEventFirst), ev, compat, w.today, false), nil
}

// AnchorMiss executes `lucid anchor miss <label>` (engine-module.md §Commands):
// log a day a habit did not happen — the only thing a habit logs as it happens.
// A miss touches the event store only. It always wins over a confirm on the
// same day, so a miss remembered after the day was confirmed is still recorded
// and still counts. One isolated miss means nothing — the signal is never miss
// twice — so the ack calls out only a run of consecutive misses reaching
// anchors.miss_alarm.
func (r *Router) AnchorMiss(req AnchorMissRequest) (AnchorEventResult, error) {
	now := whenOr(req.Now)
	w, err := r.openAnchorWrite(now)
	if err != nil {
		return AnchorEventResult{}, err
	}
	day, err := resolveAnchorDay(req.DayArg, now, w.today)
	if err != nil {
		return AnchorEventResult{}, err
	}
	f, err := w.eventTarget(req.Label)
	if err != nil {
		return AnchorEventResult{}, err
	}
	planned := engine.AnchorEvent{AnchorID: f.AnchorID, Type: engine.AnchorEventMiss, Day: day}
	if res, done, err := r.resumeEvent(w, f, planned); done || err != nil {
		return res, err // a miss is never half of a pair, so this only ever rejects
	}
	if err := requireAnchorKind(f, engine.AnchorKindHabit, "miss"); err != nil {
		return AnchorEventResult{}, err
	}
	if err := requireOnOrAfterStart(f, day); err != nil {
		return AnchorEventResult{}, err
	}

	ev := w.newEvent(f, engine.AnchorEventMiss, day, req.Note)
	if err := r.appendEventWithCompat(ev, nil, ""); err != nil {
		return AnchorEventResult{}, eventWriteError("the miss", err)
	}
	after := w.withEvent(ev).facts[f.AnchorID]
	return AnchorEventResult{Label: after.Anchor.Label, Event: ev, Ack: missAck(after, ev, r.anchorThresholds())}, nil
}

// AnchorConfirm executes `lucid anchor confirm [<label>...]` (engine-module.md
// §Commands): attest that each named habit — or, with no labels, every active
// habit — held on the days of its span that carry no logged miss. Without a
// confirm an unlogged day is unaccounted, never kept.
//
// The through-day defaults to yesterday's logical day, because today is not
// over. Each habit's span starts at the later of the day after its last confirm
// and the day it became a habit, so by default nothing before the cutover is
// claimed and a skipped week heals itself; --from widens it to a stretch the
// person vouches for (engine.ConfirmSpan). Every named habit is checked, and
// every span resolved, before anything is written, and the confirms land in
// one write: a rejection for one habit saves nothing for any. A habit whose
// span is empty writes nothing and the ack says why.
func (r *Router) AnchorConfirm(req AnchorConfirmRequest) (AnchorConfirmResult, error) {
	now := whenOr(req.Now)
	w, err := r.openAnchorWrite(now)
	if err != nil {
		return AnchorConfirmResult{}, err
	}
	through := engine.AddDays(w.today, -1)
	if strings.TrimSpace(req.ThroughArg) != "" {
		if through, err = resolveEventDay(req.ThroughArg, now, w.today); err != nil {
			return AnchorConfirmResult{}, err
		}
	}
	var from *time.Time
	if strings.TrimSpace(req.FromArg) != "" {
		vouched, fromErr := resolveEventDay(req.FromArg, now, w.today)
		if fromErr != nil {
			return AnchorConfirmResult{}, fromErr
		}
		from = &vouched
	}
	habits, err := w.confirmTargets(req.Labels)
	if err != nil {
		return AnchorConfirmResult{}, err
	}

	res := AnchorConfirmResult{
		Through:          engine.DateString(through),
		Confirmed:        []AnchorConfirmed{},
		AlreadyConfirmed: []AnchorConfirmSkipped{},
	}
	minting := w.events
	minting.Events = slices.Clone(w.events.Events)
	var events []engine.AnchorEvent
	for _, f := range habits {
		start, last, empty, spanErr := engine.ConfirmSpan(f, through, from)
		if spanErr != nil {
			return AnchorConfirmResult{}, rejectAnchor(ErrAnchorConfirmSpan,
				"%s. Nothing was saved.", strings.TrimPrefix(spanErr.Error(), "engine: "))
		}
		if empty {
			h := engine.ComputeHabitStats(f, w.today, engine.AnchorThresholds{})
			res.AlreadyConfirmed = append(res.AlreadyConfirmed, AnchorConfirmSkipped{
				Label: f.Anchor.Label, AnchorID: f.AnchorID, LastConfirmed: h.LastConfirmed, NextFrom: h.NextConfirmFrom,
			})
			continue
		}
		ev := engine.AnchorEvent{
			ID:         engine.NextAnchorEventID(minting, w.today),
			AnchorID:   f.AnchorID,
			Type:       engine.AnchorEventConfirm,
			Day:        engine.DateString(last),
			RecordedAt: w.stamp,
			From:       engine.DateString(start),
		}
		minting.Events = append(minting.Events, ev)
		events = append(events, ev)
		res.Confirmed = append(res.Confirmed, AnchorConfirmed{Label: f.Anchor.Label, Event: ev})
	}
	if len(events) > 0 {
		if err := r.store.AppendAnchorEvents(events...); err != nil {
			return AnchorConfirmResult{}, fmt.Errorf("could not record the confirm; nothing was saved: %w", err)
		}
	}
	res.Ack = confirmAck(habits, res)
	return res, nil
}

// confirmTargets resolves the habits a confirm covers, in label order: every
// active habit with no labels, else the named ones.
func (w anchorWrite) confirmTargets(labels []string) ([]engine.AnchorFacts, error) {
	if len(labels) == 0 {
		return w.activeHabits()
	}
	return w.namedHabits(labels)
}

// activeHabits is every active habit, in label order. By construction it never
// hits An-4, but a habit with an unfinished write still refuses the whole
// confirm (An-13), so the write is finished — by retrying it — before its days
// are claimed.
func (w anchorWrite) activeHabits() ([]engine.AnchorFacts, error) {
	var out []engine.AnchorFacts
	for _, a := range w.latest { // label order
		if a.IsSunset() || a.ID == "" {
			continue // a pre-id anchor takes no kind, so it is never a habit
		}
		f := w.facts[a.ID]
		if f.Kind != engine.AnchorKindHabit {
			continue
		}
		if st, pending := blockingPair(f); pending {
			return nil, unfinishedRejection(a.Label, st)
		}
		out = append(out, f)
	}
	return out, nil
}

// namedHabits resolves each named label like any event verb's (An-1, An-5,
// An-6), refuses one whose anchor has an unfinished write (An-13), and
// requires a habit (An-4). A label named twice is confirmed once. The result
// is in label order.
func (w anchorWrite) namedHabits(labels []string) ([]engine.AnchorFacts, error) {
	var out []engine.AnchorFacts
	seen := map[string]bool{}
	for _, label := range labels {
		if seen[label] {
			continue
		}
		seen[label] = true
		f, err := w.eventTarget(label)
		if err != nil {
			return nil, err
		}
		if st, pending := blockingPair(f); pending {
			return nil, unfinishedRejection(label, st)
		}
		if err := requireAnchorKind(f, engine.AnchorKindHabit, "confirm"); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	slices.SortStableFunc(out, func(a, b engine.AnchorFacts) int { return strings.Compare(a.Anchor.Label, b.Anchor.Label) })
	return out, nil
}

// confirmAck is `anchor confirm`'s ack: one line per habit, in label order,
// naming the exact span confirmed and its receipt so whoever asked can say it
// back, or why nothing was written for it.
func confirmAck(habits []engine.AnchorFacts, res AnchorConfirmResult) string {
	if len(habits) == 0 {
		return "No active habits to confirm; nothing written."
	}
	confirmed := map[string]engine.AnchorEvent{}
	for _, c := range res.Confirmed {
		confirmed[c.Event.AnchorID] = c.Event
	}
	skipped := map[string]AnchorConfirmSkipped{}
	for _, s := range res.AlreadyConfirmed {
		skipped[s.AnchorID] = s
	}
	lines := make([]string, 0, len(habits))
	for _, f := range habits {
		label := f.Anchor.Label
		if ev, ok := confirmed[f.AnchorID]; ok {
			lines = append(lines, fmt.Sprintf("%s: confirmed %s–%s as `%s`.", label, ev.From, ev.Day, ev.ID))
			continue
		}
		s := skipped[f.AnchorID]
		if s.LastConfirmed != "" && s.LastConfirmed >= res.Through { // civil YYYY-MM-DD orders lexically
			lines = append(lines, fmt.Sprintf("%s: already confirmed through %s; nothing written.", label, s.LastConfirmed))
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"%s: nothing to confirm through %s — its confirmations start %s; nothing written.",
			label, res.Through, s.NextFrom,
		))
	}
	return strings.Join(lines, "\n")
}

// missAck is `anchor miss`'s ack: the miss and its receipt, and — only when
// the run of consecutive misses it belongs to reaches the miss alarm — the run
// called out. A single isolated miss is never called out.
func missAck(f engine.AnchorFacts, ev engine.AnchorEvent, th engine.AnchorThresholds) string {
	ack := fmt.Sprintf("Logged a miss on %s for %s as `%s`", f.Anchor.Label, ev.Day, ev.ID)
	if run, alarm := engine.MissRunThrough(f, ev.Day, th); alarm {
		return fmt.Sprintf("%s — missed %d days in a row.", ack, run)
	}
	return ack + "."
}

// anchorThresholds is the effective config's anchors block as the Engine's
// thresholds. Before Boot the block is zero, which the Engine reads as its
// defaults.
func (r *Router) anchorThresholds() engine.AnchorThresholds {
	a := r.cfg.Anchors
	return engine.AnchorThresholds{MissAlarm: a.MissAlarm, GraduationDays: a.GraduationDays}
}

// anchorAddKind is `anchor add --kind`: a new anchor's v3 definition plus its
// first kind event, written as one pair, kind event first. Until the
// definition lands the kind event dangles and the fold ignores it, so an
// interrupted write never shows a half-made anchor in either binary. A label
// an active anchor already holds is rejected — reclassifying stays its own
// verb, so a date correction never changes a kind as a side effect.
func (r *Router) anchorAddKind(w anchorWrite, req AnchorAddRequest) (AnchorAddResult, error) {
	if _, held := engine.FindActiveAnchor(w.latest, req.Label); held {
		return AnchorAddResult{}, rejectAnchor(ErrAnchorLabelTaken,
			"%q is already recorded — reclassify it in place with %s. Nothing was saved.",
			req.Label, kindCommand(req.Label, req.Kind, req.Allowance, req.Window),
		)
	}
	if st, pending := w.pendingAdd(req.Label); pending {
		p := st.Intent
		if p.Compat.Date != req.Date || !sameKind(p.Event, req.Kind, req.Allowance, req.Window) {
			return AnchorAddResult{}, unfinishedRejection(req.Label, st)
		}
		if err := r.finishPair(st); err != nil {
			return AnchorAddResult{}, eventWriteError("the anchor", err)
		}
		return w.addKindResult(w.afterFinish(st), p.Compat, p.Event, true), nil
	}

	def := engine.Anchor{
		ID:         w.mintAnchorID(),
		Label:      req.Label,
		Date:       req.Date,
		Note:       req.Note,
		RecordedAt: w.stamp,
	}
	ev := engine.AnchorEvent{
		ID:         engine.NextAnchorEventID(w.events, w.today),
		AnchorID:   def.ID,
		Type:       engine.AnchorEventKind,
		Day:        w.todayString(),
		RecordedAt: w.stamp,
		Kind:       req.Kind,
		Allowance:  req.Allowance,
		WindowDays: req.Window,
	}
	if err := r.appendEventWithCompat(ev, &def, engine.AnchorPairEventFirst); err != nil {
		return AnchorAddResult{}, eventWriteError("the anchor", err)
	}
	return w.addKindResult(w.afterWrite(ev, &def, engine.AnchorPairEventFirst), def, ev, false), nil
}

// appendEventWithCompat writes one event, and the v3 record beside it when
// compat is non-nil, under the documented two-file protocol (engine-module.md
// §"Two-file verbs and the write order"). With no compat record it is a single
// event append and no pair intent is written. With one, it first reserves the
// event id with a pair intent holding both exact halves and their order, then
// writes the halves in that order — so a crash at any boundary leaves an
// incomplete pair that `lucid validate` names and a matching retry finishes.
// Every write is atomic on its own; a failure after the intent landed is
// marked [errPairUnfinished].
func (r *Router) appendEventWithCompat(event engine.AnchorEvent, compat *engine.Anchor, order string) error {
	if compat == nil {
		return r.store.AppendAnchorEvent(event)
	}
	intent := engine.AnchorPairIntent{Order: order, Event: event, Compat: *compat}
	if err := r.faultAt(pairStepIntent); err != nil {
		return err
	}
	if err := r.store.AppendAnchorPairIntent(intent); err != nil {
		return err
	}
	return r.finishPair(engine.AnchorPairState{Intent: intent, Status: engine.AnchorPairIntentOnly, CompatIndex: -1})
}

// finishPair appends whatever halves of a pair are missing, in the order its
// intent recorded, and never anything else: the exact planned event and the
// exact planned v3 record, so the pair is recognized as complete by equality.
// It is both the tail of a fresh two-file write and the whole of an explicit
// retry; it never runs on its own, so nothing is ever repaired automatically.
func (r *Router) finishPair(st engine.AnchorPairState) error {
	p := st.Intent
	writeEvent := func() error { return r.store.AppendAnchorEvent(p.Event) }
	writeCompat := func() error { return r.store.AppendAnchor(p.Compat) }
	var halves []func() error
	switch st.Status {
	case engine.AnchorPairIntentOnly:
		halves = []func() error{writeEvent, writeCompat}
		if p.Order == engine.AnchorPairCompatFirst {
			halves = []func() error{writeCompat, writeEvent}
		}
	case engine.AnchorPairCompatOnly:
		halves = []func() error{writeEvent}
	case engine.AnchorPairEventOnly:
		halves = []func() error{writeCompat}
	}
	for i, write := range halves {
		step := pairStepFirst
		if i > 0 {
			step = pairStepSecond
		}
		if err := r.faultAt(step); err != nil {
			return fmt.Errorf("%w: %w", errPairUnfinished, err)
		}
		if err := write(); err != nil {
			return fmt.Errorf("%w: %w", errPairUnfinished, err)
		}
	}
	return nil
}

// faultAt consults the fault-injection seam before a durable boundary of a
// two-file write. It is nil outside the crash tests.
func (r *Router) faultAt(step string) error {
	if r.pairFault == nil {
		return nil
	}
	return r.pairFault(step)
}

// resumeEvent applies the unfinished-write rule (error-states.md An-13) to an
// anchor about to be mutated. With no unfinished pair it does nothing. With
// one, a request matching the pair's planned event is an explicit retry: the
// missing halves are appended in their recorded order and the original event
// and receipt are returned (done is true). Any other request is rejected, so a
// provisional, conservative floor can never turn into an ordinary correction.
//
// A retry finishes honestly even when an older binary wrote in between
// (engine-module.md §"Two-file verbs and the write order"): a missing active
// v3 half is never appended to an anchor an older binary has since sunset —
// it would quietly bring the anchor back — and once the halves land,
// [Router.realignAfterRetry] makes sure the newest record is not stale.
func (r *Router) resumeEvent(
	w anchorWrite, f engine.AnchorFacts, planned engine.AnchorEvent,
) (res AnchorEventResult, done bool, err error) {
	st, pending := blockingPair(f)
	if !pending {
		return AnchorEventResult{}, false, nil
	}
	if !retryMatches(st.Intent, planned) {
		return AnchorEventResult{}, false, unfinishedRejection(f.Anchor.Label, st)
	}
	if f.Anchor.IsSunset() && st.Status != engine.AnchorPairCompatOnly && !st.Intent.Compat.IsSunset() {
		return AnchorEventResult{}, false, sunsetEventRejection(f.Anchor)
	}
	if finishErr := r.finishPair(st); finishErr != nil {
		return AnchorEventResult{}, false, eventWriteError("the earlier write", finishErr)
	}
	after := w.afterFinish(st)
	realigned, wrote, err := r.realignAfterRetry(w, f.Anchor, st.Intent, after)
	if err != nil {
		return AnchorEventResult{}, false, err
	}
	if wrote {
		after = after.with(nil, nil, []engine.Anchor{realigned})
	}
	compat := st.Intent.Compat
	res = eventResult(after, st.Intent.Event, &compat, w.today, true)
	if wrote {
		res.Ack += fmt.Sprintf(
			" It was changed in between, so one more record carries %s from %s.", realigned.Label, realigned.Date,
		)
	}
	return res, true, nil
}

// realignAfterRetry keeps a retry's planned v3 half from going stale. The
// planned record was built from the anchor as it stood when the write began;
// an older binary may since have corrected or renamed it. The exact planned
// half is still what completes the pair, so it is written either way, and
// then — only when the anchor's newest record would carry a date older than
// its honest effective date, or the label and note an older-binary write
// replaced — one more plain v3 record carries the current label and note and
// the later of the two dates.
//
// That record never moves a quit start. Its date changes only when the
// effective date passed the planned date, and nothing but an older-binary
// correction past the planned date can do that — after which the effective
// date is exactly the new start. base is the anchor's newest record before the
// retry wrote anything; after is the view once the halves landed. It returns
// the record it wrote, and whether it wrote one.
func (r *Router) realignAfterRetry(
	w anchorWrite, base engine.Anchor, p engine.AnchorPairIntent, after anchorView,
) (rec engine.Anchor, wrote bool, err error) {
	if !redatePair(p) {
		return engine.Anchor{}, false, nil
	}
	f := after.facts[p.Event.AnchorID]
	date := max(f.Anchor.Date, quitEffectiveDate(f))
	if date == f.Anchor.Date && f.Anchor.Label == base.Label && f.Anchor.Note == base.Note {
		return engine.Anchor{}, false, nil
	}
	rec = engine.Anchor{ID: base.ID, Label: base.Label, Date: date, Note: base.Note, RecordedAt: w.stamp}
	if err := r.store.AppendAnchor(rec); err != nil {
		return engine.Anchor{}, false, fmt.Errorf(
			"the earlier write is finished, but its anchor could not be brought up to date: %w", err,
		)
	}
	return rec, true, nil
}

// redatePair reports whether a pair's v3 half moves a quit's effective date —
// a slip's re-date, a reclassify to quit's re-date, or a slip void's restoring
// record — rather than writing a definition or a sunset of its own.
func redatePair(p engine.AnchorPairIntent) bool {
	switch p.Event.Type {
	case engine.AnchorEventSlip, engine.AnchorEventVoid:
		return true
	case engine.AnchorEventKind:
		return p.Order == engine.AnchorPairCompatFirst
	}
	return false
}

// unfinishedForLabel is the An-13 guard a plain `add` runs on its label: an
// unfinished pair on the active anchor it would correct refuses the
// correction, and so does an unfinished `add --kind` still holding the label
// when no active anchor does. A pre-id anchor can hold no pair.
func (v anchorView) unfinishedForLabel(label string) error {
	target, held := engine.FindActiveAnchor(v.latest, label)
	if !held {
		if st, pending := v.pendingAdd(label); pending {
			return unfinishedRejection(label, st)
		}
		return nil
	}
	return v.unfinishedFor(target)
}

// unfinishedFor is the An-13 guard the plain anchors.json verbs — a correction,
// a sunset, a rename — run on the active anchor they resolved: an unfinished
// pair on it refuses the change, so a provisional floor is never overwritten by
// an ordinary record and a graduation never strands as a plain sunset. A
// pre-id anchor can hold no pair.
func (v anchorView) unfinishedFor(target engine.Anchor) error {
	if target.ID == "" {
		return nil
	}
	if st, pending := blockingPair(v.facts[target.ID]); pending {
		return unfinishedRejection(target.Label, st)
	}
	return nil
}

// blockingPair returns the anchor's unfinished pair, if one blocks a change to
// it. An unfinished add --kind never does: its definition has not landed, so an
// anchor holding its reserved id is one an older binary minted under that id,
// and that anchor is not the write's — it stays free to change, and validate
// names the abandoned write.
func blockingPair(f engine.AnchorFacts) (engine.AnchorPairState, bool) {
	for _, st := range f.Pending {
		if !st.Intent.IsAddKind() {
			return st, true
		}
	}
	return engine.AnchorPairState{}, false
}

// retryMatches reports whether a request is a retry of the pair an intent
// planned: the same event type on the same anchor, and the same fact — the day
// of a slip, the kind and parameters of a kind event, the target of a void. A
// kind, void or graduate event's day is the logical day it was written, so it
// is deliberately not compared: a retry the next morning still finishes the
// write. The note is not compared either: the planned event is finished as it
// was planned.
func retryMatches(p engine.AnchorPairIntent, want engine.AnchorEvent) bool {
	e := p.Event
	if e.Type != want.Type || e.AnchorID != want.AnchorID {
		return false
	}
	switch e.Type {
	case engine.AnchorEventSlip:
		return e.Day == want.Day
	case engine.AnchorEventKind:
		return sameKind(e, want.Kind, want.Allowance, want.WindowDays)
	case engine.AnchorEventVoid:
		return e.Target == want.Target
	case engine.AnchorEventGraduate:
		return true
	}
	return false
}

// sameKind reports whether a kind event carries exactly this kind and these
// parameters.
func sameKind(e engine.AnchorEvent, kind string, allowance, window int) bool {
	return e.Kind == kind && e.Allowance == allowance && e.WindowDays == window
}

// unfinishedRejection is the An-13 copy: the anchor, the reserved event id and
// the missing half, with the retry remedy.
func unfinishedRejection(label string, st engine.AnchorPairState) error {
	return rejectAnchor(ErrAnchorUnfinished,
		"%q has an unfinished write (%s: %s) — retry the original command to finish it; "+
			"lucid validate names it. Nothing was saved.",
		label, st.Intent.Event.ID, missingHalves(st),
	)
}

// missingHalves names what an incomplete pair still lacks, in plain words.
func missingHalves(st engine.AnchorPairState) string {
	p := st.Intent
	event := fmt.Sprintf("the %s event", p.Event.Type)
	var record string
	switch {
	case p.Event.Type == engine.AnchorEventGraduate:
		record = "the sunset record"
	case p.Event.Type == engine.AnchorEventVoid:
		record = "the restoring record"
	case p.Event.Type == engine.AnchorEventKind && p.Order == engine.AnchorPairEventFirst:
		record = "the anchor's definition"
	default:
		record = "the re-dated record"
	}
	switch st.Status {
	case engine.AnchorPairCompatOnly:
		return event + " is missing"
	case engine.AnchorPairEventOnly:
		return record + " is missing"
	}
	if p.Order == engine.AnchorPairCompatFirst {
		return record + " and " + event + " are missing"
	}
	return event + " and " + record + " are missing"
}

// eventWriteError wraps a failed event write with the honest clause: nothing
// was saved when the failure came before anything landed, and a retry finishes
// it when a pair intent was already written.
func eventWriteError(what string, err error) error {
	if errors.Is(err, errPairUnfinished) {
		return fmt.Errorf(
			"could not finish recording %s — retry the same command to finish it; lucid validate names the unfinished write: %w",
			what, err,
		)
	}
	return fmt.Errorf("could not record %s; nothing was saved: %w", what, err)
}

// validateKindParams gates a kind and its parameters before any read, so a bad
// request is rejected without touching the store (error-states.md An-9). The
// engine's reason is the user-facing copy.
func validateKindParams(kind string, allowance, window int) error {
	if err := engine.ValidateKindParams(kind, allowance, window); err != nil {
		return rejectAnchor(ErrAnchorRejected, "%s. Nothing was saved.", strings.TrimPrefix(err.Error(), "engine: "))
	}
	return nil
}

// requireAnchorKind rejects an event the anchor's kind does not take
// (error-states.md An-4). The kind is read from the event store and never
// inferred, and the binary never reclassifies on the way to an event: the
// remedy names the reclassify command instead.
func requireAnchorKind(f engine.AnchorFacts, want, event string) error {
	if f.Kind == want {
		return nil
	}
	remedy := "lucid anchor kind " + f.Anchor.Label + " " + want
	if want == engine.AnchorKindLimit {
		remedy += " --allowance N --window D"
	}
	return rejectAnchor(ErrAnchorWrongKind,
		"%q is a %s, and a %s belongs to a %s — reclassify it as a %s first: %s. Nothing was saved.",
		f.Anchor.Label, f.Kind, event, want, want, remedy,
	)
}

// requireOnOrAfterStart rejects an event dated before the anchor's start
// (error-states.md An-12): an anchor's events begin on its start. The start is
// the fold's — the definition date a compat re-date never moves — which is the
// quit start for a quit and the anchor date every other kind's window clips at.
func requireOnOrAfterStart(f engine.AnchorFacts, day string) error {
	start := anchorStartDate(f)
	if day < start { // civil YYYY-MM-DD orders lexically
		return rejectAnchor(ErrAnchorBeforeStart,
			"that's before %q started (%s) — an anchor's events begin on its start. Nothing was saved.",
			f.Anchor.Label, start,
		)
	}
	return nil
}

// anchorStartDate is the fold's start, or the folded date for a store whose
// start could not be read.
func anchorStartDate(f engine.AnchorFacts) string {
	if f.Start != "" {
		return f.Start
	}
	return f.Anchor.Date
}

// quitEffectiveDate is the date a quit counts from (PD-5): the later of its
// start, its pending floor and every accepted use-day on or after the start —
// quit slips, plus any uses logged while it was a limit. With no use-days it is
// the start, exactly a milestone's date. Voided events are already out of the
// facts.
func quitEffectiveDate(f engine.AnchorFacts) string {
	start := anchorStartDate(f)
	eff := max(start, f.Floor)
	for _, e := range slices.Concat(f.Slips, f.Uses) {
		eff = max(eff, e.Day) // every day is >= start once it exceeds eff
	}
	return eff
}

// compatRecord is the v3 half a two-file verb writes: a full mirror of the
// anchor's folded definition with only the date changed, stamped with the
// event's recorded_at. It carries the id, label and note forward, so an older
// binary reading it sees the same anchor counting from date.
func compatRecord(a engine.Anchor, date, recordedAt string) *engine.Anchor {
	return &engine.Anchor{ID: a.ID, Label: a.Label, Date: date, Note: a.Note, RecordedAt: recordedAt}
}

// checkVoidable applies the void rules that read the target and its anchor
// (error-states.md An-6, An-8): a kind event or a void is never voided, an
// event is voided at most once, and a sunset anchor takes no void except of
// its own graduate event, which leaves the plain sunset as an ordinary
// retirement.
func checkVoidable(f engine.AnchorFacts, target engine.AnchorEvent) error {
	switch target.Type {
	case engine.AnchorEventKind:
		return rejectAnchor(ErrAnchorEventNotVoidable,
			"a kind event is never voided — reclassify with lucid anchor kind %s <kind>. Nothing was saved.",
			f.Anchor.Label,
		)
	case engine.AnchorEventVoid:
		return rejectAnchor(ErrAnchorEventNotVoidable, "a void can't itself be voided. Nothing was saved.")
	}
	if by, voided := f.Voided[target.ID]; voided {
		return rejectAnchor(ErrAnchorAlreadyVoided,
			"%q is already voided (by %q). Nothing was saved.", target.ID, by,
		)
	}
	if f.Anchor.IsSunset() && target.Type != engine.AnchorEventGraduate {
		return sunsetEventRejection(f.Anchor)
	}
	return nil
}

// sunsetEventRejection is the An-6 copy: a retired anchor takes no new events.
func sunsetEventRejection(a engine.Anchor) error {
	return rejectAnchor(ErrAnchorAlreadySunset,
		"%q was sunset on %s and takes no new events — add it again to start a new anchor under that name. "+
			"Nothing was saved.",
		a.Label, engine.SunsetDate(a),
	)
}

// resolveAnchorDay resolves an event verb's --day into the civil logical day
// the event is about (engine-module.md §"Event days"). Empty is the current
// logical day — the day metrics counts against — so a lapse logged after
// midnight and before the rollover lands on the evening it belongs to. A value
// reads the shared grammar on the strict tier, with the relative word
// re-anchored on the Engine's boundary (the day before the current logical
// day). A partial date is rejected, because an event lands on a real day, and
// so is a day after the current logical day: it has not happened yet.
func resolveAnchorDay(dayArg string, now, today time.Time) (string, error) {
	day, err := resolveEventDay(dayArg, now, today)
	if err != nil {
		return "", err
	}
	return engine.DateString(day), nil
}

// resolveEventDay is [resolveAnchorDay] returning the civil day itself, at
// midnight in today's location: what a confirm's --through and --from feed the
// span math.
func resolveEventDay(dayArg string, now, today time.Time) (time.Time, error) {
	if strings.TrimSpace(dayArg) == "" {
		return today, nil
	}
	res, err := observations.ResolveDay(dayArg, now, observations.DayOptions{AllowFuture: true})
	switch {
	case errors.Is(err, observations.ErrDayPartial):
		return time.Time{}, rejectDay(
			"an anchor event lands on a real day, not a whole year or month — give YYYY-MM-DD or @yesterday; "+
				"nothing was saved (got %q)", dayArg,
		)
	case err != nil:
		return time.Time{}, rejectDay(
			"could not read the day %q (want @yesterday or YYYY-MM-DD, optionally with a time); nothing was saved",
			dayArg,
		)
	}
	day := engine.DateOf(res.OccurredAt)
	if res.Relative {
		day = engine.AddDays(today, -1)
	}
	if day.After(today) {
		return time.Time{}, rejectDay(
			"cannot record against %s — that day has not happened yet; nothing was saved", engine.DateString(day),
		)
	}
	return day, nil
}

// anchorView is one read of both anchor files: anchors.json, the event store,
// the folded latest records and each anchor's folded facts. Every kind and
// event verb decides from one view, before any write.
type anchorView struct {
	log    engine.AnchorLog
	events engine.AnchorEventLog
	latest []engine.Anchor
	facts  map[string]engine.AnchorFacts
}

// newAnchorView folds the two files.
func newAnchorView(log engine.AnchorLog, events engine.AnchorEventLog) anchorView {
	return anchorView{
		log:    log,
		events: events,
		latest: engine.LatestAnchors(log),
		facts:  engine.FoldAnchorEvents(events, log),
	}
}

// with returns the view as it would read with these events, pair intents and
// anchors.json records appended — how a verb computes the numbers its write
// produces without reading the files back.
func (v anchorView) with(events []engine.AnchorEvent, intents []engine.AnchorPairIntent, records []engine.Anchor) anchorView {
	ev := v.events
	ev.Events = slices.Concat(v.events.Events, events)
	ev.PairIntents = slices.Concat(v.events.PairIntents, intents)
	log := v.log
	log.History = slices.Concat(v.log.History, records)
	return newAnchorView(log, ev)
}

// withEvent is the view with one more event appended.
func (v anchorView) withEvent(e engine.AnchorEvent) anchorView {
	return v.with([]engine.AnchorEvent{e}, nil, nil)
}

// afterWrite is the view once a write has landed: the event alone, or — for a
// two-file write — the pair intent and both of its halves, so the v3 half reads
// as the compat record it is rather than as a correction.
func (v anchorView) afterWrite(ev engine.AnchorEvent, compat *engine.Anchor, order string) anchorView {
	if compat == nil {
		return v.withEvent(ev)
	}
	intent := engine.AnchorPairIntent{Order: order, Event: ev, Compat: *compat}
	return v.with([]engine.AnchorEvent{ev}, []engine.AnchorPairIntent{intent}, []engine.Anchor{*compat})
}

// afterFinish is the view once a retry has appended an unfinished pair's
// missing halves. The intent is already in the view.
func (v anchorView) afterFinish(st engine.AnchorPairState) anchorView {
	p := st.Intent
	switch st.Status {
	case engine.AnchorPairCompatOnly:
		return v.withEvent(p.Event)
	case engine.AnchorPairEventOnly:
		return v.with(nil, nil, []engine.Anchor{p.Compat})
	}
	return v.with([]engine.AnchorEvent{p.Event}, nil, []engine.Anchor{p.Compat})
}

// eventTarget resolves the anchor an event verb names by label: the single
// active anchor holding it, with a durable id. A label no anchor holds names
// the recorded ones (An-1); one whose only holder is sunset takes no new events
// (An-6); one recorded before ids existed needs the explicit backfill first
// (An-5) — there is no implicit adoption write.
func (v anchorView) eventTarget(label string) (engine.AnchorFacts, error) {
	target, ok := engine.FindActiveAnchor(v.latest, label)
	if !ok {
		if prior, retired := engine.LatestSunsetFor(v.latest, label); retired {
			return engine.AnchorFacts{}, sunsetEventRejection(prior)
		}
		return engine.AnchorFacts{}, anchorStateRejection(v.latest, label)
	}
	if target.ID == "" {
		return engine.AnchorFacts{}, needsIDRejection(label)
	}
	f, ok := v.facts[target.ID]
	if !ok { // every anchor in anchors.json folds to facts; this is a broken invariant
		return engine.AnchorFacts{}, fmt.Errorf("router: anchor %s has no folded facts", target.ID)
	}
	return f, nil
}

// needsIDRejection is the An-5 copy: an anchor recorded before ids existed
// takes no kind and no event until the explicit backfill gives it one.
func needsIDRejection(label string) error {
	return rejectAnchor(ErrAnchorNeedsID,
		"%q was recorded before anchors had ids — run lucid anchor backfill-ids first, then try again. "+
			"Nothing was saved.",
		label,
	)
}

// voidTarget resolves the event a void names and the anchor it belongs to. An
// id held only by an unfinished pair intent is that anchor's unfinished write,
// not an unknown id; anything else no stored event holds is unknown (An-8).
func (v anchorView) voidTarget(id string) (engine.AnchorEvent, engine.AnchorFacts, error) {
	for _, e := range v.events.Events {
		if e.ID != id {
			continue
		}
		f, ok := v.facts[e.AnchorID]
		if !ok || engine.ValidateAnchorEvent(e) != nil {
			return engine.AnchorEvent{}, engine.AnchorFacts{}, rejectAnchor(ErrAnchorEventUnknown,
				"%q counts toward no recorded anchor (lucid validate names why), so there is nothing to void. "+
					"Nothing was saved.", id,
			)
		}
		return e, f, nil
	}
	for _, st := range engine.AnchorPairStates(v.events, v.log) {
		if st.Intent.Event.ID == id && st.Status != engine.AnchorPairComplete && st.Status != engine.AnchorPairMalformed {
			return engine.AnchorEvent{}, engine.AnchorFacts{}, unfinishedRejection(st.Intent.Compat.Label, st)
		}
	}
	return engine.AnchorEvent{}, engine.AnchorFacts{}, rejectAnchor(ErrAnchorEventUnknown,
		"no anchor event %q. Nothing was saved.", id,
	)
}

// pendingAdd returns an unfinished `anchor add --kind` for label: an
// event-first kind pair whose definition has not landed, so no anchor holds
// its id yet. While one stands, the label belongs to that write. One whose
// reserved id an older binary has since minted for another anchor can never be
// finished — writing its definition would merge two anchors under one id — so
// it holds nothing and is left for validate to name.
func (v anchorView) pendingAdd(label string) (engine.AnchorPairState, bool) {
	for _, st := range engine.AnchorPairStates(v.events, v.log) {
		p := st.Intent
		if st.Status == engine.AnchorPairComplete || st.Status == engine.AnchorPairMalformed {
			continue
		}
		if !p.IsAddKind() || p.Compat.Label != label {
			continue
		}
		if _, claimed := v.facts[p.Event.AnchorID]; claimed {
			continue // an older binary minted the reserved id for another anchor: unfinishable
		}
		return st, true
	}
	return engine.AnchorPairState{}, false
}

// anchorWrite is a view plus the clock a write stamps: the instant, its
// RFC3339 form, and the current logical day the event days and ids resolve
// against.
type anchorWrite struct {
	anchorView

	now   time.Time
	stamp string
	today time.Time
}

// openAnchorWrite scaffolds the engine tree, reads both anchor files and
// resolves the current logical day.
func (r *Router) openAnchorWrite(now time.Time) (anchorWrite, error) {
	if err := r.prepareEngine(); err != nil {
		return anchorWrite{}, err
	}
	view, err := r.readAnchorView()
	if err != nil {
		return anchorWrite{}, err
	}
	today, err := r.anchorToday(now)
	if err != nil {
		return anchorWrite{}, err
	}
	return anchorWrite{anchorView: view, now: now, stamp: now.Format(time.RFC3339), today: today}, nil
}

// readAnchorView reads both anchor files into a view. The engine tree must
// already be scaffolded.
func (r *Router) readAnchorView() (anchorView, error) {
	log, err := r.store.ReadAnchors()
	if err != nil {
		return anchorView{}, err
	}
	events, err := r.store.ReadAnchorEvents()
	if err != nil {
		return anchorView{}, err
	}
	return newAnchorView(log, events), nil
}

// anchorToday is the current logical day of the active profile — the same
// clocks `lucid metrics` counts against, so an event lands on the day the
// numbers it changes are counted for. A blank or undefined active profile falls
// back to the default clocks, as the metrics read does.
func (r *Router) anchorToday(now time.Time) (time.Time, error) {
	chain, err := r.store.ReadChainConfig()
	if err != nil {
		return time.Time{}, err
	}
	profile, err := r.store.ReadProfileState()
	if err != nil {
		return time.Time{}, err
	}
	clocks, err := chain.ClocksFor(profile.Active)
	if err != nil {
		if clocks, err = chain.ClocksFor(engine.DefaultProfile); err != nil {
			return time.Time{}, err
		}
	}
	return clocks.BaseLogicalDate(now), nil
}

// todayString is the current logical day as YYYY-MM-DD: the day a kind, void
// or graduate event carries.
func (w anchorWrite) todayString() string { return engine.DateString(w.today) }

// newEvent builds an event for an anchor with a freshly minted id. The id comes
// from the write's logical day and skips every id an event or a pair intent
// already holds, so a receipt is never reissued.
func (w anchorWrite) newEvent(f engine.AnchorFacts, eventType, day, note string) engine.AnchorEvent {
	return engine.AnchorEvent{
		ID:         engine.NextAnchorEventID(w.events, w.today),
		AnchorID:   f.AnchorID,
		Type:       eventType,
		Day:        day,
		RecordedAt: w.stamp,
		Note:       note,
	}
}

// mintAnchorID mints a new anchor id. Beside every id in anchors.json it skips
// the ids the event store names — an event's anchor and a pair intent's
// planned record — so an `add --kind` interrupted before its definition landed
// can never have its id handed to a different anchor. With no event store this
// is exactly [engine.NextAnchorID].
func (w anchorWrite) mintAnchorID() string {
	seen := engine.AnchorLog{History: slices.Clone(w.log.History)}
	for _, e := range w.events.Events {
		seen.History = append(seen.History, engine.Anchor{ID: e.AnchorID})
	}
	for _, p := range w.events.PairIntents {
		seen.History = append(seen.History, engine.Anchor{ID: p.Compat.ID})
	}
	return engine.NextAnchorID(seen, w.now)
}

// eventResult assembles a verb's result and its ack from the view after the
// write.
func eventResult(after anchorView, ev engine.AnchorEvent, compat *engine.Anchor, today time.Time, resumed bool) AnchorEventResult {
	f := after.facts[ev.AnchorID]
	ack := eventAck(after, f, ev, compat, today)
	if resumed {
		ack = "Finished the earlier unfinished write. " + ack
	}
	return AnchorEventResult{Label: f.Anchor.Label, Event: ev, Compat: compat, Ack: ack, Resumed: resumed}
}

// addKindResult assembles `anchor add --kind`'s result and ack from the view
// after the write.
func (w anchorWrite) addKindResult(after anchorView, def engine.Anchor, ev engine.AnchorEvent, resumed bool) AnchorAddResult {
	f := after.facts[def.ID]
	head := anchorAck(def)
	if prior, retired := engine.LatestSunsetFor(w.latest, def.Label); retired && !resumed {
		head = anchorReaddAck(def, engine.SunsetDate(prior))
	}
	ack := fmt.Sprintf("%s, %s. Saved as `%s`.", strings.TrimSuffix(head, "."), kindPhrase(f, w.today), ev.ID)
	if resumed {
		ack = "Finished the earlier unfinished write. " + ack
	}
	return AnchorAddResult{Anchor: def, Event: &ev, Ack: ack}
}

// kindPhrase names a kind and its parameters the way the acks read: "a quit",
// "a limit of 4 days in 90", "a habit (30-day window); confirmations start
// <day>".
func kindPhrase(f engine.AnchorFacts, today time.Time) string {
	switch f.Kind {
	case engine.AnchorKindLimit:
		return fmt.Sprintf("a limit of %d days in %d", f.Allowance, f.WindowDays)
	case engine.AnchorKindHabit:
		h := engine.ComputeHabitStats(f, today, engine.AnchorThresholds{})
		return fmt.Sprintf("a habit (%d-day window); confirmations start %s", h.WindowDays, h.NextConfirmFrom)
	}
	return "a " + f.Kind
}

// kindCommand renders the reclassify command An-7 names as its remedy.
func kindCommand(label, kind string, allowance, window int) string {
	cmd := "lucid anchor kind " + label + " " + kind
	if allowance > 0 {
		cmd += fmt.Sprintf(" --allowance %d", allowance)
	}
	if window > 0 {
		cmd += fmt.Sprintf(" --window %d", window)
	}
	return cmd
}

// eventAck builds the inventory ack for one written event from the numbers the
// write produced, naming its receipt.
func eventAck(after anchorView, f engine.AnchorFacts, ev engine.AnchorEvent, compat *engine.Anchor, today time.Time) string {
	label := f.Anchor.Label
	switch ev.Type {
	case engine.AnchorEventKind:
		return kindAck(f, ev, compat, today)
	case engine.AnchorEventSlip:
		q := engine.ComputeQuitStats(f, today)
		from, verb := quitEffectiveDate(f), "counting again from"
		if from != ev.Day {
			verb = "still counting from"
		}
		return fmt.Sprintf("Logged a slip on %s for %s as `%s` — %s %s (longest %s, %s).",
			label, ev.Day, ev.ID, verb, from, countOf(q.LongestRun, "day"), countOf(q.Slips, "slip"))
	case engine.AnchorEventUse:
		l := engine.ComputeLimitStats(f, today)
		tail := "."
		if l.OverAllowance {
			tail = ", over allowance."
		}
		return fmt.Sprintf("Logged a %s use of %s for %s as `%s` — %d of %d in the last %d days%s",
			ev.Intent, label, ev.Day, ev.ID, l.Used, l.Allowance, l.WindowDays, tail)
	case engine.AnchorEventVoid:
		return voidAck(after, f, ev, compat)
	}
	return fmt.Sprintf("Logged a %s on %s as `%s`.", ev.Type, label, ev.ID)
}

// kindAck is `anchor kind`'s ack: what the anchor is now and where it counts
// or confirms from.
func kindAck(f engine.AnchorFacts, ev engine.AnchorEvent, compat *engine.Anchor, today time.Time) string {
	label := f.Anchor.Label
	switch f.Kind {
	case engine.AnchorKindQuit:
		from := quitEffectiveDate(f)
		why := ""
		if compat != nil {
			why = ", its latest use-day"
		}
		return fmt.Sprintf("%s is now a quit — counting from %s%s. Saved as `%s`.", label, from, why, ev.ID)
	case engine.AnchorKindMilestone:
		return fmt.Sprintf("%s is now a milestone — counting from %s. Saved as `%s`.", label, f.Anchor.Date, ev.ID)
	}
	return fmt.Sprintf("%s is now %s. Saved as `%s`.", label, kindPhrase(f, today), ev.ID)
}

// voidAck is `anchor void`'s ack: what was voided, its receipt, and — when a
// restoring record was written — the date the quit counts from again.
func voidAck(after anchorView, f engine.AnchorFacts, ev engine.AnchorEvent, compat *engine.Anchor) string {
	ack := fmt.Sprintf("Voided `%s` (%s) as `%s` — it no longer counts; both records are kept.",
		ev.Target, describeEvent(after, ev.Target, f.Anchor.Label), ev.ID)
	if compat != nil {
		ack += fmt.Sprintf(" %s counts from %s again.", f.Anchor.Label, compat.Date)
	}
	return ack
}

// describeEvent names an event in plain words for the void ack: "a slip on
// sobriety, 2026-04-01".
func describeEvent(v anchorView, id, label string) string {
	for _, e := range v.events.Events {
		if e.ID != id {
			continue
		}
		what, day := e.Type, e.Day
		switch e.Type {
		case engine.AnchorEventUse:
			what = e.Intent + " use"
		case engine.AnchorEventGraduate:
			what = "graduation"
		case engine.AnchorEventConfirm:
			day = e.From + "–" + e.Day
		}
		return fmt.Sprintf("a %s on %s, %s", what, label, day)
	}
	return "an event on " + label
}

// countOf renders "1 day" / "N days".
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
