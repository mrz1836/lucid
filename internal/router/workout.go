package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/agents/workout"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
)

// PainFlagLevel is the body_state.pain value the router records for an
// unquantified pain flag — a drop that names a part as painful without a number
// ("my knee is killing me"). It is set to the recommender's default pain-flag
// threshold so an honest "it hurts" trips the deterministic pain hard stop and
// protects the part, erring toward safety exactly as a workout recommender
// should. A quantified reading always wins over this default.
const PainFlagLevel = 5

// BodyStateInput is one named part's optional soreness/pain reading captured
// alongside a workout. A pointer is nil when the caller did not state that
// dimension, so the event records only what was actually reported — never a
// fabricated zero. Both scales are 0–10; the caller (CLI or extraction) is
// responsible for range validation before the router writes.
type BodyStateInput struct {
	Part     string
	Soreness *int
	Pain     *int
}

// AnchorCount is one daily-anchor item as the user reported it: the item's name
// and, when they gave one, the count they did. HasCount separates "I did 55" from
// "I did squats" — an unstated count is recorded as unstated rather than as a
// fabricated zero, the same discipline the body-state pointers follow. The count
// is inventory: nothing compares it to the program's week target (§0).
type AnchorCount struct {
	Name     string
	Count    int
	HasCount bool
}

// WorkoutLogRequest is one completed-session capture from the structured path
// (`lucid workout log --type … --parts …`). Every field is optional — a bare
// `--type push` is a valid "I trained" record, and so is a bare Anchor ("did the
// anchor") — so the router writes only the stated fields, and an entirely empty
// request still captures a partial workout event (capture never blocks). RPE is
// nil when unstated. Anchor marks the event as a completed daily anchor and
// AnchorItems carries any per-item counts. Harness/Agent/Model/Channel are the
// optional relay provenance stamped into payload.provenance, exactly as an
// observation capture stamps it.
//
// DayArg is the optional `--day` value, resolved on the strict tier through
// the same entry point every capture verb uses, so a session done yesterday
// records as yesterday instead of as a note on today. Empty means now.
type WorkoutLogRequest struct {
	Type        string
	Movements   []string
	DurationMin int
	RPE         *int
	BodyParts   []string
	BodyStates  []BodyStateInput
	Anchor      bool
	AnchorItems []AnchorCount
	Notes       string
	Now         time.Time
	DayArg      string
	Harness     string
	Agent       string
	Model       string
	Channel     string
}

// WorkoutLogTextRequest is one completed-session capture from the spoken path
// (`lucid workout log "did pull, shoulder felt fine, ~50 min"`). The router
// runs the Workout Extraction agent over Text, then writes the same events as
// the structured path — falling back to the raw drop as the note when the model
// degrades, so a spoken capture is never lost. DayArg backdates the session
// exactly as it does on the structured path: `--day` is not content, so it
// composes with a spoken drop rather than competing with it.
type WorkoutLogTextRequest struct {
	Text    string
	Now     time.Time
	DayArg  string
	Harness string
	Agent   string
	Model   string
	Channel string
}

// WorkoutLogResult reports what a workout capture wrote: the workout event id,
// any body-state event ids, and the inventory-only ack. Rejected is set (with
// nothing written) when the workout kind is disabled — the one reject path.
// Degraded marks a spoken capture whose extraction fell back to the raw drop.
type WorkoutLogResult struct {
	WorkoutID    string
	BodyStateIDs []string
	Kind         observations.Kind
	LogicalDate  string
	Rejected     bool
	Degraded     bool
	Ack          string
}

// WorkoutLog captures a completed session from structured fields. It scaffolds
// the observations tree, rejects a disabled workout kind with the enable hint,
// resolves the optional backdate before anything is written, writes the durable
// workout event first (so a later body-state failure never leaves a session
// unrecorded), then writes each body-state reading when that kind is enabled.
// It reuses the same deterministic envelope build and append the micro-log
// capture uses — no LLM in this path.
//
// The session and its readings share one resolved occurrence, computed once
// here and handed to both builders. That is not tidiness: the recovery
// guardrail reads occurred_at while the progress trend reads logical_date, so a
// backdate applied to only one of the two events would make them disagree about
// when the session happened — silently, and only for backdated sessions.
func (r *Router) WorkoutLog(req WorkoutLogRequest) (WorkoutLogResult, error) {
	now := whenOr(req.Now)
	if err := r.prepareObservations(); err != nil {
		return WorkoutLogResult{}, err
	}
	cfg, err := r.store.ReadObservationsConfig()
	if err != nil {
		return WorkoutLogResult{}, err
	}
	if !cfg.KindEnabled(observations.KindWorkout) {
		return WorkoutLogResult{
			Kind:     observations.KindWorkout,
			Rejected: true,
			Ack:      observations.EnableHint(observations.KindWorkout),
		}, nil
	}

	when, err := resolveCaptureWhen(req.DayArg, now)
	if err != nil {
		return WorkoutLogResult{}, err
	}

	provenance, err := buildProvenance(CaptureRequest{
		Harness: req.Harness, Agent: req.Agent, Model: req.Model, Channel: req.Channel,
	})
	if err != nil {
		return WorkoutLogResult{}, err
	}

	// The durable record: the workout event is written first, so a body-state
	// append failure below never leaves the session unrecorded.
	ev, err := r.store.AppendObservation(r.buildEvent(workoutParseResult(req, when), now, provenance, observations.SourceMicrolog))
	if err != nil {
		return WorkoutLogResult{}, fmt.Errorf("could not log the workout; nothing was saved: %w", err)
	}
	res := WorkoutLogResult{WorkoutID: ev.ID, Kind: ev.Kind, LogicalDate: ev.LogicalDate}

	if cfg.KindEnabled(observations.KindBodyState) {
		for _, bs := range req.BodyStates {
			parsed, ok := bodyStateParseResult(bs, when)
			if !ok {
				continue
			}
			bev, bErr := r.store.AppendObservation(r.buildEvent(parsed, now, provenance, observations.SourceMicrolog))
			if bErr != nil {
				res.Ack = workoutLogAck(res)
				return res, fmt.Errorf("logged the workout but could not log a body-state reading: %w", bErr)
			}
			res.BodyStateIDs = append(res.BodyStateIDs, bev.ID)
		}
	}

	res.Ack = workoutLogAck(res)
	return res, nil
}

// WorkoutLogFromText captures a completed session from a spoken drop. It gates
// on the workout kind up front (so a disabled kind never spends a model call),
// runs the Workout Extraction agent, then delegates to [Router.WorkoutLog] with
// the extracted fields — carrying the degrade flag through and preserving the
// raw drop as the note when the model produced nothing usable.
//
// An unreadable `--day` is refused beside the kind gate for the same reason:
// the request is already doomed, so it should not cost a model call first.
// [Router.WorkoutLog] resolves it again authoritatively — one resolver, one
// answer — and that second pass is what the events are actually stamped from.
func (r *Router) WorkoutLogFromText(ctx context.Context, req WorkoutLogTextRequest, p provider.Provider) (WorkoutLogResult, error) {
	if err := r.prepareObservations(); err != nil {
		return WorkoutLogResult{}, err
	}
	cfg, err := r.store.ReadObservationsConfig()
	if err != nil {
		return WorkoutLogResult{}, err
	}
	if !cfg.KindEnabled(observations.KindWorkout) {
		return WorkoutLogResult{
			Kind:     observations.KindWorkout,
			Rejected: true,
			Ack:      observations.EnableHint(observations.KindWorkout),
		}, nil
	}

	if _, dayErr := resolveCaptureWhen(req.DayArg, whenOr(req.Now)); dayErr != nil {
		return WorkoutLogResult{}, dayErr
	}

	ext := workout.Extract(ctx, workout.Input{Text: req.Text, AgentVersion: workout.DefaultAgentVersion}, p)
	res, err := r.WorkoutLog(workoutLogFromExtraction(ext, req))
	if err != nil {
		return res, err
	}
	res.Degraded = res.Degraded || ext.Degraded
	return res, nil
}

// workoutParseResult builds the deterministic workout event from a structured
// request. Only stated fields land in the payload; an entirely empty request
// takes the partial path (kind kept, an empty note) so a bare "I trained"
// capture is still recorded rather than dropped. when carries the already
// resolved occurrence, so this stamps the session's real day rather than the
// moment it was typed.
func workoutParseResult(req WorkoutLogRequest, when observations.DayResolution) observations.ParseResult {
	payload := map[string]any{}
	if t := strings.TrimSpace(req.Type); t != "" {
		payload["type"] = t
	}
	if mv := trimStrings(req.Movements); len(mv) > 0 {
		payload["movements"] = mv
	}
	if req.DurationMin > 0 {
		payload["duration_min"] = req.DurationMin
	}
	if req.RPE != nil {
		payload["rpe"] = *req.RPE
	}
	if bp := trimStrings(req.BodyParts); len(bp) > 0 {
		payload["body_parts"] = bp
	}
	if anchored, items := anchorPayload(req); anchored {
		payload["anchor"] = true
		if len(items) > 0 {
			payload["anchor_items"] = items
		}
	}
	if n := strings.TrimSpace(req.Notes); n != "" {
		payload["note"] = n
	}

	partial := len(payload) == 0
	if partial {
		payload["parse"] = observations.ParseMarkerPartial
	}
	return observations.ParseResult{
		Kind:        observations.KindWorkout,
		OccurredAt:  when.OccurredAt,
		Precision:   when.Precision,
		OccurredEnd: when.End,
		Payload:     payload,
		Refs:        map[string]any{},
		Partial:     partial,
	}
}

// anchorPayload folds a request's daily-anchor fields into the marker and the
// per-item inventory the workout event carries (frozen envelope: a marker inside
// payload, never a new top-level field or a third kind). Naming any item is itself
// a report that the floor was done, so counts imply the marker. Each item is
// recorded with its count when one was given and without it when none was, so a
// bare item name stays honest inventory rather than a fabricated zero — and
// nothing here compares a count to the program's week target (§0).
//
// An anchor contributes no body parts of its own: it is the daily floor, not a
// session, so the recovery guardrail finds nothing to protect and the next day's
// card is unaffected. Parts still ride along when the same capture also states a
// session's, which is a real load and should open a real window.
func anchorPayload(req WorkoutLogRequest) (bool, []map[string]any) {
	items := make([]map[string]any, 0, len(req.AnchorItems))
	for _, item := range req.AnchorItems {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		entry := map[string]any{"name": name}
		if item.HasCount {
			entry["count"] = item.Count
		}
		items = append(items, entry)
	}
	return req.Anchor || len(items) > 0, items
}

// bodyStateParseResult builds a body_state event for one reading. It reports
// ok=false for a blank part or a reading that states neither soreness nor pain
// — an empty reading is noise, not a capture. It takes the session's resolved
// occurrence, so a reading is always dated to the session it was reported
// alongside rather than to the moment of typing.
func bodyStateParseResult(bs BodyStateInput, when observations.DayResolution) (observations.ParseResult, bool) {
	part := strings.TrimSpace(bs.Part)
	if part == "" || (bs.Soreness == nil && bs.Pain == nil) {
		return observations.ParseResult{}, false
	}
	payload := map[string]any{"body_part": part}
	if bs.Soreness != nil {
		payload["soreness"] = *bs.Soreness
	}
	if bs.Pain != nil {
		payload["pain"] = *bs.Pain
	}
	return observations.ParseResult{
		Kind:        observations.KindBodyState,
		OccurredAt:  when.OccurredAt,
		Precision:   when.Precision,
		OccurredEnd: when.End,
		Payload:     payload,
		Refs:        map[string]any{},
	}, true
}

// workoutLogFromExtraction folds an extraction into the structured request the
// router writes. Quantified soreness/pain readings become body-state inputs;
// a pain flag with no number and no existing reading records at PainFlagLevel so
// the recommender can protect the part; a named daily anchor rides through as the
// marker plus whatever counts the drop offered. A fully-degraded extraction (no
// fields) keeps the raw drop as the note, so a spoken capture is never lost.
func workoutLogFromExtraction(ext workout.Result, req WorkoutLogTextRequest) WorkoutLogRequest {
	out := WorkoutLogRequest{
		Type:        ext.Type,
		DurationMin: ext.DurationMin,
		RPE:         ext.RPE,
		BodyParts:   ext.BodyParts,
		Anchor:      ext.Anchor,
		AnchorItems: anchorCounts(ext.AnchorItems),
		Notes:       ext.Notes,
		Now:         req.Now,
		DayArg:      req.DayArg,
		Harness:     req.Harness,
		Agent:       req.Agent,
		Model:       req.Model,
		Channel:     req.Channel,
	}
	for _, bs := range ext.Soreness {
		out.BodyStates = append(out.BodyStates, BodyStateInput{Part: bs.Part, Soreness: bs.Soreness, Pain: bs.Pain})
	}
	for _, part := range ext.PainFlags {
		if hasBodyStatePart(out.BodyStates, part) {
			continue
		}
		level := PainFlagLevel
		out.BodyStates = append(out.BodyStates, BodyStateInput{Part: part, Pain: &level})
	}
	if workoutRequestEmpty(out) {
		out.Notes = strings.TrimSpace(req.Text)
	}
	return out
}

// workoutRequestEmpty reports whether a request carries no session fields — the
// signal to fall back to the raw drop so a degraded spoken capture is not lost.
// A daily anchor is content: "did the anchor" is a complete capture on its own,
// so an anchor-only request never takes the empty/partial path.
func workoutRequestEmpty(req WorkoutLogRequest) bool {
	if anchored, _ := anchorPayload(req); anchored {
		return false
	}
	return strings.TrimSpace(req.Type) == "" &&
		req.DurationMin == 0 &&
		req.RPE == nil &&
		len(trimStrings(req.BodyParts)) == 0 &&
		len(trimStrings(req.Movements)) == 0 &&
		strings.TrimSpace(req.Notes) == "" &&
		len(req.BodyStates) == 0
}

// anchorCounts maps the extraction agent's anchor items to the router's own,
// keeping the "a count was stated" distinction the agent already made rather than
// collapsing an unstated count to zero.
func anchorCounts(in []workout.AnchorCount) []AnchorCount {
	if len(in) == 0 {
		return nil
	}
	out := make([]AnchorCount, 0, len(in))
	for _, item := range in {
		out = append(out, AnchorCount{Name: item.Name, Count: item.Count, HasCount: item.HasCount})
	}
	return out
}

// hasBodyStatePart reports whether a part already has a body-state reading, so a
// pain flag never duplicates a quantified reading for the same part.
func hasBodyStatePart(list []BodyStateInput, part string) bool {
	want := strings.ToLower(strings.TrimSpace(part))
	for _, bs := range list {
		if strings.ToLower(strings.TrimSpace(bs.Part)) == want {
			return true
		}
	}
	return false
}

// workoutLogAck builds the inventory ack — "logged" plus the id, and a count of
// any body-state readings — with zero evaluative language (observations.md §0).
func workoutLogAck(res WorkoutLogResult) string {
	ack := fmt.Sprintf("Logged workout as `%s`.", res.WorkoutID)
	switch n := len(res.BodyStateIDs); {
	case n == 1:
		ack += " Logged 1 body-state reading."
	case n > 1:
		ack += fmt.Sprintf(" Logged %d body-state readings.", n)
	}
	return ack
}

// trimStrings returns the non-blank, space-trimmed entries of in, or nil.
func trimStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
