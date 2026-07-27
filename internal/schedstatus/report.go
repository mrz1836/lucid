// Package schedstatus is the pure core of `lucid scheduler status` — the
// read-only health surface that answers one plain question: "is the autonomous
// Lucid scheduler healthy, what fires next, and what happened last?"
//
// Its classification core — [Assemble], [Report], and the renderers (report.go,
// render.go) — is deliberately pure: it performs no filesystem, database, host,
// or model access, imports only the standard library, and turns an already-
// gathered [Inputs] into a fully classified [Report]. The impure gathering lives
// beside it in separate files (gather.go opens the disposable job DBs read-only,
// reads the companion receipts, and reads lucid.json / chain.json; host.go +
// host_*.go run the best-effort host/supervisor probe) and produces the [Inputs]
// and host [Check]s the pure core consumes. Keeping every classification pure
// makes the whole verdict matrix unit-testable with synthetic inputs, and no
// model or provider is reachable from here, so the package never enters the pure
// Engine scheduler's import graph.
package schedstatus

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CheckState is the health of a single check. The four states mirror the
// documented verdict contract: a positively-good check is [Ok], a benign-but-
// noteworthy condition is [Warn], a real problem is [Error], and a check the
// current platform cannot inspect is [Unknown]. Only [Warn] and [Error] lower
// the overall verdict — [Unknown] never does, so a non-macOS host or an
// unreadable supervisor can never masquerade as healthy nor as broken.
type CheckState string

// The four check states. Their string values are the stable tokens that appear
// in the JSON `state` field and in the top-level `verdict`.
const (
	Ok      CheckState = "ok"
	Warn    CheckState = "warn"
	Error   CheckState = "error"
	Unknown CheckState = "unknown"
)

// The periodic slugs the scheduler registers, mirrored from internal/schedrun
// (teeth) and internal/companion. They are duplicated here as exported
// constants rather than imported so the pure report core stays dependency-free;
// a divergence would surface as a "not registered" check, which is the honest
// signal anyway.
const (
	SlugBell             = "lucid-bell"
	SlugBellFallback     = "lucid-bell-fallback"
	SlugTripwire         = "lucid-tripwire"
	SlugCompanionMorning = "lucid-companion-morning"
	SlugCompanionNight   = "lucid-companion-night"
)

// The two repair levers a fault line names, so a report is actionable without a
// second lookup: a parked periodic is re-armed through the binary
// (usage/commands.md §"scheduler reconcile" — the durable job store is never
// hand-edited), and a periodic the store has no row for at all is seeded by one
// daemon start.
const (
	cmdReconcile = "lucid scheduler reconcile"
	cmdRun       = "lucid scheduler run"
)

// staleCursorGrace is how far behind an *active* periodic's next run may fall
// before the report reads its cursor as stuck rather than merely due. It mirrors
// the identical constant in internal/schedrun, so `status` and `reconcile` agree
// about what "parked" means: a live scheduler advances a fired periodic past now
// on its next tick, and a stopped host leaves every cursor behind, so only a full
// day behind — an entire daily occurrence elapsed with nothing advancing it —
// reads as stuck.
const staleCursorGrace = 24 * time.Hour

// dateLayout is the logical-day format companion receipts are stamped with
// (YYYY-MM-DD), the same basis the miss check compares an elapsed window's
// expected date against.
const dateLayout = "2006-01-02"

// Check is one classified health signal: a stable machine name, its state, and
// a plain-language detail a human can act on. Every non-[Ok] check contributes
// its detail to the rendered issues block.
type Check struct {
	Name   string     `json:"name"`
	State  CheckState `json:"state"`
	Detail string     `json:"detail,omitempty"`
}

// PromptPath is one companion prompt file the compose worker reads: which role
// it fills, the configured path, and whether that path exists on disk. Only the
// path and existence are ever reported — the file body is never read (a missing
// prompt would fail compose, so its absence is what matters, not its contents).
type PromptPath struct {
	Role   string `json:"role"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// CompanionInfo is the lucid.json companion/provider block as the status surface
// reports it: whether the daily companion is enabled, the provider backend and
// model its compose call uses, and each configured prompt path with an
// existence marker.
type CompanionInfo struct {
	Enabled         bool         `json:"enabled"`
	ProviderBackend string       `json:"provider_backend"`
	ProviderModel   string       `json:"provider_model"`
	Prompts         []PromptPath `json:"prompts"`
}

// ChainMarks are the two deterministic fire times inherited from chain.json:
// the evening bell (which drives the night companion) and the morning tripwire
// (which drives the morning companion). They are reported so the miss check and
// the reader both see when each window is scheduled to fire.
type ChainMarks struct {
	BellTime     string `json:"bell_time"`
	TripwireTime string `json:"tripwire_time"`
}

// PeriodicStatus is one scheduled job definition as read from a disposable job
// DB (or a synthetic placeholder for a required-but-absent slug). Present is
// false when the slug is expected but not registered — the case that flips a
// required periodic to a missing-periodic error.
//
// Its Note field is set by classification rather than by the gatherer: it
// carries the short reason an *intended-inactive* periodic is off (a
// companion-suppressed bell), so a deliberately-quiet send reads as designed
// rather than as a fault. It is empty for every periodic whose state speaks for
// itself.
type PeriodicStatus struct {
	Slug        string    `json:"slug"`
	Cron        string    `json:"cron,omitempty"`
	Active      bool      `json:"active"`
	Present     bool      `json:"present"`
	Note        string    `json:"note,omitempty"`
	NextRun     time.Time `json:"next_run,omitzero"`
	LastEnqueue time.Time `json:"last_enqueue,omitzero"`
}

// ReceiptStatus is the last companion delivery receipt for one window. Present
// is false when no receipt file exists yet (a window that has never fired), in
// which case the other fields are zero. Date is the logical day the send
// belongs to, which the miss check compares against the most-recent elapsed
// window's expected date.
type ReceiptStatus struct {
	Window      string `json:"window"`
	Present     bool   `json:"present"`
	Date        string `json:"date,omitempty"`
	MessageID   string `json:"message_id,omitempty"`
	Verified    bool   `json:"verified"`
	DeliveredAt string `json:"delivered_at,omitempty"`
}

// RunFailure is one recently-discarded job paired with the error that ended it,
// summarized from a job DB's failure view. A cluster of these is how a retry
// storm or a run of Discord timeouts becomes visible.
type RunFailure struct {
	Kind        string `json:"kind"`
	ErrorClass  string `json:"error_class,omitempty"`
	Message     string `json:"message,omitempty"`
	FinalizedAt string `json:"finalized_at,omitempty"`
}

// RunSummary is the bounded recent-run rollup across both job DBs: the failures
// worth surfacing and their count. An empty summary is the healthy case.
type RunSummary struct {
	Failures     []RunFailure `json:"failures"`
	FailureCount int          `json:"failure_count"`
}

// DBInput is the gathered state of one disposable job DB, handed to [Assemble].
// Exactly one of the failure conditions may hold: Err is non-empty when the DB
// could not be opened or read (malformed/locked), Missing is true when the file
// does not exist, and otherwise the DB was read cleanly and Periodics/Failures
// carry its contents. The gatherer never panics on a missing DB — it sets
// Missing and lets [Assemble] classify it.
type DBInput struct {
	Path      string
	Missing   bool
	Err       string
	Periodics []PeriodicStatus
	Failures  []RunFailure
}

// Inputs is everything the impure CLI layer gathers, ready for pure
// classification. [Assemble] consumes it plus the current time and returns a
// classified [Report]; nothing in this struct requires I/O to interpret.
type Inputs struct {
	Companion     CompanionInfo
	Chain         ChainMarks
	Teeth         DBInput
	CompanionJobs DBInput
	Receipts      []ReceiptStatus
	Host          []Check
}

// DBReport is one job DB as classified for output: its resolved path, whether it
// read cleanly, and the periodics it holds (including synthetic Present:false
// placeholders for required-but-absent slugs).
type DBReport struct {
	Path      string           `json:"path"`
	State     CheckState       `json:"state"`
	Detail    string           `json:"detail,omitempty"`
	Periodics []PeriodicStatus `json:"periodics"`
}

// Report is the fully-classified status document. Verdict is the rolled-up
// health (`ok`/`warn`/`error`) and mirrors [Report.ExitCode]; Checks is the flat
// list of every classification that fed the verdict (host checks live in Host,
// not here); the remaining fields carry the display sections. The JSON tags are
// the stable machine contract.
type Report struct {
	Verdict       string          `json:"verdict"`
	Companion     CompanionInfo   `json:"companion"`
	Chain         ChainMarks      `json:"chain"`
	Teeth         DBReport        `json:"teeth"`
	CompanionJobs DBReport        `json:"companion_jobs"`
	Receipts      []ReceiptStatus `json:"receipts"`
	Runs          RunSummary      `json:"runs"`
	Host          []Check         `json:"host"`
	Checks        []Check         `json:"checks"`
}

// ExitCode maps the verdict to the 3-tier process exit code the command returns,
// identical in text and JSON output: 0 for ok, 1 for warn, 2 for error. A health
// cron or agent can gate on this alone without parsing the JSON.
func (r Report) ExitCode() int {
	switch CheckState(r.Verdict) {
	case Error:
		return 2
	case Warn:
		return 1
	case Ok, Unknown:
		return 0
	default:
		return 0
	}
}

// worst returns the most severe state across the checks: Error dominates, then
// Warn; Unknown and Ok never lower the result. It is the whole verdict-rollup
// rule in one place.
func worst(groups ...[]Check) CheckState {
	result := Ok
	for _, g := range groups {
		for _, c := range g {
			switch c.State {
			case Error:
				return Error
			case Warn:
				result = Warn
			case Ok, Unknown:
				// neither lowers the verdict
			}
		}
	}
	return result
}

// Assemble applies the health-verdict thresholds to the gathered inputs and
// returns a classified report. The rules, in one place:
//
//   - companion disabled                                   -> warn
//   - missing/malformed teeth job DB                       -> error (always)
//   - missing/malformed companion job DB (companion on)    -> error
//   - missing companion job DB (companion off)             -> not a fault (expected)
//   - enabled companion with a missing prompt file         -> error
//   - required periodic inactive/missing (companion on)    -> error
//   - intended-active teeth periodic parked (off, or its
//     cursor stuck a full day behind)                      -> error, naming the repair command
//   - teeth bell inactive while the companion owns the send-> not a fault (suppressed, intended)
//   - evening backstop inactive while the companion owns
//     the send                                             -> error, naming the repair command
//   - latest receipt present but unverified                -> warn
//   - most-recent elapsed window with no/stale receipt     -> error
//   - host: daemon down / not supervised                   -> error (from probe)
//   - host: on-disk build newer than running daemon        -> warn  (from probe)
//   - host: launchd/Hush uninspectable                     -> unknown (from probe)
//
// Host checks arrive already classified from the best-effort probe (so the
// platform-specific logic stays out of this pure core); Assemble folds them into
// the verdict via [worst], where Unknown host checks are inert.
func Assemble(in Inputs, now time.Time) Report {
	r := Report{
		Companion: in.Companion,
		Chain:     in.Chain,
		Receipts:  in.Receipts,
		Host:      in.Host,
		Runs: RunSummary{
			Failures:     appendFailures(in.Teeth.Failures, in.CompanionJobs.Failures),
			FailureCount: len(in.Teeth.Failures) + len(in.CompanionJobs.Failures),
		},
	}

	var checks []Check
	checks = append(checks, classifyCompanion(in.Companion)...)

	// Teeth DB is always expected; the companion DB is only expected when the
	// companion is enabled (a disabled companion never creates its job store).
	var teethChecks, compChecks []Check
	r.Teeth, teethChecks = classifyDB(in.Teeth, "teeth", true)
	r.CompanionJobs, compChecks = classifyDB(in.CompanionJobs, "companion", in.Companion.Enabled)
	checks = append(checks, teethChecks...)
	checks = append(checks, compChecks...)

	// Periodic checks only make sense once the DB actually read.
	if r.Teeth.State == Ok {
		checks = append(checks, classifyTeethPeriodics(&r.Teeth, in.Companion.Enabled, now)...)
	}
	if in.Companion.Enabled && r.CompanionJobs.State == Ok {
		checks = append(checks, classifyCompanionPeriodics(&r.CompanionJobs)...)
	}

	// Receipts are a companion concept — only checked when it is enabled.
	if in.Companion.Enabled {
		checks = append(checks, classifyReceipts(in.Receipts, in.Chain, now)...)
	}

	r.Checks = checks
	r.Verdict = string(worst(checks, in.Host))
	return r
}

// classifyCompanion reports the enabled/disabled state and, when enabled, an
// error per missing prompt file (a compose call would fail without it).
func classifyCompanion(c CompanionInfo) []Check {
	if !c.Enabled {
		return []Check{{
			Name:   "companion.enabled",
			State:  Warn,
			Detail: "companion is disabled — no morning/night sends will be composed or delivered",
		}}
	}
	checks := []Check{{Name: "companion.enabled", State: Ok, Detail: "companion enabled"}}
	for _, p := range c.Prompts {
		if !p.Exists {
			checks = append(checks, Check{
				Name:   "companion.prompt." + p.Role,
				State:  Error,
				Detail: fmt.Sprintf("%s prompt file is missing: %s", p.Role, p.Path),
			})
		}
	}
	return checks
}

// classifyDB turns a gathered DBInput into its report and any DB-level check. A
// malformed DB is always an error; a missing DB is an error only when required
// (the teeth DB always, the companion DB when the companion is enabled). A
// missing-but-not-required DB is reported as Unknown with an explanatory detail
// and contributes no verdict-lowering check.
func classifyDB(in DBInput, name string, required bool) (DBReport, []Check) {
	rep := DBReport{Path: in.Path, Periodics: in.Periodics, State: Ok}
	switch {
	case in.Err != "":
		rep.State = Error
		rep.Detail = "unreadable: " + in.Err
		return rep, []Check{{
			Name:   name + ".db",
			State:  Error,
			Detail: fmt.Sprintf("%s job store at %s is unreadable: %s", name, in.Path, in.Err),
		}}
	case in.Missing && required:
		rep.State = Error
		rep.Detail = "missing — scheduler not initialized"
		return rep, []Check{{
			Name:   name + ".db",
			State:  Error,
			Detail: fmt.Sprintf("%s job store missing at %s — scheduler not initialized", name, in.Path),
		}}
	case in.Missing:
		rep.State = Unknown
		rep.Detail = "not created (companion disabled)"
		return rep, nil
	default:
		return rep, nil
	}
}

// teethExpectation is one teeth periodic paired with what configuration says
// about it: the label a human reads, whether it is *intended active*, and — for
// the two evening periodics that legitimately take turns — the note explaining
// why it is off, so a deliberately-quiet send reads as designed.
//
// The want flags mirror internal/schedrun's intended-active guard exactly
// (usage/commands.md §"The intended-active guard"), which is what lets a fault
// line promise `reconcile` will fix it: this report and that repair pass derive
// the same answer from the same configuration.
type teethExpectation struct {
	slug       string
	label      string
	want       bool
	offNote    string
	absentNote string
}

// teethExpectations resolves what each teeth periodic should be doing for the
// current evening-window ownership. The morning tripwire is unconditional — it is
// the escalation ladder's dead-man. The bell and its backstop move in opposite
// directions: whichever one is not delivering the evening is intended inactive,
// never parked.
func teethExpectations(companionEnabled bool) []teethExpectation {
	return []teethExpectation{
		{slug: SlugTripwire, label: "morning tripwire", want: true},
		{
			slug: SlugBell, label: "evening bell", want: !companionEnabled,
			offNote:    "suppressed by companion (intended)",
			absentNote: "not registered — the companion owns the evening send (intended)",
		},
		{
			slug: SlugBellFallback, label: "evening backstop", want: companionEnabled,
			offNote:    "stood down — the bell itself owns the evening send (intended)",
			absentNote: "not registered — the bell itself owns the evening send (intended)",
		},
	}
}

// classifyTeethPeriodics checks the teeth job DB against those expectations. An
// intended-active periodic that is parked — switched off, or with its cursor
// stuck a full day behind — is the silent failure the whole repair path exists
// for, so it is an error whose detail names the exact command that fixes it. An
// intended-inactive periodic is reported Ok with the reason it is off, never as a
// fault.
func classifyTeethPeriodics(rep *DBReport, companionEnabled bool, now time.Time) []Check {
	expectations := teethExpectations(companionEnabled)
	checks := make([]Check, 0, len(expectations))
	for _, exp := range expectations {
		checks = append(checks, classifyTeethPeriodic(rep, exp, now)...)
	}
	return checks
}

// classifyTeethPeriodic classifies one teeth periodic and annotates its row. A
// slug the store has no row for is appended as a Present:false placeholder so it
// renders visibly rather than vanishing from the list.
func classifyTeethPeriodic(rep *DBReport, exp teethExpectation, now time.Time) []Check {
	name := "periodic." + exp.slug
	i, found := indexOfPeriodic(rep.Periodics, exp.slug)
	if !found {
		rep.Periodics = append(rep.Periodics, PeriodicStatus{Slug: exp.slug, Present: false, Note: exp.absentNote})
		if !exp.want {
			return intendedInactiveCheck(name, exp.label, exp.slug, exp.absentNote)
		}
		return []Check{{
			Name:  name,
			State: Error,
			Detail: fmt.Sprintf("%s (%s) is not registered — start the daemon once to seed it; %s",
				exp.label, exp.slug, remedy(cmdRun, "")),
		}}
	}

	p := rep.Periodics[i]
	if !exp.want {
		// An intended-inactive periodic that is nevertheless active is left
		// unannotated: the row already reads "active", and this report classifies
		// state rather than adjudicating which sender owns the window.
		if p.Active {
			return nil
		}
		rep.Periodics[i].Note = exp.offNote
		return intendedInactiveCheck(name, exp.label, exp.slug, exp.offNote)
	}

	switch {
	case !p.Active:
		return []Check{{
			Name:  name,
			State: Error,
			Detail: fmt.Sprintf("%s (%s) is inactive — the send will never fire again; %s",
				exp.label, exp.slug, remedy(cmdReconcile, exp.slug)),
		}}
	case stuckCursor(p, now):
		return []Check{{
			Name:  name,
			State: Error,
			Detail: fmt.Sprintf("%s (%s) is active but its next run is stuck at %s — nothing is advancing it; %s",
				exp.label, exp.slug, p.NextRun.Format(timeLayout), remedy(cmdReconcile, exp.slug)),
		}}
	default:
		return nil
	}
}

// intendedInactiveCheck is the positive "off on purpose" signal: an Ok check
// carrying the reason, so the state is visible in the JSON check list without
// ever lowering the verdict. A blank note yields no check.
func intendedInactiveCheck(name, label, slug, note string) []Check {
	if note == "" {
		return nil
	}
	return []Check{{
		Name:   name,
		State:  Ok,
		Detail: fmt.Sprintf("%s (%s) is %s", label, slug, note),
	}}
}

// stuckCursor reports whether an active periodic's next run has fallen far
// enough behind that nothing is advancing it (see staleCursorGrace). A periodic
// with no recorded cursor is never called stuck — an unknown next run is not
// evidence of a frozen one.
func stuckCursor(p PeriodicStatus, now time.Time) bool {
	if p.NextRun.IsZero() {
		return false
	}
	return p.NextRun.Before(now.Add(-staleCursorGrace))
}

// remedy renders the repair instruction a fault line ends with, narrowed to one
// slug when the command accepts it.
func remedy(cmd, slug string) string {
	if slug == "" {
		return "run: " + cmd
	}
	return "run: " + cmd + " --slug " + slug
}

// classifyCompanionPeriodics checks the companion job DB's morning and night
// periodics, both required active whenever the companion is enabled. They live in
// the companion's own job store, which the teeth repair lever does not own, so
// their faults name no reconcile command.
func classifyCompanionPeriodics(rep *DBReport) []Check {
	checks := requireActive(rep, SlugCompanionMorning, "morning companion")
	return append(checks, requireActive(rep, SlugCompanionNight, "night companion")...)
}

// requireActive locates slug among the report's periodics and faults it when the
// store does not have it running. A missing slug is recorded as a Present:false
// placeholder so it renders visibly; a present slug that is inactive is flagged.
func requireActive(rep *DBReport, slug, label string) []Check {
	p, found := findPeriodic(rep.Periodics, slug)
	switch {
	case !found:
		rep.Periodics = append(rep.Periodics, PeriodicStatus{Slug: slug, Present: false})
		return []Check{{
			Name:   "periodic." + slug,
			State:  Error,
			Detail: fmt.Sprintf("%s (%s) is not registered", label, slug),
		}}
	case !p.Active:
		return []Check{{
			Name:   "periodic." + slug,
			State:  Error,
			Detail: fmt.Sprintf("%s (%s) is inactive", label, slug),
		}}
	default:
		return nil
	}
}

// classifyReceipts flags every present-but-unverified receipt as a warning and
// the most-recent already-elapsed window as an error when it has no receipt or a
// stale one (a date that does not match that window's expected logical day —
// the same real miss the todo's stale-receipt criterion names).
func classifyReceipts(receipts []ReceiptStatus, chain ChainMarks, now time.Time) []Check {
	var checks []Check
	for _, rec := range receipts {
		if rec.Present && !rec.Verified {
			checks = append(checks, Check{
				Name:   "companion.receipt." + rec.Window + ".verified",
				State:  Warn,
				Detail: fmt.Sprintf("%s receipt (%s) is unverified — a read-back did not confirm delivery", rec.Window, rec.Date),
			})
		}
	}

	w, ok := mostRecentElapsedWindow(now, chain)
	if !ok {
		return checks
	}
	expected := w.fireAt.Format(dateLayout)
	rec, present := receiptFor(receipts, w.window)
	switch {
	case !present:
		checks = append(checks, Check{
			Name:   "companion.receipt." + w.window,
			State:  Error,
			Detail: fmt.Sprintf("most recent %s window (%s) has no delivery receipt — the send appears to have been missed", w.window, expected),
		})
	case rec.Date != expected:
		checks = append(checks, Check{
			Name:   "companion.receipt." + w.window,
			State:  Error,
			Detail: fmt.Sprintf("most recent %s window (%s) has only a stale receipt (last delivered %s) — the current send appears to have been missed", w.window, expected, rec.Date),
		})
	}
	return checks
}

// windowFire pairs a companion window with the most recent time it was scheduled
// to fire at or before now.
type windowFire struct {
	window string
	fireAt time.Time
}

// mostRecentElapsedWindow returns the companion window whose last scheduled fire
// is the most recent one at or before now. The morning window inherits the
// tripwire time and the night window the bell time; a window whose mark cannot
// be parsed is skipped. ok is false when neither mark is parseable.
func mostRecentElapsedWindow(now time.Time, chain ChainMarks) (windowFire, bool) {
	var best windowFire
	var have bool
	for _, cand := range []struct {
		window string
		hm     string
	}{
		{"morning", chain.TripwireTime},
		{"night", chain.BellTime},
	} {
		fireAt, ok := lastFire(now, cand.hm)
		if !ok {
			continue
		}
		if !have || fireAt.After(best.fireAt) {
			best = windowFire{window: cand.window, fireAt: fireAt}
			have = true
		}
	}
	return best, have
}

// lastFire returns the most recent occurrence of the HH:MM mark at or before
// now: today's occurrence if it has already elapsed, otherwise yesterday's. ok
// is false when the mark is not a valid HH:MM.
func lastFire(now time.Time, hm string) (time.Time, bool) {
	h, m, ok := parseHM(hm)
	if !ok {
		return time.Time{}, false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if today.After(now) {
		return today.AddDate(0, 0, -1), true
	}
	return today, true
}

// parseHM parses a strict "HH:MM" 24-hour mark. It rejects anything out of range
// so an unparseable chain mark degrades to "skip the miss check" rather than a
// bogus window time.
func parseHM(hm string) (hour, minute int, ok bool) {
	parts := strings.SplitN(hm, ":", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || h < 0 || h > 23 {
		return 0, 0, false
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// findPeriodic returns the periodic with the given slug and whether it was
// found. It only considers entries actually present in the DB.
func findPeriodic(ps []PeriodicStatus, slug string) (PeriodicStatus, bool) {
	if i, ok := indexOfPeriodic(ps, slug); ok {
		return ps[i], true
	}
	return PeriodicStatus{}, false
}

// indexOfPeriodic returns the position of the present periodic with the given
// slug, so a classifier can annotate the row in place rather than a copy.
func indexOfPeriodic(ps []PeriodicStatus, slug string) (int, bool) {
	for i := range ps {
		if ps[i].Slug == slug && ps[i].Present {
			return i, true
		}
	}
	return 0, false
}

// receiptFor returns the present receipt for a window, or false when none.
func receiptFor(rs []ReceiptStatus, window string) (ReceiptStatus, bool) {
	for _, r := range rs {
		if r.Window == window && r.Present {
			return r, true
		}
	}
	return ReceiptStatus{}, false
}

// appendFailures concatenates the two per-DB failure slices into one bounded
// list without mutating either input.
func appendFailures(a, b []RunFailure) []RunFailure {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make([]RunFailure, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}
