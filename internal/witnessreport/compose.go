package witnessreport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/provider/factory"
)

// intentWitnessReport is the router-plan audit label recorded on the weekly
// compose call, mirroring the companion's per-window intents so a witness-report
// model call is attributable in exactly the same way (provider.Request.Intent).
const intentWitnessReport = "witnessreport.weekly"

// The four model-slot delimiter labels. The model returns only these labeled
// slots — the faults framing, the progress framing, the overall warm read, and
// (optionally) refined friend-asks; everything else in the report is Lucid's
// deterministic scaffold. The numbers are never a model slot: they render
// straight from the deterministic Report (render.go). Kept as package constants
// so the compose prompt and parseSlots name the identical tokens.
const (
	faultsDelim    = "%%FAULTS%%"
	progressDelim  = "%%PROGRESS%%"
	narrativeDelim = "%%NARRATIVE%%"
	asksDelim      = "%%ASKS%%"
)

// composeContextHeader introduces the deterministic context handed to the model.
// It is instruction, not friend-facing copy: Lucid renders the numbers, the
// watch-outs, and the asks into the embed itself, so the model must not restate
// them — it reads the block only to ground the prose slots it fills.
const composeContextHeader = "CONTEXT — Lucid has already rendered the numbers, the watch-outs, and the friend-asks below into the report. Do NOT restate any number or re-list these signals; read them only to ground your interpretation. Write for the friends in the witness channel, never expose private journal detail."

// slotInstruction is the fixed instruction naming the four labeled slots the
// model returns and nothing else. It names the same delimiter tokens parseSlots
// scans for, so the prompt and the parser can never drift.
const slotInstruction = "Respond with EXACTLY these four labeled slots and nothing else. " +
	"Under FAULTS, frame this week's misses constructively in 1–2 short sentences (or say the chain held if there were none). " +
	"Under PROGRESS, name what went well in 1–2 short sentences. " +
	"Under NARRATIVE, write a 2–3 sentence warm read a friend can act on. " +
	"Under ASKS, give 1–3 concrete things a friend can do this week, one per line, each grounded in a real signal above — invent nothing.\n\n" +
	faultsDelim + "\n<faults framing>\n\n" +
	progressDelim + "\n<progress framing>\n\n" +
	narrativeDelim + "\n<warm read>\n\n" +
	asksDelim + "\n- <ask>"

// ProviderBuilder constructs the model backend from a resolved provider config.
// It defaults to [factory.Build]; tests inject a builder that returns a
// [provider.Fake] so no compose test needs live vendor auth (ADR-0006), exactly
// as the companion composer does.
type ProviderBuilder func(config.ProviderConfig) (provider.Provider, error)

// Deps is everything a [Composer] needs, wired by the composition root
// (internal/cli, Phase 4) from the concrete router, storage adapter, and the
// witness_report config block. It is credential-dumb: it carries no channel id
// and no token (those stay env-only), only the explicit opaque prompt-file paths
// the compose worker reads. SystemPrompt and Template are the two required
// opaque prompt files (the operator's witness-report voice); AsksFile is the
// optional curated friend-asks override; Model optionally overrides
// provider.model for the compose call. Numbers and Records are the entire
// input surface of the deterministic core — deliberately no observations,
// journal, or raw-entry reader exists on Deps, so private detail is structurally
// unreachable (the firewall is the absence of a wider reader, not a filter).
type Deps struct {
	SystemPrompt string
	Template     string
	AsksFile     string
	Model        string
	Provider     config.ProviderConfig
	Numbers      NumbersReader
	Records      RecordsReader
	// Build overrides the provider builder; nil defaults to factory.Build.
	Build ProviderBuilder
}

// Composer is the model-allowed compose core for the weekly witness report: it
// builds the deterministic, witness-safe Report from the honest live numbers,
// reads the operator's opaque prompt files on their explicit paths, asks the
// model to fill only the labeled prose slots from that witness-safe input, runs
// the composed prose through the fail-closed witness-safe scan, and returns the
// Report with its narrative slots filled. It performs no delivery — the flywheel
// node and CLI own the send, idempotency, and read-back (Phase 5). It is the
// sibling of internal/companion's Composer, one layer stricter: there is no
// observations reader here, so the model's reach is the deterministic Report and
// the operator's voice files, nothing else.
type Composer struct {
	systemPrompt string
	template     string
	asksFile     string
	model        string
	provider     config.ProviderConfig
	numbers      NumbersReader
	records      RecordsReader
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
		systemPrompt: d.SystemPrompt,
		template:     d.Template,
		asksFile:     d.AsksFile,
		model:        d.Model,
		provider:     d.Provider,
		numbers:      d.Numbers,
		records:      d.Records,
		build:        build,
	}
}

// Compose builds the weekly witness report at `now`. It first folds the
// deterministic, witness-safe Report from the honest live numbers (BuildReport —
// no model in that path), then applies the optional curated-asks override, reads
// the two required opaque prompt files on their explicit paths (never walking a
// directory, so no traversal into the personal template tree is possible), and
// asks the model to fill only the labeled prose slots from that witness-safe
// input. On success the composed prose passes the witness-safe output scan
// before it is accepted; a tripped scan or a provider timeout / unavailable /
// unusable reply falls back to the deterministic metrics-only report (Fallback,
// and SafetyTripped when the scan caught something) — only the warmth is lost,
// never the report. Every non-provider failure (a missing prompt file, an
// unreadable projection, a bad backend) is a loud error so a caller never
// delivers a half-built or silently-empty report.
func (c *Composer) Compose(ctx context.Context, now time.Time) (Report, error) {
	// 1. The deterministic, witness-safe scaffold — honest numbers, real
	// watch-outs, and auto-drafted friend-asks, no model in the path.
	r, err := BuildReport(now, c.numbers, c.records)
	if err != nil {
		return Report{}, err
	}

	// 2. Curated-asks override (Q4-C): the operator's own friend-asks win over
	// the auto-drafted ones when the optional file carries any. An empty/unset or
	// unreadable path is simply unconfigured — the deterministic asks stay, never
	// an error, so a misconfigured path never suppresses the report.
	curated := readCuratedAsks(c.asksFile)
	curatedApplied := len(curated) > 0
	if curatedApplied {
		r.Asks = curated
	}

	// 3. The two required opaque prompt files — the operator's witness-report
	// voice. These are the prose and stay loud: a missing or unreadable prompt is
	// a configuration error, not a silent unwarm send.
	systemPrompt, err := readPromptFile(c.systemPrompt)
	if err != nil {
		return Report{}, fmt.Errorf("witnessreport: read system prompt: %w", err)
	}
	tmpl, err := readPromptFile(c.template)
	if err != nil {
		return Report{}, fmt.Errorf("witnessreport: read template: %w", err)
	}

	prov, err := c.build(c.providerConfig())
	if err != nil {
		return Report{}, fmt.Errorf("witnessreport: build provider: %w", err)
	}

	// 4. The single authorized user message is built from ONLY the witness-safe
	// Report — numbers digest, watch-outs, and drafted asks. The model gets no
	// observations, no journal, no raw entries: there is no reader wired to reach
	// them, so a private detail cannot enter the request by construction.
	resp, err := prov.Complete(ctx, provider.Request{
		Intent:   intentWitnessReport,
		System:   systemPrompt,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: composeBody(tmpl, r)}},
	})
	if err != nil {
		if errors.Is(err, provider.ErrTimeout) || errors.Is(err, provider.ErrUnavailable) {
			r.Fallback = true
			return r, nil
		}
		return Report{}, fmt.Errorf("witnessreport: compose narrative: %w", err)
	}

	slots := parseSlots(strings.TrimSpace(resp.Content))
	if slots.empty() {
		// The model returned nothing usable — deliver the deterministic report
		// rather than an empty narrative.
		r.Fallback = true
		return r, nil
	}

	// 5. witness-safe output scan (defense-in-depth behind the structural input
	// restriction). If the composed prose carries any private-detail marker, fail
	// closed: discard every model slot and deliver the deterministic metrics-only
	// report, flagged so the caller alerts. The friend-facing surface never shows
	// flagged prose.
	if !witnessSafe(slots.Faults, slots.Progress, slots.Narrative, strings.Join(slots.Asks, "\n")) {
		r.SafetyTripped = true
		r.Fallback = true
		return r, nil
	}

	// 6. Accept the composed prose. Refined asks are taken only when the week
	// carried real signal and the operator supplied no curated override — a
	// signal-free week keeps its single honest generic ask so the model can never
	// fabricate friend-facing specifics (AC-4).
	r.Faults = slots.Faults
	r.Progress = slots.Progress
	r.Narrative = slots.Narrative
	if !curatedApplied && signalGrounded(r) && len(slots.Asks) > 0 {
		r.Asks = boundAsks(slots.Asks)
	}
	r.UsedLLM = true
	return r, nil
}

// signalGrounded reports whether the deterministic asks were drafted from a real
// signal (misses, a spent budget, thin logging) rather than being the lone
// honest generic ask a signal-free week yields. Model-refined asks are accepted
// only when this holds, so a quiet, clean week can never grow fabricated
// friend-facing specifics.
func signalGrounded(r Report) bool {
	return len(r.Asks) != 1 || r.Asks[0] != askGeneric
}

// providerConfig returns the provider config for the compose call, applying the
// optional witness-report model override (an empty override inherits
// provider.model), mirroring the companion.
func (c *Composer) providerConfig() config.ProviderConfig {
	pc := c.provider
	if c.model != "" {
		pc.Model = c.model
	}
	return pc
}

// composeBody assembles the single authorized user message the model composes
// from: the operator's template (their witness-report voice), the deterministic
// witness-safe context block (the numbers digest, the watch-outs, and the
// drafted asks), and the fixed slot instruction. The model returns only the
// labeled slots; Lucid renders every number deterministically.
func composeBody(tmpl string, r Report) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(tmpl, "\n"))
	b.WriteString("\n\n")
	b.WriteString(composeContextHeader)
	b.WriteString("\n\n")
	b.WriteString(reportDigest(r))
	b.WriteString("\n\n")
	b.WriteString(slotInstruction)
	return b.String()
}

// reportDigest renders the witness-safe Report as the compact grounding block
// the model reads — the honest numbers, this week, the watch-outs, and the
// drafted asks. It carries only fields already on the deterministic Report, so
// nothing private can ride along; it is never the rendered embed (the model must
// not restate it).
func reportDigest(r Report) string {
	var b strings.Builder
	b.WriteString("Week: ")
	b.WriteString(r.ISOWeek)
	fmt.Fprintf(&b, "\nStreak: %d (longest %d)", r.Streak, r.LongestStreak)
	if r.Adherence.Decided > 0 {
		fmt.Fprintf(&b, "\n30-day adherence: %d%% (%d/%d decided)",
			pct(r.Adherence.Adherence), r.Adherence.Completed, r.Adherence.Decided)
	} else {
		b.WriteString("\n30-day adherence: building — no decided day yet")
	}
	fmt.Fprintf(&b, "\nThis week: %d/%d completed · %d logged of 7 · %d missed",
		r.Week.Completed, r.Week.Decided, r.Week.DaysAccounted, r.WeekMisses)
	fmt.Fprintf(&b, "\nError budget: %d/%d isolated misses left", r.ErrorBudget.Remaining, r.ErrorBudget.Budget)

	if len(r.WatchOuts) > 0 {
		b.WriteString("\n\nWatch-outs (already rendered — frame, do not restate):")
		for _, w := range r.WatchOuts {
			b.WriteString("\n- ")
			b.WriteString(w)
		}
	}
	b.WriteString("\n\nDrafted friend-asks (already rendered — refine wording only, add no new ask):")
	for _, a := range r.Asks {
		b.WriteString("\n- ")
		b.WriteString(a)
	}
	return b.String()
}

// narrativeSlots holds the parsed model reply: the three prose slots and the
// optional refined asks.
type narrativeSlots struct {
	Faults    string
	Progress  string
	Narrative string
	Asks      []string
}

// empty reports whether the model returned nothing usable in any slot — the cue
// to fall back to the deterministic report rather than deliver empty prose.
func (s narrativeSlots) empty() bool {
	return strings.TrimSpace(s.Faults) == "" &&
		strings.TrimSpace(s.Progress) == "" &&
		strings.TrimSpace(s.Narrative) == "" &&
		len(s.Asks) == 0
}

// parseSlots splits a model reply into its four labeled slots. It scans for the
// delimiter lines and accumulates the text between each delimiter and the next.
// The parser is deliberately tolerant: text before the first delimiter is
// ignored, and a reply with no delimiter at all is treated as a plain-prose
// NARRATIVE (a valid warm read, not a failure), mirroring the companion parser.
func parseSlots(resp string) narrativeSlots {
	lines := strings.Split(resp, "\n")
	buf := map[string][]string{}
	current := ""
	sawAny := false
	for _, ln := range lines {
		switch strings.TrimSpace(ln) {
		case faultsDelim:
			current, sawAny = faultsDelim, true
			continue
		case progressDelim:
			current, sawAny = progressDelim, true
			continue
		case narrativeDelim:
			current, sawAny = narrativeDelim, true
			continue
		case asksDelim:
			current, sawAny = asksDelim, true
			continue
		}
		if current == "" {
			continue
		}
		buf[current] = append(buf[current], ln)
	}

	if !sawAny {
		return narrativeSlots{Narrative: strings.TrimSpace(resp)}
	}
	return narrativeSlots{
		Faults:    strings.TrimSpace(strings.Join(buf[faultsDelim], "\n")),
		Progress:  strings.TrimSpace(strings.Join(buf[progressDelim], "\n")),
		Narrative: strings.TrimSpace(strings.Join(buf[narrativeDelim], "\n")),
		Asks:      parseAskLines(buf[asksDelim]),
	}
}

// parseAskLines turns the raw lines of the ASKS slot into clean ask strings:
// each non-empty line has any leading bullet marker (-, *, •) and surrounding
// whitespace stripped. Blank lines are dropped.
func parseAskLines(lines []string) []string {
	var out []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		t = strings.TrimLeft(t, "-*•")
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// boundAsks caps a set of asks at maxAsks so the friend-facing section stays
// scannable regardless of how many lines the model returned.
func boundAsks(asks []string) []string {
	if len(asks) > maxAsks {
		return asks[:maxAsks]
	}
	return asks
}

// readCuratedAsks reads the optional curated friend-asks file — the operator's
// own asks that override the auto-drafted ones. It mirrors the prompt-file
// firewall shape: it opens exactly the configured path and never walks a
// directory. An empty/unset path is unconfigured (nil, keep the drafted asks);
// a set-but-unreadable path degrades quietly to nil rather than failing the
// report (the deterministic asks still land, and the operator reviews every
// preview). Each non-empty, non-comment line is one ask, bounded to maxAsks.
func readCuratedAsks(path string) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // an operator-configured, explicit curated-asks path — read directly, never dir-walked
	if err != nil {
		return nil
	}
	var asks []string
	for _, ln := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(ln)
		t = strings.TrimLeft(t, "-*•")
		t = strings.TrimSpace(t)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "<!--") {
			continue
		}
		asks = append(asks, t)
	}
	return boundAsks(asks)
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
