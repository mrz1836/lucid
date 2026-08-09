package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	secretsDirName      = "secrets"
	secretsFileName     = "secrets.jsonl"
	secretRefIDPrefix   = "secretref_"
	secretRefNameMaxLen = 64
	secretRefNoteMaxLen = 256

	// SecretRefSchema identifies the names-only reference event family.
	SecretRefSchema = "secretref.v1"
)

// SecretRefOp is the lifecycle operation recorded by a reference event.
type SecretRefOp string

const (
	// SecretRefOpRegister makes a catalog reference live.
	SecretRefOpRegister SecretRefOp = "register"
	// SecretRefOpNote amends the note on a live reference without
	// re-registering it, so the original creation time is preserved.
	SecretRefOpNote SecretRefOp = "note"
	// SecretRefOpRemove tombstones a catalog reference.
	SecretRefOpRemove SecretRefOp = "remove"
)

// SecretRefEvent is one append-only line in the names-only reference catalog.
type SecretRefEvent struct {
	Schema string      `json:"schema"`
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Op     SecretRefOp `json:"op"`
	Note   string      `json:"note,omitempty"`
	At     string      `json:"at"`
	Source string      `json:"source"`
}

// SecretRef is the folded view of one live catalog entry.
type SecretRef struct {
	Name      string `json:"name"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"created_at"`
}

func (a *Adapter) secretsDir() string { return filepath.Join(a.home, secretsDirName) }

func (a *Adapter) secretsPath() string {
	return filepath.Join(a.secretsDir(), secretsFileName)
}

// ScaffoldSecrets creates the reference-catalog directory lazily and
// idempotently.
func (a *Adapter) ScaffoldSecrets() error {
	if err := os.MkdirAll(a.secretsDir(), dirPerm); err != nil {
		return fmt.Errorf("storage: create secrets dir: %w", err)
	}
	return nil
}

// ValidateSecretRefName enforces the catalog handle grammar.
func ValidateSecretRefName(name string) error {
	if name == "" || len(name) > secretRefNameMaxLen || !validSecretRefNameShape(name) {
		return errors.New("storage: secret reference name must match ^[A-Z_][A-Z0-9_]*$ (1-64 characters)")
	}
	return nil
}

func validSecretRefNameShape(name string) bool {
	for i := range len(name) {
		c := name[i]
		if i == 0 {
			if c != '_' && (c < 'A' || c > 'Z') {
				return false
			}
			continue
		}
		if c != '_' && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// ValidateSecretRefNote enforces the catalog note length ceiling.
func ValidateSecretRefNote(note string) error {
	if utf8.RuneCountInString(note) > secretRefNoteMaxLen {
		return errors.New("storage: secret reference note must be at most 256 characters")
	}
	return nil
}

// AppendSecretRefEvent stamps, validates, and fsync-appends one catalog event.
func (a *Adapter) AppendSecretRefEvent(ev SecretRefEvent) (SecretRefEvent, error) {
	ev.Schema = SecretRefSchema
	path := a.secretsPath()
	seq, err := a.nextSecretRefSeq(path)
	if err != nil {
		return SecretRefEvent{}, err
	}
	ev.ID = secretRefIDPrefix + strconv.Itoa(seq)
	if err = ev.validate(); err != nil {
		return SecretRefEvent{}, err
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return SecretRefEvent{}, fmt.Errorf("storage: marshal secret reference event: %w", err)
	}
	if err := appendLineFsync(path, line); err != nil {
		return SecretRefEvent{}, err
	}
	return ev, nil
}

func (a *Adapter) nextSecretRefSeq(path string) (int, error) {
	lines, err := readJSONLLines(path)
	if err != nil {
		return 0, err
	}
	maxSeq := 0
	for _, line := range lines {
		var ev SecretRefEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if seq, ok := parseSecretRefSeq(ev.ID); ok && seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq + 1, nil
}

// ReadSecretRefEvents returns valid events in append order and counts skipped
// malformed lines.
func (a *Adapter) ReadSecretRefEvents() (events []SecretRefEvent, skipped int, err error) {
	lines, err := readJSONLLines(a.secretsPath())
	if err != nil {
		return nil, 0, err
	}
	for _, line := range lines {
		ev, parseErr := unmarshalSecretRefEvent(line)
		if parseErr != nil {
			skipped++
			continue
		}
		events = append(events, ev)
	}
	return events, skipped, nil
}

func unmarshalSecretRefEvent(raw []byte) (SecretRefEvent, error) {
	var ev SecretRefEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return SecretRefEvent{}, fmt.Errorf("storage: parse secret reference event: %w", err)
	}
	if err := ev.validate(); err != nil {
		return SecretRefEvent{}, err
	}
	return ev, nil
}

func (ev SecretRefEvent) validate() error {
	if ev.Schema != SecretRefSchema {
		return fmt.Errorf("storage: secret reference schema %q is not %q", ev.Schema, SecretRefSchema)
	}
	if _, ok := parseSecretRefSeq(ev.ID); !ok {
		return fmt.Errorf("storage: secret reference id %q is invalid", ev.ID)
	}
	if err := ValidateSecretRefName(ev.Name); err != nil {
		return err
	}
	switch ev.Op {
	case SecretRefOpRegister, SecretRefOpNote, SecretRefOpRemove:
	default:
		return fmt.Errorf("storage: secret reference op %q is not one of register|note|remove", ev.Op)
	}
	if err := ValidateSecretRefNote(ev.Note); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, ev.At); err != nil {
		return fmt.Errorf("storage: secret reference at %q invalid: %w", ev.At, err)
	}
	if ev.Source == "" {
		return errors.New("storage: secret reference source is required")
	}
	return nil
}

func parseSecretRefSeq(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, secretRefIDPrefix)
	if !ok {
		return 0, false
	}
	seq, err := strconv.Atoi(rest)
	if err != nil || seq <= 0 {
		return 0, false
	}
	return seq, true
}

// foldSecretRefs reduces events in append order to the live catalog. Each
// register starts a new live generation; a note amends the live entry's note
// without disturbing its creation time; remove leaves only its history. A note
// for a name that is not currently live is ignored.
func foldSecretRefs(events []SecretRefEvent) map[string]SecretRef {
	live := make(map[string]SecretRef)
	for _, ev := range events {
		switch ev.Op {
		case SecretRefOpRegister:
			ref, exists := live[ev.Name]
			if !exists {
				ref = SecretRef{Name: ev.Name, CreatedAt: ev.At}
			}
			ref.Note = ev.Note
			live[ev.Name] = ref
		case SecretRefOpNote:
			if ref, exists := live[ev.Name]; exists {
				ref.Note = ev.Note
				live[ev.Name] = ref
			}
		case SecretRefOpRemove:
			delete(live, ev.Name)
		}
	}
	return live
}

// ListSecretRefs returns the live folded catalog sorted by name.
func (a *Adapter) ListSecretRefs() ([]SecretRef, error) {
	events, _, err := a.ReadSecretRefEvents()
	if err != nil {
		return nil, err
	}
	folded := foldSecretRefs(events)
	refs := make([]SecretRef, 0, len(folded))
	for _, ref := range folded {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b SecretRef) int { return strings.Compare(a.Name, b.Name) })
	return refs, nil
}

type secretRefLine struct {
	locator string
	raw     []byte
}

func (a *Adapter) secretRefLines() ([]secretRefLine, error) {
	lines, err := readJSONLLines(a.secretsPath())
	if err != nil {
		return nil, err
	}
	out := make([]secretRefLine, 0, len(lines))
	for i, raw := range lines {
		locator := fmt.Sprintf("line-%d", i+1)
		var ev SecretRefEvent
		if json.Unmarshal(raw, &ev) == nil && ev.ID != "" {
			locator = ev.ID
		}
		out = append(out, secretRefLine{locator: locator, raw: raw})
	}
	return out, nil
}

// ListSecretRefIDs returns a stable locator for every catalog log line.
func (a *Adapter) ListSecretRefIDs() ([]string, error) {
	lines, err := a.secretRefLines()
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(lines))
	for i, line := range lines {
		ids[i] = line.locator
	}
	return ids, nil
}

// ReadSecretRefErr validates the catalog line addressed by id without writing.
func (a *Adapter) ReadSecretRefErr(id string) error {
	lines, err := a.secretRefLines()
	if err != nil {
		return err
	}
	for _, line := range lines {
		if line.locator == id {
			_, parseErr := unmarshalSecretRefEvent(line.raw)
			return parseErr
		}
	}
	return nil
}
