package observations

// RefVoid marks a measurement correction that retracts the reading it names
// (observations.md §2, "Measurement voids carry refs.void"; mvp/data-model.md
// §"Measurement voids"). It is a boolean modifier on a refs.corrects event,
// not a link: a KindMeasurement event carrying `void: true` and a non-empty
// refs.corrects is a void, with an empty payload and no tags. Readers that
// project measurements drop both the void and its target; raw history keeps
// both. Like RefCorrects and RefRedate it is exported so the void write path
// (internal/router) stamps the very key the readers check — one source of
// truth for the wire format.
const RefVoid = "void"

// MeasurementVoidTarget reports the reading id a measurement void retracts,
// and whether the event is such a void. Only a KindMeasurement event whose
// refs.void is the JSON boolean true (a string "true" is not) and whose
// refs.corrects is a non-empty string is a void; every other event is not.
func MeasurementVoidTarget(e Event) (string, bool) {
	if e.Kind != KindMeasurement || e.Refs == nil {
		return "", false
	}
	if v, ok := e.Refs[RefVoid].(bool); !ok || !v {
		return "", false
	}
	target, ok := e.Refs[RefCorrects].(string)
	if !ok || target == "" {
		return "", false
	}
	return target, true
}

// FoldMeasurementVoids drops measurement voids and the readings they retract
// (mvp/data-model.md §"Measurement voids", fold-on-read). Callers fold the
// whole measurement history before windowing, so a void filed on a later day
// still removes its earlier target. A void naming an id that is absent drops
// only itself. Every non-KindMeasurement event passes through untouched, so
// the fold is safe on a mixed slice. It is pure — the result is a new slice in
// input order and the input is never mutated.
func FoldMeasurementVoids(events []Event) []Event {
	voided := map[string]bool{}
	for _, e := range events {
		if target, ok := MeasurementVoidTarget(e); ok {
			voided[target] = true
		}
	}
	out := make([]Event, 0, len(events))
	for _, e := range events {
		if e.Kind != KindMeasurement {
			out = append(out, e)
			continue
		}
		if _, isVoid := MeasurementVoidTarget(e); isVoid || voided[e.ID] {
			continue
		}
		out = append(out, e)
	}
	return out
}
