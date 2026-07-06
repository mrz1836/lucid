// Package structuring is the Structuring agent (agent-contracts.md §2): it
// reads one raw entry and produces one small, extractive record of emotions,
// themes, and people mentions. It is the only step that turns prose into
// structure, and it is strictly per-entry — no cross-entry generalization,
// no person-profile inference, no diagnostic vocabulary.
//
// Like Intake, it holds no storage handle and imports no Ledger package, so
// "the single raw entry passed in — nothing else" (agent-contracts.md §2
// allowed access) is enforced structurally, not by convention: the router
// hands Structuring the entry body plus a [provider.Provider], and the agent
// returns derived structure. The People routine that resolves person_key
// lives in the storage adapter and runs after this agent returns; Structuring
// always emits person_key as null (it has no read access to people/). All
// model access is behind the provider boundary (ADR-0006) so tests drive it
// with a fake and never touch a real model.
package structuring

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/mrz1836/lucid/internal/provider"
)

// Notes sentinels the two failure paths write (agent-contracts.md §2 failure
// handling; error-states.md §S-2/§S-3). They mirror the storage constants so
// the router and tests share one source of truth.
const (
	// NotesRawBodyEmpty is written when the raw entry body is empty (§S-3,
	// no retry).
	NotesRawBodyEmpty = "raw body empty"
	// NotesStructuringFailed is written when the model returned unusable
	// output twice (§S-2).
	NotesStructuringFailed = "structuring failed (parse)"
)

// Item is one extracted emotion or theme: a short name plus a one-line
// rationale grounded in the entry.
type Item struct {
	Name      string
	Rationale string
}

// Person is one extracted mention — the display name exactly as written.
// Structuring emits no person_key (it cannot see people/) and does not decide
// first_mention; the deterministic People routine in the storage adapter
// resolves both after this agent returns (agent-contracts.md §"How contracts
// compose").
type Person struct {
	DisplayName string
}

// Input is the single raw entry the router authorizes for one extraction
// (agent-contracts.md §2 Inputs). It carries the id (stamped onto the
// artifact), the entry body (the only content Structuring sees), and the
// structuring agent version to record.
type Input struct {
	RawID        string
	Body         string
	AgentVersion string
}

// Result is Structuring's extractive payload. Notes is the empty string for
// "no notes" (rendered as JSON null downstream). Degraded marks the two
// honest-failure paths (empty body, or unusable model output twice) so the
// router and tests can distinguish them from a real extraction.
type Result struct {
	Emotions     []Item
	Themes       []Item
	People       []Person
	Notes        string
	AgentVersion string
	Degraded     bool
}

// extraction is the parsed model reply for one extraction call.
type extraction struct {
	Emotions []itemPayload   `json:"emotions"`
	Themes   []itemPayload   `json:"themes"`
	People   []personPayload `json:"people"`
	Notes    *string         `json:"notes"`
}

type itemPayload struct {
	Name      string `json:"name"`
	Rationale string `json:"rationale"`
}

type personPayload struct {
	DisplayName string `json:"display_name"`
}

// Extract runs Structuring over one raw entry and returns the extractive
// payload. It never returns an error: every failure degrades to a valid,
// honest artifact (agent-contracts.md §2; the loop is never blocked on
// Structuring — architecture P10). An empty body short-circuits with
// notes:"raw body empty" and makes no model call; otherwise the model is
// asked once and, on malformed/unusable output, retried once stricter before
// degrading to notes:"structuring failed (parse)".
func Extract(ctx context.Context, in Input, p provider.Provider) Result {
	if strings.TrimSpace(in.Body) == "" {
		return degraded(in.AgentVersion, NotesRawBodyEmpty)
	}

	for _, strict := range []bool{false, true} {
		ext, ok := extractOnce(ctx, in, p, strict)
		if ok {
			return Result{
				Emotions:     toItems(ext.Emotions),
				Themes:       toItems(ext.Themes),
				People:       toPeople(ext.People),
				Notes:        notesValue(ext.Notes),
				AgentVersion: in.AgentVersion,
			}
		}
	}
	return degraded(in.AgentVersion, NotesStructuringFailed)
}

// extractOnce performs a single extraction completion, parses it, and
// validates the payload against the §2 rules. It reports ok=false for a
// transport error, malformed JSON, or a payload that breaks a validation
// rule (missing rationale, diagnostic notes, empty-without-notes) — every one
// of which the caller treats as a failed attempt.
func extractOnce(ctx context.Context, in Input, p provider.Provider, strict bool) (extraction, bool) {
	resp, err := p.Complete(ctx, provider.Request{
		Intent:   "structuring.extract",
		System:   extractSystem(strict),
		Messages: entrySlice(in.Body),
	})
	if err != nil {
		return extraction{}, false
	}
	var ext extraction
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &ext); jsonErr != nil {
		return extraction{}, false
	}
	if !validExtraction(ext) {
		return extraction{}, false
	}
	return ext, true
}

// validExtraction enforces the §2 validation rules that make an extraction
// usable: every emotion/theme carries a name and rationale, every person a
// display name, notes are free of diagnostic vocabulary, and the artifact is
// not empty-without-notes. A payload that fails any rule is rejected so the
// caller retries or degrades rather than persisting bad structure.
func validExtraction(ext extraction) bool {
	for _, e := range ext.Emotions {
		if strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Rationale) == "" {
			return false
		}
	}
	for _, th := range ext.Themes {
		if strings.TrimSpace(th.Name) == "" || strings.TrimSpace(th.Rationale) == "" {
			return false
		}
	}
	for _, pp := range ext.People {
		if strings.TrimSpace(pp.DisplayName) == "" {
			return false
		}
	}
	notes := notesValue(ext.Notes)
	if HasDiagnosticLanguage(notes) {
		return false
	}
	hasStructure := len(ext.Emotions) > 0 || len(ext.Themes) > 0 || len(ext.People) > 0
	if !hasStructure && strings.TrimSpace(notes) == "" {
		return false
	}
	return true
}

// degraded builds an honest empty artifact carrying only the given notes
// (the §S-2/§S-3 shape): empty arrays, notes set, and Degraded true.
func degraded(agentVersion, notes string) Result {
	return Result{Notes: notes, AgentVersion: agentVersion, Degraded: true}
}

// toItems maps parsed emotion/theme payloads to [Item]s.
func toItems(in []itemPayload) []Item {
	if len(in) == 0 {
		return nil
	}
	out := make([]Item, 0, len(in))
	for _, it := range in {
		out = append(out, Item(it))
	}
	return out
}

// toPeople maps parsed people payloads to [Person]s (display name only).
func toPeople(in []personPayload) []Person {
	if len(in) == 0 {
		return nil
	}
	out := make([]Person, 0, len(in))
	for _, pp := range in {
		out = append(out, Person(pp))
	}
	return out
}

// notesValue collapses a nullable notes pointer to a plain string ("" for
// null), the shape the rest of the agent and the storage layer use.
func notesValue(notes *string) string {
	if notes == nil {
		return ""
	}
	return *notes
}

// entrySlice renders the single raw entry body as the only message in the
// authorized slice. This is the entirety of what Structuring shows a model —
// no other entry, no Ledger.
func entrySlice(body string) []provider.Message {
	return []provider.Message{{Role: provider.RoleUser, Content: body}}
}

// extractSystem is the instruction for an extraction call. strict adds the
// retry emphasis after a malformed or rejected reply (agent-contracts.md §2
// failure handling).
func extractSystem(strict bool) string {
	var b strings.Builder
	b.WriteString("You are Lucid's Structuring agent. Read ONE journal entry and extract only ")
	b.WriteString("what is explicitly present: emotions and themes (each a short name with a ")
	b.WriteString("one-line rationale grounded in the text) and the people named (display name ")
	b.WriteString("exactly as written). Do not generalize across entries, infer relationships, ")
	b.WriteString("or use any diagnostic or clinical vocabulary. If nothing useful is present, ")
	b.WriteString("return empty arrays and a short factual note explaining why. Reply ONLY with ")
	b.WriteString(`JSON: {"emotions":[{"name":"","rationale":""}],"themes":[{"name":"","rationale":""}],`)
	b.WriteString(`"people":[{"display_name":""}],"notes":null}.`)
	if strict {
		b.WriteString(" Your previous reply was not valid or contained interpretation. Reply with ")
		b.WriteString("the JSON object and nothing else; keep every rationale extractive.")
	}
	return b.String()
}

// diagnosticPatterns compiles the diagnostic / labeling subset of the phrase
// blocklist (product-principles.md §6 "Phrase blocklist (compiled regex)").
// Notes are extractive, not interpretive: a diagnostic term ("anxious
// attachment", "avoidant tendencies", "trauma response", ...) makes an
// extraction invalid so Structuring retries or degrades (acceptance-criteria.md
// test case 4.6). The full blocklist (including external-action and coaching
// lines) is compiled by the Safety agent in a later phase; here only the
// labeling lines apply, because that is what can appear in a notes field.
var diagnosticPatterns = []*regexp.Regexp{ //nolint:gochecknoglobals // a fixed, read-only compiled blocklist (product-principles.md §6)
	regexp.MustCompile(`(?i)\byou (always|never)\b`),
	regexp.MustCompile(`(?i)\byou (?:'?re|have) (?:an? )?(anxious|avoidant|secure|disorganized)\b`),
	regexp.MustCompile(`(?i)\b(anxious|avoidant|secure|disorganized) (attach\w*|tendenc\w*|style|type|behavior)\b`),
	regexp.MustCompile(`(?i)\b(clearly|obviously)\b`),
	regexp.MustCompile(`(?i)\b(i (diagnos\w*|am diagnosing)|you'?re suffering from)\b`),
	regexp.MustCompile(`(?i)\b(attachment style|trauma response|narcissist|borderline)\b`),
}

// HasDiagnosticLanguage reports whether s matches any diagnostic / labeling
// pattern in the phrase blocklist. It is the deterministic (no-LLM) gate on
// Structuring's notes field.
func HasDiagnosticLanguage(s string) bool {
	for _, re := range diagnosticPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
