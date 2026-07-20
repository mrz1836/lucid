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

// numbersHeader frames the honest live-numbers block inside the compose prompt.
// It is instruction to the model — copy the numbers, never round or invent them
// — not user-facing copy, so it never appears in a delivered message.
const numbersHeader = "LIVE NUMBERS (honest — copied straight from the chain; never round or invent these):"

// recentWindowDays is the bounded look-back the companion enriches from — the
// contract-named recent-observation slice the composer reads (and a later phase
// renders into the message's context sections). It is the same order as the
// Ledger's config.recent_window (7): a week of body-state context, no wider. A
// worker constant, not config: the model-allowed reach is fixed by the contract
// and never tuned per instance (Approach A4, the sanctuary boundary).
const recentWindowDays = 7

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

// NumbersReader is the read-only engine projection the composer renders the
// honest live-numbers block from — the exact MetricsResult / StatusResult that
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

// Composer is the model-allowed compose core: it reads the operator's opaque
// prompt files and the chain's honest numbers, composes a warm message through
// the provider, appends the Engine's deterministic verdict byte-for-byte on a
// miss-day, and falls back to deterministic copy when the model is unreachable.
// It performs no delivery — the flywheel node and CLI (later phases) own the
// send, idempotency, and read-back.
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

// Result is one composed message and how it was reached. Text is the message to
// deliver. UsedLLM records the model composed it; Fallback records the
// deterministic path fired (the model was unreachable) so the caller can still
// alert that warmth was lost. MissDay records the Engine posted a user verdict
// for the window — appended verbatim on the LLM path, or the whole fallback body
// on the deterministic path.
type Result struct {
	Mode     Mode
	Text     string
	UsedLLM  bool
	Fallback bool
	MissDay  bool
	// Recent is the bounded, render-relevant recent-observation slice the
	// composer read for enrichment — empty when nothing render-relevant was
	// logged in the window or the read degraded. A later phase renders it into
	// the message's context sections; delivery ignores it (it consumes Text).
	Recent []observations.Event
	// EnrichmentDegraded records that the recent-observation read failed, so the
	// enrichment sections are omitted from an otherwise-delivered message. It is
	// how the deliberately non-fatal read stays visible — a dry-run surfaces it
	// rather than letting the omission pass silently.
	EnrichmentDegraded bool
}

// Compose builds the companion message for one window at `now`. It reads the two
// configured opaque prompt files (the system prompt and the per-mode template)
// on their explicit paths — never walking a directory, so no traversal into the
// personal template tree is possible — reads the send-free tripwire verdict and
// the honest live numbers, and composes through the provider. On success it
// returns the model's text, appending the verdict byte-for-byte on a miss-day.
// On a provider timeout / unavailable it returns the deterministic fallback with
// Fallback set. Every non-provider failure (a missing prompt file, an
// unreadable projection, a bad backend) is returned as a loud error — never a
// silent empty send — so the caller alerts.
func (c *Composer) Compose(ctx context.Context, mode Mode, now time.Time) (Result, error) {
	tmplPath, intent, err := c.modeParams(mode)
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

	// Honest live numbers, straight from the same projection the CLI exposes.
	numbers, err := c.numbersBlock(now)
	if err != nil {
		return Result{}, err
	}

	res := Result{Mode: mode, MissDay: missDay}

	// Bounded recent-observation enrichment. This read is deliberately non-fatal
	// — unlike the prompt/verdict/numbers reads above, which are the message and
	// stay loud, the observations are enrichment layered on top, so a failure
	// omits the sections and is recorded rather than killing the send.
	res.Recent, res.EnrichmentDegraded = c.recentObservations(now)

	prov, err := c.build(c.providerConfig())
	if err != nil {
		return Result{}, fmt.Errorf("companion: build provider: %w", err)
	}

	resp, err := prov.Complete(ctx, provider.Request{
		Intent:   intent,
		System:   systemPrompt,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: composeBody(tmpl, numbers)}},
	})
	if err != nil {
		if errors.Is(err, provider.ErrTimeout) || errors.Is(err, provider.ErrUnavailable) {
			fb, fbErr := c.fallback(mode, verdict, missDay, numbers)
			if fbErr != nil {
				return Result{}, fbErr
			}
			res.Text = fb
			res.Fallback = true
			return res, nil
		}
		return Result{}, fmt.Errorf("companion: compose %s: %w", mode, err)
	}

	text := strings.TrimSpace(resp.Content)
	if missDay {
		text = appendVerdict(text, verdict)
	}
	res.Text = text
	res.UsedLLM = true
	return res, nil
}

// modeParams resolves the per-mode template path and intent label, rejecting an
// unknown mode rather than silently composing the wrong window.
func (c *Composer) modeParams(mode Mode) (tmplPath, intent string, err error) {
	switch mode {
	case ModeMorning:
		return c.companion.MorningTemplate, intentMorning, nil
	case ModeNight:
		return c.companion.NightTemplate, intentNight, nil
	default:
		return "", "", fmt.Errorf("companion: unknown mode %q", mode)
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

// numbersBlock renders the honest, ramp-framed live-numbers block from the same
// MetricsResult/StatusResult the CLI exposes. Every number is copied from the
// projection — the ramp frame and the ambient signals below are formatted from
// projection integers, never invented or rounded.
func (c *Composer) numbersBlock(now time.Time) (string, error) {
	metricsRes, err := c.numbers.Metrics(now)
	if err != nil {
		return "", fmt.Errorf("companion: read metrics: %w", err)
	}
	statusRes, err := c.numbers.Status(now)
	if err != nil {
		return "", fmt.Errorf("companion: read status: %w", err)
	}
	ambient := statusAmbient(statusRes.Status)
	lines := make([]string, 0, 1+len(metricsRes.Lines)+len(ambient))
	lines = append(lines, rampFrame(metricsRes.Metrics))
	lines = append(lines, metricsRes.Lines...)
	lines = append(lines, ambient...)
	return strings.Join(lines, "\n"), nil
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

// fallback returns the deterministic message when the model is unreachable.
// On a miss-day it is the tripwire user verdict (the L1 / L2-blocked /
// storm-lapse line) for either window — the teeth still land, unsoftened. On a
// normal day it is the Engine's deterministic window nudge plus the honest
// numbers: the evening Bell for the night window; the honest numbers alone for
// the morning window, where a completed day's tripwire is silent. Only the LLM's
// warmth is lost — no dedicated companion-fallback template, no invented copy.
func (c *Composer) fallback(mode Mode, verdict string, missDay bool, numbers string) (string, error) {
	if missDay {
		return verdict, nil
	}
	var parts []string
	if mode == ModeNight {
		chain, err := c.chain.ReadChainConfig()
		if err != nil {
			return "", fmt.Errorf("companion: read chain for fallback: %w", err)
		}
		parts = append(parts, templates.Bell(chain.Label))
	}
	parts = append(parts, numbers)
	return strings.Join(parts, "\n\n"), nil
}

// composeBody joins the per-mode template with the honest live-numbers block as
// the single authorized user message the model composes from.
func composeBody(tmpl, numbers string) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(tmpl, "\n"))
	b.WriteString("\n\n")
	b.WriteString(numbersHeader)
	b.WriteString("\n")
	b.WriteString(numbers)
	return b.String()
}

// appendVerdict appends the Engine's deterministic user verdict byte-for-byte
// below the composed warm message: the model composes the warmth, the teeth line
// is appended verbatim so a model can never reword it.
func appendVerdict(msg, verdict string) string {
	msg = strings.TrimRight(msg, "\n")
	if msg == "" {
		return verdict
	}
	return msg + "\n\n" + verdict
}

// rampFrame renders the one ramp-framing line from the metrics projection. Every
// number is copied from the projection: during the early ramp (no decided day
// yet) it frames the encouragement around building rather than a hollow
// percentage; once days are decided it names the streak and the decided-day
// tally straight from the window.
func rampFrame(m engine.Metrics) string {
	a := m.Adherence
	if a.Decided == 0 {
		return fmt.Sprintf("Building — %d completed of %d accounted so far; no decided day yet.", a.Completed, a.DaysAccounted)
	}
	return fmt.Sprintf("Chain: %d-day streak — %d/%d decided days completed over the last %d.",
		m.CurrentStreak, a.Completed, a.Decided, a.Length)
}

// statusAmbient renders the ambient signals the metrics projection does not
// carry — consecutive misses and a standing storm — from the status projection.
// Both are copied from projection state, never inferred, and only appear when
// they hold so a clean day carries no noise.
func statusAmbient(st engine.Status) []string {
	var out []string
	if st.ConsecutiveMisses > 0 {
		out = append(out, fmt.Sprintf("Consecutive misses: %d.", st.ConsecutiveMisses))
	}
	if st.StormState == engine.StormStandingState {
		out = append(out, "Storm standing — the stake is stayed; contact continues.")
	}
	return out
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
