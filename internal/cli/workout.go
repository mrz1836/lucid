package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mrz1836/lucid/internal/notify"
	"github.com/mrz1836/lucid/internal/router"
	"github.com/mrz1836/lucid/internal/workout"
)

// Workout `log` flag names. The content flags carry the structured capture; the
// spoken form is the positional drop or --text. They are declared as constants
// so the "spoken vs structured, not both" guard can enumerate them in one place.
const (
	flagWType       = "type"
	flagWMovements  = "movements"
	flagWDuration   = "duration"
	flagWRPE        = "rpe"
	flagWParts      = "parts"
	flagWSoreness   = "soreness"
	flagWPain       = "pain"
	flagWAnchor     = "anchor"
	flagWAnchorItem = "anchor-item"
	flagWNotes      = "notes"
	flagWNotesFile  = "notes-file"
	flagWText       = "text"
)

// workoutAmendVerb prefixes every `lucid workout amend` refusal on stderr
// (error-states.md W-10..W-18), so the reason always names the verb that
// refused it, whether the CLI or the router decided it.
const workoutAmendVerb = "lucid workout amend"

// Flags on `lucid workout fire`. --deliver actually sends; the default is a
// dry-run compose with zero side effect. --dry-run is accepted explicitly so a
// script can be unambiguous; it is mutually exclusive with --deliver.
const (
	workoutFlagDeliver = "deliver"
	workoutFlagDryRun  = "dry-run"
)

// scaleMax is the inclusive upper bound of every 0–10 reading a workout log
// accepts (rpe, soreness, pain). An out-of-range value is a usage error, never
// silently clamped.
const scaleMax = 10

// newWorkoutCmd wires `lucid workout`: the config-gated workout companion's
// command group. A bare `lucid workout` composes the on-demand recommendation
// (deterministic pick, model-phrased delivery, deterministic fallback); the
// `log` child captures a completed session; the `amend` child corrects a logged
// session append-only; the `fire` child composes (and
// optionally delivers) one daily-slot message on demand — the same idempotent,
// read-back-verified path the scheduled daily slot takes.
func newWorkoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workout",
		Short: "Recommend, log, and review your training (config-gated)",
		Long: `lucid workout is the workout companion: it recommends today's session,
records what actually happened, and reviews progress over time. The feature is
config-gated and off by default; enable it by adding a workout block to
lucid.json and the workout/body_state kinds to observations/config.json.

Run bare, it composes today's recommendation on demand — a deterministic core
picks and vetoes the session (rotation, recovery windows, pain hard stops) and
the model only phrases it, so the message still renders with the provider down.
--json emits the decided recommendation and trend instead of the rendered text.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkout(cmd)
		},
	}
	cmd.AddCommand(newWorkoutLogCmd())
	cmd.AddCommand(newWorkoutAmendCmd())
	cmd.AddCommand(newWorkoutFireCmd())
	return cmd
}

// newWorkoutFireCmd wires `lucid workout fire [--dry-run|--deliver]`. The
// scheduled daily slot runs inside `lucid scheduler run`; this verb is the
// operator's way to compose (and optionally deliver) one slot message now — to
// preview the message, prove the pipeline end to end, or re-send after a miss.
// A dry-run composes and prints with zero side effect (no delivery, no receipt);
// --deliver posts one idempotent, read-back-verified message to the user channel
// through the same [workout.Runner] the scheduled slot uses, so a delivered test
// fire honors the missed-fire window and the delivery receipt exactly as a real
// fire would.
func newWorkoutFireCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fire",
		Short: "Compose the daily workout message now (dry-run by default)",
		Long: `fire composes one workout-slot message immediately. By default it is a
dry-run: it composes and prints the message and touches nothing (no send, no
delivery receipt). Pass --deliver to actually post one idempotent, read-back-
verified message to the user channel — the same path the scheduled daily slot
takes, so a delivered test fire honors the missed-fire window and the delivery
receipt exactly as a real fire would.`,
		Args: cobra.NoArgs,
		Example: `  # Preview today's workout message without sending it.
  lucid workout fire

  # Actually deliver one workout message now.
  lucid workout fire --deliver`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deliver, _ := cmd.Flags().GetBool(workoutFlagDeliver)
			return runWorkoutFire(cmd, deliver)
		},
	}
	cmd.Flags().Bool(workoutFlagDeliver, false, "Deliver the message (default: dry-run compose with no side effect)")
	cmd.Flags().Bool(workoutFlagDryRun, false, "Compose and print without delivering (the default)")
	cmd.MarkFlagsMutuallyExclusive(workoutFlagDeliver, workoutFlagDryRun)
	return cmd
}

// runWorkoutFire boots the Ledger + router and either captures a dry-run (the
// deterministic pick, model-phrased, printed with no side effect) or delivers
// one idempotent slot message through the shared [workout.Runner]. The deliver
// path needs the env-injected Discord transport (the credential-dumb notifier —
// token + channel come from the environment only).
func runWorkoutFire(cmd *cobra.Command, deliver bool) error {
	r, err := bootedRouter(cmd)
	if err != nil {
		return err
	}
	cfg := r.Config()
	if !cfg.Workout.Enabled {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: workout.enabled is false — the scheduler will not fire this automatically")
	}

	if !deliver {
		p, perr := buildProvider(cfg.Provider)
		if perr != nil {
			return perr
		}
		res, wErr := r.Workout(cmd.Context(), clockNow(), p)
		if wErr != nil {
			return wErr
		}
		return renderWorkoutDryRun(cmd.OutOrStdout(), res)
	}

	discord, err := notify.NewDiscordFromEnv()
	if err != nil {
		return fmt.Errorf("lucid workout fire: %w", err)
	}
	runner := workout.NewRunner(workout.Deps{
		Workout:      cfg.Workout,
		Provider:     cfg.Provider,
		Metrics:      r.WorkoutMetrics(),
		Observations: r.Store(),
		Injuries:     r.Store(),
		Build:        buildProvider,
	}, discord, r.Store())
	out, err := runner.Fire(cmd.Context(), clockNow())
	if err != nil {
		return err
	}
	return renderWorkoutFire(cmd.OutOrStdout(), out)
}

// renderWorkoutDryRun prints a composed slot message for a person to read,
// naming the deterministic-fallback and enrichment-degraded paths when they
// fired so a preview is never mistaken for the model's warm output when it was
// not.
func renderWorkoutDryRun(out io.Writer, res router.WorkoutResult) error {
	_, _ = fmt.Fprintln(out, "── workout (dry-run — not delivered) ──")
	if res.Fallback {
		_, _ = fmt.Fprintln(out, "[deterministic fallback — the provider was unreachable; only the phrasing warmth is lost]")
	}
	if res.EnrichmentDegraded {
		_, _ = fmt.Fprintln(out, "[enrichment degraded — recent workout/body-state history could not be read; today follows the plain program calendar]")
	}
	_, _ = fmt.Fprintln(out, res.Text)
	return nil
}

// renderWorkoutFire reports how a real delivery resolved: a skip (idempotent or
// past the cut-off) or a delivered message id, noting a late-note or fallback
// delivery.
func renderWorkoutFire(out io.Writer, o workout.Outcome) error {
	switch {
	case o.Skipped:
		_, _ = fmt.Fprintf(out, "workout slot skipped (%s).\n", o.SkipReason)
	case o.Delivered:
		note := ""
		if o.Late {
			note += " (late note prepended)"
		}
		if o.Fallback {
			note += " (deterministic fallback)"
		}
		_, _ = fmt.Fprintf(out, "workout delivered%s — message %s.\n", note, o.MessageID)
	}
	return nil
}

// workoutRecommendationJSON is the --json projection of the on-demand surface:
// the decided pick, the read-only trend, and today's daily anchor, exactly the
// deterministic core's output so a harness reads the same recommendation the
// message renders — plus the logged sessions that decision read, each with its
// `workout amend` corrections folded in, so a corrected value reads straight
// back. Every key is additive; a reader skips the ones it doesn't know.
type workoutRecommendationJSON struct {
	Recommendation workout.Recommendation `json:"recommendation"`
	Trend          workout.Trend          `json:"trend"`
	Anchor         workout.Anchor         `json:"anchor"`
	Sessions       []workout.SessionView  `json:"sessions"`
}

// runWorkout composes and prints the on-demand recommendation + trend. The
// deterministic core owns the pick; the model only phrases it, and a provider
// outage still renders the message deterministically. --json emits the decided
// Recommendation/Trend/Anchor projection and the folded sessions echo instead of
// the rendered message; the degrade notes (deterministic fallback,
// enrichment-degraded) go to stderr so the piped stdout stays the clean message.
func runWorkout(cmd *cobra.Command) error {
	r, err := bootedRouter(cmd)
	if err != nil {
		return err
	}
	cfg := r.Config()
	if !cfg.Workout.Enabled {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warning: workout.enabled is false — add a workout block to lucid.json to enable the daily slot")
	}
	p, err := buildProvider(cfg.Provider)
	if err != nil {
		return err
	}
	res, err := r.Workout(cmd.Context(), clockNow(), p)
	if err != nil {
		return err
	}
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		return writeJSON(cmd.OutOrStdout(), workoutRecommendationJSON{
			Recommendation: res.Recommendation,
			Trend:          res.Trend,
			Anchor:         res.Anchor,
			Sessions:       res.Sessions,
		})
	}
	if res.Fallback {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "[deterministic fallback — the provider was unreachable; only the phrasing warmth is lost]")
	}
	if res.EnrichmentDegraded {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "[enrichment degraded — recent workout/body-state history could not be read; today follows the plain program calendar]")
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Text)
	return nil
}

// newWorkoutLogCmd wires `lucid workout log`: capture a completed session two
// ways. A spoken drop (positional text or --text) is extracted by the Workout
// Extraction agent — the voice-first default. The structured flags
// (--type/--duration/--rpe/--parts/--soreness/--pain/--anchor/--anchor-item/
// --notes/--notes-file) are the precise alternative for guided or backfill
// capture. --notes-file reads the note off the command line (a path, or - for
// stdin) through the shared free-text reader, so shell metacharacters stay
// data; it is mutually exclusive with --notes. The two
// forms are mutually exclusive so a mixed invocation never silently drops half
// the input. The daily anchor rides this same verb (the top-level `lucid anchor`
// is the milestone anchor, a different concept) so there is exactly one workout
// capture verb.
//
// --day backdates the session to the logical day it actually happened. It is
// not content, so it composes with both forms — the whole point being that "I
// did this yesterday" is a fact about the session, not a second way of
// describing it.
//
//	lucid workout log "did pull, shoulder felt fine, ~50 min"
//	lucid workout log --type push --duration 45 --rpe 7 --parts chest,shoulders
//	lucid workout log --type legs --soreness quads:5 --pain knee:7
//	lucid workout log --type push --rpe 6 --day @yesterday
//	lucid workout log "2 mile bike ride, easy" --day @yesterday
//	lucid workout log --anchor --anchor-item squats:55 --anchor-item core:50
func newWorkoutLogCmd() *cobra.Command {
	var (
		typ, text                                     string
		duration, rpe                                 int
		parts, movements, soreness, pain, anchorItems []string
		anchor                                        bool
	)
	cmd := &cobra.Command{
		Use:   "log [drop...]",
		Short: "Log a completed workout (structured flags or a spoken drop)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := bootedRouter(cmd)
			if err != nil {
				return err
			}
			spoken := strings.TrimSpace(text)
			if spoken == "" {
				spoken = strings.TrimSpace(strings.Join(args, " "))
			}
			if spoken != "" {
				if workoutContentFlagsChanged(cmd) {
					return fmt.Errorf("lucid workout log: use either a spoken drop or the structured flags, not both")
				}
				return runWorkoutLogFromText(cmd, r, spoken)
			}
			// The note may arrive off the command line; it is read once, here,
			// on the structured path only (a spoken drop with --notes-file was
			// refused above as a mixed form).
			note, err := resolveOptionalText(cmd, "workout log", "notes", flagWNotes, flagWNotesFile)
			if err != nil {
				return emitErr(cmd, err)
			}
			req, err := buildWorkoutLogRequest(cmd, workoutLogFlags{
				typ: typ, movements: movements, duration: duration, rpe: rpe,
				parts: parts, notes: note, soreness: soreness, pain: pain,
				anchor: anchor, anchorItems: anchorItems,
			})
			if err != nil {
				return err
			}
			res, err := r.WorkoutLog(req)
			if err != nil {
				return emitRefusedDay(cmd, err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&typ, flagWType, "", "Session type, e.g. push, pull, legs, run")
	f.StringSliceVar(&movements, flagWMovements, nil, "Movements performed (comma-separated)")
	f.IntVar(&duration, flagWDuration, 0, "Session duration in whole minutes")
	f.IntVar(&rpe, flagWRPE, 0, "Session RPE 0-10")
	f.StringSliceVar(&parts, flagWParts, nil, "Body parts trained (comma-separated)")
	f.StringSliceVar(&soreness, flagWSoreness, nil, "Per-part soreness as part:level, e.g. quads:5")
	f.StringSliceVar(&pain, flagWPain, nil, "Per-part pain as part:level, or a bare part to flag it")
	f.BoolVar(&anchor, flagWAnchor, false, "Log today's daily anchor as done")
	f.StringArrayVar(&anchorItems, flagWAnchorItem, nil, "Daily-anchor item as name:count, or a bare name (repeatable)")
	f.String(flagWNotes, "", "Free-text note kept verbatim on the record")
	f.String(flagWNotesFile, "", "Read the note from this file (or - for stdin) instead of --notes, so shell metacharacters stay data")
	f.StringVar(&text, flagWText, "", "Spoken drop to extract instead of structured flags")
	registerProvenanceFlags(cmd)
	registerDayFlag(cmd)
	return cmd
}

// workoutLogFlags carries the structured `log` flag values into the request
// builder, keeping the RunE closure small.
type workoutLogFlags struct {
	typ                                           string
	movements, parts, soreness, pain, anchorItems []string
	duration, rpe                                 int
	anchor                                        bool
	notes                                         string
}

// buildWorkoutLogRequest validates the structured flags and assembles the
// router request. Ranges are checked here (rpe/soreness/pain 0-10, duration
// non-negative) and rejected as usage errors rather than clamped.
func buildWorkoutLogRequest(cmd *cobra.Command, in workoutLogFlags) (router.WorkoutLogRequest, error) {
	day, _ := cmd.Flags().GetString(flagDay)
	req := router.WorkoutLogRequest{
		Type:        in.typ,
		Movements:   in.movements,
		DurationMin: in.duration,
		BodyParts:   in.parts,
		Anchor:      in.anchor,
		Notes:       in.notes,
		Now:         clockNow(),
		DayArg:      day,
		Harness:     obsHarness(cmd),
		Agent:       flagOrEnv(cmd, flagAgent, envAgent, ""),
		Model:       flagOrEnv(cmd, flagModel, envModel, ""),
		Channel:     flagOrEnv(cmd, flagChannel, envChannel, ""),
	}
	if in.duration < 0 {
		return router.WorkoutLogRequest{}, fmt.Errorf("lucid workout log: --duration must be zero or more")
	}
	if cmd.Flags().Changed(flagWRPE) {
		if in.rpe < 0 || in.rpe > scaleMax {
			return router.WorkoutLogRequest{}, fmt.Errorf("lucid workout log: --rpe must be 0-%d", scaleMax)
		}
		v := in.rpe
		req.RPE = &v
	}
	states, err := parseBodyStateFlags(in.soreness, in.pain)
	if err != nil {
		return router.WorkoutLogRequest{}, err
	}
	req.BodyStates = states
	items, err := parseAnchorItemFlags(in.anchorItems)
	if err != nil {
		return router.WorkoutLogRequest{}, err
	}
	req.AnchorItems = items
	return req, nil
}

// parseAnchorItemFlags folds the repeatable --anchor-item values into per-item
// anchor counts. Each value is `name:count` (a bare `name` records the item with
// no count — the honest shape when the user did the movement without counting).
// Repeated names merge under the first spelling with the last *stated* count
// winning, so a corrected re-entry does not double-record the item and a bare
// re-entry does not erase a number already given — the same merge the per-part
// soreness/pain flags use. Unlike the 0–10 body-state scales a count is an
// ordinary tally, bounded only by being a non-negative whole number.
func parseAnchorItemFlags(raw []string) ([]router.AnchorCount, error) {
	byName := map[string]*router.AnchorCount{}
	var order []string
	for _, value := range raw {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		head, tail, hasColon := strings.Cut(value, ":")
		name := strings.TrimSpace(head)
		if name == "" {
			return nil, fmt.Errorf("lucid workout log: --%s %q is missing an item name", flagWAnchorItem, value)
		}
		item := router.AnchorCount{Name: name}
		if tail = strings.TrimSpace(tail); hasColon && tail != "" {
			n, err := strconv.Atoi(tail)
			if err != nil {
				return nil, fmt.Errorf("lucid workout log: --%s count in %q must be a number", flagWAnchorItem, value)
			}
			if n < 0 {
				return nil, fmt.Errorf("lucid workout log: --%s count in %q must be zero or more", flagWAnchorItem, value)
			}
			item.Count, item.HasCount = n, true
		}
		key := strings.ToLower(name)
		if prev, seen := byName[key]; seen {
			if item.HasCount {
				prev.Count, prev.HasCount = item.Count, true
			}
			continue
		}
		stored := item
		byName[key] = &stored
		order = append(order, key)
	}
	out := make([]router.AnchorCount, 0, len(order))
	for _, key := range order {
		out = append(out, *byName[key])
	}
	return out, nil
}

// runWorkoutLogFromText runs the spoken capture path: build the model backend
// from the lucid.json provider block, extract, and write. The router preserves
// the raw drop when the model degrades, so a spoken capture is never lost.
func runWorkoutLogFromText(cmd *cobra.Command, r *router.Router, text string) error {
	p, err := buildProvider(r.Config().Provider)
	if err != nil {
		return err
	}
	day, _ := cmd.Flags().GetString(flagDay)
	res, err := r.WorkoutLogFromText(cmd.Context(), router.WorkoutLogTextRequest{
		Text:    text,
		Now:     clockNow(),
		DayArg:  day,
		Harness: obsHarness(cmd),
		Agent:   flagOrEnv(cmd, flagAgent, envAgent, ""),
		Model:   flagOrEnv(cmd, flagModel, envModel, ""),
		Channel: flagOrEnv(cmd, flagChannel, envChannel, ""),
	}, p)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
	return nil
}

// workoutContentFlagsChanged reports whether any structured content flag was
// set, so a spoken drop combined with structured flags is rejected rather than
// silently dropping one form.
//
// --day is deliberately absent from this list, as are the provenance flags:
// they say when and where a capture came from, not what happened, so they
// compose with either form. Adding --day here would reject the most ordinary
// backdated capture there is — `lucid workout log "2 mile bike ride" --day
// @yesterday` — as if the user had asked for two things at once.
func workoutContentFlagsChanged(cmd *cobra.Command) bool {
	for _, name := range []string{
		flagWType, flagWMovements, flagWDuration, flagWRPE, flagWParts,
		flagWSoreness, flagWPain, flagWAnchor, flagWAnchorItem, flagWNotes,
		flagWNotesFile,
	} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

// newWorkoutAmendCmd wires `lucid workout amend <obs-id> [flags]`: the
// append-only correction of a logged session (usage/workout.md §"Amending a
// logged session"). It never rewrites the session's line — it appends one new
// workout event whose refs.corrects names the session, carrying only the
// changed fields, and every read surface folds the corrections on. It is
// dispatch-only over [router.AmendWorkout]: structured flags, no spoken form,
// no model call.
//
// The CLI owns only what the router never sees: the positional shape (exactly
// one id, no free text — W-13), the deferred --soreness/--pain flags (W-17),
// the at-least-one-field guard (W-12, where --day counts), and the note's
// sources (--notes or --notes-file, read once up front because stdin drains
// once — W-18). Every value check (RPE range, non-negative duration, empty
// values, the strict-tier --day) is the router's, so a rejected amend writes
// nothing however it was reached.
func newWorkoutAmendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "amend <obs-id>",
		Short: "Correct or fill in a logged session (append-only; the original is never rewritten)",
		Long: `amend corrects a logged session after the fact — add the RPE you didn't have
at the time, fix the duration, move the session to the day it actually happened —
without logging it again (a second log writes a second session and double-counts
the day). It never edits the original line: it appends one new workout event that
names the session and carries only the fields you change, and every read surface
folds the corrections onto the session, the latest winning per field. The values
you first logged stay in the history.

Only the flags you pass change; an omitted flag keeps its value, and an empty
value is refused (amend corrects a field, it doesn't clear one). --movements and
--parts replace the whole list. --day re-dates the session — its instant and
logical day move together — and counts as a change on its own. Any id in the
chain works: the id of an earlier correction amends the session it corrects.

Soreness and pain readings aren't amendable yet, and an anchor-only capture
isn't a session to correct. Every refusal says why on stderr and writes nothing.
--json emits {event_id, target_id, logical_date, changes}.`,
		Args: cobra.ArbitraryArgs,
		Example: `  lucid workout amend obs_2026_01_15_001 --rpe 4
  lucid workout amend obs_2026_01_15_001 --duration 50 --type climbing
  lucid workout amend obs_2026_01_15_001 --parts fingers,forearms
  lucid workout amend obs_2026_01_15_001 --notes-file ./session-notes.txt
  lucid workout amend obs_2026_01_15_001 --day @yesterday --json`,
		RunE: runWorkoutAmend,
	}
	f := cmd.Flags()
	f.Int(flagWRPE, 0, "Set the session RPE 0-10")
	f.Int(flagWDuration, 0, "Set the session duration in whole minutes")
	f.String(flagWType, "", "Set the session type, e.g. push, pull, climbing")
	f.StringSlice(flagWMovements, nil, "Replace the movements list (comma-separated)")
	f.StringSlice(flagWParts, nil, "Replace the body parts trained (comma-separated)")
	f.String(flagWNotes, "", "Replace the session note")
	f.String(flagWNotesFile, "", "Replace the note with this file's contents (or - for stdin), so shell metacharacters stay data")
	registerDayFlag(cmd)
	// --soreness/--pain are parsed only so reaching for them earns the W-17
	// reason instead of a bare "unknown flag"; hidden because they are not part
	// of amend's surface yet.
	f.StringSlice(flagWSoreness, nil, "Not amendable yet")
	f.StringSlice(flagWPain, nil, "Not amendable yet")
	_ = f.MarkHidden(flagWSoreness)
	_ = f.MarkHidden(flagWPain)
	return cmd
}

// runWorkoutAmend executes `lucid workout amend`: settle the CLI-side shape and
// the note before booting (so a refusal touches nothing), then dispatch to the
// append-only amend and render its receipt or --json view. Every error reaches
// stderr prefixed `lucid workout amend:` because the root sets SilenceErrors.
func runWorkoutAmend(cmd *cobra.Command, args []string) error {
	req, err := buildWorkoutAmendRequest(cmd, args)
	if err != nil {
		return emitWorkoutAmendErr(cmd, err)
	}
	r, err := bootedRouter(cmd)
	if err != nil {
		return err
	}
	res, err := r.AmendWorkout(req)
	if err != nil {
		return emitWorkoutAmendErr(cmd, err)
	}
	return renderWorkoutAmend(cmd, res)
}

// buildWorkoutAmendRequest turns the amend invocation into a router request,
// gating each field on cobra's Changed so an omitted flag leaves that field
// untouched rather than setting it to the zero value. It refuses, before any
// read of the Ledger: a missing id or trailing words (W-13), --soreness/--pain
// (W-17), and an invocation with no field to change (W-12). The note is
// resolved here, once.
func buildWorkoutAmendRequest(cmd *cobra.Command, args []string) (router.WorkoutAmendRequest, error) {
	switch {
	case len(args) == 0:
		return router.WorkoutAmendRequest{}, fmt.Errorf("an obs id is required; nothing was saved")
	case len(args) > 1:
		return router.WorkoutAmendRequest{}, fmt.Errorf(
			"amend takes a single obs id; free text isn't a field — use flags; nothing was saved",
		)
	}
	f := cmd.Flags()
	if f.Changed(flagWSoreness) || f.Changed(flagWPain) {
		return router.WorkoutAmendRequest{}, fmt.Errorf(
			"soreness/pain aren't amendable yet; body-state amendment is a planned follow-up; nothing was saved",
		)
	}
	if !workoutAmendFieldsChanged(cmd) {
		return router.WorkoutAmendRequest{}, fmt.Errorf("no fields to amend; nothing was saved")
	}
	notes, notesChanged, err := resolveWorkoutAmendNotes(cmd)
	if err != nil {
		return router.WorkoutAmendRequest{}, err
	}

	req := router.WorkoutAmendRequest{
		ObsID:        args[0],
		Notes:        notes,
		NotesChanged: notesChanged,
		Now:          clockNow(),
	}
	req.DayArg, _ = f.GetString(flagDay)
	if req.TypeChanged = f.Changed(flagWType); req.TypeChanged {
		req.Type, _ = f.GetString(flagWType)
	}
	if req.MovementsChanged = f.Changed(flagWMovements); req.MovementsChanged {
		req.Movements, _ = f.GetStringSlice(flagWMovements)
	}
	if req.DurationChanged = f.Changed(flagWDuration); req.DurationChanged {
		req.DurationMin, _ = f.GetInt(flagWDuration)
	}
	if req.RPEChanged = f.Changed(flagWRPE); req.RPEChanged {
		req.RPE, _ = f.GetInt(flagWRPE)
	}
	if req.BodyPartsChanged = f.Changed(flagWParts); req.BodyPartsChanged {
		req.BodyParts, _ = f.GetStringSlice(flagWParts)
	}
	return req, nil
}

// workoutAmendFieldsChanged reports whether the invocation names anything to
// change. --day is a field here, unlike on `log`: a re-date alone is a complete
// amend (W-12). The deferred --soreness/--pain are refused before this runs.
func workoutAmendFieldsChanged(cmd *cobra.Command) bool {
	for _, name := range []string{
		flagWRPE, flagWDuration, flagWType, flagWMovements, flagWParts,
		flagWNotes, flagWNotesFile, flagDay,
	} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

// resolveWorkoutAmendNotes resolves the replacement note from its one source:
// --notes inline, or --notes-file through the shared free-text reader (a path,
// or - for stdin) so shell metacharacters reach the Ledger as data. Both at once
// is two sources for one field and refused; a missing, unreadable, or empty file
// is refused too (W-18). changed reports whether a note source was given at all
// — an omitted note leaves the session's note untouched.
func resolveWorkoutAmendNotes(cmd *cobra.Command) (notes string, changed bool, err error) {
	f := cmd.Flags()
	inline, fromFile := f.Changed(flagWNotes), f.Changed(flagWNotesFile)
	switch {
	case inline && fromFile:
		return "", false, fmt.Errorf(
			"give the notes via --%s or --%s, not both; nothing was saved", flagWNotes, flagWNotesFile,
		)
	case fromFile:
		path, _ := f.GetString(flagWNotesFile)
		body, rerr := readBodyFile(flagWNotesFile, path, cmd.InOrStdin())
		if rerr != nil {
			return "", false, fmt.Errorf("%w; nothing was saved", rerr)
		}
		return body, true, nil
	case inline:
		v, _ := f.GetString(flagWNotes)
		return v, true, nil
	default:
		return "", false, nil
	}
}

// emitWorkoutAmendErr prints an amend refusal to stderr prefixed with the verb
// and returns it (wrapped, so a [router.DayRejectedError] stays matchable). The
// router's reasons are bare sentences; the prefix is added only here, once.
func emitWorkoutAmendErr(cmd *cobra.Command, err error) error {
	return emitErr(cmd, fmt.Errorf("%s: %w", workoutAmendVerb, err))
}

// workoutFieldChangeView is one field's transition in the amend --json view:
// the session's folded value just before this amend (null when it was unset)
// and the value the amend recorded.
type workoutFieldChangeView struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// workoutAmendView is the machine-readable projection of a `lucid workout amend
// --json` turn: the appended correction's id, the base session it corrects (the
// base even when a later correction's id was passed), the logical day the
// correction landed on, and one {from, to} entry per changed field keyed by
// record name (rpe, duration_min, type, movements, body_parts, note; a re-date
// adds occurred_at and logical_date). Built CLI-side with stable snake_case
// names so a harness branches on fields, not prose — the workout counterpart of
// memoryAmendView.
type workoutAmendView struct {
	EventID     string                            `json:"event_id"`
	TargetID    string                            `json:"target_id"`
	LogicalDate string                            `json:"logical_date"`
	Changes     map[string]workoutFieldChangeView `json:"changes"`
}

// renderWorkoutAmend prints an amend result. A disabled workout kind prints the
// enable hint and succeeds with nothing written, exactly as `workout log` does.
// Otherwise --json emits the [workoutAmendView] (changes always a non-nil
// object), and the human path is the shared receipt tail with the inventory ack.
func renderWorkoutAmend(cmd *cobra.Command, res router.WorkoutAmendResult) error {
	if res.Rejected {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.Ack)
		return nil
	}
	if asJSON, _ := cmd.Flags().GetBool(jsonFlag); asJSON {
		changes := make(map[string]workoutFieldChangeView, len(res.Changes))
		for field, c := range res.Changes {
			changes[field] = workoutFieldChangeView{From: c.From, To: c.To}
		}
		return writeJSON(cmd.OutOrStdout(), workoutAmendView{
			EventID:     res.EventID,
			TargetID:    res.TargetID,
			LogicalDate: res.LogicalDate,
			Changes:     changes,
		})
	}
	return emitReceipt(cmd, res.EventID, res.LogicalDate, res.Ack)
}

// parseBodyStateFlags folds the --soreness and --pain flag values into per-part
// body-state inputs. Each value is `part:level` (soreness always needs a level;
// pain accepts a bare `part` as an unquantified flag recorded at
// router.PainFlagLevel). Repeated parts merge, so `--soreness knee:4 --pain
// knee:7` yields one reading carrying both scales.
func parseBodyStateFlags(soreness, pain []string) ([]router.BodyStateInput, error) {
	byPart := map[string]*router.BodyStateInput{}
	var order []string
	ensure := func(part string) *router.BodyStateInput {
		key := strings.ToLower(part)
		if bs, ok := byPart[key]; ok {
			return bs
		}
		bs := &router.BodyStateInput{Part: part}
		byPart[key] = bs
		order = append(order, key)
		return bs
	}
	for _, raw := range soreness {
		part, level, hasLevel, err := parsePartLevel(raw, flagWSoreness, true)
		if err != nil {
			return nil, err
		}
		if part == "" {
			continue
		}
		v := level
		_ = hasLevel // soreness always carries a level (requireLevel=true)
		ensure(part).Soreness = &v
	}
	for _, raw := range pain {
		part, level, hasLevel, err := parsePartLevel(raw, flagWPain, false)
		if err != nil {
			return nil, err
		}
		if part == "" {
			continue
		}
		v := router.PainFlagLevel
		if hasLevel {
			v = level
		}
		ensure(part).Pain = &v
	}
	out := make([]router.BodyStateInput, 0, len(order))
	for _, key := range order {
		out = append(out, *byPart[key])
	}
	return out, nil
}

// parsePartLevel splits a `part:level` flag value. requireLevel controls whether
// a bare `part` (no colon) is allowed: --soreness needs a level, --pain accepts
// a bare part as an unquantified flag. A present level must be a 0-10 integer.
func parsePartLevel(raw, flag string, requireLevel bool) (part string, level int, hasLevel bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, false, nil
	}
	head, tail, hasColon := strings.Cut(raw, ":")
	part = strings.TrimSpace(head)
	if part == "" {
		return "", 0, false, fmt.Errorf("lucid workout log: --%s %q is missing a body part", flag, raw)
	}
	tail = strings.TrimSpace(tail)
	if !hasColon || tail == "" {
		if requireLevel {
			return "", 0, false, fmt.Errorf("lucid workout log: --%s %q needs a 0-%d level, e.g. %s:4", flag, raw, scaleMax, part)
		}
		return part, 0, false, nil
	}
	n, convErr := strconv.Atoi(tail)
	if convErr != nil {
		return "", 0, false, fmt.Errorf("lucid workout log: --%s level in %q must be a number", flag, raw)
	}
	if n < 0 || n > scaleMax {
		return "", 0, false, fmt.Errorf("lucid workout log: --%s level in %q must be 0-%d", flag, raw, scaleMax)
	}
	return part, n, true, nil
}
