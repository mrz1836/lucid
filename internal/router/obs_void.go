package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// ObsVoidRequest carries one `lucid obs void <obs-id>` turn
// (usage/commands.md §"obs void"). ID names the measurement reading to retract.
// A void files under the current logical day and takes no reason text, so the
// request carries nothing else. A zero Now defaults to the wall clock.
type ObsVoidRequest struct {
	ID  string
	Now time.Time
}

// ObsVoidResult reports what a void appended: the void's own event id
// (EventID) and logical day, the reading it retracts (TargetID) and that
// reading's logical day, plus the inventory ack. Rejected is set (with nothing
// written and a nil error) when the measurement kind is disabled — the same
// graceful gate a disabled micro-log takes.
type ObsVoidResult struct {
	EventID     string
	TargetID    string
	TargetDate  string
	LogicalDate string
	Rejected    bool
	Ack         string
}

// VoidObservation retracts one measurement reading by appending a single
// KindMeasurement void — refs.corrects naming the reading plus the reserved
// modifier refs.void, an empty payload and no tags, filed under the current
// logical day at exact precision (mvp/data-model.md §"Measurement voids"). The
// reading's own line is never rewritten; the measurement projections drop both
// records at read time ([observations.FoldMeasurementVoids]) while the day view
// keeps both.
//
// A disabled measurement kind is checked first and is not a failure: like a
// disabled micro-log it returns Rejected with the enable hint and a nil error,
// writing nothing (error-states.md Ov-6). Every other refusal is the strict
// tier (Ov-1..Ov-5): decided before any write, returned as a bare reason ending
// "nothing was saved" — the CLI prefixes it `lucid obs void:`. Only measurement
// readings are voidable; workout and memory ids name their own amend verb. It
// is deterministic and agent-free.
func (r *Router) VoidObservation(req ObsVoidRequest) (ObsVoidResult, error) {
	now := whenOr(req.Now)
	if err := r.prepareObservations(); err != nil {
		return ObsVoidResult{}, err
	}
	cfg, err := r.store.ReadObservationsConfig()
	if err != nil {
		return ObsVoidResult{}, err
	}
	if !cfg.KindEnabled(observations.KindMeasurement) {
		return ObsVoidResult{
			Rejected: true,
			Ack:      observations.EnableHint(observations.KindMeasurement),
		}, nil
	}

	id := strings.TrimSpace(req.ID)
	target, err := r.resolveVoidTarget(id)
	if err != nil {
		return ObsVoidResult{}, err
	}

	ev, err := r.store.AppendObservation(r.buildEvent(observations.ParseResult{
		Kind:       observations.KindMeasurement,
		OccurredAt: now,
		Precision:  observations.PrecisionExact,
		Payload:    map[string]any{},
		Tags:       []string{},
		Refs: map[string]any{
			observations.RefCorrects: id,
			observations.RefVoid:     true,
		},
	}, now, nil, observations.SourceMicrolog))
	if err != nil {
		return ObsVoidResult{}, fmt.Errorf("could not save the void; nothing was saved: %w", err)
	}

	return ObsVoidResult{
		EventID:     ev.ID,
		TargetID:    id,
		TargetDate:  target.LogicalDate,
		LogicalDate: ev.LogicalDate,
		Ack: fmt.Sprintf(
			"Voided measurement `%s` (%s) as `%s` — it no longer counts in the measurement export or the packet; both records are kept.",
			id, target.LogicalDate, ev.ID,
		),
	}, nil
}

// resolveVoidTarget looks up the reading a void would retract and refuses,
// before any write, every id that is not a live measurement reading: an
// unparseable id or one no event holds (Ov-1), a workout or memory id — named
// with its amend verb (Ov-4) — any other kind (Ov-5), an id that is itself a
// void (Ov-2), and a reading another void already retracts (Ov-3, naming that
// void). The already-voided check scans the whole measurement history, since a
// void files under the day it was made, not the reading's day.
func (r *Router) resolveVoidTarget(id string) (observations.Event, error) {
	if _, ok := observations.EventDate(id); !ok {
		return observations.Event{}, voidNotFoundErr(id)
	}
	target, found, err := r.store.ReadObservationByID(id)
	if err != nil {
		return observations.Event{}, voidReadErr(err)
	}
	if !found {
		return observations.Event{}, voidNotFoundErr(id)
	}

	if target.Kind == observations.KindWorkout {
		return observations.Event{}, voidAmendKindErr(id, target.Kind, "lucid workout amend")
	}
	if target.Kind == observations.KindMemory {
		return observations.Event{}, voidAmendKindErr(id, target.Kind, "lucid memory amend")
	}
	if target.Kind != observations.KindMeasurement {
		return observations.Event{}, fmt.Errorf(
			"%q is a %s observation — only measurement readings can be voided yet; nothing was saved", id, target.Kind,
		)
	}

	if _, isVoid := observations.MeasurementVoidTarget(target); isVoid {
		return observations.Event{}, fmt.Errorf(
			"%q is itself a void — a void can't be voided; log the reading again to restore it; nothing was saved", id,
		)
	}

	measurements, err := r.store.ReadObservationsKind(observations.KindMeasurement)
	if err != nil {
		return observations.Event{}, voidReadErr(err)
	}
	for _, ev := range measurements {
		if voided, ok := observations.MeasurementVoidTarget(ev); ok && voided == id {
			return observations.Event{}, fmt.Errorf("%q is already voided (by %q); nothing was saved", id, ev.ID)
		}
	}
	return target, nil
}

// voidNotFoundErr is the Ov-1 refusal: the id names no observation to void
// (unparseable, or no event holds it).
func voidNotFoundErr(id string) error {
	return fmt.Errorf("no observation %q; nothing was saved", id)
}

// voidAmendKindErr is the Ov-4 refusal: a workout or memory observation is
// corrected by overlay with its own amend verb, never by a void.
func voidAmendKindErr(id string, kind observations.Kind, verb string) error {
	return fmt.Errorf("%q is a %s observation — correct it with %s; nothing was saved", id, kind, verb)
}

// voidReadErr wraps a Ledger read failure on the void path, so a fault reports
// that nothing was written rather than reading as a refusal.
func voidReadErr(err error) error {
	return fmt.Errorf("could not read the observation; nothing was saved: %w", err)
}
