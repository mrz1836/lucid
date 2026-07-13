package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// The Mirror-root registry/state files this file owns. They are small,
// user-inspectable JSON records (no hidden memory — agent-contracts.md
// cross-cutting): the off-limits registry (architecture §5, person scope) and
// the proposal-pause runtime state whose thresholds live in lucid.json
// (agent-contracts.md §3 "Proposal pause"). Neither is under the sanctuary
// trees; both sit at the Ledger root alongside lucid.json.
const (
	offLimitsFile     = "off_limits.json"
	proposalPauseFile = "proposal_pause.json"
)

// RejectedProposal is one entry appended to a processed artifact's
// rejected_proposals[] when the user rejects a pattern (data-model.md
// §"Insight provenance and rejected proposals"). The shape_tag lets future
// Reflection runs avoid re-proposing the same shape.
type RejectedProposal struct {
	At                      time.Time
	ReflectionPromptVersion string
	ProposalText            string
	UserResponseText        string
	ShapeTag                string
}

// rejectedProposalJSON is the on-disk shape; field order matches data-model.md.
type rejectedProposalJSON struct {
	At                      string `json:"at"`
	ReflectionPromptVersion string `json:"reflection_prompt_version"`
	ProposalText            string `json:"proposal_text"`
	UserResponseText        string `json:"user_response_text"`
	ShapeTag                string `json:"shape_tag"`
}

// UnansweredProposal is one entry appended to a processed artifact's
// unanswered_proposals[] when a proposal goes unanswered (data-model.md
// §"Processed artifacts"). It is `{shape_tag, proposed_at}` — an exact
// parallel to a rejection, kept separate because silence is not rejection.
type UnansweredProposal struct {
	ShapeTag   string
	ProposedAt time.Time
}

// unansweredProposalJSON is the on-disk shape.
type unansweredProposalJSON struct {
	ShapeTag   string `json:"shape_tag"`
	ProposedAt string `json:"proposed_at"`
}

// AppendRejectedProposal appends a rejection to a processed artifact's
// rejected_proposals[] without touching any other field (no insight is written
// on a rejection — data-model.md). It re-validates the whole artifact before
// writing, so an append can never corrupt the schema.
func (a *Adapter) AppendRejectedProposal(processedID string, rp RejectedProposal) error {
	entry, err := json.Marshal(rejectedProposalJSON{
		At:                      rp.At.Format(time.RFC3339),
		ReflectionPromptVersion: rp.ReflectionPromptVersion,
		ProposalText:            rp.ProposalText,
		UserResponseText:        rp.UserResponseText,
		ShapeTag:                rp.ShapeTag,
	})
	if err != nil {
		return fmt.Errorf("storage: marshal rejected proposal: %w", err)
	}
	return a.appendProposal(processedID, func(j *processedJSON) {
		j.RejectedProposals = append(orEmptyRaw(j.RejectedProposals), entry)
	})
}

// AppendUnansweredProposal appends an unanswered proposal to a processed
// artifact's unanswered_proposals[] (data-model.md). Same discipline as the
// rejection append: re-validate before write.
func (a *Adapter) AppendUnansweredProposal(processedID string, up UnansweredProposal) error {
	entry, err := json.Marshal(unansweredProposalJSON{
		ShapeTag:   up.ShapeTag,
		ProposedAt: up.ProposedAt.Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("storage: marshal unanswered proposal: %w", err)
	}
	return a.appendProposal(processedID, func(j *processedJSON) {
		j.UnansweredProposals = append(orEmptyRaw(j.UnansweredProposals), entry)
	})
}

// appendProposal reads a processed artifact, applies mutate to its decoded
// JSON, normalizes the proposal arrays so they never serialize as null,
// re-validates, and writes it back — preserving every other field byte-for-
// byte (produced_at included, so an append never re-times the artifact).
func (a *Adapter) appendProposal(processedID string, mutate func(*processedJSON)) error {
	path := filepath.Join(a.processedDir(), processedID+processedExt)
	b, err := os.ReadFile(path) //nolint:gosec // adapter-internal path under the processed tree
	if err != nil {
		return fmt.Errorf("storage: read processed %q: %w", processedID, err)
	}
	var j processedJSON
	if err = json.Unmarshal(b, &j); err != nil {
		return fmt.Errorf("storage: parse processed %q: %w", processedID, err)
	}
	mutate(&j)
	j.RejectedProposals = orEmptyRaw(j.RejectedProposals)
	j.UnansweredProposals = orEmptyRaw(j.UnansweredProposals)

	content, err := marshalJSON(j)
	if err != nil {
		return err
	}
	if err := ValidateProcessedArtifact(content); err != nil {
		return fmt.Errorf("storage: processed %q failed validation after proposal append: %w", processedID, err)
	}
	return os.WriteFile(path, content, filePerm)
}

// RejectedShapeTags extracts the shape_tag of every rejected proposal on a
// processed artifact. The router unions these across the recent window to
// build reflection.propose's rejected_shape_tags input.
func RejectedShapeTags(art ProcessedArtifact) []string {
	return shapeTagsFrom(art.RejectedProposals)
}

// UnansweredShapeTags extracts the shape_tag of every unanswered proposal on a
// processed artifact (the parallel to RejectedShapeTags).
func UnansweredShapeTags(art ProcessedArtifact) []string {
	return shapeTagsFrom(art.UnansweredProposals)
}

// shapeTagsFrom pulls the shape_tag field out of each opaque proposal entry,
// skipping any that does not carry one.
func shapeTagsFrom(entries []json.RawMessage) []string {
	var tags []string
	for _, raw := range entries {
		var probe struct {
			ShapeTag string `json:"shape_tag"`
		}
		if err := json.Unmarshal(raw, &probe); err == nil && probe.ShapeTag != "" {
			tags = append(tags, probe.ShapeTag)
		}
	}
	return tags
}

// ProposalPauseState is the runtime state of the router's proposal pause
// (agent-contracts.md §3). ConsecutiveUnanswered counts unanswered proposals
// since the last answered one; PausedUntil, when set and in the future,
// suspends reflection.propose. The pause is silent — no copy ever mentions it.
type ProposalPauseState struct {
	ConsecutiveUnanswered int
	PausedUntil           *time.Time
}

// proposalPauseStateJSON is the on-disk shape.
type proposalPauseStateJSON struct {
	ConsecutiveUnanswered int     `json:"consecutive_unanswered"`
	PausedUntil           *string `json:"paused_until"`
}

// ReadProposalPauseState loads the proposal-pause state, returning the zero
// state (no pause, zero count) when the file is absent — the first-run case.
func (a *Adapter) ReadProposalPauseState() (ProposalPauseState, error) {
	path := filepath.Join(a.home, proposalPauseFile)
	j, _, err := readJSONOptional[proposalPauseStateJSON](path, "proposal pause state")
	if err != nil {
		return ProposalPauseState{}, err
	}
	until, err := parseOptionalTimePtr(j.PausedUntil)
	if err != nil {
		return ProposalPauseState{}, err
	}
	return ProposalPauseState{ConsecutiveUnanswered: j.ConsecutiveUnanswered, PausedUntil: until}, nil
}

// WriteProposalPauseState persists the proposal-pause state atomically enough
// for the single-writer local runtime.
func (a *Adapter) WriteProposalPauseState(s ProposalPauseState) error {
	content, err := marshalJSON(proposalPauseStateJSON{
		ConsecutiveUnanswered: s.ConsecutiveUnanswered,
		PausedUntil:           formatNullableTime(s.PausedUntil),
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.home, dirPerm); err != nil {
		return fmt.Errorf("storage: prepare ledger home: %w", err)
	}
	path := filepath.Join(a.home, proposalPauseFile)
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return fmt.Errorf("storage: write proposal pause state: %w", err)
	}
	return nil
}

// offLimitsJSON is the on-disk shape of the off-limits registry (person scope).
type offLimitsJSON struct {
	PersonKeys []string `json:"person_keys"`
}

// ReadOffLimitsPersonKeys returns the person keys the user has marked
// off-limits to inference (architecture §5). They are redacted from every
// agent slice at slice-build (agent-contracts.md cross-cutting; error-states.md
// §P-3). An absent registry means nothing is off-limits.
func (a *Adapter) ReadOffLimitsPersonKeys() ([]string, error) {
	path := filepath.Join(a.home, offLimitsFile)
	b, err := os.ReadFile(path) //nolint:gosec // adapter-internal path at the Ledger root
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read off-limits registry: %w", err)
	}
	var j offLimitsJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("storage: parse off-limits registry: %w", err)
	}
	return j.PersonKeys, nil
}

// WriteOffLimitsPersonKeys persists the off-limits registry. It exists so
// tests and setup can mark a person off-limits; the redaction reads it.
func (a *Adapter) WriteOffLimitsPersonKeys(keys []string) error {
	content, err := marshalJSON(offLimitsJSON{PersonKeys: orEmpty(keys)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.home, dirPerm); err != nil {
		return fmt.Errorf("storage: prepare ledger home: %w", err)
	}
	path := filepath.Join(a.home, offLimitsFile)
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return fmt.Errorf("storage: write off-limits registry: %w", err)
	}
	return nil
}
