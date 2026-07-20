package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// insightsDirName is the flat Mirror subtree of validated insights, one
// Markdown-with-frontmatter file per accepted/nuanced validation
// (data-model.md §"Validated insights"). Only the adapter writes it; insights
// are rebuildable from raw/ plus the user's accept/reject responses.
const (
	insightsDirName = "insights"
	insightExt      = ".md"
	insightIDPrefix = "i_"
)

// reflectionVersionPrefix is the required stamp on an insight's
// reflection_prompt_version provenance (parallels structuringVersionPrefix on
// processed artifacts). The provenance validator rejects any insight whose
// version does not begin with it.
const reflectionVersionPrefix = "reflection-"

// frameworkLabelPattern is the provenance.framework label grammar: "<id>
// v<version>" (e.g. "stoicism v1", "attachment-theory v2"), the exact string
// form frameworks.Lens.Label stamps on a lens-framed insight (docs/frameworks.md
// §2; data-model.md §"Insight provenance"). A nil framework is the baseline,
// lens-neutral voice and is always valid; a present label must match this shape
// so a malformed stamp can never persist.
var frameworkLabelPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)* v[1-9][0-9]*$`)

// Insight status values (data-model.md §"Validated insights"). There is no
// "pending": an unanswered proposal never produces an insight.
const (
	InsightStatusAccepted = "accepted"
	InsightStatusRetired  = "retired"
)

// User-response kinds recorded in provenance (data-model.md). An insight is
// only ever written from an accepted or nuanced validation.
const (
	ResponseAccepted = "accepted"
	ResponseNuanced  = "nuanced"
)

// Rule-history kinds (data-model.md §"Validated insights"). stated is set at
// validation; kept/lapsed/retired are appended at recall (a later phase).
const (
	RuleStated = "stated"
	RuleKept   = "kept"
	RuleLapsed = "lapsed"
	RuleRetire = "retired"
)

// TimedEvent is one entry in status_history[] or rule_history[]: a timestamp
// and a kind. Both histories are append-only (data-model.md).
type TimedEvent struct {
	At   time.Time
	Kind string
}

// InsightProvenance carries the audit trail every insight must have
// (data-model.md §"Validated insights"). Framework is nil in the MVP (the
// frameworks layer has not shipped); it renders as `null`.
type InsightProvenance struct {
	RawEntryIDs             []string
	ProcessedArtifactID     string
	ReflectionPromptVersion string
	Framework               *string
	UserResponseKind        string
	UserResponseText        string
}

// Insight is one validated insight (data-model.md §"Validated insights").
// Body is the canonical statement in the user's words. Rule is nil until the
// user answers the fixed rule prompt; the nullable *At fields are set later at
// recall. StatusHistory is append-only and non-empty from the first write.
type Insight struct {
	ID                  string
	CreatedAt           time.Time
	Status              string
	NuancedFromProposal bool
	Provenance          InsightProvenance
	StatusHistory       []TimedEvent
	LastConfirmedAt     *time.Time
	LastSoftenedAt      *time.Time
	RetiredAt           *time.Time
	Rule                *string
	RuleHistory         []TimedEvent
	Body                string
}

// WriteInsightResult reports the id WriteInsight assigned to a new insight.
type WriteInsightResult struct {
	InsightID string
	Path      string
}

// insightFrontmatter is the on-disk YAML frontmatter shape; field order
// matches data-model.md §"Validated insights" so a written insight reads like
// the documented schema.
type insightFrontmatter struct {
	ID                  string           `yaml:"id"`
	CreatedAt           string           `yaml:"created_at"`
	Status              string           `yaml:"status"`
	NuancedFromProposal bool             `yaml:"nuanced_from_proposal"`
	Provenance          provenanceYAML   `yaml:"provenance"`
	StatusHistory       []timedEventYAML `yaml:"status_history"`
	LastConfirmedAt     *string          `yaml:"last_confirmed_at"`
	LastSoftenedAt      *string          `yaml:"last_softened_at"`
	RetiredAt           *string          `yaml:"retired_at"`
	Rule                *string          `yaml:"rule"`
	RuleHistory         []timedEventYAML `yaml:"rule_history,omitempty"`
}

// provenanceYAML is the on-disk provenance block.
type provenanceYAML struct {
	RawEntryIDs             []string `yaml:"raw_entry_ids"`
	ProcessedArtifactID     string   `yaml:"processed_artifact_id"`
	ReflectionPromptVersion string   `yaml:"reflection_prompt_version"`
	Framework               *string  `yaml:"framework"`
	UserResponseKind        string   `yaml:"user_response_kind"`
	UserResponseText        string   `yaml:"user_response_text"`
}

// timedEventYAML is the on-disk shape of a status_history / rule_history entry.
type timedEventYAML struct {
	At   string `yaml:"at"`
	Kind string `yaml:"kind"`
}

// insightsDir returns ~/.lucid/insights/.
func (a *Adapter) insightsDir() string { return filepath.Join(a.home, insightsDirName) }

// WriteInsight allocates the next per-day id slot, renders the insight
// document, validates its provenance, and writes it under insights/<id>.md.
// The id is i_YYYY_MM_DD_<slot> where <slot> advances a, b, c, ... within a
// day (data-model.md §"Naming conventions"). A provenance gap is caught before
// any file is written (error-states.md §St-5: the validator raises, no insight
// is stored). CreatedAt seeds both the id date and the first status_history
// entry when the caller left StatusHistory empty.
func (a *Adapter) WriteInsight(in Insight) (WriteInsightResult, error) {
	if in.CreatedAt.IsZero() {
		return WriteInsightResult{}, errors.New("storage: write_insight: created_at is required")
	}
	if in.Status == "" {
		in.Status = InsightStatusAccepted
	}
	if len(in.StatusHistory) == 0 {
		in.StatusHistory = []TimedEvent{{At: in.CreatedAt, Kind: in.Provenance.UserResponseKind}}
	}

	dir := a.insightsDir()
	if err := ensureDir(dir, "insights"); err != nil {
		return WriteInsightResult{}, err
	}
	id, err := a.nextInsightID(in.CreatedAt)
	if err != nil {
		return WriteInsightResult{}, err
	}
	in.ID = id

	content, err := renderInsight(in)
	if err != nil {
		return WriteInsightResult{}, err
	}
	if err := ValidateInsight(content); err != nil {
		return WriteInsightResult{}, fmt.Errorf("storage: rendered insight failed provenance validation: %w", err)
	}
	path := filepath.Join(dir, id+insightExt)
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return WriteInsightResult{}, fmt.Errorf("storage: write insight %q: %w", id, err)
	}
	return WriteInsightResult{InsightID: id, Path: path}, nil
}

// ReadInsight loads and decodes the insight with the given id.
func (a *Adapter) ReadInsight(id string) (Insight, error) {
	path, err := a.insightPath(id)
	if err != nil {
		return Insight{}, err
	}
	b, err := os.ReadFile(path) //nolint:gosec // adapter-internal path under the insights tree, id is separator-checked
	if err != nil {
		return Insight{}, fmt.Errorf("storage: read insight %q: %w", id, err)
	}
	fm, body, err := parseFrontmatterInto[insightFrontmatter](b, fmt.Sprintf("insight %q", id))
	if err != nil {
		return Insight{}, err
	}
	return fm.decode(extractInsightStatement(string(body)))
}

// insightHeading and changeLogHeading are the fixed Markdown scaffolding
// renderInsight writes around an insight's canonical statement.
const (
	insightHeading   = "# Insight"
	changeLogHeading = "## Change log"
)

// extractInsightStatement returns just the canonical statement from an insight
// body, stripping the "# Insight" heading and the trailing "## Change log"
// section — the inverse of renderInsight, so ReadInsight yields the clean
// statement a caller stored (parallel to RawDocument.EntryText).
func extractInsightStatement(body string) string {
	s := strings.TrimSpace(body)
	if s == insightHeading {
		return ""
	}
	if rest, ok := strings.CutPrefix(s, insightHeading+"\n"); ok {
		s = rest
	}
	if idx := strings.Index(s, changeLogHeading); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// SetInsightRule records the user's one-line rule verbatim on an insight and
// appends a `stated` entry to rule_history (data-model.md §"Validated
// insights"; agent-contracts.md §3 "Rules"). It is the storage side of the
// fixed rule prompt asked once per insight; the router calls it only on an
// answer, never on a skip. A rule is testimony, not obligation — no surface
// scores it — so this op only records; it does not re-ask or track.
func (a *Adapter) SetInsightRule(insightID, rule string, at time.Time) error {
	if strings.TrimSpace(rule) == "" {
		return errors.New("storage: set_insight_rule: empty rule")
	}
	ins, err := a.ReadInsight(insightID)
	if err != nil {
		return err
	}
	r := rule
	ins.Rule = &r
	ins.RuleHistory = append(ins.RuleHistory, TimedEvent{At: at, Kind: RuleStated})

	content, err := renderInsight(ins)
	if err != nil {
		return err
	}
	if err = ValidateInsight(content); err != nil {
		return fmt.Errorf("storage: insight failed validation after rule set: %w", err)
	}
	path, err := a.insightPath(insightID)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, filePerm)
}

// insightPath resolves insights/<id>.md, rejecting a separator-bearing id so a
// malformed id can never escape the tree.
func (a *Adapter) insightPath(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("storage: invalid insight id %q", id)
	}
	return filepath.Join(a.insightsDir(), id+insightExt), nil
}

// nextInsightID returns the next free id for the given day, advancing the slot
// label past any insight already written for that date. Slots are bijective
// base-26 (a, b, ..., z, aa, ...), so a day never runs out of ids.
func (a *Adapter) nextInsightID(createdAt time.Time) (string, error) {
	datePart := fmt.Sprintf("%04d_%02d_%02d", createdAt.Year(), int(createdAt.Month()), createdAt.Day())
	prefix := insightIDPrefix + datePart + "_"

	entries, err := os.ReadDir(a.insightsDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("storage: scan insights dir: %w", err)
	}
	used := map[string]bool{}
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), insightExt)
		if slot, ok := strings.CutPrefix(name, prefix); ok {
			used[slot] = true
		}
	}
	for n := 0; ; n++ {
		slot := slotLabel(n)
		if !used[slot] {
			return prefix + slot, nil
		}
	}
}

// slotLabel renders the nth per-day slot as a bijective base-26 lowercase
// label: 0→a, 25→z, 26→aa, 27→ab, ... It never returns the empty string, so
// every insight in a day gets a unique, monotonic id.
func slotLabel(n int) string {
	var b []byte
	for {
		b = append([]byte{byte('a' + n%26)}, b...)
		n = n/26 - 1
		if n < 0 {
			break
		}
	}
	return string(b)
}

// renderInsight builds the full Markdown-with-frontmatter insight document.
// The body carries the canonical statement under a fixed "# Insight" heading;
// a "## Change log" section is appended so status updates have a home
// (data-model.md §"Validated insights").
func renderInsight(in Insight) ([]byte, error) {
	fm := insightFrontmatter{
		ID:                  in.ID,
		CreatedAt:           in.CreatedAt.Format(time.RFC3339),
		Status:              in.Status,
		NuancedFromProposal: in.NuancedFromProposal,
		Provenance: provenanceYAML{
			RawEntryIDs:             orEmpty(in.Provenance.RawEntryIDs),
			ProcessedArtifactID:     in.Provenance.ProcessedArtifactID,
			ReflectionPromptVersion: in.Provenance.ReflectionPromptVersion,
			Framework:               in.Provenance.Framework,
			UserResponseKind:        in.Provenance.UserResponseKind,
			UserResponseText:        in.Provenance.UserResponseText,
		},
		StatusHistory:   encodeTimedEvents(in.StatusHistory),
		LastConfirmedAt: formatNullableTime(in.LastConfirmedAt),
		LastSoftenedAt:  formatNullableTime(in.LastSoftenedAt),
		RetiredAt:       formatNullableTime(in.RetiredAt),
		Rule:            in.Rule,
		RuleHistory:     encodeTimedEvents(in.RuleHistory),
	}

	var buf bytes.Buffer
	buf.WriteString(fence + "\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fm); err != nil {
		return nil, fmt.Errorf("storage: encode insight frontmatter: %w", err)
	}
	_ = enc.Close()
	buf.WriteString(fence + "\n\n" + insightHeading + "\n")
	if body := strings.TrimSpace(in.Body); body != "" {
		buf.WriteString("\n" + body + "\n")
	}
	buf.WriteString("\n" + changeLogHeading + "\n")
	return buf.Bytes(), nil
}

// decode parses the on-disk frontmatter and body back into an Insight.
func (fm insightFrontmatter) decode(body string) (Insight, error) {
	created, err := parseRFC3339(fm.CreatedAt, "insight created_at")
	if err != nil {
		return Insight{}, err
	}
	statusHist, err := decodeTimedEvents(fm.StatusHistory)
	if err != nil {
		return Insight{}, err
	}
	ruleHist, err := decodeTimedEvents(fm.RuleHistory)
	if err != nil {
		return Insight{}, err
	}
	lastConfirmed, err := parseOptionalTimePtr(fm.LastConfirmedAt)
	if err != nil {
		return Insight{}, err
	}
	lastSoftened, err := parseOptionalTimePtr(fm.LastSoftenedAt)
	if err != nil {
		return Insight{}, err
	}
	retired, err := parseOptionalTimePtr(fm.RetiredAt)
	if err != nil {
		return Insight{}, err
	}
	return Insight{
		ID:                  fm.ID,
		CreatedAt:           created,
		Status:              fm.Status,
		NuancedFromProposal: fm.NuancedFromProposal,
		Provenance: InsightProvenance{
			RawEntryIDs:             fm.Provenance.RawEntryIDs,
			ProcessedArtifactID:     fm.Provenance.ProcessedArtifactID,
			ReflectionPromptVersion: fm.Provenance.ReflectionPromptVersion,
			Framework:               fm.Provenance.Framework,
			UserResponseKind:        fm.Provenance.UserResponseKind,
			UserResponseText:        fm.Provenance.UserResponseText,
		},
		StatusHistory:   statusHist,
		LastConfirmedAt: lastConfirmed,
		LastSoftenedAt:  lastSoftened,
		RetiredAt:       retired,
		Rule:            fm.Rule,
		RuleHistory:     ruleHist,
		Body:            body,
	}, nil
}

// ValidateInsight is the deterministic provenance gate for an insight
// (error-states.md §St-5; acceptance-criteria.md Phase 5 provenance check). It
// confirms the id, an ISO created_at, a known status, a non-empty append-only
// status_history, and complete provenance: at least one raw entry id, a
// processed artifact id, a reflection-prompt version with the expected prefix,
// a known user_response_kind, and non-empty user_response_text. It runs on the
// rendered bytes before every write, so no insight missing provenance can be
// persisted.
func ValidateInsight(content []byte) error {
	front, _, err := SplitFrontmatter(content)
	if err != nil {
		return err
	}
	var fm insightFrontmatter
	if err := yaml.Unmarshal(front, &fm); err != nil {
		return fmt.Errorf("storage: parse insight frontmatter: %w", err)
	}
	if fm.ID == "" {
		return errors.New("storage: insight id is empty")
	}
	if !isISOTimestamp(fm.CreatedAt) {
		return fmt.Errorf("storage: insight created_at %q is not an ISO-8601 timestamp", fm.CreatedAt)
	}
	if fm.Status != InsightStatusAccepted && fm.Status != InsightStatusRetired {
		return fmt.Errorf("storage: insight status %q is not accepted|retired", fm.Status)
	}
	if len(fm.StatusHistory) == 0 {
		return errors.New("storage: insight status_history is empty")
	}
	return validateProvenance(fm.Provenance)
}

// validateProvenance checks the provenance block is complete.
func validateProvenance(p provenanceYAML) error {
	if len(p.RawEntryIDs) == 0 {
		return errors.New("storage: insight provenance has no raw_entry_ids")
	}
	if p.ProcessedArtifactID == "" {
		return errors.New("storage: insight provenance processed_artifact_id is empty")
	}
	if !strings.HasPrefix(p.ReflectionPromptVersion, reflectionVersionPrefix) {
		return fmt.Errorf("storage: insight provenance reflection_prompt_version %q lacks the %q prefix",
			p.ReflectionPromptVersion, reflectionVersionPrefix)
	}
	if p.UserResponseKind != ResponseAccepted && p.UserResponseKind != ResponseNuanced {
		return fmt.Errorf("storage: insight provenance user_response_kind %q is not accepted|nuanced", p.UserResponseKind)
	}
	if strings.TrimSpace(p.UserResponseText) == "" {
		return errors.New("storage: insight provenance user_response_text is empty")
	}
	// framework is nullable: nil is the baseline voice, a present value must be a
	// well-formed "<id> v<version>" lens label (frameworks layer, docs/frameworks.md §2).
	if p.Framework != nil && !frameworkLabelPattern.MatchString(*p.Framework) {
		return fmt.Errorf("storage: insight provenance framework %q is not a %q label", *p.Framework, "<id> v<version>")
	}
	return nil
}

// encodeTimedEvents renders status/rule history to their on-disk shape.
func encodeTimedEvents(events []TimedEvent) []timedEventYAML {
	if len(events) == 0 {
		return nil
	}
	out := make([]timedEventYAML, 0, len(events))
	for _, e := range events {
		out = append(out, timedEventYAML{At: e.At.Format(time.RFC3339), Kind: e.Kind})
	}
	return out
}

// decodeTimedEvents parses on-disk history entries back into TimedEvents.
func decodeTimedEvents(in []timedEventYAML) ([]TimedEvent, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]TimedEvent, 0, len(in))
	for _, e := range in {
		at, err := parseRFC3339(e.At, fmt.Sprintf("insight history at %q", e.At))
		if err != nil {
			return nil, err
		}
		out = append(out, TimedEvent{At: at, Kind: e.Kind})
	}
	return out, nil
}

// formatNullableTime renders a nullable timestamp to a nullable string pointer
// (nil → YAML null), for last_confirmed_at / last_softened_at / retired_at.
func formatNullableTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.RFC3339)
	return &s
}

// parseRFC3339 parses an RFC3339 timestamp, wrapping a failure with label so
// the offending frontmatter field is named in the error. It is the shared
// parse-and-wrap every record decoder used to hand-copy per timestamp field.
func parseRFC3339(value, label string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("storage: %s: %w", label, err)
	}
	return t, nil
}

// parseOptionalTimePtr parses a nullable timestamp pointer back to a *time.Time
// (nil / empty → nil).
func parseOptionalTimePtr(s *string) (*time.Time, error) {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil, nil //nolint:nilnil // a null timestamp legitimately decodes to no value and no error
	}
	t, err := parseRFC3339(*s, fmt.Sprintf("insight optional time %q", *s))
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListInsightIDs returns the ids of every insight on disk, sorted (the id sort
// is chronological). It is the read primitive the router and later recall
// phases build windows on top of.
func (a *Adapter) ListInsightIDs() ([]string, error) {
	entries, err := os.ReadDir(a.insightsDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: scan insights dir: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != insightExt {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), insightExt))
	}
	slices.Sort(ids)
	return ids, nil
}
