// Package workout is the config-gated, off-by-default workout companion
// (docs/mvp/workout-module.md): the Mirror-side surface that recommends today's
// session, records what actually happened, and reviews progress over time. It is
// the model-allowed sibling of the pure accountability teeth — like
// internal/companion it may reach the model through internal/provider to phrase a
// message, but the decision it phrases is always made first by a deterministic
// core.
//
// This file owns the generic program schema and its loader. A program is the
// generic, versioned body of what to do — the rotation, the session cards, the
// per-body-part recovery windows, the daily anchor, the guardrails, and the
// safety copy. The schema is OSS and synthetic; the personal values (a real
// body's injuries, recovery windows, and rehab guardrails) are operator config
// on an opaque path, read directly by [LoadProgram] and never in the public
// repo. This mirrors the firewall the companion draws for its routine and
// template files: the mechanism ships here, the content stays in the operator's
// own file.
package workout

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// ProgramSchema is the only program schema version this build understands. A
// program carrying any other version is rejected by [Program.Validate] rather
// than silently coerced — the frozen-envelope discipline the observation kinds
// follow, applied to the program file.
const ProgramSchema = 1

// dateLayout is the civil-date string form (YYYY-MM-DD) used by start_date and
// the dated calendar overrides. It matches the Ledger's logical-date form so the
// program's calendar joins the Engine's civil day.
const dateLayout = "2006-01-02"

// Card load levels. A card's load is the key the recovery guardrail reads: only
// a *non-light* load opens a per-body-part recovery window, so a light or
// recovery card can never be vetoed for repeating a focus (workout-module.md
// §"The deterministic recommender contract", rule 2). A recovery/mobility card
// carries LoadNone.
const (
	LoadNone     = "none"
	LoadLight    = "light"
	LoadModerate = "moderate"
	LoadHard     = "hard"
)

// weekdayAbbrev maps a Go weekday to the lowercase three-letter token the
// rotation uses (`mon`..`sun`). It is the canonical form; [validWeekday] and
// [matchWeekday] also accept the full English name so an operator can write
// either.
var weekdayAbbrev = map[time.Weekday]string{ //nolint:gochecknoglobals // a fixed, read-only weekday-token lookup shared by the schema validator and the recommender
	time.Sunday:    "sun",
	time.Monday:    "mon",
	time.Tuesday:   "tue",
	time.Wednesday: "wed",
	time.Thursday:  "thu",
	time.Friday:    "fri",
	time.Saturday:  "sat",
}

// Card is one session in the program's library. Focus names the body parts the
// card loads — the vocabulary the recovery guardrail and the pain-flag hard stop
// read. Load is one of the Load* levels. Easier is the lighter variant offered as
// the message's fallback door; it is a partial Card (its Focus is inherited from
// the parent when the recommender builds the fallback offering) and never carries
// its own Easier. Equipment and Minutes are optional per-card requirements the
// equipment/time veto reads (absent → the card is always runnable); they are not
// in the synthetic example, so that veto stays inert unless a program opts in.
type Card struct {
	ID        string   `json:"id,omitempty"`
	Name      string   `json:"name"`
	Focus     []string `json:"focus,omitempty"`
	Load      string   `json:"load"`
	Movements []string `json:"movements,omitempty"`
	Equipment []string `json:"equipment,omitempty"`
	Minutes   int      `json:"minutes,omitempty"`
	Easier    *Card    `json:"easier,omitempty"`
}

// RotationEntry maps a weekday to a card id — the default weekly calendar. The
// weekday is a `mon`..`sun` abbreviation or the full English name.
type RotationEntry struct {
	Weekday string `json:"weekday"`
	Card    string `json:"card"`
}

// CalendarEntry overrides the weekday rotation for one civil date — a dated
// exception a program may carry (`{date, card}`). A calendar entry for today
// wins over the rotation.
type CalendarEntry struct {
	Date string `json:"date"`
	Card string `json:"card"`
}

// AnchorItem is one movement in the daily anchor — the "something every day"
// floor. Target is the count for the item (overridable per program week); Mode
// "accumulate" marks a movement done in small sets through the day. The anchor is
// inventory only: nothing here is a target the system grades.
type AnchorItem struct {
	Name   string `json:"name"`
	Target int    `json:"target,omitempty"`
	Mode   string `json:"mode,omitempty"`
}

// DailyAnchor is the program's daily floor. Items are the anchor movements;
// TargetsByWeek overrides item targets for a given 1-indexed program week (the
// week is counted from the program's start_date at the Ledger's rollover
// boundary).
type DailyAnchor struct {
	Items         []AnchorItem              `json:"items,omitempty"`
	TargetsByWeek map[string]map[string]int `json:"targets_by_week,omitempty"`
}

// Guardrails are the generic movement/focus vetoes the operator fills with their
// real specifics. AvoidMovements and ProvocativePositions are never recommended;
// NoStrengthen names body parts the program deliberately does not load. All three
// are matched case-insensitively against a card's movements and focus before the
// recovery pass.
type Guardrails struct {
	AvoidMovements       []string `json:"avoid_movements,omitempty"`
	ProvocativePositions []string `json:"provocative_positions,omitempty"`
	NoStrengthen         []string `json:"no_strengthen,omitempty"`
}

// Program is the generic, versioned body of what to do. It is loaded from an
// opaque operator path and validated on load; a bad or missing program is a
// loader error the surface degrades on (workout-module.md §"Error states" W-1)
// rather than a crash. The schema is synthetic-only in this repo — see
// [ExampleProgram] — and the personal values live in the operator's file.
type Program struct {
	Version           int             `json:"version"`
	ProgramID         string          `json:"program_id"`
	Label             string          `json:"label,omitempty"`
	StartDate         string          `json:"start_date,omitempty"`
	Goals             []string        `json:"goals,omitempty"`
	Equipment         []string        `json:"equipment,omitempty"`
	SessionMinutes    int             `json:"session_minutes,omitempty"`
	Rotation          []RotationEntry `json:"rotation,omitempty"`
	Calendar          []CalendarEntry `json:"calendar,omitempty"`
	Cards             []Card          `json:"cards"`
	RecoveryHours     map[string]int  `json:"recovery_hours,omitempty"`
	DailyAnchor       DailyAnchor     `json:"daily_anchor"`
	Guardrails        Guardrails      `json:"guardrails"`
	PainFlagThreshold int             `json:"pain_flag_threshold,omitempty"`
	SafetyCopy        string          `json:"safety_copy,omitempty"`
}

// LoadProgram reads and validates a program from an explicit, opaque path. It
// opens exactly that file and never walks a directory (the OSS/personal firewall
// seam — the same shape as the companion's routine files), so no traversal into
// the personal program tree is possible. An empty path, an unreadable file, a
// malformed JSON body, or a program that fails [Program.Validate] is returned as
// a loud error; the surface layer decides to degrade to "no program" on it. The
// content is trusted operator config, never user input.
func LoadProgram(path string) (Program, error) {
	if strings.TrimSpace(path) == "" {
		return Program{}, errors.New("workout: empty program path")
	}
	b, err := os.ReadFile(path) //nolint:gosec // an operator-configured, explicit program path — read directly, never dir-walked
	if err != nil {
		return Program{}, fmt.Errorf("workout: read program %q: %w", path, err)
	}
	var p Program
	if err := json.Unmarshal(b, &p); err != nil {
		return Program{}, fmt.Errorf("workout: parse program %q: %w", path, err)
	}
	if err := p.Validate(); err != nil {
		return Program{}, fmt.Errorf("workout: invalid program %q: %w", path, err)
	}
	return p, nil
}

// Validate reports the first structural problem with a program before it is
// used. It guards the schema version, the presence of at least one uniquely-ided
// card with a valid load, and the referential integrity of the rotation and
// calendar (every referenced card exists, every weekday and date parses). It does
// not police the operator's training choices — only that the file is well-formed
// enough for the deterministic recommender to read.
func (p Program) Validate() error {
	if p.Version != ProgramSchema {
		return fmt.Errorf("workout: unsupported program version %d (want %d)", p.Version, ProgramSchema)
	}
	if strings.TrimSpace(p.ProgramID) == "" {
		return errors.New("workout: program_id is required")
	}
	ids, err := p.validateCards()
	if err != nil {
		return err
	}
	if err := p.validateSchedule(ids); err != nil {
		return err
	}
	if p.StartDate != "" {
		if _, err := time.Parse(dateLayout, p.StartDate); err != nil {
			return fmt.Errorf("workout: invalid start_date %q", p.StartDate)
		}
	}
	if p.PainFlagThreshold < 0 || p.PainFlagThreshold > 10 {
		return fmt.Errorf("workout: pain_flag_threshold %d out of range 0-10", p.PainFlagThreshold)
	}
	return nil
}

// validateCards checks every card has a unique, non-empty id and a valid load
// (and, when present, a valid easier variant), returning the set of known card
// ids for the schedule check.
func (p Program) validateCards() (map[string]bool, error) {
	if len(p.Cards) == 0 {
		return nil, errors.New("workout: program has no cards")
	}
	ids := make(map[string]bool, len(p.Cards))
	for i, c := range p.Cards {
		if strings.TrimSpace(c.ID) == "" {
			return nil, fmt.Errorf("workout: card %d has no id", i)
		}
		if ids[c.ID] {
			return nil, fmt.Errorf("workout: duplicate card id %q", c.ID)
		}
		ids[c.ID] = true
		if !validLoad(c.Load) {
			return nil, fmt.Errorf("workout: card %q has invalid load %q", c.ID, c.Load)
		}
		if c.Easier != nil && c.Easier.Load != "" && !validLoad(c.Easier.Load) {
			return nil, fmt.Errorf("workout: card %q easier variant has invalid load %q", c.ID, c.Easier.Load)
		}
	}
	return ids, nil
}

// validateSchedule checks that every rotation and calendar entry names a known
// card and a parseable weekday/date.
func (p Program) validateSchedule(ids map[string]bool) error {
	for i, r := range p.Rotation {
		if !validWeekday(r.Weekday) {
			return fmt.Errorf("workout: rotation[%d] has invalid weekday %q", i, r.Weekday)
		}
		if !ids[r.Card] {
			return fmt.Errorf("workout: rotation[%d] references unknown card %q", i, r.Card)
		}
	}
	for i, c := range p.Calendar {
		if _, err := time.Parse(dateLayout, c.Date); err != nil {
			return fmt.Errorf("workout: calendar[%d] has invalid date %q", i, c.Date)
		}
		if !ids[c.Card] {
			return fmt.Errorf("workout: calendar[%d] references unknown card %q", i, c.Card)
		}
	}
	return nil
}

// card returns the library card with the given id.
func (p Program) card(id string) (Card, bool) {
	for _, c := range p.Cards {
		if c.ID == id {
			return c, true
		}
	}
	return Card{}, false
}

// cardByLoad returns the first library card carrying the given load — the seam
// the downshift path uses to find a program-authored recovery/light card before
// synthesizing a generic one.
func (p Program) cardByLoad(load string) (Card, bool) {
	for _, c := range p.Cards {
		if c.Load == load {
			return c, true
		}
	}
	return Card{}, false
}

// validLoad reports whether a load string is one of the four known levels.
func validLoad(l string) bool {
	switch l {
	case LoadNone, LoadLight, LoadModerate, LoadHard:
		return true
	default:
		return false
	}
}

// validWeekday reports whether s names a weekday in either the `mon`..`sun`
// abbreviation or the full English form (case-insensitive).
func validWeekday(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for wd, abbr := range weekdayAbbrev {
		if s == abbr || s == strings.ToLower(wd.String()) {
			return true
		}
	}
	return false
}

// matchWeekday reports whether the rotation-entry weekday token names wd,
// accepting both the abbreviation and the full English name (case-insensitive).
func matchWeekday(entry string, wd time.Weekday) bool {
	entry = strings.ToLower(strings.TrimSpace(entry))
	return entry == weekdayAbbrev[wd] || entry == strings.ToLower(wd.String())
}

// ExampleProgram returns the synthetic Foundation-rotation program from
// docs/mvp/workout-module.md. It carries no personal content — a generic weekly
// rotation, recovery windows, a daily anchor, and generic guardrail slots — and
// exists so tests and docs in this repo have a valid program without ever
// touching the operator's private one (product-principles.md §9, synthetic
// examples only).
func ExampleProgram() Program {
	return Program{
		Version:        ProgramSchema,
		ProgramID:      "example_foundation",
		Label:          "Foundation rotation",
		StartDate:      "2026-01-05",
		Goals:          []string{"build a base", "protect recovering joints", "stay consistent"},
		Equipment:      []string{"bodyweight", "light dumbbells", "band"},
		SessionMinutes: 50,
		Rotation: []RotationEntry{
			{Weekday: "mon", Card: "legs"},
			{Weekday: "tue", Card: "push"},
			{Weekday: "wed", Card: "pull"},
			{Weekday: "thu", Card: "cardio"},
			{Weekday: "fri", Card: "full_body"},
			{Weekday: "sat", Card: "power_skill"},
			{Weekday: "sun", Card: "recovery"},
		},
		Cards: []Card{
			{
				ID: "legs", Name: "Legs + hips", Focus: []string{"legs"}, Load: LoadHard,
				Movements: []string{"goblet squat", "hip hinge", "split squat"},
				Easier: &Card{
					Name: "Easy legs", Load: LoadLight,
					Movements: []string{"bodyweight squat", "glute bridge"},
				},
			},
			{
				ID: "push", Name: "Push", Focus: []string{"chest", "shoulders"}, Load: LoadModerate,
				Movements: []string{"incline press", "push-up", "band press"},
				Easier: &Card{
					Name: "Easy push", Load: LoadLight,
					Movements: []string{"incline push-up", "band press"},
				},
			},
			{
				ID: "pull", Name: "Pull + posture", Focus: []string{"back", "rear_shoulders"}, Load: LoadModerate,
				Movements: []string{"supported row", "band pull-apart", "face pull"},
				Easier: &Card{
					Name: "Easy pull", Load: LoadLight,
					Movements: []string{"band pull-apart", "face pull"},
				},
			},
			{
				ID: "cardio", Name: "Zone-2 cardio", Focus: []string{"cardio"}, Load: LoadLight,
				Movements: []string{"brisk walk", "easy bike"},
			},
			{
				ID: "full_body", Name: "Full body", Focus: []string{"legs", "back", "chest"}, Load: LoadModerate,
				Movements: []string{"squat", "row", "push-up"},
				Easier: &Card{
					Name: "Easy circuit", Load: LoadLight,
					Movements: []string{"bodyweight squat", "band row"},
				},
			},
			{
				ID: "power_skill", Name: "Power + skill", Focus: []string{"legs", "core"}, Load: LoadHard,
				Movements: []string{"jump", "carry", "balance drill"},
				Easier: &Card{
					Name: "Skill only", Load: LoadLight,
					Movements: []string{"balance drill", "easy carry"},
				},
			},
			{
				ID: "recovery", Name: "Recovery + mobility", Focus: nil, Load: LoadNone,
				Movements: []string{"gentle mobility", "easy walk", "light stretching"},
			},
		},
		RecoveryHours: map[string]int{"back": 48, "chest": 48, "legs": 48, "core": 24, "shoulders": 48},
		DailyAnchor: DailyAnchor{
			Items: []AnchorItem{
				{Name: "squats", Target: 50},
				{Name: "core", Target: 40},
				{Name: "easy push-ups", Target: 20, Mode: "accumulate"},
			},
			TargetsByWeek: map[string]map[string]int{
				"2": {"squats": 55, "core": 50, "easy push-ups": 25},
			},
		},
		Guardrails: Guardrails{
			AvoidMovements:       []string{"loaded end-range overhead reaches"},
			ProvocativePositions: []string{"any loaded position that reproduces a specific joint pain"},
			NoStrengthen:         []string{"upper_traps"},
		},
		PainFlagThreshold: 5,
	}
}
