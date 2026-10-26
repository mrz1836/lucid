package validate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"

	"github.com/mrz1836/lucid/internal/engine"
)

// The Ledger-relative paths the anchors check reports against
// (engine-module.md §"Anchor event store" → "Reads, older binaries, validation
// and backup").
const (
	anchorsFilePath      = "engine/anchors.json"
	anchorEventsFilePath = "engine/anchor_events.json"
)

// The anchors sub-rules, each a stable string a script (or a test) can branch
// on. They split into the two files' read and record schemas, the event
// store's cross-record and cross-file joins, and the pair intents a two-file
// write leaves behind (error-states.md An-14).
const (
	ruleAnchorsRead           = "anchors-read"
	ruleAnchorsVersion        = "anchors-version"
	ruleAnchorLabel           = "anchor-label"
	ruleAnchorDate            = "anchor-date"
	ruleAnchorState           = "anchor-state"
	ruleAnchorEventsRead      = "anchor-events-read"
	ruleAnchorEvent           = "anchor-event"
	ruleAnchorEventDuplicate  = "anchor-event-duplicate"
	ruleAnchorEventTarget     = "anchor-event-target"
	ruleAnchorVoidTarget      = "anchor-void-target"
	ruleAnchorPairMalformed   = "anchor-pair-malformed"
	ruleAnchorPairDuplicate   = "anchor-pair-duplicate"
	ruleAnchorPairMismatch    = "anchor-pair-mismatch"
	ruleAnchorPairHalfWritten = "anchor-pair-half-written"
	ruleAnchorPairAbandoned   = "anchor-pair-abandoned"
)

// CheckAnchorFiles validates anchors.json and the anchor event store, read-only
// (the [CheckAnchors] gate; engine-module.md §"Anchor event store" →
// "Validation"). It checks each anchors.json record, each stored event, and the
// joins between them: every event targets a recorded anchor, every void
// target is legal, and every pair intent is whole. Each defect is one finding
// naming the file, the record or event and the rule; nothing is ever rewritten
// or repaired.
//
// It never fails and never panics: a file that cannot be read is itself a
// finding, and the checks that need it are skipped rather than flooding the
// report with consequences of one defect. A missing anchors.json or a missing
// event store is clean — a Ledger that never recorded a kind has no store.
func CheckAnchorFiles(src LedgerSource) []Finding {
	var s anchorSweep

	anchors, anchorsErr := src.ReadAnchorsErr()
	anchorsPresent := !errors.Is(anchorsErr, fs.ErrNotExist)
	anchorsOK := s.readable(anchorsFilePath, ruleAnchorsRead, anchorsErr)
	if anchorsOK && anchorsPresent {
		s.checkAnchorRecords(anchors)
	}

	events, eventsErr := src.ReadAnchorEventsErr()
	eventsOK := s.readable(anchorEventsFilePath, ruleAnchorEventsRead, eventsErr)
	if !eventsOK {
		return s.findings
	}
	idx := s.checkEventRecords(events)
	s.checkVoids(idx)
	if anchorsOK {
		s.checkEventTargets(events, anchors, idx)
		s.checkPairIntents(events, anchors, idx)
	}
	return s.findings
}

// anchorSweep accumulates the anchors check's findings.
type anchorSweep struct {
	findings []Finding
}

// add records one finding at path under rule.
func (s *anchorSweep) add(sev Severity, path, rule, format string, args ...any) {
	s.findings = append(s.findings, Finding{
		Check:    CheckAnchors,
		Severity: sev,
		Path:     path,
		Rule:     rule,
		Message:  fmt.Sprintf(format, args...),
	})
}

// readable turns a file read into at most one finding and reports whether the
// file's content can be checked. A missing file is clean (nothing recorded
// yet); any other failure is one error-severity finding naming the file — a
// body that is not JSON at all, or not the file's shape, says so plainly.
func (s *anchorSweep) readable(path, rule string, err error) bool {
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return true
	}
	name := path[strings.LastIndexByte(path, '/')+1:]
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntaxErr):
		s.add(SeverityError, path, rule, "%s is not valid JSON — %v", name, syntaxErr)
	case errors.As(err, &typeErr):
		s.add(SeverityError, path, rule, "%s does not have the file's shape — %v", name, typeErr)
	default:
		s.add(SeverityError, path, rule, "%s could not be read — %v", name, err)
	}
	return false
}

// checkAnchorRecords is the anchors.json schema check: a version of at least
// 1 (every binary that ever wrote the file stamped one), and per record a
// non-empty label, a civil date and a state of empty or sunset. One record
// with two defects is two findings.
func (s *anchorSweep) checkAnchorRecords(log engine.AnchorLog) {
	if log.Version < 1 {
		s.add(SeverityError, anchorsFilePath, ruleAnchorsVersion,
			"version %d is below 1; every binary that writes anchors.json stamps at least 1", log.Version)
	}
	for i, a := range log.History {
		loc := anchorRecordLocator(i, a)
		if strings.TrimSpace(a.Label) == "" {
			s.add(SeverityError, anchorsFilePath, ruleAnchorLabel, "%s: the label is empty", loc)
		}
		if _, err := time.Parse(time.DateOnly, a.Date); err != nil {
			s.add(SeverityError, anchorsFilePath, ruleAnchorDate, "%s: date %q is not a YYYY-MM-DD date", loc, a.Date)
		}
		if a.State != "" && a.State != engine.AnchorStateSunset {
			s.add(SeverityError, anchorsFilePath, ruleAnchorState,
				"%s: state %q is neither empty (active) nor %q", loc, a.State, engine.AnchorStateSunset)
		}
	}
}

// anchorRecordLocator names one anchors.json record by its position, plus its
// id — or, for a record written before ids existed, its label.
func anchorRecordLocator(i int, a engine.Anchor) string {
	if a.ID != "" {
		return fmt.Sprintf("history[%d] (%s)", i, a.ID)
	}
	return fmt.Sprintf("history[%d] (%q)", i, a.Label)
}

// eventIndex is the event store as the fold reads it: the first well-formed
// record under each id, in append order, plus every id any record carries
// (well formed or not), so a reference to a malformed record can say so.
type eventIndex struct {
	valid []engine.AnchorEvent
	byID  map[string]engine.AnchorEvent
	anyID map[string]bool
}

// checkEventRecords is the event-store schema check. Each stored event must be
// well formed ([engine.ValidateAnchorEvent]: the id grammar, a minted anchor
// id, a known type, a civil day, an RFC3339 recorded_at and its per-type
// fields, kind parameters included), and an id is a receipt, so a second
// well-formed record under an id already read is a duplicate the fold
// ignores. The store's version is not checked: it is a signal, not a gate.
func (s *anchorSweep) checkEventRecords(log engine.AnchorEventLog) eventIndex {
	idx := eventIndex{byID: map[string]engine.AnchorEvent{}, anyID: map[string]bool{}}
	for i, e := range log.Events {
		idx.anyID[e.ID] = true
		if err := engine.ValidateAnchorEvent(e); err != nil {
			s.add(SeverityError, anchorEventsFilePath, ruleAnchorEvent, "events[%d]: %s", i, engineCopy(err))
			continue
		}
		if _, dup := idx.byID[e.ID]; dup {
			s.add(SeverityError, anchorEventsFilePath, ruleAnchorEventDuplicate,
				"events[%d]: %s reuses an event id already recorded; an id is a receipt, so only the first counts", i, e.ID)
			continue
		}
		idx.byID[e.ID] = e
		idx.valid = append(idx.valid, e)
	}
	return idx
}

// checkVoids reports every void whose target is not legal, in the order the
// fold applies voids: the target must be a well-formed event of the same
// anchor, of a voidable type (never a kind event, never a void), and not
// already voided by an earlier void. The fold ignores each void reported here.
func (s *anchorSweep) checkVoids(idx eventIndex) {
	voidedBy := map[string]string{}
	for _, v := range idx.valid {
		if v.Type != engine.AnchorEventVoid {
			continue
		}
		if problem := voidProblem(v, idx, voidedBy); problem != "" {
			s.add(SeverityError, anchorEventsFilePath, ruleAnchorVoidTarget, "%s: %s; the fold ignores it", v.ID, problem)
			continue
		}
		voidedBy[v.Target] = v.ID
	}
}

// voidProblem explains why a void's target is not legal, or returns "" when it
// is.
func voidProblem(v engine.AnchorEvent, idx eventIndex, voidedBy map[string]string) string {
	t, ok := idx.byID[v.Target]
	switch {
	case !ok && idx.anyID[v.Target]:
		return fmt.Sprintf("voids %s, a record that is not well formed", v.Target)
	case !ok:
		return fmt.Sprintf("voids %s, which no recorded event holds", v.Target)
	case t.AnchorID != v.AnchorID:
		return fmt.Sprintf("voids %s, an event of another anchor (%s)", v.Target, t.AnchorID)
	case t.Type == engine.AnchorEventKind:
		return fmt.Sprintf("voids the kind event %s; a kind event is never voided — a wrong kind is reclassified", v.Target)
	case t.Type == engine.AnchorEventVoid:
		return fmt.Sprintf("voids %s, which is itself a void; a void is never voided", v.Target)
	}
	if by, already := voidedBy[v.Target]; already {
		return fmt.Sprintf("voids %s, which %s already voided", v.Target, by)
	}
	return ""
}

// checkEventTargets reports every well-formed event whose anchor_id no record
// in anchors.json holds: the fold ignores it, so it counts toward nothing. The
// one exception is the kind event of an unfinished `anchor add --kind`, which
// dangles by design until its definition lands — its pair intent is reported
// instead, so the defect is named once.
func (s *anchorSweep) checkEventTargets(events engine.AnchorEventLog, anchors engine.AnchorLog, idx eventIndex) {
	known := recordedAnchorIDs(anchors)
	dangling := map[string]bool{}
	for _, st := range engine.AnchorPairStates(events, anchors) {
		if st.Status != engine.AnchorPairComplete && st.Status != engine.AnchorPairMalformed && st.Intent.IsAddKind() {
			dangling[st.Intent.Event.ID] = true
		}
	}
	for _, e := range idx.valid {
		if known[e.AnchorID] || dangling[e.ID] {
			continue
		}
		s.add(SeverityError, anchorEventsFilePath, ruleAnchorEventTarget,
			"%s targets %s, which no anchor in anchors.json holds; the fold ignores it", e.ID, e.AnchorID)
	}
}

// recordedAnchorIDs is the set of minted ids any anchors.json record carries.
func recordedAnchorIDs(anchors engine.AnchorLog) map[string]bool {
	known := make(map[string]bool, len(anchors.History))
	for _, a := range anchors.History {
		if a.ID != "" {
			known[a.ID] = true
		}
	}
	return known
}

// checkPairIntents reports every pair intent that is not whole: a malformed
// intent, a second intent reserving an id already reserved, and every
// incomplete pair — intent-only, compat-only or event-only — as a half-written
// pair naming its reserved event id, the missing half and the command to
// retry. A pair whose stored event differs from the planned one can't be
// finished by a retry and says so. An unfinished `anchor add --kind` whose
// reserved anchor id is already recorded can never be finished and nothing
// reads it, so it is the check's one warning.
func (s *anchorSweep) checkPairIntents(events engine.AnchorEventLog, anchors engine.AnchorLog, idx eventIndex) {
	known := recordedAnchorIDs(anchors)
	facts := engine.FoldAnchorEvents(events, anchors)
	reserved := map[string]bool{}
	for i, st := range engine.AnchorPairStates(events, anchors) {
		id := st.Intent.Event.ID
		switch {
		case st.Status == engine.AnchorPairMalformed:
			s.add(SeverityError, anchorEventsFilePath, ruleAnchorPairMalformed, "pair_intents[%d]: %s", i, engineCopy(st.Err))
			continue
		case reserved[id]:
			s.add(SeverityError, anchorEventsFilePath, ruleAnchorPairDuplicate,
				"pair_intents[%d]: %s is already reserved by an earlier pair intent", i, id)
			continue
		}
		reserved[id] = true
		if st.Status != engine.AnchorPairComplete {
			s.incompletePair(st, known, facts, idx)
		}
	}
}

// incompletePair reports one incomplete pair with the finding that fits it.
func (s *anchorSweep) incompletePair(
	st engine.AnchorPairState, known map[string]bool, facts map[string]engine.AnchorFacts, idx eventIndex,
) {
	p := st.Intent
	id := p.Event.ID
	switch {
	case p.IsAddKind() && known[p.Event.AnchorID]:
		s.add(SeverityWarn, anchorEventsFilePath, ruleAnchorPairAbandoned,
			"%s: unfinished anchor add --kind for %q can no longer be finished — anchor id %s is already recorded, so %s",
			id, p.Compat.Label, p.Event.AnchorID, abandonedConsequence(st))
	case st.Status != engine.AnchorPairEventOnly && idx.anyID[id]:
		s.add(SeverityError, anchorEventsFilePath, ruleAnchorPairMismatch,
			"%s: half-written pair — %s, and the event recorded under its id is not the one its intent planned, "+
				"so a retry can't finish it", id, st.MissingHalves())
	default:
		s.add(SeverityError, anchorEventsFilePath, ruleAnchorPairHalfWritten,
			"%s: half-written pair — %s; retry %s", id, st.MissingHalves(), retryCommand(p, currentLabel(p, facts)))
	}
}

// abandonedConsequence says what an unfinishable add --kind left behind.
func abandonedConsequence(st engine.AnchorPairState) string {
	p := st.Intent
	switch st.Status {
	case engine.AnchorPairEventOnly:
		return "its kind event is ignored and nothing reads it"
	case engine.AnchorPairCompatOnly:
		return fmt.Sprintf("its definition landed without its kind event and reads as a milestone — "+
			"reclassify it with %s if it should carry the kind", kindCommand(p.Compat.Label, p.Event))
	}
	return "it recorded no fact and nothing reads it"
}

// currentLabel is the label a retry names: the anchor's current folded label
// (an older binary may have renamed it since the write began), falling back to
// the planned record's label when no anchor holds the id.
func currentLabel(p engine.AnchorPairIntent, facts map[string]engine.AnchorFacts) string {
	if f, ok := facts[p.Event.AnchorID]; ok && f.Anchor.Label != "" {
		return f.Anchor.Label
	}
	return p.Compat.Label
}

// retryCommand is the command whose explicit retry finishes a pair: the
// original verb with the facts a retry is matched on (error-states.md An-13)
// — a slip's day, a kind event's kind and parameters, a void's target. An
// add --kind is retried with its planned label and date.
func retryCommand(p engine.AnchorPairIntent, label string) string {
	e := p.Event
	switch e.Type {
	case engine.AnchorEventSlip:
		return fmt.Sprintf("lucid anchor slip %s --day %s", shellWord(label), e.Day)
	case engine.AnchorEventVoid:
		return "lucid anchor void " + e.Target
	case engine.AnchorEventGraduate:
		return fmt.Sprintf("lucid anchor sunset %s --graduated", shellWord(label))
	case engine.AnchorEventKind:
		if p.IsAddKind() {
			return fmt.Sprintf("lucid anchor add %s %s --kind %s%s",
				shellWord(p.Compat.Label), p.Compat.Date, e.Kind, kindParams(e))
		}
		return kindCommand(label, e)
	}
	return "the original command"
}

// kindCommand is the `anchor kind` command that records kind event e's kind
// and parameters on label.
func kindCommand(label string, e engine.AnchorEvent) string {
	return fmt.Sprintf("lucid anchor kind %s %s%s", shellWord(label), e.Kind, kindParams(e))
}

// kindParams renders a kind event's parameters as the flags that set them.
func kindParams(e engine.AnchorEvent) string {
	var b strings.Builder
	if e.Allowance > 0 {
		fmt.Fprintf(&b, " --allowance %d", e.Allowance)
	}
	if e.WindowDays > 0 {
		fmt.Fprintf(&b, " --window %d", e.WindowDays)
	}
	return b.String()
}

// shellSafeWord matches a word a POSIX shell passes through unchanged.
var shellSafeWord = regexp.MustCompile(`^[A-Za-z0-9_.,:@%+=/-]+$`)

// shellWord renders a label so the retry command can be pasted as written: a
// plain word as it is, anything else single-quoted.
func shellWord(s string) string {
	if shellSafeWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// engineCopy is an engine validation error as finding copy: the package
// prefixes, which nest when one validator wraps another, are dropped.
func engineCopy(err error) string {
	return strings.ReplaceAll(err.Error(), "engine: ", "")
}
