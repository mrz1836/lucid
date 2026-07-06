package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// processedDirName is the flat Mirror subtree of JSON extraction artifacts,
// one per raw entry, named by the raw id (data-model.md §"Processed
// artifacts"). Only the adapter writes it; the artifact is rebuildable.
const (
	processedDirName = "processed"
	processedExt     = ".json"
)

// structuringVersionPrefix is the required agent_version stamp for a
// Structuring artifact (acceptance-criteria.md Phase 4 verification:
// `.agent_version | test("^structuring-")`). The processed validator rejects
// any artifact whose version does not begin with it.
const structuringVersionPrefix = "structuring-"

// Notes sentinels the Structuring/degrade paths write when extraction yields
// nothing useful (agent-contracts.md §2 failure handling; error-states.md
// §S-2/§S-3). They keep the loop honest: an empty artifact still explains why.
const (
	// NotesRawBodyEmpty is written when the raw entry body is empty (§S-3).
	NotesRawBodyEmpty = "raw body empty"
	// NotesStructuringFailed is written when the model returned unusable JSON
	// twice (§S-2).
	NotesStructuringFailed = "structuring failed (parse)"
)

// ProcessedItem is one extracted emotion or theme: a short name with a
// one-line rationale grounded in the entry (data-model.md §"Processed
// artifacts").
type ProcessedItem struct {
	Name      string
	Rationale string
}

// ProcessedPerson pairs a display name (as written) with its resolved
// person_key and a first_mention flag. The Structuring agent emits
// PersonKey empty (its null); the People routine back-fills it before write,
// so a persisted artifact never carries an empty person_key.
type ProcessedPerson struct {
	DisplayName  string
	PersonKey    string
	FirstMention bool
}

// ProcessedArtifact is one Structuring output for a raw entry
// (data-model.md §"Processed artifacts"). Notes is nil when absent (renders
// as JSON null). RejectedProposals/UnansweredProposals are always empty on a
// Structuring write; the validation paths append to them later via their own
// adapter ops, so they are carried opaquely here to round-trip without this
// phase modeling their Phase-5 shape.
type ProcessedArtifact struct {
	ID                  string
	EntryID             string
	ProducedAt          time.Time
	AgentVersion        string
	Emotions            []ProcessedItem
	Themes              []ProcessedItem
	People              []ProcessedPerson
	Notes               *string
	RejectedProposals   []json.RawMessage
	UnansweredProposals []json.RawMessage
}

// processedJSON is the on-disk JSON shape; field order matches data-model.md
// §"Processed artifacts" so a written artifact reads like the documented
// schema.
type processedJSON struct {
	ID                  string            `json:"id"`
	EntryID             string            `json:"entry_id"`
	ProducedAt          string            `json:"produced_at"`
	AgentVersion        string            `json:"agent_version"`
	Emotions            []itemJSON        `json:"emotions"`
	Themes              []itemJSON        `json:"themes"`
	People              []personRefJSON   `json:"people"`
	Notes               *string           `json:"notes"`
	RejectedProposals   []json.RawMessage `json:"rejected_proposals"`
	UnansweredProposals []json.RawMessage `json:"unanswered_proposals"`
}

// itemJSON is the on-disk shape of an emotion/theme entry.
type itemJSON struct {
	Name      string `json:"name"`
	Rationale string `json:"rationale"`
}

// personRefJSON is the on-disk shape of a processed people[] entry.
type personRefJSON struct {
	DisplayName  string `json:"display_name"`
	PersonKey    string `json:"person_key"`
	FirstMention bool   `json:"first_mention"`
}

// WriteProcessed writes the processed artifact for a raw entry under
// processed/<id>.json, validating the rendered bytes first so a malformed
// artifact never reaches disk. The artifact is rebuildable (data-model.md),
// so unlike raw entries an existing file is overwritten — a re-run of
// Structuring replaces it, differing only in produced_at
// (acceptance-criteria.md test case 4.3).
func (a *Adapter) WriteProcessed(art ProcessedArtifact) error {
	content, err := renderProcessed(art)
	if err != nil {
		return err
	}
	if err := ValidateProcessedArtifact(content); err != nil {
		return fmt.Errorf("storage: rendered processed artifact failed validation: %w", err)
	}
	dir := a.processedDir()
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("storage: prepare processed dir: %w", err)
	}
	path := filepath.Join(dir, art.ID+processedExt)
	if err := os.WriteFile(path, content, filePerm); err != nil {
		return fmt.Errorf("storage: write processed %q: %w", art.ID, err)
	}
	return nil
}

// ListProcessedIDs returns the ids of every processed artifact on disk,
// sorted (the id sort is chronological, since ids encode minute precision).
// The router builds Reflection's recent window on top of it.
func (a *Adapter) ListProcessedIDs() ([]string, error) {
	entries, err := os.ReadDir(a.processedDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("storage: scan processed dir: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != processedExt {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), processedExt))
	}
	sort.Strings(ids)
	return ids, nil
}

// ReadProcessed loads the processed artifact with the given id.
func (a *Adapter) ReadProcessed(id string) (ProcessedArtifact, error) {
	path := filepath.Join(a.processedDir(), id+processedExt)
	b, err := os.ReadFile(path) //nolint:gosec // adapter-internal path under the processed tree, id derived from a raw id
	if err != nil {
		return ProcessedArtifact{}, fmt.Errorf("storage: read processed %q: %w", id, err)
	}
	var j processedJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return ProcessedArtifact{}, fmt.Errorf("storage: parse processed %q: %w", id, err)
	}
	return j.decode()
}

// processedDir returns ~/.lucid/processed/.
func (a *Adapter) processedDir() string { return filepath.Join(a.home, processedDirName) }

// renderProcessed builds the on-disk JSON for an artifact, normalizing nil
// slices to empty arrays so emotions/themes/people/proposals never serialize
// as null, and formatting produced_at with the host's local TZ offset.
func renderProcessed(art ProcessedArtifact) ([]byte, error) {
	j := processedJSON{
		ID:                  art.ID,
		EntryID:             art.EntryID,
		ProducedAt:          art.ProducedAt.Format(time.RFC3339),
		AgentVersion:        art.AgentVersion,
		Emotions:            encodeItems(art.Emotions),
		Themes:              encodeItems(art.Themes),
		People:              encodePeople(art.People),
		Notes:               art.Notes,
		RejectedProposals:   orEmptyRaw(art.RejectedProposals),
		UnansweredProposals: orEmptyRaw(art.UnansweredProposals),
	}
	return marshalJSON(j)
}

// decode parses the on-disk JSON shape back into a ProcessedArtifact.
func (j processedJSON) decode() (ProcessedArtifact, error) {
	produced, err := time.Parse(time.RFC3339, j.ProducedAt)
	if err != nil {
		return ProcessedArtifact{}, fmt.Errorf("storage: processed produced_at: %w", err)
	}
	art := ProcessedArtifact{
		ID:                  j.ID,
		EntryID:             j.EntryID,
		ProducedAt:          produced,
		AgentVersion:        j.AgentVersion,
		Notes:               j.Notes,
		RejectedProposals:   j.RejectedProposals,
		UnansweredProposals: j.UnansweredProposals,
	}
	for _, e := range j.Emotions {
		art.Emotions = append(art.Emotions, ProcessedItem(e))
	}
	for _, th := range j.Themes {
		art.Themes = append(art.Themes, ProcessedItem(th))
	}
	for _, p := range j.People {
		art.People = append(art.People, ProcessedPerson(p))
	}
	return art, nil
}

// encodeItems renders emotions/themes to their on-disk shape.
func encodeItems(items []ProcessedItem) []itemJSON {
	out := make([]itemJSON, 0, len(items))
	for _, it := range items {
		out = append(out, itemJSON(it))
	}
	return out
}

// encodePeople renders the people[] slice to its on-disk shape.
func encodePeople(people []ProcessedPerson) []personRefJSON {
	out := make([]personRefJSON, 0, len(people))
	for _, p := range people {
		out = append(out, personRefJSON(p))
	}
	return out
}

// orEmptyRaw returns xs, or an empty (non-nil) slice when xs is nil, so a
// JSON array field renders `[]` rather than `null`.
func orEmptyRaw(xs []json.RawMessage) []json.RawMessage {
	if xs == nil {
		return []json.RawMessage{}
	}
	return xs
}

// ValidateProcessedArtifact is the deterministic schema gate for a processed
// artifact, mirroring the jq contract in acceptance-criteria.md Phase 4:
//
//   - id == entry_id;
//   - produced_at is an ISO-8601 timestamp (YYYY-MM-DDT...);
//   - agent_version begins with "structuring-";
//   - every people[] entry has display_name, a non-empty person_key, and a
//     first_mention flag — no person_key is null on disk;
//   - at least one of emotions/themes/people is non-empty, or notes is set.
//
// It runs on the rendered bytes before every write, so no artifact that
// breaks the contract can be persisted.
func ValidateProcessedArtifact(content []byte) error {
	var j processedJSON
	if err := json.Unmarshal(content, &j); err != nil {
		return fmt.Errorf("storage: parse processed json: %w", err)
	}
	if j.ID == "" {
		return errors.New("storage: processed id is empty")
	}
	if j.ID != j.EntryID {
		return fmt.Errorf("storage: processed id %q != entry_id %q", j.ID, j.EntryID)
	}
	if !isISOTimestamp(j.ProducedAt) {
		return fmt.Errorf("storage: processed produced_at %q is not an ISO-8601 timestamp", j.ProducedAt)
	}
	if !strings.HasPrefix(j.AgentVersion, structuringVersionPrefix) {
		return fmt.Errorf("storage: processed agent_version %q lacks the %q prefix", j.AgentVersion, structuringVersionPrefix)
	}
	for i, p := range j.People {
		if p.DisplayName == "" {
			return fmt.Errorf("storage: processed people[%d] has an empty display_name", i)
		}
		if p.PersonKey == "" {
			return fmt.Errorf("storage: processed people[%d] (%s) has a null person_key", i, p.DisplayName)
		}
	}
	hasStructure := len(j.Emotions) > 0 || len(j.Themes) > 0 || len(j.People) > 0
	hasNotes := j.Notes != nil && strings.TrimSpace(*j.Notes) != ""
	if !hasStructure && !hasNotes {
		return errors.New("storage: processed artifact has empty emotions/themes/people and no notes")
	}
	return nil
}

// isISOTimestamp reports whether s begins with a YYYY-MM-DDT prefix, the
// shape the acceptance jq check asserts for produced_at. A full parse would
// also reject a bad offset, but the contract check is the date-T anchor.
func isISOTimestamp(s string) bool {
	if len(s) < len("2006-01-02T") {
		return false
	}
	for i, r := range "0000-00-00T" {
		switch r {
		case '0':
			if s[i] < '0' || s[i] > '9' {
				return false
			}
		case '-':
			if s[i] != '-' {
				return false
			}
		case 'T':
			if s[i] != 'T' {
				return false
			}
		}
	}
	return true
}
