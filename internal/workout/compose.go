package workout

// This file owns the workout module's **on-demand surface**: the model-allowed
// composer that turns an already-decided recommendation into the delivered
// message. It mirrors internal/companion/compose.go exactly — the deterministic
// core (recommend.go) owns the pick, the trend (trend.go) and the render
// (render.go) own the numbers and the layout, and the model is asked for only a
// bounded phrasing slot. On a provider outage or an unusable reply the whole
// message renders deterministically; the model never changes the pick. See
// docs/mvp/workout-module.md §"Surfaces" and §"The message scaffold".

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/config"
	"github.com/mrz1836/lucid/internal/engine"
	"github.com/mrz1836/lucid/internal/observations"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/provider/factory"
)

// intentDaily is the provider audit label stamped on the phrasing call — the
// router-plan name recorded on the model request so a compose is attributable in
// the same way the Mirror agents' calls are (provider.Request.Intent). The
// deterministic core has already decided the plan; this call only phrases it.
const intentDaily = "workout.daily"

// emojiCoach heads the model's bounded phrasing slot — a warm coach's note that
// leads the deterministic card. It is the only region the model contributes;
// every other region of the message is Lucid's.
const emojiCoach = "💬"

// contextHeader introduces the deterministic digest handed to the model. It is
// instruction, not user-facing copy: Lucid has already decided the plan and will
// render the card below, so the model must phrase, never restate, restructure, or
// override.
const contextHeader = "CONTEXT — Lucid has already decided today's session and will render the header, the three options, the progress panel, and the reason below. Do NOT restate them, do NOT add, drop, or change any option, and do NOT give medical or diagnostic advice — read this only to ground your phrasing."

// slotInstruction tells the model to return only the bounded phrasing slot: a
// short, warm, non-commanding note. Everything structural is Lucid's, so the
// model is asked for prose and nothing else.
const slotInstruction = "Respond with ONLY 2–4 short sentences in a warm, grounded, non-clinical voice that phrase today's session and invite the user to take whichever option fits how their body actually feels. Do not list the options (Lucid renders them below), do not name any medical condition, and never tell them what they \"should\" or \"must\" do. Write nothing but those sentences."

// ProviderBuilder constructs the model backend from a resolved provider config.
// It defaults to [factory.Build]; tests inject a builder that returns a
// [provider.Fake] so no compose test needs live vendor auth (ADR-0006).
type ProviderBuilder func(config.ProviderConfig) (provider.Provider, error)

// MetricsReader is the read-only engine projection the composer folds the streak
// and adherence from — the same fold `lucid metrics` exposes, read in-process so
// a composed message and a scripted read never diverge. It returns [engine.Metrics]
// directly rather than the router's result type so this package never imports the
// router (the two would otherwise form an import cycle — the router adapts its own
// Metrics result to satisfy this interface).
type MetricsReader interface {
	Metrics(now time.Time) (engine.Metrics, error)
}

// ObservationsReader is the read-only, bounded recent-observation seam the
// composer reads the workout/body-state history from. RecentObservations returns
// the events whose logical day falls in the [now-windowDays, now] window;
// *storage.Adapter satisfies it. It is an interface so the compose core is
// testable with a scripted fake.
type ObservationsReader interface {
	RecentObservations(now time.Time, windowDays int) ([]observations.Event, error)
}

// InjuryReader supplies the injury registry the pain guardrail reads — an active
// injury naming a targeted part is its own back-off signal. *storage.Adapter
// satisfies it via ReadRegistryKind.
type InjuryReader interface {
	ReadRegistryKind(kind string) ([]observations.Registry, error)
}

// Deps is everything a [Composer] needs, wired by the composition root (the
// router) from the concrete storage adapter and the metrics projection. The
// reader dependencies are interfaces so the compose core is testable with fakes
// and never imports the router. A nil Observations / Injuries reader leaves the
// recommendation on its plain-calendar path rather than failing it.
type Deps struct {
	Workout      config.WorkoutConfig
	Provider     config.ProviderConfig
	Metrics      MetricsReader
	Observations ObservationsReader
	Injuries     InjuryReader
	// Build overrides the provider builder; nil defaults to factory.Build.
	Build ProviderBuilder
}

// Composer is the model-allowed on-demand surface: it loads the program, reads
// the bounded recent workout/body-state slice + injuries, folds the honest engine
// numbers, runs the deterministic [Recommend] + [BuildTrend], and asks the model
// only to phrase the already-decided plan. On a provider outage or an empty reply
// it falls back to the deterministic [Render]; the model never changes the pick.
// It performs no delivery — the CLI and the daily-slot node own the send.
type Composer struct {
	workout      config.WorkoutConfig
	provider     config.ProviderConfig
	metrics      MetricsReader
	observations ObservationsReader
	injuries     InjuryReader
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
		workout:      d.Workout,
		provider:     d.Provider,
		metrics:      d.Metrics,
		observations: d.Observations,
		injuries:     d.Injuries,
		build:        build,
	}
}

// Result is one composed on-demand recommendation and how it was reached. Text is
// the rendered message. UsedLLM records the model phrased the leading note;
// Fallback records the deterministic-only path fired (the provider was
// unreachable or returned nothing usable), so the caller can still note that only
// warmth was lost. EnrichmentDegraded records that the recent-observation / injury
// read failed, so the pick fell to the plain-calendar path. Recommendation and
// Trend are the decided projection, surfaced verbatim for the --json output.
type Result struct {
	Text               string
	UsedLLM            bool
	Fallback           bool
	EnrichmentDegraded bool
	Recommendation     Recommendation
	Trend              Trend
}

// Compose builds the on-demand workout message at now. It reads the configured
// opaque program + prompt files on their explicit paths (never walking a
// directory, so no traversal into the personal program tree is possible), folds
// the honest engine numbers and the bounded recent slice, runs the deterministic
// recommender + trend, and asks the model to phrase the decided plan. A provider
// timeout / unavailable or an unusable reply renders the deterministic scaffold
// with Fallback set; every non-provider failure (a missing program or prompt
// file, an unreadable live-number read, a bad backend) is a loud error — never a
// silent empty message. The deterministic pick is identical on both paths: the
// model only adds the leading note.
func (c *Composer) Compose(ctx context.Context, now time.Time) (Result, error) {
	prog, err := LoadProgram(c.workout.Program)
	if err != nil {
		return Result{}, fmt.Errorf("workout: %w", err)
	}

	// Live numbers stay loud (the streak/adherence the trend reports); the
	// recent-slice + injury reads are enrichment and degrade quietly to the
	// plain-calendar path rather than failing the message.
	metrics, err := c.metrics.Metrics(now)
	if err != nil {
		return Result{}, fmt.Errorf("workout: read metrics: %w", err)
	}
	workouts, bodyState, injuries, degraded := c.readContext(now)

	loc := now.Location()
	rec := Recommend(RecommendInput{
		Program:        prog,
		RecentWorkouts: workouts,
		BodyState:      bodyState,
		Injuries:       injuries,
		Metrics:        metrics,
		Now:            now,
		Loc:            loc,
	})
	tr := BuildTrend(TrendInput{
		Workouts:  workouts,
		BodyState: bodyState,
		Metrics:   metrics,
		Now:       now,
		Loc:       loc,
	})
	res := Result{Recommendation: rec, Trend: tr, EnrichmentDegraded: degraded}

	systemPrompt, err := readPromptFile(c.workout.SystemPrompt)
	if err != nil {
		return Result{}, fmt.Errorf("workout: read system prompt: %w", err)
	}
	tmpl, err := readPromptFile(c.workout.Template)
	if err != nil {
		return Result{}, fmt.Errorf("workout: read template: %w", err)
	}

	prov, err := c.build(c.providerConfig())
	if err != nil {
		return Result{}, fmt.Errorf("workout: build provider: %w", err)
	}

	resp, err := prov.Complete(ctx, provider.Request{
		Intent:   intentDaily,
		System:   systemPrompt,
		Messages: []provider.Message{{Role: provider.RoleUser, Content: composeBody(tmpl, rec, tr)}},
	})
	if err != nil {
		if errors.Is(err, provider.ErrTimeout) || errors.Is(err, provider.ErrUnavailable) {
			res.Text = Render(rec, tr, now)
			res.Fallback = true
			return res, nil
		}
		return Result{}, fmt.Errorf("workout: compose daily: %w", err)
	}

	note := strings.TrimSpace(resp.Content)
	if note == "" {
		// The model returned nothing usable — render the deterministic scaffold
		// rather than deliver an empty phrasing slot.
		res.Text = Render(rec, tr, now)
		res.Fallback = true
		return res, nil
	}
	res.Text = renderWithNote(note, rec, tr, now)
	res.UsedLLM = true
	return res, nil
}

// readContext reads the bounded recent workout/body-state slice and the active
// injuries. Unlike the program, prompt, and metrics reads — which are the message
// and stay loud — these are enrichment: on any reader error the affected source is
// empty and degraded=true, and the recommender falls to its plain-calendar path
// (the missing-data rule) rather than the message failing.
func (c *Composer) readContext(now time.Time) (workouts, bodyState []observations.Event, injuries []observations.Registry, degraded bool) {
	workouts, bodyState, obsFailed := c.readRecent(now)
	injuries, injFailed := c.readInjuries()
	return workouts, bodyState, injuries, obsFailed || injFailed
}

// readRecent reads the bounded recent slice and splits it into the two kinds the
// recommender and the trend read. The window is the trend's full four-week
// look-back, wide enough for both the recovery guardrail and the trend. A nil
// reader (the seam simply unwired) yields empty slices and no degrade; a read
// error yields empty slices and failed=true.
func (c *Composer) readRecent(now time.Time) (workouts, bodyState []observations.Event, failed bool) {
	if c.observations == nil {
		return nil, nil, false
	}
	all, err := c.observations.RecentObservations(now, defaultTrendWindowDays)
	if err != nil {
		return nil, nil, true
	}
	for _, ev := range all {
		switch ev.Kind { //nolint:exhaustive // only the two workout-surface kinds are collected; every other kind is deliberately ignored
		case observations.KindWorkout:
			workouts = append(workouts, ev)
		case observations.KindBodyState:
			bodyState = append(bodyState, ev)
		}
	}
	return workouts, bodyState, false
}

// readInjuries reads the injury registry the pain guardrail reads. A nil reader
// yields none and no degrade; a read error yields none and failed=true.
func (c *Composer) readInjuries() (injuries []observations.Registry, failed bool) {
	if c.injuries == nil {
		return nil, false
	}
	inj, err := c.injuries.ReadRegistryKind(observations.RegistryInjury)
	if err != nil {
		return nil, true
	}
	return inj, false
}

// providerConfig returns the provider config for the phrasing call, applying the
// optional workout.model override (an empty override inherits provider.model).
func (c *Composer) providerConfig() config.ProviderConfig {
	pc := c.provider
	if c.workout.Model != "" {
		pc.Model = c.workout.Model
	}
	return pc
}

// composeBody assembles the single authorized user message the model phrases
// from: the operator's template (their voice), a compact deterministic digest of
// the decided plan (so the model grounds its note without restating it), and the
// slot instruction. The model returns only the phrasing slot; Lucid renders the
// scaffold.
func composeBody(tmpl string, rec Recommendation, tr Trend) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(tmpl, "\n"))
	b.WriteString("\n\n")
	b.WriteString(contextHeader)
	b.WriteString("\n\n")
	b.WriteString(planDigest(rec, tr))
	b.WriteString("\n\n")
	b.WriteString(slotInstruction)
	return b.String()
}

// planDigest renders the decided plan as a compact, one-line-per-fact block for
// the model to ground on — never the rendered scaffold (the model must not
// restate it). It reuses the same deterministic offering/trend helpers the
// message renders from, so the digest and the delivered card can never drift.
func planDigest(rec Recommendation, tr Trend) string {
	lines := []string{
		"Today's pick: " + cardOffering(rec.Primary),
		"Easier option: " + cardOffering(rec.Fallback),
		"Back-off option: " + backOffOffering(rec),
	}
	if reason := strings.TrimSpace(rec.Reason); reason != "" {
		lines = append(lines, "Why: "+reason)
	}
	lines = append(lines, "Progress: "+strings.Join(progressDigest(tr), "; "))
	return strings.Join(lines, "\n")
}

// progressDigest renders the trend as compact model-facing phrases, reusing the
// render helpers so the digest numbers match the rendered panel exactly.
func progressDigest(tr Trend) []string {
	out := []string{streakLine(tr.Streak), frequencyLine(tr)}
	if body := bodyResponseLine(tr.BodyResponse); body != "" {
		out = append(out, body)
	}
	return out
}

// renderWithNote leads the deterministic scaffold with the model's bounded
// phrasing note. The spine is [Render] verbatim, so the message renders byte-
// identically to the deterministic path with the note simply prepended — the
// model contributes the note and nothing else, and it can never restructure the
// card or add a fourth door. An empty note falls back to
// the pure spine.
func renderWithNote(note string, rec Recommendation, tr Trend, now time.Time) string {
	spine := Render(rec, tr, now)
	note = strings.TrimSpace(note)
	if note == "" {
		return spine
	}
	return fmt.Sprintf("%s %s\n\n%s", emojiCoach, note, spine)
}

// readPromptFile reads one explicit, opaque prompt file. It opens exactly the
// configured path and never walks a directory, so no traversal into the personal
// template tree is possible — the config block is the whole firewall seam, the
// same shape the program loader and the companion draw. An empty path is a
// configuration error surfaced here rather than an empty read.
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
