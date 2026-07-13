package storage

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// reflectionsDirName is the Mirror subtree of weekly reflection records, one
// Markdown-with-frontmatter file per ISO week (data-model.md §"Weekly
// reflections"). Only the adapter writes it; a record is append-only within a
// single week.
const (
	reflectionsDirName = "reflections"
	reflectionExt      = ".md"
)

// Recall status-history transition kinds appended to an insight when the user
// answers a `/reflect` surface (data-model.md §"Validated insights";
// acceptance-criteria.md Phase 6). `accepted` (the initial kind) is set at
// validation; these three come from recall responses. `retired` also flips the
// insight's status field and stamps retired_at.
const (
	RecallConfirmed = "confirmed"
	RecallSoftened  = "softened"
	RecallRetired   = "retired"
)

// isRecallStatusKind reports whether kind is a valid status-history recall
// transition. `unanswered` is not a transition — it is recorded on the
// reflection record, not on the insight — so it is intentionally excluded.
func isRecallStatusKind(kind string) bool {
	switch kind {
	case RecallConfirmed, RecallSoftened, RecallRetired:
		return true
	default:
		return false
	}
}

// isRuleStatusKind reports whether kind is a valid rule-history recall
// transition (data-model.md §"Validated insights": kept | lapsed | retired).
// `stated` is set only at validation via SetInsightRule, never at recall.
func isRuleStatusKind(kind string) bool {
	switch kind {
	case RuleKept, RuleLapsed, RuleRetire:
		return true
	default:
		return false
	}
}

// UpdateInsightStatus appends one status-history transition to an insight and
// stamps the matching timestamp field (last_confirmed_at / last_softened_at /
// retired_at), re-rendering and re-validating before write so an update can
// never corrupt the record (agent-contracts.md §3; acceptance-criteria.md
// 6.1). status_history is append-only: a repeat confirm within a week appends a
// second entry rather than deduplicating, which is exactly the idempotent
// behavior error-states.md §R-9 specifies. `retired` also flips status to
// retired so the insight leaves future recall windows.
func (a *Adapter) UpdateInsightStatus(insightID, kind string, at time.Time) error {
	if !isRecallStatusKind(kind) {
		return fmt.Errorf("storage: update_insight_status: %q is not confirmed|softened|retired", kind)
	}
	ins, err := a.ReadInsight(insightID)
	if err != nil {
		return err
	}
	ins.StatusHistory = append(ins.StatusHistory, TimedEvent{At: at, Kind: kind})
	switch kind {
	case RecallConfirmed:
		ins.LastConfirmedAt = timePtr(at)
	case RecallSoftened:
		ins.LastSoftenedAt = timePtr(at)
	case RecallRetired:
		ins.Status = InsightStatusRetired
		ins.RetiredAt = timePtr(at)
	}
	return a.rewriteInsight(insightID, ins)
}

// UpdateInsightRuleStatus appends one rule-history transition (kept | lapsed |
// retired) to a ruled insight, re-rendering and re-validating before write. It
// is the storage side of the recall rule question; the router calls it only
// when the user's answer maps to one of the three kinds — an unmapped answer
// records nothing (error-states.md §R-15), so this op is never reached for it.
// A rule transition never changes the insight's own status: a lapsed rule is a
// judgment-free record, not a retirement (agent-contracts.md §3 "Rules").
func (a *Adapter) UpdateInsightRuleStatus(insightID, kind string, at time.Time) error {
	if !isRuleStatusKind(kind) {
		return fmt.Errorf("storage: update_insight_rule_status: %q is not kept|lapsed|retired", kind)
	}
	ins, err := a.ReadInsight(insightID)
	if err != nil {
		return err
	}
	if ins.Rule == nil {
		return fmt.Errorf("storage: update_insight_rule_status: insight %q has no rule", insightID)
	}
	ins.RuleHistory = append(ins.RuleHistory, TimedEvent{At: at, Kind: kind})
	return a.rewriteInsight(insightID, ins)
}

// rewriteInsight renders, validates, and overwrites an insight file in place —
// the shared tail of the two recall update ops. Validation runs on the rendered
// bytes, so a bad transition is caught before it can reach disk.
func (a *Adapter) rewriteInsight(insightID string, ins Insight) error {
	content, err := renderInsight(ins)
	if err != nil {
		return err
	}
	if err = ValidateInsight(content); err != nil {
		return fmt.Errorf("storage: insight failed validation after recall update: %w", err)
	}
	path, err := a.insightPath(insightID)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, filePerm)
}

// timePtr returns a pointer to t (the nullable *At fields on an insight are
// pointers so an absent timestamp renders as YAML null).
func timePtr(t time.Time) *time.Time { return &t }

// ReadInsightsWindow returns the accepted insights a recall pass surfaces. It
// loads every insight, keeps those whose status is `accepted` and whose
// created_at is at or after `since` (pass the zero Time for "any age"), sorts
// them most-recent-first by created_at, and truncates to `limit` (limit <= 0
// means no cap). `/reflect` calls it with since = now−7d and no cap; `/reflect
// gate` with the zero Time and the 50-insight cap; the empty-week fallback with
// the zero Time and a cap of two (agent-contracts.md §3; error-states.md §R-7).
// Retired insights are excluded — recall surfaces only what is still standing.
func (a *Adapter) ReadInsightsWindow(since time.Time, limit int) ([]Insight, error) {
	ids, err := a.ListInsightIDs()
	if err != nil {
		return nil, err
	}
	out := make([]Insight, 0, len(ids))
	for _, id := range ids {
		ins, err := a.ReadInsight(id)
		if err != nil {
			return nil, err
		}
		if ins.Status != InsightStatusAccepted {
			continue
		}
		if !since.IsZero() && ins.CreatedAt.Before(since) {
			continue
		}
		out = append(out, ins)
	}
	slices.SortStableFunc(out, func(a, b Insight) int { return b.CreatedAt.Compare(a.CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// lastStatusAt returns the timestamp of an insight's most recent status_history
// transition — the recency key /ask orders the insights slice by
// (agent-contracts.md §3 answer_grounded: "capped at the 50 most recent by
// status_history[].at of the last accept/confirm"). A validated insight always
// carries at least the initial accepted entry, so the fallback to created_at is
// defensive only.
func lastStatusAt(ins Insight) time.Time {
	if n := len(ins.StatusHistory); n > 0 {
		return ins.StatusHistory[n-1].At
	}
	return ins.CreatedAt
}

// ReadAcceptedInsights returns accepted insights ordered most-recent-first by
// the timestamp of their last status_history transition, capped at limit
// (limit <= 0 means no cap). It is the insights-slice primitive `/ask` builds
// on (agent-contracts.md §3). Ties on the transition timestamp break by id
// descending so the ordering — and therefore any output computed from it — is
// byte-stable across repeated runs (S-6, S-22). Retired insights are excluded.
func (a *Adapter) ReadAcceptedInsights(limit int) ([]Insight, error) {
	ids, err := a.ListInsightIDs()
	if err != nil {
		return nil, err
	}
	out := make([]Insight, 0, len(ids))
	for _, id := range ids {
		ins, err := a.ReadInsight(id)
		if err != nil {
			return nil, err
		}
		if ins.Status != InsightStatusAccepted {
			continue
		}
		out = append(out, ins)
	}
	slices.SortStableFunc(out, func(a, b Insight) int {
		if c := lastStatusAt(b).Compare(lastStatusAt(a)); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ReflectionSurfaced is one line of a reflection record's insights_surfaced[]:
// which insight was surfaced and how the user answered (data-model.md §"Weekly
// reflections"). response_kind is confirmed | softened | retired | unanswered.
type ReflectionSurfaced struct {
	ID           string
	ResponseKind string
}

// Reflection is one weekly reflection record (data-model.md §"Weekly
// reflections"). A record is created on the first `/reflect` of its ISO week
// and appended to on later passes: Surfaced and ChangeLog accumulate while the
// body Summary is set once. ID / ISOWeek / WindowStart / WindowEnd come from
// the isoweek helper; NewInsightIDs is always empty because `/reflect` never
// proposes.
type Reflection struct {
	ID            string
	ISOWeek       string
	WindowStart   time.Time
	WindowEnd     time.Time
	CreatedAt     time.Time
	AgentVersion  string
	Surfaced      []ReflectionSurfaced
	NewInsightIDs []string
	Notes         *string
	Summary       string
	ChangeLog     []string
}

// ReflectionResult reports where a reflection record landed and whether this
// pass created it (versus appending to an existing week file).
type ReflectionResult struct {
	ID      string
	Path    string
	Created bool
}

// reflectionFrontmatter is the on-disk YAML frontmatter; field order matches
// data-model.md §"Weekly reflections" so a written record reads like the
// documented schema.
type reflectionFrontmatter struct {
	ID               string                   `yaml:"id"`
	ISOWeek          string                   `yaml:"iso_week"`
	WindowStart      string                   `yaml:"window_start"`
	WindowEnd        string                   `yaml:"window_end"`
	CreatedAt        string                   `yaml:"created_at"`
	AgentVersion     string                   `yaml:"agent_version"`
	InsightsSurfaced []reflectionSurfacedYAML `yaml:"insights_surfaced"`
	NewInsightIDs    []string                 `yaml:"new_insight_ids"`
	Notes            *string                  `yaml:"notes"`
}

// reflectionSurfacedYAML is the on-disk shape of an insights_surfaced[] entry.
type reflectionSurfacedYAML struct {
	ID           string `yaml:"id"`
	ResponseKind string `yaml:"response_kind"`
}

// reflectionsDir returns ~/.lucid/reflections/.
func (a *Adapter) reflectionsDir() string { return filepath.Join(a.home, reflectionsDirName) }

// reflectionHeadingPrefix and changeLogHeading are the fixed Markdown scaffold
// around a reflection's body. changeLogHeading is shared with insights.go.
const reflectionHeadingPrefix = "# Weekly recall — "

// WriteReflection persists one `/reflect` pass to its ISO-week record. On the
// first pass of the week it creates the file from rec (Summary, Surfaced,
// ChangeLog); on a later pass it reads the existing record, appends this pass's
// Surfaced entries and ChangeLog lines, and re-renders — leaving created_at,
// the window bounds, and the body Summary untouched (set once), so a second
// `/reflect` in the same week extends the change log without duplicating the
// body (acceptance-criteria.md 6.4). The whole record is re-rendered from
// merged state, so reruns with identical input are byte-stable.
func (a *Adapter) WriteReflection(rec Reflection) (ReflectionResult, error) {
	if strings.TrimSpace(rec.ID) == "" {
		return ReflectionResult{}, errors.New("storage: write_reflection: id is required")
	}
	dir := a.reflectionsDir()
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return ReflectionResult{}, fmt.Errorf("storage: prepare reflections dir: %w", err)
	}
	path := filepath.Join(dir, rec.ID+reflectionExt)

	existing, err := a.readReflectionIfPresent(path)
	if err != nil {
		return ReflectionResult{}, err
	}
	merged, created := mergeReflection(existing, rec)

	content, err := renderReflection(merged)
	if err != nil {
		return ReflectionResult{}, err
	}
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return ReflectionResult{}, fmt.Errorf("storage: write reflection %q: %w", rec.ID, err)
	}
	return ReflectionResult{ID: merged.ID, Path: path, Created: created}, nil
}

// mergeReflection folds this pass (rec) into any existing week record. When
// there is no existing record the pass is the record; otherwise the existing
// created_at, window, agent version, and body Summary win and this pass's
// Surfaced entries and ChangeLog lines are appended. It reports created=true
// when a fresh file is being written.
func mergeReflection(existing *Reflection, rec Reflection) (Reflection, bool) {
	if existing == nil {
		out := rec
		out.NewInsightIDs = orEmpty(out.NewInsightIDs)
		return out, true
	}
	out := *existing
	out.Surfaced = append(out.Surfaced, rec.Surfaced...)
	out.ChangeLog = append(out.ChangeLog, rec.ChangeLog...)
	out.NewInsightIDs = orEmpty(out.NewInsightIDs)
	return out, false
}

// ReadReflection loads and decodes the reflection record with the given id,
// including its body Summary and change-log lines.
func (a *Adapter) ReadReflection(id string) (Reflection, error) {
	path := filepath.Join(a.reflectionsDir(), id+reflectionExt)
	rec, err := a.readReflectionIfPresent(path)
	if err != nil {
		return Reflection{}, err
	}
	if rec == nil {
		return Reflection{}, fmt.Errorf("storage: reflection %q not found", id)
	}
	return *rec, nil
}

// readReflectionIfPresent reads and decodes a reflection file, returning nil
// (no error) when the file is absent — the first-pass-of-the-week case.
func (a *Adapter) readReflectionIfPresent(path string) (*Reflection, error) {
	b, err := os.ReadFile(path) //nolint:gosec // adapter-internal path under the reflections tree
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // an absent record legitimately decodes to no value and no error
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read reflection: %w", err)
	}
	rec, err := decodeReflection(b)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// decodeReflection parses a reflection document back into a Reflection.
func decodeReflection(content []byte) (Reflection, error) {
	front, body, err := SplitFrontmatter(content)
	if err != nil {
		return Reflection{}, fmt.Errorf("storage: parse reflection: %w", err)
	}
	var fm reflectionFrontmatter
	if err = yaml.Unmarshal(front, &fm); err != nil {
		return Reflection{}, fmt.Errorf("storage: decode reflection frontmatter: %w", err)
	}
	created, err := parseRFC3339(fm.CreatedAt, "reflection created_at")
	if err != nil {
		return Reflection{}, err
	}
	windowStart, err := parseRFC3339(fm.WindowStart, "reflection window_start")
	if err != nil {
		return Reflection{}, err
	}
	windowEnd, err := parseRFC3339(fm.WindowEnd, "reflection window_end")
	if err != nil {
		return Reflection{}, err
	}
	summary, changeLog := splitReflectionBody(string(body))
	surfaced := make([]ReflectionSurfaced, 0, len(fm.InsightsSurfaced))
	for _, s := range fm.InsightsSurfaced {
		surfaced = append(surfaced, ReflectionSurfaced(s))
	}
	return Reflection{
		ID:            fm.ID,
		ISOWeek:       fm.ISOWeek,
		WindowStart:   windowStart,
		WindowEnd:     windowEnd,
		CreatedAt:     created,
		AgentVersion:  fm.AgentVersion,
		Surfaced:      surfaced,
		NewInsightIDs: fm.NewInsightIDs,
		Notes:         fm.Notes,
		Summary:       summary,
		ChangeLog:     changeLog,
	}, nil
}

// splitReflectionBody extracts the one-time body Summary and the change-log
// lines from a reflection document body. The Summary is the prose between the
// "# Weekly recall — …" heading and the "## Change log" heading; each change-log
// line is a "- …" bullet after that heading, returned without its bullet.
func splitReflectionBody(body string) (summary string, changeLog []string) {
	s := strings.TrimSpace(body)
	if rest, ok := cutAfterLine(s, reflectionHeadingPrefix); ok {
		s = rest
	}
	if idx := strings.Index(s, changeLogHeading); idx >= 0 {
		summary = strings.TrimSpace(s[:idx])
		for _, line := range strings.Split(s[idx+len(changeLogHeading):], "\n") {
			if entry, ok := strings.CutPrefix(strings.TrimSpace(line), "- "); ok {
				changeLog = append(changeLog, entry)
			}
		}
		return summary, changeLog
	}
	return strings.TrimSpace(s), nil
}

// cutAfterLine returns the body following the first line that begins with
// prefix (the "# Weekly recall — …" heading), so the caller can isolate the
// summary from the heading regardless of the week label in it.
func cutAfterLine(s, prefix string) (string, bool) {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, prefix) {
			if idx := strings.Index(s, line); idx >= 0 {
				return s[idx+len(line):], true
			}
		}
	}
	return s, false
}

// renderReflection builds the full Markdown-with-frontmatter reflection
// document from merged state. The body carries the one-time Summary under the
// "# Weekly recall — week WW, YYYY" heading, then the append-only change log
// (data-model.md §"Weekly reflections").
func renderReflection(rec Reflection) ([]byte, error) {
	fm := reflectionFrontmatter{
		ID:               rec.ID,
		ISOWeek:          rec.ISOWeek,
		WindowStart:      rec.WindowStart.Format(time.RFC3339),
		WindowEnd:        rec.WindowEnd.Format(time.RFC3339),
		CreatedAt:        rec.CreatedAt.Format(time.RFC3339),
		AgentVersion:     rec.AgentVersion,
		InsightsSurfaced: encodeReflectionSurfaced(rec.Surfaced),
		NewInsightIDs:    orEmpty(rec.NewInsightIDs),
		Notes:            rec.Notes,
	}

	var buf bytes.Buffer
	buf.WriteString(fence + "\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return nil, fmt.Errorf("storage: encode reflection frontmatter: %w", err)
	}
	_ = enc.Close()

	buf.WriteString(fence + "\n\n" + reflectionHeading(rec.ISOWeek) + "\n")
	if s := strings.TrimSpace(rec.Summary); s != "" {
		buf.WriteString("\n" + s + "\n")
	}
	buf.WriteString("\n" + changeLogHeading + "\n")
	if len(rec.ChangeLog) > 0 {
		buf.WriteString("\n")
		for _, line := range rec.ChangeLog {
			buf.WriteString("- " + line + "\n")
		}
	}
	return buf.Bytes(), nil
}

// reflectionHeading renders the "# Weekly recall — week WW, YYYY" heading from
// an ISO-week label (`YYYY-Www`). A malformed label degrades to the raw label
// so the heading is always present.
func reflectionHeading(isoWeek string) string {
	year, week, ok := parseISOWeekLabel(isoWeek)
	if !ok {
		return reflectionHeadingPrefix + isoWeek
	}
	return fmt.Sprintf("%sweek %d, %d", reflectionHeadingPrefix, week, year)
}

// parseISOWeekLabel splits a `YYYY-Www` label into its year and week numbers.
func parseISOWeekLabel(label string) (year, week int, ok bool) {
	y, w, found := strings.Cut(label, "-W")
	if !found {
		return 0, 0, false
	}
	year, err := strconv.Atoi(y)
	if err != nil {
		return 0, 0, false
	}
	week, err = strconv.Atoi(w)
	if err != nil {
		return 0, 0, false
	}
	return year, week, true
}

// encodeReflectionSurfaced renders the insights_surfaced[] slice to its on-disk
// shape (nil stays nil so an empty record renders the field as an empty list).
func encodeReflectionSurfaced(surfaced []ReflectionSurfaced) []reflectionSurfacedYAML {
	out := make([]reflectionSurfacedYAML, 0, len(surfaced))
	for _, s := range surfaced {
		out = append(out, reflectionSurfacedYAML(s))
	}
	return out
}

// ListReflectionIDs returns the ids of every reflection record on disk, sorted.
// The `reflection_YYYY_wWW` id sorts chronologically (zero-padded week), so the
// last id is the most recent week — the pointer-line check reads it.
func (a *Adapter) ListReflectionIDs() ([]string, error) {
	entries, err := os.ReadDir(a.reflectionsDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: scan reflections dir: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != reflectionExt {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), reflectionExt))
	}
	slices.Sort(ids)
	return ids, nil
}

// ReadReflections returns the most recent weekly reflection records, newest
// ISO-week first, capped at limit (limit <= 0 means no cap). The
// `reflection_YYYY_wWW` id sorts chronologically (zero-padded week), so the
// last ids are the most recent weeks. It is the reflections-slice primitive
// `/ask` builds on (agent-contracts.md §3 answer_grounded: reflections capped
// at the 12 most recent ISO-week records).
func (a *Adapter) ReadReflections(limit int) ([]Reflection, error) {
	ids, err := a.ListReflectionIDs()
	if err != nil {
		return nil, err
	}
	// ids are chronological ascending; take the newest `limit` from the tail.
	if limit > 0 && len(ids) > limit {
		ids = ids[len(ids)-limit:]
	}
	out := make([]Reflection, 0, len(ids))
	// Emit newest first so the slice the router hands the agent is recency-ordered.
	for i := len(ids) - 1; i >= 0; i-- {
		rec, err := a.ReadReflection(ids[i])
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// LatestReflectionCreatedAt returns the created_at of the most recent
// reflection record, or ok=false when none exist. The router uses it to decide
// whether processed entries have accumulated since the last recall pass and so
// whether to append the `/checkin` pointer line (agent-contracts.md §3;
// error-states.md §R-7).
func (a *Adapter) LatestReflectionCreatedAt() (time.Time, bool, error) {
	ids, err := a.ListReflectionIDs()
	if err != nil {
		return time.Time{}, false, err
	}
	if len(ids) == 0 {
		return time.Time{}, false, nil
	}
	rec, err := a.ReadReflection(ids[len(ids)-1])
	if err != nil {
		return time.Time{}, false, err
	}
	return rec.CreatedAt, true, nil
}
