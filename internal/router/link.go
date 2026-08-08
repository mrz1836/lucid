package router

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/storage"
)

// link.go is the single place link/unlink/annotate semantics live: one parser
// for the --to grammar and one intent that decides what each op does against
// the current fold. Every surface that links media routes through here — the
// three verbs and `attach --to` alike — so the no-op rules, the subject
// resolution, and the write order can never fork into two subtly different
// implementations (data-model.md §"Link ledger").

// ParseSubjectRef splits a --to token on its FIRST colon only: a key may itself
// contain colons (an observation id, say), a kind may not. kind must match
// ^[a-z][a-z0-9_]*$ — the same shape the ledger's opaque kind satisfies — so a
// malformed kind is refused here, before resolution (D2). The token is trimmed
// first, so surrounding whitespace never becomes part of the kind or key.
func ParseSubjectRef(tok string) (kind, key string, err error) {
	tok = strings.TrimSpace(tok)
	i := strings.IndexByte(tok, ':')
	if i < 0 {
		return "", "", fmt.Errorf("subject %q must be <kind>:<key> (a colon separates the two)", tok)
	}
	kind, key = tok[:i], tok[i+1:]
	if !validKindShape(kind) {
		return "", "", fmt.Errorf("subject kind %q is not a valid kind name (want ^[a-z][a-z0-9_]*$)", kind)
	}
	if key == "" {
		return "", "", fmt.Errorf("subject %q has an empty key after the colon", tok)
	}
	return kind, key, nil
}

// validKindShape reports whether kind matches ^[a-z][a-z0-9_]*$: a lowercase
// letter first, then lowercase letters, digits, or underscores. It is
// hand-rolled rather than a package-level compiled regexp so no global is
// introduced, mirroring storage's own kind check.
func validKindShape(kind string) bool {
	if kind == "" {
		return false
	}
	for i, r := range kind {
		switch {
		case r >= 'a' && r <= 'z':
			// a lowercase letter is legal anywhere, including the first position
		case i > 0 && (r == '_' || (r >= '0' && r <= '9')):
			// digits and underscores are legal only after the first character
		default:
			return false
		}
	}
	return true
}

// subjectRef is one parsed, resolved subject: the kind and key an event is
// written against. It is the internal value the intent carries between
// resolution and append, so a subject is parsed and resolved exactly once.
type subjectRef struct {
	kind string
	key  string
}

// ref renders a subject reference back as its <kind>:<key> token, for an ack.
func (s subjectRef) ref() string { return s.kind + ":" + s.key }

// LinkRequest is one link/unlink/annotate turn. Media is the sidecar id (a
// stored path is accepted and reduced to its basename); Subjects are the
// repeatable --to tokens; Op selects the verb; Note rides an annotate. Now
// stamps every appended event's at (a zero Now defaults to the wall clock) and
// Source is the provenance token (empty defaults to the local CLI surface).
type LinkRequest struct {
	Media    string
	Subjects []string
	Op       storage.LinkOp
	Note     string
	Now      time.Time
	Source   string
}

// LinkApplied reports what one subject in a turn resolved to: the parsed kind
// and key, the id of the event appended for it, and whether it was a no-op. A
// no-op carries no EventID — re-linking a live pair or unlinking a pair that is
// not live appends nothing (D4).
type LinkApplied struct {
	Kind    string
	Key     string
	EventID string
	NoOp    bool
}

// LinkResult reports what a turn did: the resolved media id, one LinkApplied per
// subject (in request order), and the human-first ack.
type LinkResult struct {
	MediaID string
	Applied []LinkApplied
	Ack     string
}

// Link executes one link/unlink/annotate turn — the single entry for all three
// ops so their semantics can never diverge (Scope §"link / unlink / annotate").
// It resolves the media and every subject up front, before appending anything,
// so a bad reference leaves the ledger byte-identical: a partially applied
// multi-subject turn would be a silent half-write to an association ledger,
// which is worse than a clean refusal. It never touches the media binary or its
// sidecar — all mutability lives in the append-only log (SC-5).
func (r *Router) Link(req LinkRequest) (LinkResult, error) {
	now := whenOr(req.Now)
	source, err := resolveSource(req.Source)
	if err != nil {
		return LinkResult{}, fmt.Errorf("invalid source; nothing was linked: %w", err)
	}

	if len(req.Subjects) == 0 {
		return LinkResult{}, fmt.Errorf("%s needs at least one --to <kind>:<key> subject; nothing was linked", req.Op)
	}

	// Resolve the media first: an unknown media is refused before any subject is
	// touched, so a typo'd id never appends against a subject that does resolve.
	mediaID, err := r.resolveMediaRef(req.Media)
	if err != nil {
		return LinkResult{}, err
	}

	// Resolve every subject up front. A single refusal refuses the whole turn
	// and writes nothing (SC-4's growth-friendly semantics still apply per pair
	// once every subject is known-good).
	refs, err := r.resolveSubjects(req.Subjects)
	if err != nil {
		return LinkResult{}, err
	}

	// Fold the media's current links once, so the per-pair no-op decision reads
	// the same liveness view for every subject in the turn.
	live, err := r.liveSubjects(mediaID)
	if err != nil {
		return LinkResult{}, err
	}

	applied := make([]LinkApplied, 0, len(refs))
	for _, ref := range refs {
		a, aerr := r.applyOne(mediaID, ref, req, now, source, live)
		if aerr != nil {
			return LinkResult{}, aerr
		}
		applied = append(applied, a)
	}

	return LinkResult{
		MediaID: mediaID,
		Applied: applied,
		Ack:     linkAck(req.Op, mediaID, applied),
	}, nil
}

// applyOne appends (or skips, per D4) the event for one already-resolved
// subject and reports what happened. A link on a live pair and an unlink on a
// pair that is not live are no-op acks that append nothing; an annotate always
// appends, because it folds independently of liveness (D1).
func (r *Router) applyOne(
	mediaID string, ref subjectRef, req LinkRequest, now time.Time, source string, live map[subjectRef]bool,
) (LinkApplied, error) {
	switch req.Op {
	case storage.LinkOpLink:
		if live[ref] {
			return LinkApplied{Kind: ref.kind, Key: ref.key, NoOp: true}, nil
		}
	case storage.LinkOpUnlink:
		if !live[ref] {
			return LinkApplied{Kind: ref.kind, Key: ref.key, NoOp: true}, nil
		}
	case storage.LinkOpAnnotate:
		// annotate always appends: it never implies link and may target a pair
		// that is not currently linked.
	}

	ev, err := r.store.AppendLinkEvent(storage.LinkEvent{
		MediaID: mediaID,
		Subject: storage.LinkSubject{Kind: ref.kind, Key: ref.key},
		Op:      req.Op,
		At:      now.Format(time.RFC3339),
		Source:  source,
		Note:    noteFor(req.Op, req.Note),
	})
	if err != nil {
		return LinkApplied{}, fmt.Errorf("could not record the %s; nothing was linked for %s: %w", req.Op, ref.ref(), err)
	}
	return LinkApplied{Kind: ref.kind, Key: ref.key, EventID: ev.ID}, nil
}

// noteFor returns the note only for an annotate op — the ledger refuses a note
// on any other op, so a stray note never rides a link or unlink.
func noteFor(op storage.LinkOp, note string) string {
	if op == storage.LinkOpAnnotate {
		return note
	}
	return ""
}

// resolveMediaRef normalizes a media reference to its stored id and refuses an
// unknown one before anything is written (D3). It accepts the sidecar id
// directly or a stored path (reduced to its basename); a sidecar path
// (`<id>.json`) resolves too, by falling back to the id with a trailing `.json`
// trimmed — tried only when the basename itself names no stored media, so a
// media id that legitimately ends in `.json` still resolves directly.
func (r *Router) resolveMediaRef(media string) (string, error) {
	name := filepath.Base(strings.TrimSpace(media))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "", fmt.Errorf("link needs a media id; nothing was linked")
	}
	if rec, found, err := r.store.ReadMediaByID(name); err != nil {
		return "", err
	} else if found {
		return rec.ID, nil
	}
	if stripped := strings.TrimSuffix(name, ".json"); stripped != name {
		if rec, found, err := r.store.ReadMediaByID(stripped); err != nil {
			return "", err
		} else if found {
			return rec.ID, nil
		}
	}
	return "", fmt.Errorf("no media is stored under %q; nothing was linked", name)
}

// resolveSubjects parses and resolves every --to token, returning the resolved
// refs or the first refusal. It is shared by [Router.Link] and [Router.Attach]
// so a subject is validated by exactly one rule everywhere, and it writes
// nothing — resolution always precedes any append.
func (r *Router) resolveSubjects(subjects []string) ([]subjectRef, error) {
	refs := make([]subjectRef, 0, len(subjects))
	for _, tok := range subjects {
		kind, key, err := ParseSubjectRef(tok)
		if err != nil {
			return nil, err
		}
		if err := r.store.ResolveSubject(kind, key); err != nil {
			return nil, err
		}
		refs = append(refs, subjectRef{kind: kind, key: key})
	}
	return refs, nil
}

// liveSubjects folds one media's current links into a set of the (kind, key)
// subjects that are live, so a link/unlink turn can decide each subject's no-op
// in a single read (SC-8's fold, consumed through the seam rather than raw
// state). The media is fixed for the whole turn, so a bare subjectRef keys it.
func (r *Router) liveSubjects(mediaID string) (map[subjectRef]bool, error) {
	views, err := r.store.LinksForMedia(mediaID)
	if err != nil {
		return nil, err
	}
	live := make(map[subjectRef]bool, len(views))
	for _, v := range views {
		live[subjectRef{kind: v.Subject.Kind, key: v.Subject.Key}] = true
	}
	return live, nil
}

// linkAck renders the human-first acknowledgement for a turn, naming what
// actually changed and what was already in the desired state — so "already
// linked" reads as an acknowledgement of growth, never an error (SC-4).
func linkAck(op storage.LinkOp, mediaID string, applied []LinkApplied) string {
	var changed, noop []string
	for _, a := range applied {
		ref := a.Kind + ":" + a.Key
		if a.NoOp {
			noop = append(noop, ref)
		} else {
			changed = append(changed, ref)
		}
	}
	switch op {
	case storage.LinkOpUnlink:
		return ackParts(mediaID,
			ackClause("Unlinked", "from", changed),
			ackClause("was not linked", "to", noop))
	case storage.LinkOpAnnotate:
		return ackParts(mediaID, ackClause("Annotated", "for", changed), "")
	case storage.LinkOpLink:
		return ackParts(mediaID,
			ackClause("Linked", "to", changed),
			ackClause("already linked", "to", noop))
	}
	// Unreachable: op is one of the three link ops, validated before any append.
	return ackParts(mediaID, ackClause("Linked", "to", changed), ackClause("already linked", "to", noop))
}

// ackClause renders one half of an ack ("Linked … to a, b") or "" when its list
// is empty, so an ack names only the outcomes that actually occurred.
func ackClause(verb, prep string, refs []string) string {
	if len(refs) == 0 {
		return ""
	}
	return fmt.Sprintf("%s %s %s", verb, prep, strings.Join(refs, ", "))
}

// ackParts joins the non-empty clauses of an ack into one line, prefixed with
// the media id and closed with a period.
func ackParts(mediaID string, clauses ...string) string {
	var parts []string
	for _, c := range clauses {
		if c != "" {
			parts = append(parts, c)
		}
	}
	if len(parts) == 0 {
		return mediaID + ": nothing to do."
	}
	return fmt.Sprintf("%s: %s.", mediaID, strings.Join(parts, "; "))
}
