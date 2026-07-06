package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// obsVerb is the generic capture prefix (`/obs <kind> …`); when the caller
// includes it, it is stripped so the next token is the kind.
const obsVerb = "obs"

// CaptureRequest is one micro-log turn (observations-module.md §Commands).
// Tokens is the whitespace-split argument list including the leading verb
// (e.g. ["pain","6","knee"], ["obs","where","Lisbon"]); the router resolves
// the kind and parses the rest deterministically — no LLM in the path.
type CaptureRequest struct {
	Tokens []string
	Now    time.Time
	Source string
}

// CaptureResult reports what a capture wrote and the inventory-only ack.
// Rejected is set (with no event written) when the kind is disabled — the one
// reject path; every other input is captured, partial or not (P1/P10).
type CaptureResult struct {
	EventID     string
	Kind        string
	LogicalDate string
	Partial     bool
	Rejected    bool
	PlaceKey    string
	Ack         string
}

// Capture executes an observation micro-log (observations-module.md §Commands,
// observations.md §4). It resolves the verb to a kind, rejects a disabled kind
// with the enable hint, parses the head deterministically (unparseable →
// partial, kind preserved), resolves a sticky location to its place registry,
// derives the logical day, and appends one frozen envelope. The ack is
// inventory — "logged" plus the id, zero evaluative language (§0).
func (r *Router) Capture(req CaptureRequest) (CaptureResult, error) {
	now := whenOr(req.Now)
	if err := r.store.ScaffoldObservations(); err != nil {
		return CaptureResult{}, fmt.Errorf("could not prepare the observations tree: %w", err)
	}

	tokens := req.Tokens
	if len(tokens) > 0 && strings.EqualFold(tokens[0], obsVerb) {
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return CaptureResult{}, fmt.Errorf("nothing to log — name a kind, e.g. `pain 6 knee` or `where Lisbon`")
	}

	kind, class, ok := observations.ResolveVerb(tokens[0])
	if !ok {
		return CaptureResult{}, fmt.Errorf("no observation kind named %q", tokens[0])
	}

	cfg, err := r.store.ReadObservationsConfig()
	if err != nil {
		return CaptureResult{}, err
	}
	if !cfg.KindEnabled(kind) {
		return CaptureResult{Kind: kind, Rejected: true, Ack: observations.EnableHint(kind)}, nil
	}

	parsed := observations.ParseMicrolog(observations.ParseInput{
		Kind:      kind,
		Class:     class,
		Args:      tokens[1:],
		Now:       now,
		SpelledOK: true,
	})

	placeKey, err := r.resolvePlace(&parsed, now)
	if err != nil {
		return CaptureResult{}, err
	}

	ev, err := r.store.AppendObservation(r.buildEvent(parsed, now))
	if err != nil {
		return CaptureResult{}, fmt.Errorf("could not log the observation; nothing was saved: %w", err)
	}

	return CaptureResult{
		EventID:     ev.ID,
		Kind:        ev.Kind,
		LogicalDate: ev.LogicalDate,
		Partial:     parsed.Partial,
		PlaceKey:    placeKey,
		Ack:         captureAck(ev.Kind, ev.ID, parsed.Partial),
	}, nil
}

// resolvePlace turns a sticky-location capture into a place registry entry and
// stamps place_ref/refs.place on the event (observations-module.md §Commands:
// "/obs where <place> → sticky context.location event + place registry"). It
// is a no-op for every other kind.
func (r *Router) resolvePlace(parsed *observations.ParseResult, now time.Time) (string, error) {
	if parsed.Kind != observations.KindLocation || parsed.PlaceName == "" {
		return "", nil
	}
	key, err := r.store.ResolveRegistryKey(observations.RegistryPlace, parsed.PlaceName)
	if err != nil {
		return "", err
	}
	if _, err := r.store.UpdateRegistry(observations.RegistryPlace, key, observations.RegistryPatch{
		DisplayName: parsed.PlaceName,
		At:          now.Format(time.RFC3339),
	}); err != nil {
		return "", err
	}
	parsed.Payload["place_ref"] = key
	parsed.Refs["place"] = key
	return key, nil
}

// buildEvent assembles the frozen envelope from a parse result, deriving the
// logical day under the MVP default rollover so it joins the Engine's civil
// day. The storage adapter assigns the id.
func (r *Router) buildEvent(parsed observations.ParseResult, now time.Time) observations.Event {
	ev := observations.Event{
		Schema:              observations.Schema,
		Kind:                parsed.Kind,
		RecordedAt:          now.Format(time.RFC3339),
		OccurredAt:          parsed.OccurredAt.Format(time.RFC3339),
		OccurredAtPrecision: parsed.Precision,
		LogicalDate:         observations.DeriveLogicalDate(parsed.OccurredAt, parsed.Precision, observations.DefaultRolloverMin),
		Source:              observations.SourceMicrolog,
		Payload:             parsed.Payload,
		Tags:                parsed.Tags,
		Refs:                parsed.Refs,
	}
	if parsed.OccurredEnd != nil {
		end := parsed.OccurredEnd.Format(time.RFC3339)
		ev.OccurredAtEnd = &end
	}
	return ev
}

// captureAck builds the inventory ack: "logged" plus the id, nothing
// evaluative (observations.md §0). A partial capture says it was kept as
// written — still no streak, score, or judgment.
func captureAck(kind, id string, partial bool) string {
	if partial {
		return fmt.Sprintf("Logged %s as `%s` — kept as written.", kind, id)
	}
	return fmt.Sprintf("Logged %s as `%s`.", kind, id)
}
