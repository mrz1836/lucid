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
// @-grammar (approximate for a past date). EntryRef is the raw entry id of an
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
// kind with the enable hint (like Capture), validates the certainty before any
// write (error-states.md §St-1: nothing saved on a bad enum), backdates the
// occurred_at through the shared @-grammar so the story files under its own
// logical day (never overwriting a current-day log), resolves the era/place/
// people/media refs, and appends one frozen event sourced as excavation. It is
// deterministic and agent-free.
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

	occ, precision, end := observations.ResolveBackdate(req.Day, now)
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
		OccurredAt:  occ,
		Precision:   precision,
		OccurredEnd: end,
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
