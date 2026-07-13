package router

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/storage"
)

// obsVerb is the generic capture prefix (`/obs <kind> …`); when the caller
// includes it, it is stripped so the next token is the kind.
const obsVerb = "obs"

// CaptureRequest is one micro-log turn (observations-module.md §Commands).
// Tokens is the whitespace-split argument list including the leading verb
// (e.g. ["pain","6","knee"], ["obs","where","Lisbon"]); the router resolves
// the kind and parses the rest deterministically — no LLM in the path.
// Harness, Agent, Model, and Channel are optional harness provenance: when a
// capture arrives through a chat harness rather than a terminal they are
// stamped into the event's payload.provenance sub-object (observations.md §2),
// leaving the frozen envelope's own source at microlog. A bare terminal
// capture leaves them empty, so the event carries no provenance key and stays
// byte-identical to a pre-provenance event.
type CaptureRequest struct {
	Tokens  []string
	Now     time.Time
	Harness string
	Agent   string
	Model   string
	Channel string
}

// CaptureResult reports what a capture wrote and the inventory-only ack.
// Rejected is set (with no event written) when the kind is disabled — the one
// reject path; every other input is captured, partial or not (P1/P10).
type CaptureResult struct {
	EventID     string
	Kind        observations.Kind
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
	if err := r.prepareObservations(); err != nil {
		return CaptureResult{}, err
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

	// Resolve harness provenance before any write: a malformed harness token
	// must leave nothing on disk (observations.md §2 byte-stability + honest
	// reject), so it is validated ahead of the place-registry write in
	// resolvePlace and the event append.
	provenance, err := buildProvenance(req)
	if err != nil {
		return CaptureResult{}, err
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

	ev, err := r.store.AppendObservation(r.buildEvent(parsed, now, provenance))
	if err != nil {
		return CaptureResult{}, fmt.Errorf("could not log the observation; nothing was saved: %w", err)
	}

	ack, err := r.captureAckWithPointer(ev.Kind, ev.ID, parsed.Partial, now)
	if err != nil {
		return CaptureResult{}, err
	}
	return CaptureResult{
		EventID:     ev.ID,
		Kind:        ev.Kind,
		LogicalDate: ev.LogicalDate,
		Partial:     parsed.Partial,
		PlaceKey:    placeKey,
		Ack:         ack,
	}, nil
}

// packetPointerLine is the deterministic discovery template (observations.md
// §7): the ack for /obs intervention may append it at most once per 30 days —
// a pointer, never a question, never a standalone ping.
const packetPointerLine = "A clinician packet for appointments is available: /packet clinician."

// captureAckWithPointer builds the inventory ack and, for an intervention
// capture, appends the clinician-packet discovery pointer when the ephemeral
// backoff (once per 30 days) allows. The pointer decision is the only place a
// capture ack varies by kind, and it is still inventory — no evaluation.
func (r *Router) captureAckWithPointer(kind observations.Kind, id string, partial bool, now time.Time) (string, error) {
	ack := captureAck(kind, id, partial)
	if kind != observations.KindIntervention {
		return ack, nil
	}
	show, err := r.store.ShouldShowPacketPointer(now)
	if err != nil {
		return "", err
	}
	if show {
		ack += " " + packetPointerLine
	}
	return ack, nil
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
// day. The storage adapter assigns the id. Harness provenance, when supplied,
// rides in payload.provenance — the envelope never grows a top-level field
// (observations.md §2), and the event's own source stays microlog.
func (r *Router) buildEvent(parsed observations.ParseResult, now time.Time, provenance map[string]any) observations.Event {
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
	// Stamp provenance only when the harness supplied any; a bare capture
	// omits the key entirely so the on-disk event is byte-identical to a
	// pre-provenance event (observations.md §2 byte-stability).
	if len(provenance) > 0 {
		ev.Payload["provenance"] = provenance
	}
	return ev
}

// buildProvenance assembles the optional payload.provenance sub-object from a
// capture's harness provenance (observations.md §2 "{harness?, agent?, model?,
// channel?}"). The harness token is validated through the shared
// storage.NormalizeSource grammar — one grammar across raw entries and
// observations, not two — so a malformed token is rejected honestly and the
// caller writes nothing. Agent, model, and channel are opaque identifiers
// recorded only when supplied. When no provenance is supplied it returns an
// empty (non-nil) map, so buildEvent's len guard omits the provenance key
// entirely and every existing event stays byte-identical.
func buildProvenance(req CaptureRequest) (map[string]any, error) {
	prov := map[string]any{}
	if req.Harness != "" {
		harness, err := storage.NormalizeSource(req.Harness)
		if err != nil {
			return prov, fmt.Errorf("invalid harness; nothing was saved: %w", err)
		}
		prov["harness"] = harness
	}
	if req.Agent != "" {
		prov["agent"] = req.Agent
	}
	if req.Model != "" {
		prov["model"] = req.Model
	}
	if req.Channel != "" {
		prov["channel"] = req.Channel
	}
	return prov, nil
}

// captureAck builds the inventory ack: "logged" plus the id, nothing
// evaluative (observations.md §0). A partial capture says it was kept as
// written — still no streak, score, or judgment.
func captureAck(kind observations.Kind, id string, partial bool) string {
	if partial {
		return fmt.Sprintf("Logged %s as `%s` — kept as written.", kind, id)
	}
	return fmt.Sprintf("Logged %s as `%s`.", kind, id)
}
