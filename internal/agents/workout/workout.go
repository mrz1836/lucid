// Package workout is the Workout Extraction agent (agent-contracts.md
// §"Workout Extraction"): it reads one spoken free-text drop about a completed
// session and turns it into the small structured record the router captures as
// a workout observation (plus any body-state readings). It is the voice-first
// default capture path — "just say how it went" — the structured `lucid workout
// log` flags are the precise alternative.
//
// Like Structuring and Intake it holds no storage handle and imports no Ledger
// package, so "the single drop passed in — nothing else" (agent-contracts.md
// §"Workout Extraction" allowed access) is enforced structurally, not by
// convention: the router hands [Extract] the drop text plus a
// [provider.Provider] and gets back derived fields. Writing the events, gating
// the kinds, and deriving recovery/pain signal all happen in the router and the
// deterministic recommender after this agent returns. All model access is
// behind the provider boundary (ADR-0006) so tests drive it with a fake and
// never touch a real model.
package workout

import (
	"context"
	"strings"

	"github.com/mrz1836/lucid/internal/agents/agentutil"
	"github.com/mrz1836/lucid/internal/provider"
)

// DefaultAgentVersion stamps which prompt/version of the extraction agent is
// current. The router passes it (or its own configured override) into [Input]
// so a captured record can later be attributed to the prompt that produced it,
// mirroring the other agents' version stamps without requiring a config change
// for this Mirror-side surface.
const DefaultAgentVersion = "workout-2026.05.0"

// scaleMin and scaleMax bound every 0–10 reading the drop may carry (rpe,
// soreness, pain). A value outside the range makes an extraction invalid so the
// agent retries or degrades rather than clamping — the same never-clamp
// discipline the deterministic micro-log parser follows (error-states.md §W-4).
const (
	scaleMin = 0
	scaleMax = 10
)

// Input is the single spoken drop the router authorizes for one extraction
// (agent-contracts.md §"Workout Extraction" Inputs). Text is the only content
// the agent sees; AgentVersion is the version to record (empty falls back to
// [DefaultAgentVersion]).
type Input struct {
	Text         string
	AgentVersion string
}

// BodyState is one extracted per-part reading: a soreness and/or a pain value
// on the same 0–10 scale the body_state observation kind uses. A pointer is nil
// when the drop did not state that dimension, so the router writes only what the
// user actually said — never a fabricated zero.
type BodyState struct {
	Part     string
	Soreness *int
	Pain     *int
}

// Result is the extraction's structured payload. RPE is nil when the drop gave
// no session RPE. Soreness holds the quantified per-part readings; PainFlags
// names parts the drop called out as painful without a number (the router
// records those at a conservative flag level so the recommender's pain hard stop
// can protect the part). Degraded marks the honest-failure paths (empty text, or
// unusable model output twice) so the router preserves the raw drop rather than
// dropping the capture.
type Result struct {
	Type         string
	DurationMin  int
	RPE          *int
	BodyParts    []string
	Soreness     []BodyState
	PainFlags    []string
	Notes        string
	AgentVersion string
	Degraded     bool
}

// extraction is the parsed model reply for one extraction call.
type extraction struct {
	Type        string             `json:"type"`
	DurationMin int                `json:"duration_min"`
	RPE         *int               `json:"rpe"`
	BodyParts   []string           `json:"body_parts"`
	Soreness    []bodyStatePayload `json:"soreness"`
	PainFlags   []string           `json:"pain_flags"`
	Notes       *string            `json:"notes"`
}

type bodyStatePayload struct {
	Part     string `json:"part"`
	Soreness *int   `json:"soreness"`
	Pain     *int   `json:"pain"`
}

// Extract runs the Workout Extraction agent over one spoken drop and returns the
// structured fields. It never returns an error: every failure degrades to a
// valid, honest result (agent-contracts.md §"Workout Extraction"; capture is
// never blocked on the model — architecture P10). An empty drop short-circuits
// with Degraded and makes no model call; otherwise the model is asked once and,
// on malformed/unusable output, retried once stricter before degrading.
func Extract(ctx context.Context, in Input, p provider.Provider) Result {
	version := in.AgentVersion
	if strings.TrimSpace(version) == "" {
		version = DefaultAgentVersion
	}
	if strings.TrimSpace(in.Text) == "" {
		return Result{AgentVersion: version, Degraded: true}
	}

	for _, strict := range []bool{false, true} {
		ext, ok := extractOnce(ctx, in.Text, p, strict)
		if ok {
			return toResult(ext, version)
		}
	}
	return Result{AgentVersion: version, Degraded: true}
}

// extractOnce performs one extraction completion, decodes it, and validates the
// payload against the §"Workout Extraction" rules. It reports ok=false for a
// transport error, malformed JSON, or a payload that breaks a validation rule
// (an out-of-range scale, a body-state reading with no value, an all-empty
// reply) — each of which the caller treats as a failed attempt.
func extractOnce(ctx context.Context, text string, p provider.Provider, strict bool) (extraction, bool) {
	ext, err := agentutil.CompleteJSON[extraction](ctx, p, provider.Request{
		Intent:   "workout.extract",
		System:   extractSystem(strict),
		Messages: []provider.Message{{Role: provider.RoleUser, Content: text}},
	})
	if err != nil {
		return extraction{}, false
	}
	if !validExtraction(ext) {
		return extraction{}, false
	}
	return ext, true
}

// validExtraction enforces the rules that make an extraction usable: every scale
// value is in 0–10, every body-state reading names a part and carries at least
// one value, every pain flag names a part, and the reply is not all-empty (a
// reply with nothing extracted is a failed attempt, not a valid empty capture).
func validExtraction(ext extraction) bool {
	if ext.RPE != nil && !inScale(*ext.RPE) {
		return false
	}
	if ext.DurationMin < 0 {
		return false
	}
	for _, bs := range ext.Soreness {
		if strings.TrimSpace(bs.Part) == "" {
			return false
		}
		if bs.Soreness == nil && bs.Pain == nil {
			return false
		}
		if bs.Soreness != nil && !inScale(*bs.Soreness) {
			return false
		}
		if bs.Pain != nil && !inScale(*bs.Pain) {
			return false
		}
	}
	// Blank body_parts / pain_flags entries are dropped by trimmed rather than
	// rejected — a stray empty string is noise, not a malformed reply.
	return hasContent(ext)
}

// hasContent reports whether an extraction carries at least one meaningful
// field. An extraction with only empty strings and no readings is unusable — the
// caller retries or degrades rather than writing an empty workout record.
func hasContent(ext extraction) bool {
	return strings.TrimSpace(ext.Type) != "" ||
		len(trimmed(ext.BodyParts)) > 0 ||
		ext.DurationMin > 0 ||
		ext.RPE != nil ||
		len(ext.Soreness) > 0 ||
		len(trimmed(ext.PainFlags)) > 0 ||
		notesValue(ext.Notes) != ""
}

// toResult maps a validated extraction to the agent [Result], trimming string
// fields and copying the pointer scales through untouched.
func toResult(ext extraction, version string) Result {
	return Result{
		Type:         strings.TrimSpace(ext.Type),
		DurationMin:  ext.DurationMin,
		RPE:          ext.RPE,
		BodyParts:    trimmed(ext.BodyParts),
		Soreness:     toBodyStates(ext.Soreness),
		PainFlags:    trimmed(ext.PainFlags),
		Notes:        notesValue(ext.Notes),
		AgentVersion: version,
	}
}

// toBodyStates maps parsed body-state payloads to [BodyState]s, dropping any
// with a blank part (validExtraction has already rejected a reply carrying one).
func toBodyStates(in []bodyStatePayload) []BodyState {
	if len(in) == 0 {
		return nil
	}
	out := make([]BodyState, 0, len(in))
	for _, bs := range in {
		part := strings.TrimSpace(bs.Part)
		if part == "" {
			continue
		}
		out = append(out, BodyState{Part: part, Soreness: bs.Soreness, Pain: bs.Pain})
	}
	return out
}

// inScale reports whether v is a valid 0–10 reading.
func inScale(v int) bool { return v >= scaleMin && v <= scaleMax }

// trimmed returns the non-blank, space-trimmed entries of in, or nil when none
// remain.
func trimmed(in []string) []string {
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

// notesValue collapses a nullable notes pointer to a trimmed string ("" for
// null), the shape the router uses.
func notesValue(notes *string) string {
	if notes == nil {
		return ""
	}
	return strings.TrimSpace(*notes)
}

// extractSystem is the instruction for one extraction call. strict adds the
// retry emphasis after a malformed or rejected reply (agent-contracts.md
// §"Workout Extraction" failure handling).
func extractSystem(strict bool) string {
	var b strings.Builder
	b.WriteString("You are Lucid's Workout Extraction agent. Read ONE spoken note about a workout ")
	b.WriteString("that already happened and extract only what is explicitly present: the session ")
	b.WriteString("type (a short label like \"push\", \"pull\", \"legs\", \"run\"), duration in whole ")
	b.WriteString("minutes, session RPE 0-10, the body parts trained, any soreness or pain readings ")
	b.WriteString("(each a body part with a 0-10 soreness and/or 0-10 pain when a number is given), ")
	b.WriteString("and any body part the note calls painful without a number. Do not diagnose, do ")
	b.WriteString("not give advice, and do not invent numbers that were not said. Put anything else ")
	b.WriteString("worth keeping in notes. Reply ONLY with JSON: ")
	b.WriteString(`{"type":"","duration_min":0,"rpe":null,"body_parts":[],`)
	b.WriteString(`"soreness":[{"part":"","soreness":null,"pain":null}],"pain_flags":[],"notes":null}.`)
	b.WriteString(" Use null for any value the note did not state; use [] for a list with nothing to add.")
	if strict {
		b.WriteString(" Your previous reply was not valid JSON or invented a value. Reply with the JSON ")
		b.WriteString("object and nothing else; every number must be 0-10 and must come from the note.")
	}
	return b.String()
}
