package companion

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/engine/templates"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/provider/factory"
	"github.com/mrz1836/lucid/internal/router"
)

// Mode names the two daily windows the companion composes for. The fire times
// are not the companion's concern — they are inherited from the chain.json
// bell/tripwire marks — but the copy differs per window, so a compose call names
// which one it is building.
type Mode string

// The two companion windows.
const (
	ModeMorning Mode = "morning"
	ModeNight   Mode = "night"
)

// Provider intent audit labels — the router-plan name recorded on each model
// call so a compose is attributable in the same way the Mirror agents' calls
// are (provider.Request.Intent).
const (
	intentMorning = "companion.morning"
	intentNight   = "companion.night"
)

// recentWindowDays is the bounded look-back the companion enriches from — the
// contract-named recent-observation slice the composer reads and renders into
// the message's context sections. It is the same order as the Ledger's
// config.recent_window (7): a week of body-state context, no wider. A worker
// constant, not config: the model-allowed reach is fixed by the contract and
// never tuned per instance (Approach A4, the sanctuary boundary).
const recentWindowDays = 7

// contextHeader introduces the deterministic context handed to the model. It is
// instruction, not user-facing copy: Lucid has already rendered the header, the
// status panel, and the context sections into the delivered message, so the
// model must not restate any of it — it reads the block only to ground its two
// slots.
const contextHeader = "CONTEXT — Lucid has already rendered the header, the status panel, and the context sections below into the message. Do NOT restate any numbers or re-list these signals; read them only to ground your interpretation."

// slotInstruction tells the model to return exactly the two labeled slots the
// scaffold drops into fixed places. It names the same delimiter tokens the
// renderer's parseSlots scans for (interpDelim / actionsDelim), so the prompt
// and the parser can never drift.
const slotInstruction = "Respond with EXACTLY the two labeled slots below and nothing else. Lucid renders everything else. " +
	"Write the interpretation as 2–4 short sentences: what matters now, what changed, what needs attention. " +
	"Then give one or two concrete, low-effort next actions.\n\n" +
	interpDelim + "\n<your interpretation>\n\n" +
	actionsDelim + "\n- <first action>\n- <optional second action>"

// The deterministic fallback copy fired when the model is unreachable or returns
// nothing usable. Only warmth is lost — the panel, the context sections, and (on
// a miss-day) the Engine verdict all still land. The copy is generic and never
// personal; the degradation itself is recorded on Result.Fallback and surfaced
// by the dry-run, so the delivered body need not announce it.
const (
	fallbackInterpMorning = "A quieter read today — the numbers above tell the real story. Start small and put the day on the board."
	fallbackInterpNight   = "A quieter read tonight — the day's signals are above. Read them honestly, then let the day close."
	fallbackActionMorning = "Start with the smallest move on the chain — two minutes to begin."
)

// renderKinds is the render-relevant observation vocabulary the companion
// surfaces — the body-state signals (mood, pain, sleep, symptom) and the
// companion-context kinds (withdrawal, habit_change, commitment). A recent read
// is filtered to these, so neither the message nor the model ever sees an
// unrelated kind (intake, med, measurement, memory, location): the reach is the
// named slice, not the whole Ledger.
var renderKinds = map[observations.Kind]bool{ //nolint:gochecknoglobals // a fixed, read-only set of the render-relevant observation kinds the composer surfaces
	observations.KindMood:        true,
	observations.KindPain:        true,
	observations.KindSleep:       true,
	observations.KindSymptom:     true,
	observations.KindWithdrawal:  true,
	observations.KindHabitChange: true,
	observations.KindCommitment:  true,
}

// sectionSpec maps a group of render-relevant kinds to one rendered context
// section — the emoji + label header and the set of kinds whose events land in
// it. The specs are ordered morning-first (body-state, then change, then
// commitments); the renderer reorders the whole section group for the night
// close-out. Their kinds cover exactly renderKinds, so every event the composer
// reads has a home.
type sectionSpec struct {
	emoji string
	label string
	kinds map[observations.Kind]bool
}

// sectionSpecs is the ordered section layout the composer groups recent
// observations into.
var sectionSpecs = []sectionSpec{ //nolint:gochecknoglobals // a fixed, read-only layout table mapping kinds to their rendered context section
	{
		emoji: "🫀",
		label: "Body & state",
		kinds: map[observations.Kind]bool{
			observations.KindMood:    true,
			observations.KindPain:    true,
			observations.KindSleep:   true,
			observations.KindSymptom: true,
		},
	},
	{
		emoji: "🔄",
		label: "Change & withdrawal",
		kinds: map[observations.Kind]bool{
			observations.KindHabitChange: true,
			observations.KindWithdrawal:  true,
		},
	},
	{
		emoji: "📌",
		label: "Commitments",
		kinds: map[observations.Kind]bool{observations.KindCommitment: true},
	},
}

// NumbersReader is the read-only engine projection the composer builds the
// compact status panel from — the exact MetricsResult / StatusResult that
// `lucid metrics --json` and `lucid status --json` expose, read in-process so a
// composed message and a scripted read never diverge. *router.Router satisfies
// it. The companion never recomputes a number; it copies this projection.
type NumbersReader interface {
	Metrics(now time.Time) (router.MetricsResult, error)
	Status(now time.Time) (router.StatusResult, error)
}

// VerdictReader is the send-free tripwire read the companion appends verbatim on
// a miss-day. It returns the Engine's user-channel verdict text (the L1 /
// L2-blocked / storm-lapse line) without sending or persisting, and "" on a
// normal/completed day. *scheduler.Scheduler satisfies it via
// TripwireUserVerdict. The model never touches this text.
type VerdictReader interface {
	TripwireUserVerdict(now time.Time) (string, error)
}

// ChainReader supplies the chain config the deterministic night fallback names
// (the Engine's evening Bell carries the chain label). It is read only on the
// rare provider-down night path. *storage.Adapter satisfies it via
// ReadChainConfig.
type ChainReader interface {
	ReadChainConfig() (engine.ChainConfig, error)
}

// ObservationsReader is the read-only, bounded recent-observation seam the
// composer enriches from. RecentObservations returns the events whose logical
// day falls in the [now-windowDays, now] window, sorted by id; *storage.Adapter
// satisfies it via RecentObservations. The companion reads only this named
// slice — never the whole Ledger — so the model-allowed reach stays confined to
// a week of the render-relevant kinds (Approach A4, the sanctuary boundary). It
// is an interface so the compose core is testable with a scripted fake, exactly
// like the numbers/verdict/chain readers.
type ObservationsReader interface {
	RecentObservations(now time.Time, windowDays int) ([]observations.Event, error)
}

// ProviderBuilder constructs the model backend from a resolved provider config.
// It defaults to [factory.Build]; tests inject a builder that returns a
// [provider.Fake] so no compose test needs live vendor auth (ADR-0006).
type ProviderBuilder func(config.ProviderConfig) (provider.Provider, error)

// Deps is everything a [Composer] needs, wired by the composition root
// (internal/cli) from the concrete router, scheduler, and storage adapter. The
// reader dependencies are interfaces so the compose core is testable with fakes
// and the model-allowed reach stays confined to this package. Observations is
// the optional recent-observation enrichment seam: a nil reader (the feature
// simply unconfigured) leaves the message unenriched rather than failing it.
type Deps struct {
	Companion    config.CompanionConfig
	Provider     config.ProviderConfig
	Numbers      NumbersReader
	Verdict      VerdictReader
	Chain        ChainReader
	Observations ObservationsReader
	// Build overrides the provider builder; nil defaults to factory.Build.
	Build ProviderBuilder
}

// Composer is the model-allowed compose core: it gathers the deterministic
// pieces (the compact status panel, the recent-observation context sections,
// and the intended-routine context), asks the model for only two short slots (an
// interpretation and one or two next actions), and renders the whole sectioned
// scaffold deterministically. It appends the Engine's deterministic verdict as
// the final group on a miss-day and falls back to a full deterministic scaffold
// when the model is unreachable. It performs no delivery — the flywheel node and
// CLI own the send, idempotency, and read-back.
type Composer struct {
	companion    config.CompanionConfig
	provider     config.ProviderConfig
	numbers      NumbersReader
	verdict      VerdictReader
	chain        ChainReader
	observations ObservationsReader
	build        ProviderBuilder
}

// New constructs a Composer over its dependencies, defaulting the provider
// builder to [factory.Build] when Deps.Build is nil.
func New(d Deps) *Composer {
	build := d.Build
	if build == nil {
		build = factory.Build
	}
	return &Composer{
		companion:    d.Companion,
		provider:     d.Provider,
		numbers:      d.Numbers,
		verdict:      d.Verdict,
		chain:        d.Chain,
		observations: d.Observations,
		build:        build,
	}
}

// Result is one composed message and how it was reached. Text is the rendered
// scaffold to deliver. UsedLLM records the model filled the two slots; Fallback
// records the deterministic path fired (the model was unreachable or returned
// nothing usable) so the caller can still alert that warmth was lost. MissDay
// records the Engine posted a user verdict for the window — rendered as the
// final scaffold group on both the LLM and fallback paths.
type Result struct {
	Mode     Mode
	Text     string
	UsedLLM  bool
	Fallback bool
	MissDay  bool
	// Recent is the bounded, render-relevant recent-observation slice the
	// composer read for enrichment — empty when nothing render-relevant was
	// logged in the window or the read degraded. It is rendered into the
	// message's context sections; delivery ignores it (it consumes Text).
	Recent []observations.Event
	// EnrichmentDegraded records that the recent-observation read failed, so the
	// enrichment sections are omitted from an otherwise-delivered message. It is
	// how the deliberately non-fatal read stays visible — a dry-run surfaces it
	// rather than letting the omission pass silently.
	EnrichmentDegraded bool
	// RoutineDegraded records that a configured routine file was set but could
	// not be read, so the model composed without routine-grounded context. Like
	// EnrichmentDegraded it is non-fatal and surfaced by the dry-run — a set path
	// that silently reads as unconfigured would hide a misconfiguration.
	RoutineDegraded bool
}

// Compose builds the companion message for one window at `now`. It reads the two
// configured opaque prompt files (the system prompt and the per-mode template)
// on their explicit paths — never walking a directory, so no traversal into the
// personal template tree is possible — reads the send-free tripwire verdict and
// the honest live numbers, gathers the deterministic status panel, context
// sections, and routine context, and asks the model for only the interpretation
// and next-action slots. On success it renders the full scaffold; on a provider
// timeout / unavailable or an unusable reply it renders the deterministic
// fallback scaffold with Fallback set. Every non-provider failure (a missing
// prompt file, an unreadable projection, a bad backend) is returned as a loud
// error — never a silent empty send — so the caller alerts.
func (c *Composer) Compose(ctx context.Context, mode Mode, now time.Time) (Result, error) {
	tmplPath, routinePath, intent, err := c.modeParams(mode)
	if err != nil {
		return Result{}, err
	}

	systemPrompt, err := readPromptFile(c.companion.SystemPrompt)
	if err != nil {
		return Result{}, fmt.Errorf("companion: read system prompt: %w", err)
	}
	tmpl, err := readPromptFile(tmplPath)
	if err != nil {
		return Result{}, fmt.Errorf("companion: read %s template: %w", mode, err)
	}

	// The send-free teeth verdict: "" on a normal/completed day, the deterministic
	// L1 / L2-blocked / storm-lapse line on a miss-day. A read failure is loud —
	// the verdict is life-critical and is never silently dropped.
	verdict, err := c.verdict.TripwireUserVerdict(now)
	if err != nil {
		return Result{}, fmt.Errorf("companion: read tripwire verdict: %w", err)
	}
	verdict = strings.TrimRight(verdict, "\n")
	missDay := verdict != ""

	// Honest live numbers, straight from the same projection the CLI exposes,
	// rendered as the compact status panel. A read failure is loud.
	metrics, status, err := c.readNumbers(now)
	if err != nil {
		return Result{}, err
	}
	panel := buildStatusPanel(metrics, status)

	res := Result{Mode: mode, MissDay: missDay}

	// Bounded recent-observation enrichment. This read is deliberately non-fatal
	// — unlike the prompt/verdict/numbers reads above, which are the message and
	// stay loud, the observations are enrichment layered on top, so a failure
	// omits the sections and is recorded rather than killing the send.
	res.Recent, res.EnrichmentDegraded = c.recentObservations(now)
	sections := buildSections(res.Recent, now)

	// Intended-routine context for the model (never rendered verbatim — the docs
	// are long). A set-but-unreadable path is omitted and recorded; an empty path
	// is simply unconfigured.
	routine, routineDegraded := readRoutine(routinePath)
	res.RoutineDegraded = routineDegraded

	// The deterministic scaffold, complete but for the two model slots. The
	// miss-day verdict is set now so it renders as the final group on either path.
	brief := Briefing{Mode: mode, Date: now, Panel: panel, Sections: sections}
	if missDay {
		brief.Verdict = verdict
	}

	prov, err := c.build(c.providerConfig())
	if err != nil {
		return Result{}, fmt.Errorf("companion: build provider: %w", err)
	}

	resp, err := prov.Complete(ctx, provider.Request{
		Intent:   intent,
		System:   systemPrompt,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: composeBody(tmpl, mode, panel, sections, routine)}},
	})
	if err != nil {
		if errors.Is(err, provider.ErrTimeout) || errors.Is(err, provider.ErrUnavailable) {
			return c.deterministicFallback(res, brief)
		}
		return Result{}, fmt.Errorf("companion: compose %s: %w", mode, err)
	}

	interp, actions, _ := parseSlots(strings.TrimSpace(resp.Content))
	if strings.TrimSpace(interp) == "" && len(actions) == 0 {
		// The model returned nothing usable — render the deterministic scaffold
		// rather than deliver an empty interpretation.
		return c.deterministicFallback(res, brief)
	}

	brief.Interpretation = interp
	brief.Actions = actions
	res.Text = Render(brief)
	res.UsedLLM = true
	return res, nil
}

// deterministicFallback fills the fallback interpretation + action into the
// already-built scaffold and renders it, marking the result a fallback. The
// panel, the context sections, and any miss-day verdict all still land — only
// the warmth is lost, never the send.
func (c *Composer) deterministicFallback(res Result, brief Briefing) (Result, error) {
	brief.Interpretation = fallbackInterpretation(brief.Mode)
	actions, err := c.fallbackActions(brief.Mode)
	if err != nil {
		return Result{}, err
	}
	brief.Actions = actions
	res.Text = Render(brief)
	res.Fallback = true
	res.UsedLLM = false
	return res, nil
}

// fallbackInterpretation returns the deterministic interpretation copy for a
// window's fallback scaffold.
func fallbackInterpretation(mode Mode) string {
	if mode == ModeNight {
		return fallbackInterpNight
	}
	return fallbackInterpMorning
}

// fallbackActions returns the deterministic next-action(s) for a window's
// fallback scaffold. The night reuses the Engine's evening Bell (naming the
// chain) so the close-out cue is the same deterministic copy the teeth post; the
// morning uses a fixed start-small nudge. A night fallback that cannot read the
// chain is a loud error — the Bell needs the chain label.
func (c *Composer) fallbackActions(mode Mode) ([]string, error) {
	if mode == ModeNight {
		chain, err := c.chain.ReadChainConfig()
		if err != nil {
			return nil, fmt.Errorf("companion: read chain for fallback: %w", err)
		}
		return []string{templates.Bell(chain.Label)}, nil
	}
	return []string{fallbackActionMorning}, nil
}

// modeParams resolves the per-mode template path, routine path, and intent
// label, rejecting an unknown mode rather than silently composing the wrong
// window.
func (c *Composer) modeParams(mode Mode) (tmplPath, routinePath, intent string, err error) {
	switch mode {
	case ModeMorning:
		return c.companion.MorningTemplate, c.companion.MorningRoutine, intentMorning, nil
	case ModeNight:
		return c.companion.NightTemplate, c.companion.NightRoutine, intentNight, nil
	default:
		return "", "", "", fmt.Errorf("companion: unknown mode %q", mode)
	}
}

// providerConfig returns the provider config for the compose call, applying the
// optional companion.model override (an empty override inherits provider.model).
func (c *Composer) providerConfig() config.ProviderConfig {
	pc := c.provider
	if c.companion.Model != "" {
		pc.Model = c.companion.Model
	}
	return pc
}

// readNumbers reads the same MetricsResult/StatusResult the CLI exposes and
// returns the two engine projections the status panel is built from. Both reads
// are loud — the honest numbers are the message, never enrichment.
func (c *Composer) readNumbers(now time.Time) (engine.Metrics, engine.Status, error) {
	metricsRes, err := c.numbers.Metrics(now)
	if err != nil {
		return engine.Metrics{}, engine.Status{}, fmt.Errorf("companion: read metrics: %w", err)
	}
	statusRes, err := c.numbers.Status(now)
	if err != nil {
		return engine.Metrics{}, engine.Status{}, fmt.Errorf("companion: read status: %w", err)
	}
	return metricsRes.Metrics, statusRes.Status, nil
}

// recentObservations reads the bounded recent-observation slice and filters it
// to the render-relevant kinds. Unlike the prompt, verdict, and numbers reads —
// which are the message and stay loud — this read is enrichment layered on top,
// so it is deliberately non-fatal: on any reader error it returns no events and
// degraded=true, and the caller omits the enrichment sections while still
// delivering the life-critical message. A nil reader (the feature simply
// unconfigured) is not a degradation — it returns no events, degraded=false.
func (c *Composer) recentObservations(now time.Time) (events []observations.Event, degraded bool) {
	if c.observations == nil {
		return nil, false
	}
	all, err := c.observations.RecentObservations(now, recentWindowDays)
	if err != nil {
		return nil, true
	}
	events = make([]observations.Event, 0, len(all))
	for _, ev := range all {
		if renderKinds[ev.Kind] {
			events = append(events, ev)
		}
	}
	return events, false
}

// buildSections groups the render-relevant recent events into the ordered
// context sections. Each section's Meta carries the "as logged <date>" freshness
// stamp of its newest event (with a stale flag past the threshold); a section
// with no matching event is omitted, so an absent signal leaves no stray header.
func buildSections(events []observations.Event, now time.Time) []Section {
	sections := make([]Section, 0, len(sectionSpecs))
	for _, spec := range sectionSpecs {
		lines := make([]string, 0, len(events))
		var newest observations.Event
		var have bool
		for _, ev := range events {
			if !spec.kinds[ev.Kind] {
				continue
			}
			lines = append(lines, observationLine(ev))
			if !have || ev.LogicalDate > newest.LogicalDate {
				newest, have = ev, true
			}
		}
		if len(lines) == 0 {
			continue
		}
		sections = append(sections, Section{
			Emoji: spec.emoji,
			Label: spec.label,
			Meta:  freshnessStamp(newest, now),
			Lines: lines,
		})
	}
	return sections
}

// observationLine renders one recent event as a compact bullet line. It reads
// the frozen payload defensively — a scale value may be an int (a freshly parsed
// capture) or a float64 (a JSONL round-trip) — and always yields a non-empty
// line, falling back to the verbatim note for a partial capture.
func observationLine(ev observations.Event) string {
	switch ev.Kind { //nolint:exhaustive // deliberately partial: only render-relevant kinds get a bespoke line; every other kind falls to the free-text default
	case observations.KindMood:
		return scaleLine("mood", ev.Payload, "level", "word")
	case observations.KindPain:
		return painLine(ev.Payload)
	case observations.KindSleep:
		return sleepLine(ev.Payload)
	case observations.KindSymptom:
		return symptomLine(ev.Payload)
	case observations.KindWithdrawal:
		return scaleLine("withdrawal", ev.Payload, "severity", "")
	case observations.KindHabitChange:
		return scaleLine("habit change", ev.Payload, "load", "")
	case observations.KindCommitment:
		return freeLine(string(ev.Kind), ev.Payload, "what")
	default:
		return freeLine(string(ev.Kind), ev.Payload, "note")
	}
}

// scaleLine renders "<label> <n> — <descriptor>": the optional scale value, then
// the word field (when named) or the note as the trailing descriptor. A partial
// capture with only a note still reads ("mood — wired feeling").
func scaleLine(label string, p map[string]any, scaleField, wordField string) string {
	line := label
	if v, ok := payloadInt(p, scaleField); ok {
		line += fmt.Sprintf(" %d", v)
	}
	desc := ""
	if wordField != "" {
		if w, ok := payloadString(p, wordField); ok {
			desc = w
		}
	}
	if desc == "" {
		if n, ok := payloadString(p, "note"); ok {
			desc = n
		}
	}
	if desc != "" {
		line += " — " + desc
	}
	return line
}

// painLine renders "pain <n> — <side> <site>" from the pain payload, falling
// back to the note when no site was captured.
func painLine(p map[string]any) string {
	line := "pain"
	if v, ok := payloadInt(p, "intensity"); ok {
		line += fmt.Sprintf(" %d", v)
	}
	if site, ok := payloadString(p, "site"); ok {
		if side, ok := payloadString(p, "side"); ok {
			site = side + " " + site
		}
		return line + " — " + site
	}
	if n, ok := payloadString(p, "note"); ok {
		return line + " — " + n
	}
	return line
}

// sleepLine renders "sleep · quality <n> — <note>" from the sleep payload.
func sleepLine(p map[string]any) string {
	line := "sleep"
	if q, ok := payloadInt(p, "quality"); ok {
		line += fmt.Sprintf(" · quality %d", q)
	}
	if n, ok := payloadString(p, "note"); ok {
		line += " — " + n
	}
	return line
}

// symptomLine renders "<name> <severity> — <note>" from the symptom payload,
// leading with the symptom name rather than a generic label.
func symptomLine(p map[string]any) string {
	name, ok := payloadString(p, "name")
	if !ok {
		if n, ok := payloadString(p, "note"); ok {
			return "symptom — " + n
		}
		return "symptom"
	}
	line := name
	if v, ok := payloadInt(p, "severity"); ok {
		line += fmt.Sprintf(" %d", v)
	}
	if n, ok := payloadString(p, "note"); ok {
		line += " — " + n
	}
	return line
}

// freeLine renders a free-text kind as its primary field, then the note, then
// the bare label — the commitment's "what" is the whole line, no prefix.
func freeLine(label string, p map[string]any, field string) string {
	if v, ok := payloadString(p, field); ok {
		return v
	}
	if n, ok := payloadString(p, "note"); ok {
		return n
	}
	return label
}

// payloadInt reads an int-valued payload field, tolerating the int a freshly
// parsed capture carries and the float64 a JSONL round-trip decodes to.
func payloadInt(p map[string]any, key string) (int, bool) {
	switch n := p[key].(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// payloadString reads a non-empty string-valued payload field, trimming
// surrounding whitespace.
func payloadString(p map[string]any, key string) (string, bool) {
	s, ok := p[key].(string)
	if !ok {
		return "", false
	}
	if s = strings.TrimSpace(s); s == "" {
		return "", false
	}
	return s, true
}

// composeBody assembles the single authorized user message the model composes
// from: the per-mode template (the operator's voice), the deterministic context
// block (status panel + recent-signal digest + intended routine), and the
// two-slot instruction. The model returns only the two slots; Lucid renders
// everything else.
func composeBody(tmpl string, mode Mode, panel []string, sections []Section, routine string) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(tmpl, "\n"))
	b.WriteString("\n\n")
	b.WriteString(contextBlock(mode, panel, sections, routine))
	b.WriteString("\n\n")
	b.WriteString(slotInstruction)
	return b.String()
}

// contextBlock renders the deterministic context the model grounds its slots on:
// the status panel, a compact recent-signal digest (label + freshness + lines),
// and the intended routine when one is configured and readable.
func contextBlock(mode Mode, panel []string, sections []Section, routine string) string {
	var b strings.Builder
	b.WriteString(contextHeader)
	b.WriteString("\n\nStatus:\n")
	b.WriteString(strings.Join(panel, "\n"))
	if digest := sectionsDigest(sections); digest != "" {
		b.WriteString("\n\nRecent signals:\n")
		b.WriteString(digest)
	}
	if routine != "" {
		fmt.Fprintf(&b, "\n\nIntended %s routine:\n%s", mode, routine)
	}
	return b.String()
}

// sectionsDigest renders the context sections as a compact one-line-per-section
// digest for the model — "<label> (<freshness>): <line>; <line>" — never the
// rendered scaffold (the model must not restate it).
func sectionsDigest(sections []Section) string {
	out := make([]string, 0, len(sections))
	for _, s := range sections {
		if len(s.Lines) == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("%s (%s): %s", s.Label, s.Meta, strings.Join(s.Lines, "; ")))
	}
	return strings.Join(out, "\n")
}

// readRoutine reads one configured, opaque routine file for the model's context.
// It mirrors the prompt-file firewall shape — it opens exactly the configured
// path and never walks a directory. An empty path is simply unconfigured (skip,
// not a degradation); a set-but-unreadable path is omitted and reported as
// degraded so a dry-run surfaces the misconfiguration; an empty file is
// skipped. The content is never rendered verbatim into the message — the docs
// are long — only handed to the model as grounding context.
func readRoutine(path string) (content string, degraded bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	b, err := os.ReadFile(path) //nolint:gosec // an operator-configured, explicit routine path — read directly, never dir-walked
	if err != nil {
		return "", true
	}
	return strings.TrimSpace(string(b)), false
}

// readPromptFile reads one explicit, opaque prompt file. It opens exactly the
// configured path and never walks a directory, so no traversal into the personal
// template tree is possible — the config block is the whole firewall seam. An
// empty path is a configuration error surfaced here rather than an empty read.
func readPromptFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("empty prompt path")
	}
	b, err := os.ReadFile(path) //nolint:gosec // an operator-configured, explicit prompt path — read directly, never dir-walked
	if err != nil {
		return "", err
	}
	return string(b), nil
}
