package router

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/observations"
)

// memory.go is the story-capture write path for the life-archive module
// (mvp/life-archive.md §3). A story is one KindMemory event written at a
// backdated occurred_at with source: excavation, carrying the convention
// payload (text/certainty/tone/why_it_matters/follow_up/people) and refs to the
// era it sits in, the place it happened, the people in it, and — when a photo is
// attached via lucid attach — the linked raw entry. It reuses the frozen event
// envelope and the same buildEvent → AppendObservation write the micro-log
// capture path uses; only the source and the structured payload differ. It is
// deterministic and agent-free — no model runs here (architecture P9).

// MemoryWriteRequest carries one `lucid memory` story-capture turn. Text is the
// story in the owner's words (it anchors the memory; an empty text takes the
// partial path). Certainty, when set, is one of vivid/hazy/reconstructed. Era is
// an era registry key the story sits in, referenced verbatim (the chapter is
// authored via `lucid era`); Place is a place name resolved and registered
// exactly as a sticky location; People are names kept as testimony and linked to
// any person key that already resolves. Tone/WhyItMatters/FollowUp are the
// convention payload keys. Day backdates the occurred_at through the shared
// strict grammar (approximate for a past date); an unreadable or future value
// is refused with nothing written. EntryRef is the raw entry id of an
// attached photo (set by the CLI after `lucid attach`), referenced from
// refs.entry. A zero Now defaults to the wall clock.
type MemoryWriteRequest struct {
	Text         string
	Certainty    string
	Era          string
	Place        string
	People       []string
	Tone         string
	WhyItMatters string
	FollowUp     string
	Day          string
	EntryRef     string
	Now          time.Time
}

// MemoryWriteResult reports what a story capture wrote and the inventory ack.
// Rejected is set (with no event written) when the memory kind is disabled — the
// one reject path, mirroring [Router.Capture]. Refs echoes the resolved
// relational links (era/place/person/entry) so a harness can confirm what the
// story was tied to.
type MemoryWriteResult struct {
	EventID     string
	LogicalDate string
	Partial     bool
	Rejected    bool
	Refs        map[string]any
	Ack         string
}

// WriteMemory captures one excavated story as a KindMemory event on the frozen
// bitemporal envelope (mvp/life-archive.md §3). It rejects a disabled memory
// kind with the enable hint (like Capture), validates the certainty and the
// backdate before any write (error-states.md §St-1: nothing saved on a bad
// enum or an unreadable day), backdates the occurred_at through the shared
// strict grammar so the story files under its own logical day (never
// overwriting a current-day log), resolves the era/place/people/media refs,
// and appends one frozen event sourced as excavation. It is deterministic and
// agent-free.
func (r *Router) WriteMemory(req MemoryWriteRequest) (MemoryWriteResult, error) {
	now := whenOr(req.Now)
	if err := r.prepareObservations(); err != nil {
		return MemoryWriteResult{}, err
	}

	cfg, err := r.store.ReadObservationsConfig()
	if err != nil {
		return MemoryWriteResult{}, err
	}
	if !cfg.KindEnabled(observations.KindMemory) {
		return MemoryWriteResult{Rejected: true, Ack: observations.EnableHint(observations.KindMemory)}, nil
	}

	if c := strings.TrimSpace(req.Certainty); c != "" && !observations.IsMemoryCertainty(strings.ToLower(c)) {
		return MemoryWriteResult{}, fmt.Errorf(
			"unknown certainty %q (want vivid, hazy, or reconstructed); nothing was saved", req.Certainty,
		)
	}

	// --day is a deliberate flag, so it runs on the strict tier
	// ([observations.ResolveDay], reached through the capture verbs' shared
	// entry point) alongside the certainty check above: an unreadable token or
	// a day that has not happened yet is refused before anything is written,
	// rather than silently recording the story at now. `lucid memory --attach`
	// forwards the same value into [Router.Attach], which resolves it through
	// this very function — so the story and its photo can never land on
	// different days.
	when, err := resolveCaptureWhen(req.Day, now)
	if err != nil {
		return MemoryWriteResult{}, err
	}

	payload, partial := observations.ParseMemoryFields(observations.MemoryInput{
		Text:         req.Text,
		Certainty:    req.Certainty,
		Tone:         req.Tone,
		WhyItMatters: req.WhyItMatters,
		FollowUp:     req.FollowUp,
		People:       req.People,
	})

	parsed := observations.ParseResult{
		Kind:        observations.KindMemory,
		OccurredAt:  when.OccurredAt,
		Precision:   when.Precision,
		OccurredEnd: when.End,
		Payload:     payload,
		Refs:        map[string]any{},
		Partial:     partial,
	}
	if err = r.resolveMemoryRefs(&parsed, req, now); err != nil {
		return MemoryWriteResult{}, err
	}

	ev, err := r.store.AppendObservation(r.buildEvent(parsed, now, nil, observations.SourceExcavation))
	if err != nil {
		return MemoryWriteResult{}, fmt.Errorf("could not save the memory; nothing was saved: %w", err)
	}

	return MemoryWriteResult{
		EventID:     ev.ID,
		LogicalDate: ev.LogicalDate,
		Partial:     partial,
		Refs:        ev.Refs,
		Ack:         captureAck(ev.Kind, ev.ID, partial),
	}, nil
}

// resolveMemoryRefs stamps a story's relational links onto the event's refs
// (mvp/life-archive.md §3, the frozen refs contract). era is a registry key the
// story sits in, referenced verbatim (the chapter is authored via `lucid era`).
// place mirrors a sticky location exactly — the name is resolved to a place key
// and the place registry is upserted (context.location's resolvePlace) — so a
// story's setting becomes a first-class, browsable place. people are kept in the
// payload as testimony (ParseMemoryFields) and, for each name that already
// resolves to exactly one person record, linked into refs.person (v1 links to
// existing people only; it does not run the structuring pass). entry is the
// attached photo's raw id (set by the CLI after `lucid attach`), the media link.
// It writes only the place registry; every other ref is a pure lookup.
func (r *Router) resolveMemoryRefs(parsed *observations.ParseResult, req MemoryWriteRequest, now time.Time) error {
	if era := strings.TrimSpace(req.Era); era != "" {
		parsed.Refs["era"] = era
	}
	if place := strings.TrimSpace(req.Place); place != "" {
		key, err := r.store.ResolveRegistryKey(observations.RegistryPlace, place)
		if err != nil {
			return fmt.Errorf("could not resolve the place; nothing was saved: %w", err)
		}
		if _, err := r.store.UpdateRegistry(observations.RegistryPlace, key, observations.RegistryPatch{
			DisplayName: place,
			At:          now.Format(time.RFC3339),
		}); err != nil {
			return fmt.Errorf("could not save the place; nothing was saved: %w", err)
		}
		parsed.Refs["place"] = key
	}
	keys, err := r.resolvePeopleKeys(req.People)
	if err != nil {
		return err
	}
	if len(keys) > 0 {
		parsed.Refs["person"] = keys
	}
	if entry := strings.TrimSpace(req.EntryRef); entry != "" {
		parsed.Refs["entry"] = entry
	}
	return nil
}

// resolvePeopleKeys returns the person keys for the story's people that already
// resolve to exactly one person record, sorted and de-duplicated so the ref is
// byte-stable. A name that matches no one — or is ambiguous — is left as payload
// testimony only: it links existing people, never mints one here. A read
// failure is surfaced honestly so a story is never quietly half-written.
func (r *Router) resolvePeopleKeys(names []string) ([]string, error) {
	var keys []string
	for _, name := range names {
		n := strings.TrimSpace(name)
		if n == "" {
			continue
		}
		matches, err := r.matchPeople(n)
		if err != nil {
			return nil, fmt.Errorf("could not resolve people; nothing was saved: %w", err)
		}
		if len(matches) == 1 {
			keys = append(keys, matches[0].PersonKey)
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	slices.Sort(keys)
	return slices.Compact(keys), nil
}

// AmendMemoryRequest carries one `lucid memory amend <obs_id>` turn (Q4/Q5,
// mvp/data-model.md). ObsID names the base memory being corrected. Each amendable
// field pairs a value with a "changed" bool so an omitted flag (Changed=false)
// leaves that field untouched, distinct from an explicit set: Body corrects the
// story text (resolved from --body-file by the CLI — the router never reads a
// path), Era re-files the story under an existing era key, Certainty resets the
// recall enum, Followup sets the follow-up thread. ClearFollowup removes the
// follow-up field — the explicit clear, distinct from an ambiguous empty set. A
// zero Now defaults to the wall clock.
type AmendMemoryRequest struct {
	ObsID string

	Body        string
	BodyChanged bool

	Era        string
	EraChanged bool

	Certainty        string
	CertaintyChanged bool

	Followup        string
	FollowupChanged bool
	ClearFollowup   bool

	Now time.Time
}

// AmendMemoryResult reports what an amend appended: the amendment event's own id
// (EventID, freshly assigned in the target's day), the base memory it corrects
// (TargetID), the shared logical day both file under, the amendment's refs
// (corrects/era/cleared), and the inventory ack.
type AmendMemoryResult struct {
	EventID     string
	TargetID    string
	LogicalDate string
	Refs        map[string]any
	Ack         string
}

// AmendMemory corrects or re-files a stored memory by appending a new KindMemory
// event that carries refs.corrects plus only the changed fields — the frozen
// envelope is never rewritten, so the base story's line stays byte-identical and
// its prior values remain recoverable (mvp/data-model.md, append-only amendment).
// Every input is validated before any write, so a rejected amend appends nothing
// (error-states.md §St-1): the id must name an existing base memory (not an
// unknown id, a non-memory event, or an amendment — a correction-event id is an
// audit record, never an amendable base); at least one field must change; a
// changed certainty must be in the closed enum; a changed era must already exist;
// and --followup can be neither set-and-cleared at once nor set to an ambiguous
// empty value. The amendment reuses the target's logical_date/occurred_at so it
// files in the same day file (the per-day /day fold needs no cross-day scan) and
// is sourced as excavation like the base story. It is deterministic and
// agent-free.
func (r *Router) AmendMemory(req AmendMemoryRequest) (AmendMemoryResult, error) {
	now := whenOr(req.Now)
	if err := r.prepareObservations(); err != nil {
		return AmendMemoryResult{}, err
	}

	obsID := strings.TrimSpace(req.ObsID)
	if _, ok := observations.EventDate(obsID); !ok {
		return AmendMemoryResult{}, memoryNotFoundErr(obsID)
	}

	target, found, err := r.store.ReadObservationByID(obsID)
	if err != nil {
		return AmendMemoryResult{}, fmt.Errorf("could not read the memory; nothing was saved: %w", err)
	}
	if !found || target.Kind != observations.KindMemory {
		return AmendMemoryResult{}, memoryNotFoundErr(obsID)
	}
	// A correction event is an audit record keyed to a base memory, never an
	// amendable base itself — amending one would append a correction-of-a-
	// correction the field fold would never apply. Reject it: amend the base id.
	if _, isAmendment := target.Refs[observations.RefCorrects]; isAmendment {
		return AmendMemoryResult{}, fmt.Errorf(
			"%q is a memory amendment, not a base memory; amend the memory it corrects instead; nothing was saved", obsID,
		)
	}

	// Reject a contradictory or ambiguous follow-up before any write: setting and
	// clearing at once has no defined precedence, and a bare empty --followup can
	// mean either "set to empty" or "clear" — the explicit --clear-followup is the
	// only way to remove it (Q4).
	if req.FollowupChanged && req.ClearFollowup {
		return AmendMemoryResult{}, fmt.Errorf("give --followup or --clear-followup, not both; nothing was saved")
	}
	if req.FollowupChanged && strings.TrimSpace(req.Followup) == "" {
		return AmendMemoryResult{}, fmt.Errorf(
			"--followup was given an empty value; use --clear-followup to remove it, or pass the new text; nothing was saved",
		)
	}

	if !req.BodyChanged && !req.EraChanged && !req.CertaintyChanged && !req.FollowupChanged && !req.ClearFollowup {
		return AmendMemoryResult{}, fmt.Errorf("no fields to amend; nothing was saved")
	}

	if req.CertaintyChanged {
		if c := strings.ToLower(strings.TrimSpace(req.Certainty)); !observations.IsMemoryCertainty(c) {
			return AmendMemoryResult{}, fmt.Errorf(
				"unknown certainty %q (want vivid, hazy, or reconstructed); nothing was saved", req.Certainty,
			)
		}
	}

	eraKey := strings.TrimSpace(req.Era)
	if req.EraChanged {
		if eraKey == "" {
			return AmendMemoryResult{}, fmt.Errorf("--era needs an era key; nothing was saved")
		}
		_, ok, rerr := r.store.ReadRegistry(observations.RegistryEra, eraKey)
		if rerr != nil {
			return AmendMemoryResult{}, fmt.Errorf("could not read the era; nothing was saved: %w", rerr)
		}
		if !ok {
			return AmendMemoryResult{}, fmt.Errorf(
				"era %q not found — file the memory under an existing era key (see `lucid era list`); nothing was saved", eraKey,
			)
		}
	}

	// Build the amendment: only the changed payload fields, refs.corrects always,
	// refs.era on a re-file, refs.cleared on a follow-up clear.
	payload := map[string]any{}
	if req.BodyChanged {
		payload[observations.MemoryFieldText] = strings.TrimSpace(req.Body)
	}
	if req.CertaintyChanged {
		payload[observations.MemoryFieldCertainty] = strings.ToLower(strings.TrimSpace(req.Certainty))
	}
	if req.FollowupChanged {
		payload[observations.MemoryFieldFollowUp] = strings.TrimSpace(req.Followup)
	}

	refs := map[string]any{observations.RefCorrects: obsID}
	if req.EraChanged {
		refs[observations.RefEra] = eraKey
	}
	if req.ClearFollowup {
		refs[observations.RefCleared] = []string{observations.MemoryFieldFollowUp}
	}

	amendment := observations.Event{
		Schema:              observations.Schema,
		Kind:                observations.KindMemory,
		RecordedAt:          now.Format(time.RFC3339),
		OccurredAt:          target.OccurredAt,
		OccurredAtPrecision: target.OccurredAtPrecision,
		LogicalDate:         target.LogicalDate,
		Source:              observations.SourceExcavation,
		Payload:             payload,
		Refs:                refs,
	}
	// Copy the occurrence end by value so the amendment never aliases the target's
	// pointer; a range memory keeps its span on the correction line.
	if target.OccurredAtEnd != nil {
		end := *target.OccurredAtEnd
		amendment.OccurredAtEnd = &end
	}

	ev, err := r.store.AppendObservation(amendment)
	if err != nil {
		return AmendMemoryResult{}, fmt.Errorf("could not save the amendment; nothing was saved: %w", err)
	}

	return AmendMemoryResult{
		EventID:     ev.ID,
		TargetID:    obsID,
		LogicalDate: ev.LogicalDate,
		Refs:        ev.Refs,
		Ack:         fmt.Sprintf("Amended memory `%s`; recorded as `%s`.", obsID, ev.ID),
	}, nil
}

// memoryNotFoundErr is the shared rejection when an amend target does not name an
// amendable base memory — an unparseable id, an id that names no event, or an
// event of another kind. All three mean "there is no memory here to amend", so
// they share one honest message and nothing is written.
func memoryNotFoundErr(obsID string) error {
	return fmt.Errorf("memory %q not found; nothing was saved", obsID)
}

// readFoldedMemories reads every memory event through the storage projection
// seam and folds each story's amendments onto its base (fold-on-read everywhere,
// Q2) — the single folded-memory read the browse/search surfaces (recall,
// excavate) share, so amended values surface identically on both without either
// re-implementing the fold. FoldMemoryAmendments is pure and drops amendment
// events, so a caller sees only current base memories, sorted by id. The fold is
// deliberately never pushed into storage.ReadObservationsKind, whose generic
// per-kind read also feeds the export path (blast radius); it lives here at the
// two read surfaces that must reflect amendments.
func (r *Router) readFoldedMemories() ([]observations.Event, error) {
	memories, err := r.store.ReadObservationsKind(observations.KindMemory)
	if err != nil {
		return nil, err
	}
	return observations.FoldMemoryAmendments(memories), nil
}

// ShowMemoryResult is one folded story plus its amendment history — what
// `lucid memory show` renders. Memory is the base memory with every amendment
// folded on (its current values); History is the ordered per-field change trail
// (nil when the story was never amended), each entry naming the field, its prior
// value, the new value (or a clear), and the amendment's recorded_at.
type ShowMemoryResult struct {
	Memory  observations.Event
	History []observations.MemoryFieldChange
}

// ShowMemory reads one story by its base id and returns its folded current
// values plus the amendment history trail (fold-on-read, Q2). It loads the
// complete memory event set — a single-id read cannot see the appended
// amendments, which live as separate events — folds it, and returns the folded
// base. The id must name a base memory: an unknown/unparseable id, or an id that
// names a correction event (refs.corrects), is rejected, so `show` never renders
// a bare amendment. It is read-only and agent-free.
func (r *Router) ShowMemory(obsID string) (ShowMemoryResult, error) {
	if err := r.prepareObservations(); err != nil {
		return ShowMemoryResult{}, err
	}
	id := strings.TrimSpace(obsID)
	if _, ok := observations.EventDate(id); !ok {
		return ShowMemoryResult{}, memoryReadNotFoundErr(id)
	}

	memories, err := r.store.ReadObservationsKind(observations.KindMemory)
	if err != nil {
		return ShowMemoryResult{}, fmt.Errorf("could not read the memory: %w", err)
	}

	// The id must name a base memory. A correction-event id (refs.corrects) is an
	// audit record folded onto its base — never shown on its own, so reject it and
	// point at the base it corrects.
	for _, ev := range memories {
		if ev.ID != id {
			continue
		}
		if _, isAmendment := ev.Refs[observations.RefCorrects]; isAmendment {
			return ShowMemoryResult{}, fmt.Errorf(
				"%q is a memory amendment, not a base memory; show the memory it corrects instead", id,
			)
		}
		break
	}

	var current observations.Event
	found := false
	for _, ev := range observations.FoldMemoryAmendments(memories) {
		if ev.ID == id {
			current = ev
			found = true
			break
		}
	}
	if !found {
		return ShowMemoryResult{}, memoryReadNotFoundErr(id)
	}

	return ShowMemoryResult{
		Memory:  current,
		History: observations.FoldMemoryHistory(memories, id),
	}, nil
}

// memoryReadNotFoundErr is the read-path (`memory show`) sibling of
// memoryNotFoundErr: an unknown or unparseable id names no story to read. It
// drops the "nothing was saved" clause the write path carries — a read saves
// nothing regardless — while keeping the same "not found" wording.
func memoryReadNotFoundErr(obsID string) error {
	return fmt.Errorf("memory %q not found", obsID)
}
