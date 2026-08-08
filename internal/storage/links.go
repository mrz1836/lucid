package storage

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Link-ledger names under ~/.lucid/links/ (data-model.md §"Link ledger").
// Only this adapter touches the tree; every link/unlink/annotate is one
// append-only JSONL event in a single file, and current links are a fold of
// that log. The immutable media binary and sidecar are never touched by a link
// operation — all mutability lives here (data-model.md §"The fold"). subject
// kind and key are stored as opaque values, never as path components: there is
// exactly one file, so the open taxonomy costs one resolver per kind, not one
// path per kind (data-model.md §"Link ledger").
const (
	linksDirName  = "links"
	linksFileName = "links.jsonl"
	linkIDPrefix  = "link_"

	// LinkSchema stamps the record family on every event so a future format
	// change is distinguishable on disk (data-model.md §"Link ledger").
	LinkSchema = "link.v1"
)

// LinkOp is the verb one link event records. link makes a (media, subject) pair
// live, unlink makes it not live, and annotate attaches a note without changing
// liveness — annotate is its own op and never implies link.
type LinkOp string

// The three link ops. Any other value is refused by [LinkEvent.validate].
const (
	LinkOpLink     LinkOp = "link"
	LinkOpUnlink   LinkOp = "unlink"
	LinkOpAnnotate LinkOp = "annotate"
)

// LinkSubject names the thing a media is linked to, opaquely: kind selects the
// resolver family (person, injury, anchor, …) and key is that family's own
// native id. The link ledger never interprets either — resolution happens
// upstream, before an event is appended.
type LinkSubject struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
}

// LinkEvent is one append-only line of the link ledger (data-model.md §"Link
// ledger"). The caller supplies everything but Schema and ID; storage stamps
// the schema and assigns the monotonic id, mirroring [Adapter.AppendObservation].
type LinkEvent struct {
	// Schema is the record-family tag (LinkSchema); storage stamps it.
	Schema string `json:"schema"`
	// ID is the monotonic per-file sequence id (link_<n>); storage assigns it.
	ID string `json:"id"`
	// MediaID is the media sidecar id the event points from.
	MediaID string `json:"media_id"`
	// Subject is the thing the media is linked to (kind + native key).
	Subject LinkSubject `json:"subject"`
	// Op is the verb: link, unlink, or annotate.
	Op LinkOp `json:"op"`
	// At is when the event was recorded (RFC3339, local offset).
	At string `json:"at"`
	// Source is the provenance token (cli, or a harness token), stored as-is.
	Source string `json:"source"`
	// Free-text annotation carried by an annotate op; present only then.
	Note string `json:"note,omitempty"`
}

// linksDir returns the ~/.lucid/links/ store root.
func (a *Adapter) linksDir() string { return filepath.Join(a.home, linksDirName) }

// linksPath returns the single append-only log ~/.lucid/links/links.jsonl.
func (a *Adapter) linksPath() string { return filepath.Join(a.linksDir(), linksFileName) }

// ScaffoldLinks creates the links/ tree if missing (data-model.md §"Link
// ledger"). It is lazy and idempotent — a re-run makes no changes — mirroring
// [Adapter.ScaffoldMedia]. [Adapter.AppendLinkEvent] also creates the tree on
// demand, so scaffolding is only about having the store present from init.
func (a *Adapter) ScaffoldLinks() error {
	if err := os.MkdirAll(a.linksDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create links dir: %w", err)
	}
	return nil
}

// AppendLinkEvent stamps the schema, assigns the next per-file sequence id under
// the single-writer discipline, validates the event, and appends it as one
// fsync'd JSONL line — the same append shape as [Adapter.AppendObservation]. It
// returns the event with its id filled. It never touches the media binary or
// its sidecar (data-model.md §"Immutability").
func (a *Adapter) AppendLinkEvent(ev LinkEvent) (LinkEvent, error) {
	ev.Schema = LinkSchema
	path := a.linksPath()

	seq, err := a.nextLinkSeq(path)
	if err != nil {
		return LinkEvent{}, err
	}
	ev.ID = linkIDPrefix + strconv.Itoa(seq)

	if err = ev.validate(); err != nil {
		return LinkEvent{}, err
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return LinkEvent{}, fmt.Errorf("storage: marshal link event: %w", err)
	}
	if err := appendLineFsync(path, line); err != nil {
		return LinkEvent{}, err
	}
	return ev, nil
}

// nextLinkSeq returns max-seq+1 over the well-formed lines in the log — the id
// is parsed from each line, never derived from the line count, so a truncated
// or malformed line never perturbs id assignment and an id is never reused. A
// missing file starts at seq 1. This mirrors [Adapter.nextObsSeq].
func (a *Adapter) nextLinkSeq(path string) (int, error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return 0, err
	}
	maxSeq := 0
	for _, ln := range lines {
		var ev LinkEvent
		if json.Unmarshal(ln, &ev) != nil {
			continue // unparseable line: contributes no seq
		}
		if s, ok := parseLinkSeq(ev.ID); ok && s > maxSeq {
			maxSeq = s
		}
	}
	return maxSeq + 1, nil
}

// ReadLinkEvents reads every link event in append order, skipping malformed
// lines and reporting how many were skipped (mirroring [Adapter.readObsFile]).
// A malformed line is one that fails to parse or fails validation, so the fold
// never sees a partial event.
func (a *Adapter) ReadLinkEvents() (events []LinkEvent, skipped int, err error) {
	lines, err := readJSONLLines(a.linksPath())
	if err != nil {
		return nil, 0, err
	}
	for _, ln := range lines {
		ev, perr := unmarshalLinkEvent(ln)
		if perr != nil {
			skipped++
			continue
		}
		events = append(events, ev)
	}
	return events, skipped, nil
}

// ListLinkEventIDs returns a locator for every line in the log, in append order
// — the parsed event id when the line is well-formed, else a positional
// line-<n> locator so a malformed line is still nameable in a validate finding.
// It backs the schema sweep (CheckLedgerSchema), which is the first JSONL family
// it walks. A missing log yields no ids and no error.
func (a *Adapter) ListLinkEventIDs() ([]string, error) {
	lines, err := a.linkLines()
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(lines))
	for i, ln := range lines {
		ids[i] = ln.locator
	}
	return ids, nil
}

// ReadLinkEventErr re-parses the single line addressed by locator and returns
// only its parse/validation error (nil when it is well-formed). It is the
// per-record read the schema sweep pairs with [Adapter.ListLinkEventIDs]; an
// unknown locator is treated as clean (the listing and the read see the same
// file). It is read-only — the record is never rewritten.
func (a *Adapter) ReadLinkEventErr(id string) error {
	lines, err := a.linkLines()
	if err != nil {
		return err
	}
	for _, ln := range lines {
		if ln.locator == id {
			_, perr := unmarshalLinkEvent(ln.raw)
			return perr
		}
	}
	return nil
}

// linkLine pairs a raw JSONL line with the locator the schema sweep addresses it
// by: the event's own id when the line parses to one, else a positional
// line-<n> locator so a malformed line is still nameable.
type linkLine struct {
	locator string
	raw     []byte
}

// linkLines reads the log's raw lines and assigns each a stable locator, so the
// listing and per-record read agree on how every line — well-formed or not — is
// named. A missing log yields no lines and no error.
func (a *Adapter) linkLines() ([]linkLine, error) {
	lines, err := readJSONLLines(a.linksPath())
	if err != nil {
		return nil, err
	}
	out := make([]linkLine, 0, len(lines))
	for i, ln := range lines {
		loc := fmt.Sprintf("line-%d", i+1)
		var ev LinkEvent
		if json.Unmarshal(ln, &ev) == nil && ev.ID != "" {
			loc = ev.ID
		}
		out = append(out, linkLine{locator: loc, raw: ln})
	}
	return out, nil
}

// unmarshalLinkEvent decodes and validates one JSONL line into a LinkEvent. It
// is the single definition of "well-formed" every reader shares, so the fold,
// the schema sweep, and the listing never disagree about which lines are good.
func unmarshalLinkEvent(raw []byte) (LinkEvent, error) {
	var ev LinkEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return LinkEvent{}, fmt.Errorf("storage: parse link event: %w", err)
	}
	if err := ev.validate(); err != nil {
		return LinkEvent{}, err
	}
	return ev, nil
}

// validate reports the first structural problem with a link event before it is
// written and after it is read back. kind and key are checked for presence only
// — the ledger stores them opaquely; the resolver upstream decides what they
// name.
func (ev LinkEvent) validate() error {
	if ev.MediaID == "" {
		return errors.New("storage: link event media_id is required")
	}
	if ev.Subject.Kind == "" {
		return errors.New("storage: link event subject.kind is required")
	}
	if ev.Subject.Key == "" {
		return errors.New("storage: link event subject.key is required")
	}
	switch ev.Op {
	case LinkOpLink, LinkOpUnlink, LinkOpAnnotate:
	default:
		return fmt.Errorf("storage: link event op %q is not one of link|unlink|annotate", ev.Op)
	}
	if _, err := time.Parse(time.RFC3339, ev.At); err != nil {
		return fmt.Errorf("storage: link event at %q invalid: %w", ev.At, err)
	}
	if ev.Source == "" {
		return errors.New("storage: link event source is required")
	}
	if ev.Note != "" && ev.Op != LinkOpAnnotate {
		return fmt.Errorf("storage: link event note is allowed only on an annotate op, not %q", ev.Op)
	}
	return nil
}

// parseLinkSeq extracts the numeric sequence from a link_<n> id.
func parseLinkSeq(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, linkIDPrefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// LinkView is one live association plus every annotation ever applied to it —
// the shape both fold reads return (data-model.md §"The fold"). LinkedAt and
// Source are the most recent link event's; Notes accumulate across the whole
// history in append order.
type LinkView struct {
	MediaID  string
	Subject  LinkSubject
	LinkedAt string
	Source   string
	Notes    []LinkNote
}

// LinkNote is one annotation folded onto a pair.
type LinkNote struct{ At, Source, Text string }

// linkPairKey identifies one folded association: a (media, subject) pair.
type linkPairKey struct {
	mediaID string
	kind    string
	key     string
}

// foldedLink is the mutable accumulator foldLinks builds per pair before the
// public reads project it to a [LinkView]. Notes are recorded regardless of
// liveness (an annotate folds independently); only live pairs are surfaced.
type foldedLink struct {
	subject  LinkSubject
	mediaID  string
	live     bool
	linkedAt string
	source   string
	notes    []LinkNote
}

// view projects the accumulator to the read shape.
func (fl *foldedLink) view() LinkView {
	return LinkView{
		MediaID:  fl.mediaID,
		Subject:  fl.subject,
		LinkedAt: fl.linkedAt,
		Source:   fl.source,
		Notes:    fl.notes,
	}
}

// foldLinks folds the log to current state per (media_id, kind, key),
// last-write-wins ordered by at then append order (data-model.md §"The fold"):
// link → live, unlink → not live, annotate → liveness unchanged but the note
// recorded. The stable sort on the already-append-ordered events makes a
// hand-edited or clock-skewed at degrade to insertion order rather than reorder
// history.
func foldLinks(events []LinkEvent) map[linkPairKey]*foldedLink {
	ordered := slices.Clone(events)
	slices.SortStableFunc(ordered, func(a, b LinkEvent) int { return cmp.Compare(a.At, b.At) })

	folded := map[linkPairKey]*foldedLink{}
	for _, ev := range ordered {
		k := linkPairKey{mediaID: ev.MediaID, kind: ev.Subject.Kind, key: ev.Subject.Key}
		fl := folded[k]
		if fl == nil {
			fl = &foldedLink{subject: ev.Subject, mediaID: ev.MediaID}
			folded[k] = fl
		}
		switch ev.Op {
		case LinkOpLink:
			fl.live = true
			fl.linkedAt = ev.At
			fl.source = ev.Source
		case LinkOpUnlink:
			fl.live = false
		case LinkOpAnnotate:
			fl.notes = append(fl.notes, LinkNote{At: ev.At, Source: ev.Source, Text: ev.Note})
		}
	}
	return folded
}

// LinksForSubject returns the media currently linked to one subject, folded from
// the append-only log (data-model.md §"The fold"). Output is sorted (media id,
// then kind, then key) so every consuming surface is byte-stable. Only live
// pairs are returned.
func (a *Adapter) LinksForSubject(kind, key string) ([]LinkView, error) {
	return a.foldedViews(func(k linkPairKey, fl *foldedLink) bool {
		return fl.live && k.kind == kind && k.key == key
	})
}

// LinksForMedia returns the subjects one media is currently linked to, folded
// the same way and sorted the same way. Only live pairs are returned.
func (a *Adapter) LinksForMedia(mediaID string) ([]LinkView, error) {
	return a.foldedViews(func(k linkPairKey, fl *foldedLink) bool {
		return fl.live && k.mediaID == mediaID
	})
}

// foldedViews reads the log, folds it, and returns the views of the pairs the
// predicate keeps — the shared body of the two directional reads, so they can
// never disagree about a link.
func (a *Adapter) foldedViews(keep func(linkPairKey, *foldedLink) bool) ([]LinkView, error) {
	events, _, err := a.ReadLinkEvents()
	if err != nil {
		return nil, err
	}
	folded := foldLinks(events)
	var out []LinkView
	for k, fl := range folded {
		if keep(k, fl) {
			out = append(out, fl.view())
		}
	}
	sortLinkViews(out)
	return out, nil
}

// sortLinkViews orders views by media id, then subject kind, then subject key.
func sortLinkViews(views []LinkView) {
	slices.SortFunc(views, func(a, b LinkView) int {
		return cmp.Or(
			cmp.Compare(a.MediaID, b.MediaID),
			cmp.Compare(a.Subject.Kind, b.Subject.Kind),
			cmp.Compare(a.Subject.Key, b.Subject.Key),
		)
	})
}
